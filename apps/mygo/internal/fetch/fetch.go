// Package fetch reads public web pages and feeds: every address a name
// resolves to must be public, the connection goes to the address that was
// checked, redirects stay in scope and never leave HTTPS, and size and time
// are bounded.
package fetch

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"trie/internal/web"
)

// MaxBytes is ReaderFetch's default body limit, after decompression.
const MaxBytes = 5 * 1024 * 1024

// Response is a finished read: the address it ended at after redirects, the
// status, the Content-Type and the decompressed body.
type Response struct {
	URL    string
	Status int
	Type   string
	Body   []byte
}

// Fetch reads url, following redirects scope allows, with a
// body of at most maxBytes.
type Fetch func(ctx context.Context, url string, scope func(string) bool, maxBytes int) (*Response, error)

// NetError is a failure of the connection or DNS rather than of the policy;
// Node gives these an error code. Transient is ECONNRESET or EAI_AGAIN: the
// connection dropped (also before TLS finished) or DNS failed for now, the
// errors the reader and subscriptions try once more.
type NetError struct {
	Err       error
	Transient bool
}

func (e *NetError) Error() string { return e.Err.Error() }
func (e *NetError) Unwrap() error { return e.Err }

var (
	errNotPublic  = errors.New("仅支持公开网站的 HTTP(S) 地址")
	errScope      = errors.New("重定向超出来源范围或形成循环")
	errBudget     = errors.New("本篇文章的读取额度已用完")
	errDNSTimeout = errors.New("网站地址解析超时")
	errAddress    = errors.New("网站地址未通过公开网络检查")
	errEncoding   = errors.New("不支持的网页编码")
	errTooBig     = errors.New("网页或图片超出大小限制")
	errTimeout    = errors.New("读取网站超时")
	errHops       = errors.New("重定向次数超出限制")
	errDowngrade  = errors.New("不跟随 HTTPS 降级跳转")
)

func prefixes(list ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(list))
	for i, p := range list {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

// The SpecialRanges of ipaddr.js 2.5.0: an address in none of them is unicast.
var (
	special4 = prefixes("0.0.0.0/8", "255.255.255.255/32", "224.0.0.0/4", "169.254.0.0/16", "127.0.0.0/8", "100.64.0.0/10",
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "192.175.48.0/24", "192.31.196.0/24", "192.52.193.0/24")
	special6 = prefixes("::/128", "fe80::/10", "ff00::/8", "::1/128", "fc00::/7", "::ffff:0:0/96", "fec0::/10", "100::/64",
		"::ffff:0:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32", "2001:2::/48", "2001:3::/32",
		"2001:4:112::/48", "2620:4f:8000::/48", "2001:10::/28", "2001:20::/28", "2001:30::/28", "5f00::/16", "2001::/23",
		"2001:db8::/32", "3fff::/20")
	synthetic = netip.MustParsePrefix("198.18.0.0/15")
)

// PublicAddress is whether value is a unicast
// address as ipaddr.js judges it, IPv4-mapped IPv6 read as IPv4. allowSynthetic
// also passes 198.18.0.0/15, the range fake-IP proxies (Clash, mihomo, Surge)
// answer every lookup with while they resolve the real host themselves.
func PublicAddress(value string, allowSynthetic bool) bool {
	a, err := netip.ParseAddr(value)
	return err == nil && public(a, allowSynthetic)
}

func public(a netip.Addr, allowSynthetic bool) bool {
	// A zoned address matches no prefix, so the zone goes first.
	a = a.WithZone("").Unmap()
	if allowSynthetic && synthetic.Contains(a) {
		return true
	}
	ranges := special6
	if a.Is4() {
		ranges = special4
	}
	return !slices.ContainsFunc(ranges, func(p netip.Prefix) bool { return p.Contains(a) })
}

func unbracket(host string) string {
	return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
}

// PublicURL is an http(s) address of a public
// site with no credentials or port, its fragment dropped.
func PublicURL(value string) (*web.URL, error) {
	u, err := web.ParseURL(value, nil)
	if err != nil {
		return nil, err
	}
	host := strings.ToLower(strings.TrimSuffix(unbracket(u.Hostname()), "."))
	_, err = netip.ParseAddr(host)
	ip := err == nil
	if u.Scheme != "http" && u.Scheme != "https" || u.Username != "" || u.Password != "" || hasPort(u) ||
		host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		!strings.Contains(host, ".") && !ip || ip && !PublicAddress(host, false) {
		return nil, errNotPublic
	}
	if href := u.Href(); strings.Contains(href, "#") {
		// url.hash = '': the first # of a serialized URL starts its fragment.
		return web.ParseURL(href[:strings.IndexByte(href, '#')], nil)
	}
	return u, nil
}

// hasPort is whether url.port names a port, for an http(s) URL without
// credentials, whose href is scheme://host[:port]/path.
func hasPort(u *web.URL) bool {
	return strings.HasPrefix(strings.TrimPrefix(u.Href(), u.Scheme+"://"+u.Hostname()), ":")
}

// query is url.search without its "?", for a URL PublicURL returned. An
// empty query is no query, as url.search reads it.
func query(u *web.URL) string {
	return strings.TrimPrefix(strings.TrimPrefix(u.Href(), u.Scheme+"://"+u.Hostname()+u.Pathname()), "?")
}

// PublisherScope is where a read of home may
// be redirected. The publisher's host, with or without www., or a subdomain
// of it (baoyu.io/feed.xml moves to s.baoyu.io); HTTP may become HTTPS.
func PublisherScope(home string) (func(string) bool, error) {
	root, err := PublicURL(home)
	if err != nil {
		return nil, err
	}
	own := strings.TrimPrefix(root.Hostname(), "www.")
	return func(value string) bool {
		u, err := PublicURL(value)
		if err != nil {
			return false
		}
		host := strings.TrimPrefix(u.Hostname(), "www.")
		return (host == own || strings.HasSuffix(host, "."+own)) && (u.Scheme == root.Scheme || root.Scheme == "http" && u.Scheme == "https")
	}, nil
}

// Client is one read's worth of requests, redirects included,
// shared by every Fetch on it.
type Client struct {
	limit     int
	timeout   time.Duration
	resolve   func(ctx context.Context, host string) ([]netip.Addr, error)
	transport *http.Transport

	mu       sync.Mutex
	requests int
}

// New is a Client of at most limit requests, a 15 s stall timeout
// and the system resolver.
func New(limit int) *Client {
	return &Client{limit: limit, timeout: 15 * time.Second, resolve: systemResolve, transport: shared}
}

func systemResolve(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// One pool for every client, as node:https shares its global agent: a pooled
// connection was dialled to an address checked for its host.
var shared = newTransport(nil, (&net.Dialer{}).DialContext)

type pinKey struct{}

func newTransport(config *tls.Config, dial func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Transport {
	return &http.Transport{
		// No proxy from the environment: the connection goes to the checked address.
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// The address checked for this request's host, never a fresh lookup (DNS rebinding).
			ip, ok := ctx.Value(pinKey{}).(netip.Addr)
			_, port, err := net.SplitHostPort(addr)
			if !ok || err != nil {
				return nil, errors.New("fetch: no checked address for " + addr)
			}
			return dial(ctx, network, net.JoinHostPort(ip.String(), port))
		},
		TLSClientConfig: config,
		// HTTP/1.1 only, as node:https speaks.
		TLSNextProto:           map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxResponseHeaderBytes: 16 * 1024,
		IdleConnTimeout:        5 * time.Second,
	}
}

func redirect(status int) bool {
	return status == 301 || status == 302 || status == 303 || status == 307 || status == 308
}

// Fetch reads value within the client's request limit.
func (c *Client) Fetch(ctx context.Context, value string, scope func(string) bool, maxBytes int) (*Response, error) {
	u, err := PublicURL(value)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for hop := 0; hop <= 5; hop++ {
		href := u.Href()
		if !scope(href) || seen[href] {
			return nil, errScope
		}
		seen[href] = true
		c.mu.Lock()
		c.requests++
		over := c.requests > c.limit
		c.mu.Unlock()
		if over {
			return nil, errBudget
		}
		addrs, err := c.lookup(ctx, unbracket(u.Hostname()))
		if err != nil {
			return nil, err
		}
		// A URL naming a fake-IP address still fails PublicURL; only answers may be one.
		if len(addrs) == 0 || slices.ContainsFunc(addrs, func(a netip.Addr) bool { return !public(a, true) }) {
			return nil, errAddress
		}
		res, location, err := c.get(ctx, u, addrs[0].WithZone("").Unmap(), maxBytes)
		if err != nil {
			return nil, err
		}
		if !redirect(res.Status) {
			res.URL = href
			return res, nil
		}
		if location == "" || hop == 5 {
			return nil, errHops
		}
		target, err := web.ResolveURL(location, href)
		if err != nil {
			return nil, err
		}
		next, err := PublicURL(target.Href())
		if err != nil {
			return nil, err
		}
		if u.Scheme == "https" && next.Scheme != "https" {
			return nil, errDowngrade
		}
		u = next
	}
	return nil, errHops
}

// lookup resolves host within 6 s, whether or not the resolver watches ctx.
func (c *Client) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, 6*time.Second, errDNSTimeout)
	defer cancel()
	type answer struct {
		addrs []netip.Addr
		err   error
	}
	done := make(chan answer, 1)
	go func() {
		addrs, err := c.resolve(ctx, host)
		done <- answer{addrs, err}
	}()
	select {
	case a := <-done:
		if a.err != nil {
			var dns *net.DNSError
			return nil, &NetError{Err: a.err, Transient: errors.As(a.err, &dns) && (dns.IsTemporary || dns.IsTimeout)}
		}
		return a.addrs, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// get makes one request to ip. A redirect comes back with its Location and no body.
func (c *Client) get(ctx context.Context, u *web.URL, ip netip.Addr, maxBytes int) (*Response, string, error) {
	ctx, cancel := context.WithCancelCause(context.WithValue(ctx, pinKey{}, ip))
	defer cancel(nil)
	// The timeout is for a stalled connection; a slow one still delivering bytes may run up to four times as long.
	idle := time.AfterFunc(c.timeout, func() { cancel(errTimeout) })
	defer idle.Stop()
	limit := time.AfterFunc(4*c.timeout, func() { cancel(errTimeout) })
	defer limit.Stop()
	fail := func(err error) error {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		// Node reports a peer that closed or reset the connection as ECONNRESET; Go sees EOF, or EPIPE when the reset
		// came before the client hello was written.
		dropped := errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
		return &NetError{Err: err, Transient: dropped}
	}
	// Opaque keeps the request target as node:https sends it, pathname + search as the WHATWG parser serialized them.
	target := &url.URL{Scheme: u.Scheme, Host: u.Hostname(), Opaque: u.Pathname(), RawQuery: query(u)}
	req := (&http.Request{Method: "GET", URL: target, Host: target.Host, Header: http.Header{
		"User-Agent": {"Trie/0.1 (Personal blog reader)"},
		"Accept":     {"text/html,application/xhtml+xml,application/rss+xml,application/atom+xml,image/*;q=0.8,*/*;q=0.5"},
		// Accept-Encoding is left to the transport, which asks for gzip and undoes it (no brotli in the standard library).
	}}).WithContext(ctx)
	resp, err := c.transport.RoundTrip(req)
	if err != nil {
		return nil, "", fail(err)
	}
	defer resp.Body.Close()
	res := &Response{Status: resp.StatusCode, Type: latin1(resp.Header.Get("Content-Type")), Body: []byte{}}
	if redirect(res.Status) {
		return res, latin1(resp.Header.Get("Location")), nil
	}
	body := io.Reader(resp.Body)
	switch encoding := resp.Header.Get("Content-Encoding"); {
	case resp.Uncompressed || encoding == "" || encoding == "identity":
	case encoding == "deflate":
		z, err := zlib.NewReader(body)
		if err != nil {
			return nil, "", fail(err)
		}
		body = z
	default:
		return nil, "", errEncoding
	}
	var buf bytes.Buffer
	chunk := make([]byte, 32*1024)
	for {
		n, err := body.Read(chunk)
		if n > 0 {
			idle.Reset(c.timeout)
			if buf.Len()+n > maxBytes {
				return nil, "", errTooBig
			}
			buf.Write(chunk[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fail(err)
		}
	}
	res.Body = buf.Bytes()
	return res, "", nil
}

// latin1 reads a header value as Node does, one character per byte: a raw
// UTF-8 Location becomes mojibake there too.
func latin1(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			r := make([]rune, len(s))
			for j := 0; j < len(s); j++ {
				r[j] = rune(s[j])
			}
			return string(r)
		}
	}
	return s
}

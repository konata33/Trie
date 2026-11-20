package fetch

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Client cases, with a real TLS server: the client resolves through a stub,
// dials whatever address the policy pins and is connected to the test
// server, which presents a certificate for the requested host.

type reply struct {
	status  int
	headers map[string]string
	body    []byte
	stall   bool // never answer
	trickle bool // answer, then send a byte every 10ms forever
}

type wire struct {
	srv *httptest.Server
	mu  sync.Mutex
	// What reached the server and where the client tried to connect.
	requests []*http.Request
	dialed   []string
}

var certificate = sync.OnceValue(func() tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "writer.example"},
		DNSNames: []string{"writer.example", "www.writer.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
})

func newWire(t *testing.T, replies ...reply) *wire { return startWire(t, false, replies...) }

func startWire(t *testing.T, h2 bool, replies ...reply) *wire {
	w := &wire{}
	w.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		i := len(w.requests)
		w.requests = append(w.requests, r)
		w.mu.Unlock()
		rep := reply{}
		if i < len(replies) {
			rep = replies[i]
		}
		if rep.stall {
			<-r.Context().Done()
			return
		}
		for k, v := range rep.headers {
			rw.Header().Set(k, v)
		}
		rw.WriteHeader(max(rep.status, 200))
		if rep.trickle {
			for {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(10 * time.Millisecond):
					rw.Write([]byte("x"))
					rw.(http.Flusher).Flush()
				}
			}
		}
		if rep.body == nil {
			rep.body = []byte("body")
		}
		rw.Write(rep.body)
	}))
	w.srv.TLS = &tls.Config{Certificates: []tls.Certificate{certificate()}}
	w.srv.EnableHTTP2 = h2
	w.srv.StartTLS()
	t.Cleanup(w.srv.Close)
	return w
}

func (w *wire) calls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.requests)
}

type resolver = func(ctx context.Context, host string) ([]netip.Addr, error)

func answers(list ...string) resolver {
	return func(context.Context, string) ([]netip.Addr, error) {
		out := []netip.Addr{}
		for _, a := range list {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
}

var google = answers("8.8.8.8")

func (w *wire) client(limit int, timeout time.Duration, resolve resolver) *Client {
	roots := x509.NewCertPool()
	roots.AddCert(certificate().Leaf)
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		w.mu.Lock()
		w.dialed = append(w.dialed, addr)
		w.mu.Unlock()
		return (&net.Dialer{}).DialContext(ctx, network, w.srv.Listener.Addr().String())
	}
	return &Client{limit: limit, timeout: timeout, resolve: resolve, transport: newTransport(&tls.Config{RootCAs: roots}, dial)}
}

func anywhere(string) bool { return true }

func fails(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %v, want %q", err, want)
	}
}

// reader network policy rejects private, reserved and mapped IP addresses
func TestPublicAddress(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1", "2001:db8::1", "198.18.1.2",
		// Every other ipaddr.js range, a zoned link-local, a mapped private address, NAT64.
		"172.16.0.1", "192.0.0.1", "192.0.2.1", "192.88.99.1", "198.51.100.1", "203.0.113.1", "240.0.0.1", "255.255.255.255", "224.0.0.1",
		"192.175.48.1", "192.31.196.1", "192.52.193.1", "fe80::1%en0", "::ffff:10.0.0.1", "64:ff9b::808:808", "2002::1", "2001::1", "fec0::1", "ff02::1", "::", "100::1", "5f00::1", "3fff::1"} {
		if PublicAddress(a, false) {
			t.Errorf("%s passed", a)
		}
	}
	for _, a := range []string{"8.8.8.8", "2606:4700:4700::1111", "::ffff:8.8.8.8"} {
		if !PublicAddress(a, false) {
			t.Errorf("%s refused", a)
		}
	}
	if !PublicAddress("198.18.1.2", true) || !PublicAddress("198.19.255.1", true) || !PublicAddress("::ffff:198.18.0.1", true) || PublicAddress("10.0.0.1", true) || !PublicAddress("198.20.0.1", false) {
		t.Error("fake-IP range")
	}
	if PublicAddress("not an address", false) {
		t.Error("text passed")
	}
}

// reader accepts only credential-free standard public HTTP(S) URLs
func TestPublicURL(t *testing.T) {
	for _, v := range []string{"file:///etc/passwd", "javascript:alert(1)", "http://localhost/", "http://localhost./", "http://test.local/", "http://127.0.0.1/", "http://[::ffff:127.0.0.1]/", "https://user:secret@writer.example/", "https://writer.example:8443/",
		"http://intranet/", "http://0x7f.1/", "http://[::1]/", "http://a.localhost/", "https://:secret@writer.example/", "http://198.18.3.4/", "not a url"} {
		if u, err := PublicURL(v); err == nil {
			t.Errorf("%s passed as %s", v, u.Href())
		}
	}
	for v, want := range map[string]string{"https://writer.example/post/#section": "https://writer.example/post/", "https://writer.example:443/a?b#c": "https://writer.example/a?b",
		"https://8.8.8.8/": "https://8.8.8.8/", "https://WWW.Writer.example/#": "https://www.writer.example/"} {
		if u, err := PublicURL(v); err != nil || u.Href() != want {
			t.Errorf("%s: %v %v", v, u, err)
		}
	}
}

func scope(t *testing.T, home string) func(string) bool {
	s, err := PublisherScope(home)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// publisher redirect scope permits www and HTTPS upgrade only
func TestPublisherScope(t *testing.T) {
	s := scope(t, "https://www.writer.example/")
	for v, want := range map[string]bool{"https://writer.example/posts/one": true, "http://writer.example/posts/one": false, "https://writer.example.evil.example/": false, "https://writer.example:8443/": false} {
		if s(v) != want {
			t.Errorf("%s: %v", v, !want)
		}
	}
	if !scope(t, "http://writer.example/")("https://writer.example/") {
		t.Error("upgrade refused")
	}
	if _, err := PublisherScope("http://127.0.0.1/"); err == nil {
		t.Error("private home accepted")
	}
}

// a publisher may move a read to its own subdomain, never to a look-alike or a parent platform
func TestPublisherSubdomain(t *testing.T) {
	s := scope(t, "https://baoyu.io/feed.xml")
	for v, want := range map[string]bool{"https://s.baoyu.io/feed.xml": true, "https://www.baoyu.io/feed.xml": true, "https://evilbaoyu.io/feed.xml": false, "https://baoyu.io.evil.example/feed.xml": false} {
		if s(v) != want {
			t.Errorf("%s: %v", v, !want)
		}
	}
	writer := scope(t, "https://alice.github.io/post")
	if writer("https://bob.github.io/post") || writer("https://github.io/") {
		t.Error("a neighbour on a shared host is someone else")
	}
}

// all DNS results are validated before issuing a request
func TestEveryAnswerChecked(t *testing.T) {
	for _, list := range [][]string{{"8.8.8.8", "127.0.0.1"}, {"10.0.0.1"}, {"8.8.8.8", "::1"}, {"::ffff:192.168.0.1"}, {}} {
		w := newWire(t)
		_, err := w.client(5, time.Second, answers(list...)).Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes)
		fails(t, err, "公共互联网")
		if w.calls() != 0 || len(w.dialed) != 0 {
			t.Fatalf("%v: %d requests, dialed %v", list, w.calls(), w.dialed)
		}
	}
}

// fake-IP proxy answers resolve, but a URL naming one does not
func TestFakeIPAnswers(t *testing.T) {
	w := newWire(t, reply{})
	res, err := w.client(5, time.Second, answers("198.18.3.4")).Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes)
	if err != nil || res.Status != 200 || w.dialed[0] != "198.18.3.4:443" {
		t.Fatal(res, err, w.dialed)
	}
	if _, err := PublicURL("https://198.18.3.4/"); err == nil {
		t.Fatal("a URL naming a fake-IP address passed")
	}
}

// request DNS is pinned while original hostname and credential-free headers remain
func TestPinnedAddress(t *testing.T) {
	w := newWire(t, reply{})
	res, err := w.client(5, time.Second, google).Fetch(context.Background(), "https://writer.example/a%20b?q=1&r=%E4%B8%AD#x", anywhere, MaxBytes)
	if err != nil || res.Status != 200 || string(res.Body) != "body" || res.URL != "https://writer.example/a%20b?q=1&r=%E4%B8%AD" {
		t.Fatal(res, err)
	}
	r := w.requests[0]
	if w.dialed[0] != "8.8.8.8:443" || r.Host != "writer.example" || r.RequestURI != "/a%20b?q=1&r=%E4%B8%AD" {
		t.Fatalf("dialed %v, Host %q, target %q", w.dialed, r.Host, r.RequestURI)
	}
	if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("User-Agent") != "Trie/0.1 (Personal blog reader)" ||
		r.Header.Get("Accept") != "text/html,application/xhtml+xml,application/rss+xml,application/atom+xml,image/*;q=0.8,*/*;q=0.5" || r.Header.Get("Accept-Encoding") != "gzip" || r.Proto != "HTTP/1.1" {
		t.Fatalf("headers %v %s", r.Header, r.Proto)
	}
}

// A site that speaks HTTP/2 is read over it, to the checked address: the reads after the first share its connection,
// at once, and keep the request target and headers.
func TestHTTP2SharesConnection(t *testing.T) {
	w := startWire(t, true)
	c := w.client(10, time.Second, google)
	if _, err := c.Fetch(context.Background(), "https://writer.example/first", anywhere, MaxBytes); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Go(func() {
			res, err := c.Fetch(context.Background(), "https://writer.example/a%20b?q=1", anywhere, MaxBytes)
			if err == nil && (res.Status != 200 || string(res.Body) != "body") {
				err = errors.New("bad response")
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	r := w.requests[len(w.requests)-1]
	if r.ProtoMajor != 2 || r.Host != "writer.example" || r.RequestURI != "/a%20b?q=1" || r.Header.Get("User-Agent") != "Trie/0.1 (Personal blog reader)" {
		t.Fatalf("%s Host %q target %q headers %v", r.Proto, r.Host, r.RequestURI, r.Header)
	}
	if len(w.dialed) != 1 || w.dialed[0] != "8.8.8.8:443" {
		t.Fatalf("dialed %v", w.dialed)
	}
}

// The pool's TLS hello offers no post-quantum key share, which fake-IP proxies often drop: a server that refuses
// such a hello still answers the app, and refuses Go's default one.
func TestClassicHello(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) { rw.Write([]byte("feed")) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{certificate()}, GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		if slices.Contains(hello.SupportedCurves, tls.X25519MLKEM768) {
			return nil, errors.New("hello too large")
		}
		return nil, nil
	}}
	srv.StartTLS()
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(certificate().Leaf)
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	read := func(config *tls.Config) error {
		config.RootCAs = roots
		c := &Client{limit: 5, timeout: time.Second, resolve: google, transport: newTransport(config, dial)}
		_, err := c.Fetch(context.Background(), "https://writer.example/feed", anywhere, MaxBytes)
		return err
	}
	if err := read(shared.TLSClientConfig.Clone()); err != nil {
		t.Fatal(err)
	}
	if read(&tls.Config{}) == nil {
		t.Fatal("the server took a post-quantum hello")
	}
}

// The connection goes to the checked address even when the environment names a proxy.
func TestNoEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://proxy.example:3128")
	t.Setenv("HTTP_PROXY", "http://proxy.example:3128")
	w := newWire(t, reply{})
	if _, err := w.client(5, time.Second, google).Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes); err != nil || w.dialed[0] != "8.8.8.8:443" {
		t.Fatal(err, w.dialed)
	}
}

// DNS rebinding: every hop is resolved and checked again, and the connection
// goes to the checked address, never to a second answer for the same name.
func TestRebinding(t *testing.T) {
	lookups := 0
	rebind := func(context.Context, string) ([]netip.Addr, error) {
		lookups++
		if lookups == 1 {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	w := newWire(t, reply{status: 302, headers: map[string]string{"Location": "/next"}})
	_, err := w.client(5, time.Second, rebind).Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes)
	fails(t, err, "公共互联网")
	if w.calls() != 1 || lookups != 2 || len(w.dialed) != 1 || w.dialed[0] != "8.8.8.8:443" {
		t.Fatalf("%d requests, %d lookups, dialed %v", w.calls(), lookups, w.dialed)
	}
	// The transport never dials by itself: a request without a checked address cannot connect.
	_, err = newTransport(nil, (&net.Dialer{}).DialContext).DialContext(context.Background(), "tcp", "writer.example:443")
	fails(t, err, "no checked address")
}

// redirects cannot leave scope, reach private addresses or downgrade HTTPS
func TestRedirectLimits(t *testing.T) {
	byName := func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "internal.writer.example" {
			return []netip.Addr{netip.MustParseAddr("10.1.2.3")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	for target, want := range map[string]string{"https://other.example/": "站外", "http://127.0.0.1/": "公开网站", "http://writer.example/": "跳到了 HTTP",
		"https://internal.writer.example/": "公共互联网", "https://[::1]/": "公开网站", "http://user:pw@writer.example/": "公开网站", "file:///etc/passwd": "公开网站"} {
		w := newWire(t, reply{status: 302, headers: map[string]string{"Location": target}})
		_, err := w.client(5, time.Second, byName).Fetch(context.Background(), "https://writer.example/", scope(t, "https://writer.example/"), MaxBytes)
		fails(t, err, want)
		if w.calls() != 1 {
			t.Fatalf("%s: %d requests", target, w.calls())
		}
	}
	// Subscriptions follow any public redirect, but never from HTTPS to HTTP.
	w := newWire(t, reply{status: 301, headers: map[string]string{"Location": "http://writer.example/feed"}})
	_, err := w.client(5, time.Second, google).Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes)
	fails(t, err, "跳到了 HTTP")
	if w.calls() != 1 {
		t.Fatalf("%d requests", w.calls())
	}
	// Five redirects are followed; the sixth is one too many.
	hops := func(n int) []reply {
		out := []reply{}
		for i := range n {
			out = append(out, reply{status: []int{301, 302, 303, 307, 308}[i%5], headers: map[string]string{"Location": "/" + string(rune('a'+i))}})
		}
		return out
	}
	w = newWire(t, append(hops(5), reply{body: []byte("arrived")})...)
	res, err := w.client(10, time.Second, google).Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes)
	if err != nil || string(res.Body) != "arrived" || res.URL != "https://writer.example/e" {
		t.Fatal(res, err)
	}
	w = newWire(t, hops(6)...)
	_, err = w.client(10, time.Second, google).Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes)
	fails(t, err, "跳转的次数太多")
	if w.calls() != 6 {
		t.Fatalf("%d requests", w.calls())
	}
	w = newWire(t, reply{status: 302})
	_, err = w.client(5, time.Second, google).Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes)
	fails(t, err, "跳转的次数太多")
	// Other statuses are answers, not redirects.
	w = newWire(t, reply{status: 304, headers: map[string]string{"Location": "/x"}}, reply{status: 404, body: []byte("missing")})
	c := w.client(5, time.Second, google)
	if res, err := c.Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes); err != nil || res.Status != 304 {
		t.Fatal(res, err)
	}
	if res, err := c.Fetch(context.Background(), "https://writer.example/gone", anywhere, MaxBytes); err != nil || res.Status != 404 || string(res.Body) != "missing" {
		t.Fatal(res, err)
	}
}

func gzipped(b []byte) []byte {
	var buf bytes.Buffer
	z := gzip.NewWriter(&buf)
	z.Write(b)
	z.Close()
	return buf.Bytes()
}

// decompressed bodies are bounded and stalled requests time out
func TestSizeAndStall(t *testing.T) {
	w := newWire(t, reply{headers: map[string]string{"Content-Encoding": "gzip"}, body: gzipped(bytes.Repeat([]byte("x"), 10000))})
	_, err := w.client(5, time.Second, google).Fetch(context.Background(), "https://writer.example/", anywhere, 100)
	fails(t, err, "太大")
	w = newWire(t, reply{stall: true})
	start := time.Now()
	_, err = w.client(5, 100*time.Millisecond, google).Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes)
	fails(t, err, "太久没有回应")
	// The stall timeout, not the limit at four times it.
	if took := time.Since(start); took > 300*time.Millisecond {
		t.Fatalf("a stalled request ran %v, want about 100ms", took)
	}
}

// A plain body over the limit, one exactly at it, a
// gzip body inside it, deflate, an unsupported encoding, and a response that
// keeps delivering a byte at a time, stopped at four times the timeout.
func TestBodies(t *testing.T) {
	big := bytes.Repeat([]byte("y"), 1000)
	w := newWire(t, reply{body: big}, reply{body: big}, reply{headers: map[string]string{"Content-Encoding": "gzip"}, body: gzipped(big)},
		reply{headers: map[string]string{"Content-Encoding": "deflate"}, body: deflated(big)}, reply{headers: map[string]string{"Content-Encoding": "br"}, body: big},
		reply{headers: map[string]string{"Content-Type": "text/html; charset=\xe9"}})
	c := w.client(10, time.Second, google)
	get := func(max int) (*Response, error) {
		return c.Fetch(context.Background(), "https://writer.example/", anywhere, max)
	}
	_, err := get(999)
	fails(t, err, "太大")
	for range 3 {
		if res, err := get(1000); err != nil || !bytes.Equal(res.Body, big) {
			t.Fatal(res, err)
		}
	}
	_, err = get(1000)
	fails(t, err, "不支持的文字编码")
	// Header values read one character per byte, as Node reads them.
	if res, err := get(1000); err != nil || res.Type != "text/html; charset=é" {
		t.Fatal(res, err)
	}
	w = newWire(t, reply{trickle: true})
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = w.client(5, 60*time.Millisecond, google).Fetch(ctx, "https://writer.example/", anywhere, MaxBytes)
	fails(t, err, "太久没有回应")
	if took := time.Since(start); took < 200*time.Millisecond || took > 2*time.Second {
		t.Fatalf("a slow response ran %v, want about 240ms", took)
	}
}

func deflated(b []byte) []byte {
	var buf bytes.Buffer
	z := zlib.NewWriter(&buf)
	z.Write(b)
	z.Close()
	return buf.Bytes()
}

// redirect requests consume the shared document budget and loops stop
func TestBudgetAndLoops(t *testing.T) {
	w := newWire(t, reply{status: 301, headers: map[string]string{"Location": "/next"}}, reply{})
	_, err := w.client(1, time.Second, google).Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes)
	fails(t, err, "请求太多")
	if w.calls() != 1 {
		t.Fatalf("%d requests", w.calls())
	}
	w = newWire(t, reply{status: 302, headers: map[string]string{"Location": "/"}})
	_, err = w.client(5, time.Second, google).Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes)
	fails(t, err, "来回跳")
	if w.calls() != 1 {
		t.Fatalf("%d requests", w.calls())
	}
	// The budget is the client's, across reads.
	w = newWire(t, reply{}, reply{})
	c := w.client(2, time.Second, google)
	for i, want := range []string{"", "", "请求太多"} {
		_, err := c.Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes)
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Fatalf("read %d: %v", i, err)
		}
	}
}

// A connection dropped before TLS finished, or reset, is a transient network
// error, which the reader and subscriptions try once more; DNS that
// fails for now is too, a name that does not exist is not.
func TestDroppedConnections(t *testing.T) {
	for _, reset := range []bool{false, true} {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				if reset {
					conn.(*net.TCPConn).SetLinger(0)
				}
				conn.Close()
			}
		}()
		dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, l.Addr().String())
		}
		c := &Client{limit: 5, timeout: time.Second, resolve: google, transport: newTransport(nil, dial)}
		_, err = c.Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes)
		l.Close()
		var ne *NetError
		if !errors.As(err, &ne) || !ne.Transient {
			t.Fatalf("reset=%v: %#v %v", reset, err, err)
		}
	}
	for err, transient := range map[error]bool{&net.DNSError{Err: "server misbehaving", IsTemporary: true}: true, &net.DNSError{Err: "i/o timeout", IsTimeout: true}: true,
		&net.DNSError{Err: "no such host", IsNotFound: true}: false} {
		c := &Client{limit: 5, timeout: time.Second, resolve: func(context.Context, string) ([]netip.Addr, error) { return nil, err }}
		_, got := c.Fetch(context.Background(), "https://writer.example/", anywhere, MaxBytes)
		var ne *NetError
		if !errors.As(got, &ne) || ne.Transient != transient {
			t.Fatalf("%v: %#v", err, got)
		}
	}
	slow := &Client{limit: 5, timeout: time.Second, resolve: func(ctx context.Context, _ string) ([]netip.Addr, error) { select {} }}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := slow.Fetch(ctx, "https://writer.example/", anywhere, MaxBytes); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

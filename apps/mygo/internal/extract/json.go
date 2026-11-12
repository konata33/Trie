package extract

import (
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
)

// jsObject is a JSON object as JSON.parse leaves it: a repeated key keeps its first place and its last value, and
// Object.entries lists array-index keys first, in numeric order, then the rest as written.
type jsObject struct {
	keys   []string
	values map[string]any
}

func (o *jsObject) get(key string) any { return o.values[key] }

func (o *jsObject) entries() []string {
	keys := slices.Clone(o.keys)
	index := func(k string) (uint64, bool) {
		n, err := strconv.ParseUint(k, 10, 32)
		return n, err == nil && n < 1<<32-1 && strconv.FormatUint(n, 10) == k
	}
	slices.SortStableFunc(keys, func(a, b string) int {
		x, xi := index(a)
		y, yi := index(b)
		switch {
		case xi && yi:
			return int(x) - int(y)
		case xi:
			return -1
		case yi:
			return 1
		}
		return 0
	})
	return keys
}

// parseJS is JSON.parse: objects come back as *jsObject, arrays as []any, numbers as json.Number.
func parseJS(s string) (any, error) {
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	v, err := readJS(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("Unexpected non-whitespace character after JSON")
	}
	return v, nil
}

func readJS(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		o := &jsObject{values: map[string]any{}}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			v, err := readJS(d)
			if err != nil {
				return nil, err
			}
			if _, ok := o.values[k.(string)]; !ok {
				o.keys = append(o.keys, k.(string))
			}
			o.values[k.(string)] = v
		}
		_, err = d.Token()
		return o, err
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, err := readJS(d)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err = d.Token()
		return a, err
	}
	return t, nil
}

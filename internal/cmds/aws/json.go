package aws

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// This file provides a minimal JSON value model that mirrors the parts of
// serde_json::Value the AWS filters rely on: order-preserving objects, a
// distinct number type that keeps the integer/float distinction, and helpers
// for typed field access. Go's encoding/json with a plain map[string]any would
// lose object key order and collapse all numbers to float64, so we decode with
// json.Decoder(UseNumber) and build our own object type.

// jsonNumber is a raw JSON number (e.g. "42", "19.99"), preserving the exact
// textual form. Mirrors serde_json::Number.
type jsonNumber string

// jsonObject is an order-preserving JSON object.
type jsonObject struct {
	keysOrder []string
	values    map[string]any
}

func newJSONObject() *jsonObject {
	return &jsonObject{values: map[string]any{}}
}

func (o *jsonObject) set(key string, val any) {
	if _, exists := o.values[key]; !exists {
		o.keysOrder = append(o.keysOrder, key)
	}
	o.values[key] = val
}

func (o *jsonObject) get(key string) any {
	if o == nil {
		return nil
	}
	return o.values[key]
}

func (o *jsonObject) has(key string) bool {
	if o == nil {
		return false
	}
	_, ok := o.values[key]
	return ok
}

func (o *jsonObject) len() int {
	if o == nil {
		return 0
	}
	return len(o.keysOrder)
}

func (o *jsonObject) keys() []string {
	if o == nil {
		return nil
	}
	return o.keysOrder
}

// parseJSON parses a JSON document into the jsonObject/[]any/string/jsonNumber/
// bool/nil value model. ok=false on parse error. Mirrors serde_json::from_str.
func parseJSON(s string) (any, bool) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, false
	}
	// Reject trailing garbage so "not json" and partials fail, matching serde.
	if dec.More() {
		return nil, false
	}
	return v, true
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return decodeFromToken(dec, tok)
}

func decodeFromToken(dec *json.Decoder, tok json.Token) (any, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := newJSONObject()
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key := keyTok.(string)
				val, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				obj.set(key, val)
			}
			// consume closing '}'
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				val, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			// consume closing ']'
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, errUnexpectedDelim
	case json.Number:
		return jsonNumber(string(t)), nil
	case string:
		return t, nil
	case bool:
		return t, nil
	case nil:
		return nil, nil
	}
	return nil, errUnexpectedToken
}

var (
	errUnexpectedDelim = &jsonErr{"unexpected delimiter"}
	errUnexpectedToken = &jsonErr{"unexpected token"}
)

type jsonErr struct{ msg string }

func (e *jsonErr) Error() string { return e.msg }

// --- typed access helpers (mirror serde_json::Value::as_* ) ---

// asObject returns v as a *jsonObject, or nil if it isn't one.
func asObject(v any) *jsonObject {
	o, _ := v.(*jsonObject)
	return o
}

// asArray returns v as []any, or nil if it isn't one.
func asArray(v any) []any {
	a, _ := v.([]any)
	return a
}

// asString returns (s, true) if v is a JSON string.
func asString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// strOr returns the string value of v, or def when v is not a string.
func strOr(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

// asI64 returns (n, true) if v is a JSON number representable as int64.
func asI64(v any) (int64, bool) {
	n, ok := v.(jsonNumber)
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil {
		return 0, false
	}
	return i, true
}

// i64Or returns the int64 value of v, or def when v is not an integer number.
func i64Or(v any, def int64) int64 {
	if i, ok := asI64(v); ok {
		return i
	}
	return def
}

// asF64 returns (f, true) if v is a JSON number representable as float64.
func asF64(v any) (float64, bool) {
	n, ok := v.(jsonNumber)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// isObject reports whether v is a JSON object.
func isObject(v any) bool {
	_, ok := v.(*jsonObject)
	return ok
}

// get walks nested object fields by key, returning nil if any step is missing
// or not an object. Mirrors chained serde indexing like v["a"]["b"].
func get(v any, keys ...string) any {
	cur := v
	for _, k := range keys {
		o := asObject(cur)
		if o == nil {
			return nil
		}
		cur = o.get(k)
	}
	return cur
}

// marshalCompact serializes a value model node back to compact JSON, matching
// serde_json::to_string: object keys in insertion order, no spaces. rtk builds
// serde_json with the "preserve_order" feature, so its to_string keeps the
// original key order; we mirror that to stay byte-faithful (e.g. a SecretString
// re-serialized as {"username":...,"password":...}).
func marshalCompact(v any) (string, bool) {
	var b bytes.Buffer
	if err := writeCompact(&b, v); err != nil {
		return "", false
	}
	return b.String(), true
}

func writeCompact(b *bytes.Buffer, v any) error {
	switch val := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if val {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case jsonNumber:
		b.WriteString(string(val))
	case string:
		enc, err := json.Marshal(val)
		if err != nil {
			return err
		}
		b.Write(enc)
	case []any:
		b.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCompact(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case *jsonObject:
		b.WriteByte('{')
		keys := val.keys()
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			enc, err := json.Marshal(k)
			if err != nil {
				return err
			}
			b.Write(enc)
			b.WriteByte(':')
			if err := writeCompact(b, val.get(k)); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		enc, err := json.Marshal(val)
		if err != nil {
			return err
		}
		b.Write(enc)
	}
	return nil
}

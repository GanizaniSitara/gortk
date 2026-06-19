package summary

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// jsonKind classifies a parsed top-level JSON value.
type jsonKind int

const (
	jsonScalar jsonKind = iota
	jsonArray
	jsonObject
)

// parseJSONShape parses output as a single JSON value and reports its top-level
// shape, mirroring rtk's summarize_json. For objects it preserves key insertion
// order (rtk builds serde_json with the preserve_order feature, so Go's default
// map ordering would diverge — we stream the tokens instead). The returned
// values:
//   - kind: array / object / scalar
//   - count: array length or object key count
//   - keys: ordered object keys (nil for non-objects)
//   - scalar: the rendered value for the scalar case
//   - ok: false when the input is not valid JSON
func parseJSONShape(output string) (kind jsonKind, count int, keys []string, scalar string, ok bool) {
	trimmed := strings.TrimSpace(output)

	// rtk treats the whole captured output as a single JSON document; reject
	// trailing garbage the same way serde_json::from_str would.
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return jsonScalar, 0, nil, "", false
	}

	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '[':
			n, err := consumeArray(dec)
			if err != nil || trailing(dec) {
				return jsonScalar, 0, nil, "", false
			}
			return jsonArray, n, nil, "", true
		case '{':
			orderedKeys, err := consumeObjectKeys(dec)
			if err != nil || trailing(dec) {
				return jsonScalar, 0, nil, "", false
			}
			return jsonObject, len(orderedKeys), orderedKeys, "", true
		default:
			return jsonScalar, 0, nil, "", false
		}
	default:
		// Scalar (string/number/bool/null). Validate no trailing garbage.
		if trailing(dec) {
			return jsonScalar, 0, nil, "", false
		}
		return jsonScalar, 0, nil, renderScalar(t), true
	}
}

// consumeArray counts the top-level elements of an array whose opening '[' has
// already been read, consuming through the closing ']'.
func consumeArray(dec *json.Decoder) (int, error) {
	count := 0
	for dec.More() {
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return 0, err
		}
		count++
	}
	// Consume the closing ']'.
	if _, err := dec.Token(); err != nil {
		return 0, err
	}
	return count, nil
}

// consumeObjectKeys collects the keys of an object whose opening '{' has already
// been read, in insertion order, consuming through the closing '}'.
func consumeObjectKeys(dec *json.Decoder) ([]string, error) {
	var keys []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, isStr := keyTok.(string)
		if !isStr {
			return nil, fmt.Errorf("non-string object key")
		}
		keys = append(keys, key)
		// Skip the value (any shape).
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
	}
	// Consume the closing '}'.
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return keys, nil
}

// trailing reports whether the decoder has anything left after the first
// complete value — either another valid token or non-whitespace garbage. Only a
// clean EOF means the document was a single value (matching serde_json's
// from_str, which rejects trailing content).
func trailing(dec *json.Decoder) bool {
	_, err := dec.Token()
	return err != io.EOF
}

// renderScalar renders a scalar JSON token roughly the way serde_json's
// Value::to_string would for the summarize_json scalar branch.
func renderScalar(t interface{}) string {
	switch v := t.(type) {
	case nil:
		return "null"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case json.Number:
		return v.String()
	case string:
		// serde_json renders a JSON string value with surrounding quotes.
		b, _ := json.Marshal(v)
		return string(b)
	default:
		return fmt.Sprintf("%v", v)
	}
}

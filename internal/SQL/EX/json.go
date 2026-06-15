package EX

import (
	"encoding/json"
	"fmt"
	"strings"
)

// json.go implements SQL JSON functions and operators (REQ000264+265).
// JSON values are stored as UTF-8 encoded strings. Functions parse
// on demand rather than maintaining a parsed representation.

// parseJSON parses a JSON string into an interface{}.
func parseJSON(s string) (interface{}, error) {
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("json: invalid JSON: %w", err)
	}
	return v, nil
}

// toJSON serializes a value to JSON string.
func toJSON(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// jsonExtract implements json_extract(json, path).
// Path is a simple dot-separated key path like "a.b.c" or "a.0.b".
func jsonExtract(v interface{}, path string) (interface{}, error) {
	if path == "" {
		return v, nil
	}
	parts := strings.Split(path, ".")
	current := v
	for _, part := range parts {
		switch obj := current.(type) {
		case map[string]interface{}:
			val, ok := obj[part]
			if !ok {
				return nil, nil
			}
			current = val
		case []interface{}:
			idx := 0
			if _, err := fmt.Sscanf(part, "%d", &idx); err != nil {
				return nil, nil
			}
			if idx < 0 || idx >= len(obj) {
				return nil, nil
			}
			current = obj[idx]
		default:
			return nil, nil
		}
	}
	return current, nil
}

// jsonType returns the type of a JSON value.
func jsonType(v interface{}) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "integer" // SQLite stores booleans as integers
	case float64:
		return "real"
	case string:
		return "text"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	default:
		return "null"
	}
}

// jsonValid checks if a string is valid JSON.
func jsonValid(s string) bool {
	var v interface{}
	return json.Unmarshal([]byte(s), &v) == nil
}

// jsonArray builds a JSON array from arguments.
func jsonArray(args []interface{}) (string, error) {
	b, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// jsonObject builds a JSON object from key-value pairs.
func jsonObject(args []interface{}) (string, error) {
	if len(args)%2 != 0 {
		return "", fmt.Errorf("json_object(): odd number of arguments")
	}
	m := make(map[string]interface{}, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		key, ok := args[i].(string)
		if !ok {
			return "", fmt.Errorf("json_object(): key must be a string")
		}
		m[key] = args[i+1]
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// jsonSet implements json_set(json, path, value).
func jsonSet(jsonStr string, path string, value interface{}) (string, error) {
	var v interface{}
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return "", fmt.Errorf("json_set: invalid JSON: %w", err)
	}
	parts := strings.Split(path, ".")
	if err := jsonSetPath(v, parts, value); err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func jsonSetPath(v interface{}, parts []string, value interface{}) error {
	if len(parts) == 0 {
		return fmt.Errorf("json_set: empty path")
	}
	part := parts[0]
	rest := parts[1:]

	switch obj := v.(type) {
	case map[string]interface{}:
		if len(rest) == 0 {
			obj[part] = value
			return nil
		}
		child, ok := obj[part]
		if !ok {
			child = make(map[string]interface{})
			obj[part] = child
		}
		return jsonSetPath(child, rest, value)
	case []interface{}:
		idx := 0
		if _, err := fmt.Sscanf(part, "%d", &idx); err != nil {
			return fmt.Errorf("json_set: invalid array index: %s", part)
		}
		if idx < 0 || idx >= len(obj) {
			return fmt.Errorf("json_set: array index out of bounds: %d", idx)
		}
		if len(rest) == 0 {
			obj[idx] = value
			return nil
		}
		return jsonSetPath(obj[idx], rest, value)
	default:
		return fmt.Errorf("json_set: cannot set path on %T", v)
	}
}

// evalJSONFunc evaluates JSON functions.
func evalJSONFunc(name string, args []interface{}) (interface{}, error) {
	switch strings.ToUpper(name) {
	case "JSON_EXTRACT":
		if len(args) < 2 {
			return nil, fmt.Errorf("json_extract(): requires 2 arguments")
		}
		jsonStr, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("json_extract(): first argument must be a string")
		}
		path, ok := args[1].(string)
		if !ok {
			return nil, fmt.Errorf("json_extract(): second argument must be a string")
		}
		v, err := parseJSON(jsonStr)
		if err != nil {
			return nil, err
		}
		result, err := jsonExtract(v, path)
		if err != nil {
			return nil, err
		}
		// Format result as JSON string
		switch r := result.(type) {
		case nil:
			return nil, nil
		case string:
			return r, nil
		case float64, bool:
			return r, nil
		default:
			s, err := toJSON(r)
			if err != nil {
				return nil, err
			}
			return s, nil
		}

	case "JSON_TYPE":
		if len(args) < 1 {
			return nil, fmt.Errorf("json_type(): requires 1 argument")
		}
		jsonStr, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("json_type(): argument must be a string")
		}
		v, err := parseJSON(jsonStr)
		if err != nil {
			return nil, err
		}
		return jsonType(v), nil

	case "JSON_VALID":
		if len(args) < 1 {
			return nil, fmt.Errorf("json_valid(): requires 1 argument")
		}
		jsonStr, ok := args[0].(string)
		if !ok {
			return int64(0), nil
		}
		if jsonValid(jsonStr) {
			return int64(1), nil
		}
		return int64(0), nil

	case "JSON_ARRAY":
		return jsonArray(args)

	case "JSON_OBJECT":
		return jsonObject(args)

	case "JSON_SET":
		if len(args) < 3 {
			return nil, fmt.Errorf("json_set(): requires 3 arguments")
		}
		jsonStr, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("json_set(): first argument must be a string")
		}
		path, ok := args[1].(string)
		if !ok {
			return nil, fmt.Errorf("json_set(): second argument must be a string")
		}
		return jsonSet(jsonStr, path, args[2])

	default:
		return nil, fmt.Errorf("unknown json function: %s", name)
	}
}

// isJSONFunc returns true if the function name is a JSON function.
func isJSONFunc(name string) bool {
	switch strings.ToUpper(name) {
	case "JSON_EXTRACT", "JSON_TYPE", "JSON_VALID", "JSON_ARRAY", "JSON_OBJECT", "JSON_SET":
		return true
	}
	return false
}

// jsonArrowOperator implements the -> operator (extract as JSON).
func jsonArrowOperator(jsonStr string, key string) (interface{}, error) {
	v, err := parseJSON(jsonStr)
	if err != nil {
		return nil, err
	}
	result, err := jsonExtract(v, key)
	if err != nil {
		return nil, err
	}
	s, err := toJSON(result)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// jsonArrowTextOperator implements the ->> operator (extract as text).
func jsonArrowTextOperator(jsonStr string, key string) (interface{}, error) {
	v, err := parseJSON(jsonStr)
	if err != nil {
		return nil, err
	}
	result, err := jsonExtract(v, key)
	if err != nil {
		return nil, err
	}
	switch r := result.(type) {
	case nil:
		return nil, nil
	case string:
		return r, nil
	default:
		return toJSON(r)
	}
}

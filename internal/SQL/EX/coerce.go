package EX

import (
	"fmt"
	"strconv"

	"github.com/cyw0ng95/razordata/internal/SQL/LX"
	"github.com/cyw0ng95/razordata/internal/SQL/PS"
)

// Affinity represents SQLite-like type affinity (REQ000208)
type Affinity int

const (
	AffinityNone    Affinity = iota // No affinity, keep original type
	AffinityText                    // TEXT affinity: convert to string
	AffinityNumeric                 // NUMERIC affinity: try REAL, then INTEGER
	AffinityInteger                 // INTEGER affinity: always INTEGER
	AffinityReal                    // REAL affinity: always floating point
)

// affinityNames for debugging
var affinityNames = map[Affinity]string{
	AffinityNone:    "NONE",
	AffinityText:    "TEXT",
	AffinityNumeric: "NUMERIC",
	AffinityInteger: "INTEGER",
	AffinityReal:    "REAL",
}

func (a Affinity) String() string {
	return affinityNames[a]
}

// TypeToAffinity maps SQL type tokens to affinity (SQLite rules)
// REF: https://www.sqlite.org/datatype3.html#determination_of_column_affinity
func TypeToAffinity(typeInfo *PS.TypeInfo) Affinity {
	if typeInfo == nil {
		return AffinityNone
	}

	switch LX.TokenType(typeInfo.Type) {
	// TEXT affinity
	case LX.T_TEXT, LX.T_VARCHAR:
		return AffinityText

	// INTEGER affinity
	case LX.T_INT_KW, LX.T_INT, LX.T_BIGINT:
		return AffinityInteger

	// REAL affinity
	case LX.T_FLOAT_KW, LX.T_FLOAT:
		return AffinityReal

	// NUMERIC affinity (numbers but allow text)
	case LX.T_NUMERIC, LX.T_DECIMAL, LX.T_BOOL:
		return AffinityNumeric

	// Default: NONE (BLOB-like, no conversion)
	default:
		return AffinityNone
	}
}

// ApplyAffinity applies the affinity rules to convert a value
// SQLite affinity rules:
//   - TEXT: convert to string
//   - INTEGER: convert to int64, or keep original if fails
//   - REAL: convert to float64, or keep original if fails
//   - NUMERIC: try REAL first, then INTEGER, keep original if both fail
//   - NONE: no conversion (BLOB)
func ApplyAffinity(v interface{}, affinity Affinity) (interface{}, error) {
	if v == nil {
		return nil, nil
	}

	switch affinity {
	case AffinityNone:
		return v, nil

	case AffinityText:
		return applyTextAffinity(v)

	case AffinityInteger:
		return applyIntegerAffinity(v)

	case AffinityReal:
		return applyRealAffinity(v)

	case AffinityNumeric:
		return applyNumericAffinity(v)

	default:
		return v, nil
	}
}

func applyTextAffinity(v interface{}) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

func applyIntegerAffinity(v interface{}) (interface{}, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case float64:
		// Truncate to integer
		return int64(x), nil
	case string:
		// Try integer first
		if n, err := strconv.ParseInt(x, 10, 64); err == nil {
			return n, nil
		}
		// Try float, then truncate
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			return int64(f), nil
		}
		// Cannot convert, return original
		return v, nil
	case bool:
		if x {
			return int64(1), nil
		}
		return int64(0), nil
	default:
		return v, nil
	}
}

func applyRealAffinity(v interface{}) (interface{}, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int64:
		return float64(x), nil
	case string:
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			return f, nil
		}
		// Cannot convert, return original
		return v, nil
	default:
		return v, nil
	}
}

func applyNumericAffinity(v interface{}) (interface{}, error) {
	switch x := v.(type) {
	case int64, float64:
		return x, nil
	case string:
		// Try REAL first (NUMERIC prefers REAL)
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			return f, nil
		}
		// Try INTEGER
		if n, err := strconv.ParseInt(x, 10, 64); err == nil {
			return n, nil
		}
		// Cannot convert, return original string
		return v, nil
	case bool:
		if x {
			return int64(1), nil
		}
		return int64(0), nil
	default:
		return v, nil
	}
}

// CompareWithAffinity compares two values using affinity rules
// Returns -1, 0, or 1 like cmp.Compare
func CompareWithAffinity(a, b interface{}, affinity Affinity) (int, error) {
	// Apply affinity to both values
	av, err := ApplyAffinity(a, affinity)
	if err != nil {
		return 0, err
	}
	bv, err := ApplyAffinity(b, affinity)
	if err != nil {
		return 0, err
	}

	return compareValuesInternal(av, bv)
}

// compareValuesInternal returns -1, 0, or 1 for a vs b.
func compareValuesInternal(a, b interface{}) (int, error) {
	// Handle nil
	if a == nil && b == nil {
		return 0, nil
	}
	if a == nil {
		return -1, nil
	}
	if b == nil {
		return 1, nil
	}

	// Type-based comparison
	switch av := a.(type) {
	case string:
		if bv, ok := b.(string); ok {
			if av < bv {
				return -1, nil
			} else if av > bv {
				return 1, nil
			}
			return 0, nil
		}
	case int64:
		if bv, ok := b.(int64); ok {
			if av < bv {
				return -1, nil
			} else if av > bv {
				return 1, nil
			}
			return 0, nil
		}
	case float64:
		if bv, ok := b.(float64); ok {
			if av < bv {
				return -1, nil
			} else if av > bv {
				return 1, nil
			}
			return 0, nil
		}
	case bool:
		if bv, ok := b.(bool); ok {
			if !av && bv {
				return -1, nil
			} else if av && !bv {
				return 1, nil
			}
			return 0, nil
		}
	}

	// Mixed types: convert to string and compare
	as := fmt.Sprintf("%v", a)
	bs := fmt.Sprintf("%v", b)
	if as < bs {
		return -1, nil
	} else if as > bs {
		return 1, nil
	}
	return 0, nil
}

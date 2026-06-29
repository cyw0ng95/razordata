package PL

// cmpFloat compares two float64 values. Returns -1, 0, or 1.
func cmpFloat(a, b float64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// CompareValue compares two Values. Returns -1, 0, or 1.
func CompareValue(a, b Value) int {
	if a.Kind == KindNull && b.Kind == KindNull {
		return 0
	}
	if a.Kind == KindNull {
		return -1
	}
	if b.Kind == KindNull {
		return 1
	}
	if a.Kind == KindInt && b.Kind == KindInt {
		if a.I64 < b.I64 {
			return -1
		}
		if a.I64 > b.I64 {
			return 1
		}
		return 0
	}
	if a.Kind == KindFloat && b.Kind == KindFloat {
		return cmpFloat(a.F64, b.F64)
	}
	if (a.Kind == KindInt || a.Kind == KindFloat) &&
		(b.Kind == KindInt || b.Kind == KindFloat) {
		var af, bf float64
		if a.Kind == KindInt {
			af = float64(a.I64)
		} else {
			af = a.F64
		}
		if b.Kind == KindInt {
			bf = float64(b.I64)
		} else {
			bf = b.F64
		}
		return cmpFloat(af, bf)
	}
	if a.Kind == KindText && b.Kind == KindText {
		return cmpString(a.S, b.S)
	}
	if a.Kind == KindBool && b.Kind == KindBool {
		return cmpBool(a.Bo, b.Bo)
	}
	if a.Kind == KindBlob && b.Kind == KindBlob {
		minLen := len(a.B)
		if len(b.B) < minLen {
			minLen = len(b.B)
		}
		for i := 0; i < minLen; i++ {
			if a.B[i] < b.B[i] {
				return -1
			}
			if a.B[i] > b.B[i] {
				return 1
			}
		}
		if len(a.B) < len(b.B) {
			return -1
		}
		if len(a.B) > len(b.B) {
			return 1
		}
		return 0
	}
	return 0
}

// EqualValueValue compares two Values for equality.
func EqualValueValue(a, b Value) bool {
	if a.Kind == KindNull || b.Kind == KindNull {
		return a.Kind == KindNull && b.Kind == KindNull
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindInt:
		return a.I64 == b.I64
	case KindFloat:
		return a.F64 == b.F64
	case KindText:
		return a.S == b.S
	case KindBool:
		return a.Bo == b.Bo
	case KindBlob:
		if len(a.B) != len(b.B) {
			return false
		}
		for i := range a.B {
			if a.B[i] != b.B[i] {
				return false
			}
		}
		return true
	}
	return false
}

func cmpString(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpBool(a, b bool) int {
	if a == b {
		return 0
	}
	if a {
		return 1
	}
	return -1
}

// EqualValue compares two any-typed values for equality (REQ000754).
// Accepts both Value types and Go primitives, with int/float fast paths.
func EqualValue(a, b any) bool {
	if av, ok := a.(Value); ok {
		a = av.ToAny()
	}
	if bv, ok := b.(Value); ok {
		b = bv.ToAny()
	}
	if a == nil || b == nil {
		return false
	}
	if ai, aok := a.(int64); aok {
		if bi, bok := b.(int64); bok {
			return ai == bi
		}
		if bf, bok := b.(float64); bok {
			return float64(ai) == bf
		}
		if bi, bok := b.(int); bok {
			return ai == int64(bi)
		}
		return false
	}
	if af, aok := a.(float64); aok {
		if bf, bok := b.(float64); bok {
			return af == bf
		}
		if bi, bok := b.(int64); bok {
			return af == float64(bi)
		}
	}
	if ai, aok := a.(int); aok {
		if bi, bok := b.(int); bok {
			return ai == bi
		}
		if bi, bok := b.(int64); bok {
			return int64(ai) == bi
		}
		return false
	}
	return a == b
}
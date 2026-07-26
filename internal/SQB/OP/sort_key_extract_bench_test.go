package OP

import (
	"testing"

	DT "github.com/cyw0ng95/razordata/internal/SQB/DT"
	EV "github.com/cyw0ng95/razordata/internal/SQB/EV"
	PS "github.com/cyw0ng95/razordata/internal/SQF/PS"
)

// extractSortKeys extracts sort keys from a row buffer into a flat key cache.
// This is the core key extraction logic used by Sort's materialization path,
// extracted here for standalone benchmarking without Sort.Next() overhead.
// REQ002038: benchmarks key extraction logic without row-based Next() method.
func extractSortKeys(buf []Row, keys []PS.OrderItem, params []any) ([][]DT.Value, error) {
	n := len(buf)
	if n == 0 {
		return nil, nil
	}

	numKeys := len(keys)

	// Pre-analyze sort key expressions for direct SlotIdx access.
	// REQ001202+: bypasses EvalValue call + type switch + bounds check + EqualFold
	// in the hot inner loop.
	keyAccess := make([]struct {
		isSlot  bool
		slotIdx int
	}, numKeys)

	for j, k := range keys {
		switch e := k.Expr.(type) {
		case *PS.Ident:
			if e.SlotIdx >= 0 && e.SlotIdx < len(buf[0].Data) {
				keyAccess[j] = struct {
					isSlot  bool
					slotIdx int
				}{isSlot: true, slotIdx: e.SlotIdx}
			}
		case *PS.QualifiedName:
			if e.SlotIdx >= 0 && e.SlotIdx < len(buf[0].Data) {
				keyAccess[j] = struct {
					isSlot  bool
					slotIdx int
				}{isSlot: true, slotIdx: e.SlotIdx}
			}
		}
	}

	// REQ001025: use a flat buffer to avoid N allocations.
	keyCache := make([][]DT.Value, n)
	flatKeys := make([]DT.Value, n*numKeys)

	for i, r := range buf {
		sk := flatKeys[i*numKeys : (i+1)*numKeys]
		for j, k := range keys {
			var v DT.Value
			if keyAccess[j].isSlot {
				v = r.Data[keyAccess[j].slotIdx]
			} else {
				var err error
				v, err = EV.EvalValue(k.Expr, &r, params)
				if err != nil {
					return nil, err
				}
			}
			sk[j] = v
		}
		keyCache[i] = sk
	}

	return keyCache, nil
}

func BenchmarkSortExtractKeys(b *testing.B) {
	row := Row{
		Cols: []string{"a", "b", "c", "d", "e"},
		Data: make([]DT.Value, 5),
	}

	// Create 10-row buffer
	buf := make([]Row, 10)
	for i := range buf {
		cp := Row{Cols: row.Cols, Data: make([]DT.Value, 5)}
		for j := range cp.Data {
			cp.Data[j] = DT.Value{Kind: DT.KindInt, I64: int64((10 - i) * 100 + j)}
		}
		cp.Data[3] = DT.Value{Kind: DT.KindText, S: "hello"}
		buf[i] = cp
	}

	// Benchmark key extraction scenarios
	benchKeyExtract := func(name string, keys []PS.OrderItem, buf []Row, b *testing.B) {
		b.Run(name, func(b *testing.B) {
			b.ResetTimer()
			for range b.N {
				_, _ = extractSortKeys(buf, keys, nil)
			}
		})
	}

	// 10-row scenarios
	benchKeyExtract("EvalValue-1key",
		[]PS.OrderItem{{Expr: &PS.Ident{Name: "d", SlotIdx: -1}}}, buf, b)
	benchKeyExtract("SlotIdx-1key",
		[]PS.OrderItem{{Expr: &PS.Ident{Name: "d", SlotIdx: 3}}}, buf, b)
	benchKeyExtract("EvalValue-2keys",
		[]PS.OrderItem{
			{Expr: &PS.Ident{Name: "a", SlotIdx: -1}},
			{Expr: &PS.Ident{Name: "b", SlotIdx: -1}},
		}, buf, b)
	benchKeyExtract("SlotIdx-2keys",
		[]PS.OrderItem{
			{Expr: &PS.Ident{Name: "a", SlotIdx: 0}},
			{Expr: &PS.Ident{Name: "b", SlotIdx: 1}},
		}, buf, b)

	// 100-row scenarios
	bigBuf := make([]Row, 100)
	for i := range bigBuf {
		cp := Row{Cols: row.Cols, Data: make([]DT.Value, 5)}
		for j := range cp.Data {
			cp.Data[j] = DT.Value{Kind: DT.KindInt, I64: int64((100 - i) * 1000 + j)}
		}
		bigBuf[i] = cp
	}

	benchKeyExtract("EvalValue-100rows",
		[]PS.OrderItem{{Expr: &PS.Ident{Name: "c", SlotIdx: -1}}}, bigBuf, b)
	benchKeyExtract("SlotIdx-100rows",
		[]PS.OrderItem{{Expr: &PS.Ident{Name: "c", SlotIdx: 2}}}, bigBuf, b)
}
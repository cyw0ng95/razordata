// REQ001237: rowDataPool reuses []Value backing arrays across operator
// Next() calls. Producers (SeqScan.cloneRow) get slices from the pool;
// consumers (Sort.Close) return them. Slices are zeroed before reuse to
// prevent reference leaks.
//
// REQ002099: rowDataPool removed — the pool was never populated
// (putRowData was never called), so every getRowData fell back to
// make([]Value, n). The sync.Pool eface boxing overhead was worse
// than the direct allocation. Use make([]Value, n) directly.
package OP

func getRowData(n int) []Value {
	return make([]Value, n)
}
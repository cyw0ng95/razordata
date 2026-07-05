// REQ001237: rowDataPool reuses []Value backing arrays across operator
// Next() calls. Producers (SeqScan.cloneRow) get slices from the pool;
// consumers (Sort.Close) return them. Slices are zeroed before reuse to
// prevent reference leaks.
package OP

import (
	"sync"
)

var rowDataPool sync.Pool

func getRowData(n int) []Value {
	if v := rowDataPool.Get(); v != nil {
		buf := *v.(*[]Value)
		if cap(buf) >= n {
			return buf[:n]
		}
	}
	return make([]Value, n)
}

func putRowData(s []Value) {
	for i := range s {
		s[i] = Value{}
	}
	rowDataPool.Put(&s)
}
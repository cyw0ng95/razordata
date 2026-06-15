package VL

import "sync/atomic"

var txnCounter atomic.Uint64

func NextTS() uint64 {
	return txnCounter.Add(1)
}

func GetCurrentTS() uint64 {
	return txnCounter.Load()
}

package ls

import "errors"

// ErrNotFound is returned by Engine.Get when a key is absent.
var ErrNotFound = errors.New("eng: key not found")

// ErrBloomMiss is returned by bloom filter queries when a key is absent.
var ErrBloomMiss = errors.New("bloom filter miss")

package ls

import "errors"

// ErrBloomMiss is returned by bloom filter queries when a key is absent.
var ErrBloomMiss = errors.New("ls: bloom filter miss")

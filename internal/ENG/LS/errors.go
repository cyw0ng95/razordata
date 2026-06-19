package ls

import "errors"

// ErrBloomMiss is returned by bloom filter queries when a key is absent.
var ErrBloomMiss = errors.New("bloom filter miss")

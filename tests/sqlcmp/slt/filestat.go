package slt

// fileStat is the per-file outcome tracked by the corpus
// runner. It lives in its own file (no build tag) so the
// JUnit writer, which is also tag-free, can read it.
type fileStat struct {
	path  string
	stats Stats
}

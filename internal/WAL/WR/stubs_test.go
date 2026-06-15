package wr

// nullWriter is a no-op io.Writer used to silence the logger in tests.
type nullWriter struct{}

func (n *nullWriter) Write(p []byte) (int, error) { return len(p), nil }

//go:build !debug

package EX

// cteTraceSeed is a no-op when debug tag is not set.
func cteTraceSeed(name string, numRows int) {}

// cteTraceIteration is a no-op when debug tag is not set.
func cteTraceIteration(name string, iter int, rowsIn, rowsOut int) {}

// cteTraceMaxIterations is a no-op when debug tag is not set.
func cteTraceMaxIterations(name string) {}
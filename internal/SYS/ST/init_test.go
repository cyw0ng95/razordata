// Package ST_test is an external test package used solely to
// trigger the SE and SY packages' init() side effects. The SE
// package registers its Session constructor with SY in init();
// without this side effect, SY.Begin returns AP.ErrClosed because
// the constructor is nil. Blanking-importing SE and SY from a
// separate _test package (rather than from package ST) avoids
// creating a ST → SE → SY → ST import cycle in the regular test
// compilation.
package ST_test

import (
	_ "github.com/cyw0ng95/razordata/internal/SYS/SE"
	_ "github.com/cyw0ng95/razordata/internal/SYS/SY"
)

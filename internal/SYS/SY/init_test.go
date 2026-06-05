// Package SY_test is an external test package used solely to
// trigger the SE package's init() side effect. The SE package
// registers its Session constructor with SY in init(); without
// this side effect, SY.Begin returns AP.ErrClosed because the
// constructor is nil. Blanking-importing SE from a separate
// _test package (rather than from package SY) avoids creating
// an SY → SE → SY import cycle in the regular test compilation.
package SY_test

import (
	_ "github.com/cyw0ng95/razordata/internal/SYS/SE"
)

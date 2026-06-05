// Package SYS is the public entry point for the razordata database.
//
// Construct an Engine with SY.Open, then call Begin to obtain a
// Session. The session is the unit of concurrency: it holds an
// optional Transaction, a deadline, and per-connection stats.
package SYS

import (
	"context"

	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SE"
	"github.com/cyw0ng95/razordata/internal/SYS/SY"
)

// Open is the convenience entry point. It is equivalent to SY.Open
// but exported from the top-level SYS package so callers don't need
// to import SY directly.
func Open(ctx context.Context, dir string, opts AP.Options) (AP.Engine, error) {
	return SY.Open(ctx, dir, opts)
}

// Compile-time hook to ensure SE.init runs when SYS is imported by
// callers. The blank reference is enough to force package init.
var _ = seBlank

var seBlank = SE.NewSession

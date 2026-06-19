package fl

import (
	"sync/atomic"
	"time"
)

// groupCommitReq represents a single Sync request waiting in the group
// commit pipeline.
type groupCommitReq struct {
	done    chan struct{}
	err     error
	lsn     LSN
	timeout bool
}

// groupCommit is the group commit pipeline coordinator (REQ000542).
// Txns waiting on Sync() join a group; when the first tx arrives or a
// deadline fires, the entire group fsyncs together. Each waiter is
// unblocked with its assigned LSN.
type groupCommit struct {
	reqCh    chan *groupCommitReq
	closed   atomic.Bool
	closedCh chan struct{}

	// fsyncFn performs the actual disk sync (injected by flusher).
	fsyncFn func() error

	// stats
	groupsFlushed atomic.Int64
	batchSizeHist atomic.Int64 // histogram bucket counter
}

// groupCommitOptions configures the group commit pipeline.
type groupCommitOptions struct {
	Timeout time.Duration // default 50µs
}

// newGroupCommit creates a new group commit coordinator.
func newGroupCommit(opts groupCommitOptions) *groupCommit {
	if opts.Timeout == 0 {
		opts.Timeout = 50 * time.Microsecond
	}
	gc := &groupCommit{
		reqCh:    make(chan *groupCommitReq, 1024),
		closedCh: make(chan struct{}),
	}
	go gc.run(opts.Timeout)
	return gc
}

// run is the background group commit loop. It collects pending Sync
// requests and dispatches a single fsync when either:
//   - the first request arrives (immediate flush), or
//   - the deadline (default 50µs) fires.
func (gc *groupCommit) run(timeout time.Duration) {
	var pending []*groupCommitReq
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	// Start with the timer running so the first request triggers immediate flush.
	// We'll stop it before each dispatch.

	for {
		select {
		case req := <-gc.reqCh:
			pending = append(pending, req)
			// If this is the first request in the batch, trigger immediate flush.
			if len(pending) == 1 {
				// Stop the timer if it's running (from previous iteration).
				if !timer.Stop() {
					<-timer.C
				}
				gc.flush(pending)
				pending = pending[:0]
				// Reset timer for next batch.
				timer.Reset(timeout)
			}
		case <-timer.C:
			// Deadline fired — flush whatever we have.
			if len(pending) > 0 {
				gc.flush(pending)
				pending = pending[:0]
			}
			// Reset timer for next batch.
			timer.Reset(timeout)
		case <-gc.closedCh:
			// Drain any remaining pending requests.
			if len(pending) > 0 {
				gc.flush(pending)
			}
			// Drain reqCh.
			for {
				select {
				case req := <-gc.reqCh:
					req.err = ErrFlusherClosed
					close(req.done)
				default:
					goto done
				}
			}
		done:
			return
		}
	}
}

// flush executes the actual fsync for a batch of requests.
// All requests in the batch share the same fsync cost.
func (gc *groupCommit) flush(pending []*groupCommitReq) {
	// Assign LSNs sequentially.
	// The caller (flusher) is responsible for assigning LSNs before
	// sending the request, so we just record them here.
	gc.groupsFlushed.Add(1)
	gc.batchSizeHist.Add(int64(len(pending)))

	var syncErr error
	if gc.fsyncFn != nil {
		syncErr = gc.fsyncFn()
	}

	for _, req := range pending {
		req.err = syncErr
		close(req.done)
	}
}

// SetFsyncFn sets the fsync callback used during batch flush.
func (gc *groupCommit) SetFsyncFn(fn func() error) {
	gc.fsyncFn = fn
}

// Submit submits a Sync request to the group commit pipeline.
// The caller should wait on the returned channel to be unblocked.
func (gc *groupCommit) Submit(req *groupCommitReq) {
	select {
	case gc.reqCh <- req:
	case <-gc.closedCh:
		req.err = ErrFlusherClosed
		close(req.done)
	}
}

// Close shuts down the group commit pipeline.
func (gc *groupCommit) Close() {
	if gc.closed.CompareAndSwap(false, true) {
		close(gc.closedCh)
	}
}

// Stats returns group commit statistics.
func (gc *groupCommit) Stats() GroupCommitStats {
	return GroupCommitStats{
		GroupsFlushed: gc.groupsFlushed.Load(),
	}
}

// GroupCommitStats reports group commit pipeline statistics.
type GroupCommitStats struct {
	GroupsFlushed int64
}

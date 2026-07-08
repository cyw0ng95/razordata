package fl

import (
	"sync/atomic"
	"time"
)

const (
	idleTimerCap    = 1 * time.Millisecond
	activeTimeout   = 50 * time.Microsecond
	idleThreshold   = 1 * time.Millisecond
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

	// lastRequestTime is the unix nano timestamp of the last submitted
	// request, used for idle backoff of the commit timer.
	lastRequestTime atomic.Int64
}

// groupCommitOptions configures the group commit pipeline.
type groupCommitOptions struct {
	Timeout time.Duration // default 50µs
}

// newGroupCommit creates a new group commit coordinator.
func newGroupCommit(opts groupCommitOptions) *groupCommit {
	if opts.Timeout == 0 {
		opts.Timeout = activeTimeout
	}
	gc := &groupCommit{
		reqCh:    make(chan *groupCommitReq, 1024),
		closedCh: make(chan struct{}),
	}
	gc.lastRequestTime.Store(time.Now().UnixNano())
	go gc.run(opts.Timeout)
	return gc
}

// run is the background group commit loop. It collects pending Sync
// requests and dispatches a single fsync when the deadline fires.
// All requests that arrive during the batching window share one fsync.
// Implements adaptive timer: idle periods lengthen the timeout to
// reduce wakeups; new requests immediately shrink it back.
func (gc *groupCommit) run(baseTimeout time.Duration) {
	var pending []*groupCommitReq
	timer := time.NewTimer(baseTimeout)
	defer timer.Stop()

	for {
		select {
		case req := <-gc.reqCh:
			gc.lastRequestTime.Store(time.Now().UnixNano())
			pending = append(pending, req)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(baseTimeout)
		case <-timer.C:
			if len(pending) > 0 {
				gc.flush(pending)
				pending = pending[:0]
			}
			// Adaptive timer: if no request arrived for >1ms, back off.
			last := gc.lastRequestTime.Load()
			nextTimeout := baseTimeout
			if last > 0 && time.Since(time.Unix(0, last)) > idleThreshold {
				nextTimeout = idleTimerCap
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(nextTimeout)
		case <-gc.closedCh:
			// Pending requests that have not yet been dispatched
			// are drained with ErrFlusherClosed. Do not flush —
			// the flusher is being torn down and fsync would race
			// with the close.
			for _, req := range pending {
				req.err = ErrFlusherClosed
				close(req.done)
			}
			pending = nil
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

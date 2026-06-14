//go:build linux && !no_uring

// Package uring provides a Linux io_uring wrapper for the FIL
// subsystem. REQ000295 (iter-27).
//
// The wrapper exposes a Ring that manages the SQ/CQ rings via
// mmap and supports pread/pwrite/fsync SQE preparation with
// submit-and-wait. Linked submission (write→fsync via
// IOSQE_IO_LINK) is exposed for REQ000301.
//
// The package is gated on linux + a build tag (negated by
// `no_uring` for non-Linux CI).
package uring

import (
	"errors"
	"fmt"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// SQE opcodes.
const (
	IORING_OP_NOP         = 0
	IORING_OP_READV       = 1
	IORING_OP_WRITEV      = 2
	IORING_OP_FSYNC       = 3
	IORING_OP_READ_FIXED  = 7
	IORING_OP_WRITE_FIXED = 8
)

// SQE flags.
const (
	IOSQE_FIXED_FILE  = 1 << 0
	IOSQE_IO_DRAIN    = 1 << 1
	IOSQE_IO_LINK     = 1 << 2
	IOSQE_IO_HARDLINK = 1 << 3
)

// Fsync flags.
const IORING_FSYNC_DATASYNC = 1

// Register/enter flags.
const (
	IORING_ENTER_GETEVENTS       = 1
	IORING_REGISTER_FIXED_FILE   = 4
	IORING_UNREGISTER_FIXED_FILE = 5
	IORING_UNREGISTER_FILES      = 11
)

// Syscall numbers (x86_64 / aarch64).
const (
	sysIoUringSetup    = 425
	sysIoUringEnter    = 426
	sysIoUringRegister = 427
)

const sqeSize = 64
const cqeSize = 16

// uring_sqe mirrors `struct io_uring_sqe` (64 bytes).
type uring_sqe struct {
	opcode   uint8     // 0
	flags    uint8     // 1
	ioprio   uint16    // 2-3
	fd       int32     // 4-7
	off      uint64    // 8-15
	addr     uint64    // 16-23
	len      uint32    // 24-27
	rwFlags  uint32    // 28-31
	userData uint64    // 32-39
	bufIndex uint16    // 40-41
	_        uint16    // 42-43
	_        int32     // 44-47
	_        uint64    // 48-55
	_        uint64    // 56-63
}

// uring_cqe mirrors `struct io_uring_cqe` (16 bytes).
type uring_cqe struct {
	userData uint64
	res      int32
	flags    uint32
}

// uring_params mirrors `struct io_uring_params` (120 bytes).
type uring_params struct {
	sqEntries    uint32     // 0-3
	cqEntries    uint32     // 4-7
	flags        uint32     // 8-11
	sqThreadIdle uint32     // 12-15
	sqThreadCpu  uint32     // 16-19
	features     uint32     // 20-23
	wqFd         uint32     // 24-27
	resv         [4]uint32  // 28-43
	sqOff        [8]uint32  // 44-75
	cqOff        [8]uint32  // 76-107
	resv2        [3]uint32  // 108-119
}

const sizeofParams = 120

// Ring wraps a Linux io_uring instance. REQ000295 (iter-27).
type Ring struct {
	fd         int
	closed     atomic.Bool
	entries    uint32
	ringMask   uint32
	cqMask     uint32
	sqHead     *uint32
	sqTail     *uint32
	sqArray    *uint32
	sqeRing    []byte
	cqHead     *uint32
	cqTail     *uint32
	cqOverflow *uint32
	cqRing     []byte
	mmapSq     []byte
	mmapCq     []byte
}

// mmap calls syscall.Mmap and returns the slice and errno.
func mmap(fd int, offset int64, length int) ([]byte, syscall.Errno) {
	area, err := syscall.Mmap(fd, offset, length,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_SHARED|syscall.MAP_POPULATE)
	if err != nil {
		return nil, err.(syscall.Errno)
	}
	return area, 0
}

// New creates a new io_uring instance.
func New(entries int) (*Ring, error) {
	if entries <= 0 {
		return nil, errors.New("uring: entries must be > 0")
	}
	var params uring_params
	fd, _, errno := syscall.Syscall(
		sysIoUringSetup, uintptr(entries),
		uintptr(unsafe.Pointer(&params)), sizeofParams,
	)
	if errno != 0 {
		return nil, fmt.Errorf("uring: setup: %w", syscall.Errno(errno))
	}

	r := &Ring{
		fd:       int(fd),
		entries:  params.sqEntries,
		ringMask: params.sqEntries - 1,
		cqMask:   params.cqEntries - 1,
	}

	// mmap SQ ring (head, tail, ringMask, entries, flags, dropped, array).
	// The kernel maps all SQ ring structures at offset 0, with the SQ
	// array immediately after the ring fields.
	sqRingSize := int(params.sqOff[6]) + int(params.sqEntries)*4
	sqArea, sqErrno := mmap(int(fd), 0, sqRingSize)
	if sqErrno != 0 {
		syscall.Close(int(fd))
		return nil, fmt.Errorf("uring: mmap sq: %w", sqErrno)
	}
	r.mmapSq = sqArea

	// mmap the SQE array (may be at a different offset).
	sqeOff := params.sqOff[7]
	sqeBytes := int(params.sqEntries) * sqeSize
	r.sqeRing, errno = mmap(int(fd), int64(sqeOff), sqeBytes)
	if errno != 0 {
		syscall.Munmap(r.mmapSq)
		syscall.Close(int(fd))
		return nil, fmt.Errorf("uring: mmap sqe: %w", errno)
	}

	// mmap CQ ring (may share with SQ ring).
	if params.cqOff[0] != params.sqOff[0] {
		cqRingSize := int(params.cqOff[4]) + int(params.cqEntries)*cqeSize
		r.mmapCq, errno = mmap(int(fd), 0, cqRingSize)
		if errno != 0 {
			syscall.Munmap(r.mmapSq)
			if &r.sqeRing[0] != &r.mmapSq[0] {
				syscall.Munmap(r.sqeRing)
			}
			syscall.Close(int(fd))
			return nil, fmt.Errorf("uring: mmap cq: %w", errno)
		}
	} else {
		r.mmapCq = r.mmapSq
	}

	// Pointers into mmap'd regions.
	r.sqHead = (*uint32)(unsafe.Pointer(&r.mmapSq[params.sqOff[0]]))
	r.sqTail = (*uint32)(unsafe.Pointer(&r.mmapSq[params.sqOff[1]]))
	r.sqArray = (*uint32)(unsafe.Pointer(&r.mmapSq[params.sqOff[6]]))

	r.cqHead = (*uint32)(unsafe.Pointer(&r.mmapCq[params.cqOff[0]]))
	r.cqTail = (*uint32)(unsafe.Pointer(&r.mmapCq[params.cqOff[1]]))
	r.cqOverflow = (*uint32)(unsafe.Pointer(&r.mmapCq[params.cqOff[4]]))
	r.cqRing = r.mmapCq[params.cqOff[5]:]

	return r, nil
}

// Close releases the io_uring resources.
func (r *Ring) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}
	var firstErr error
	if r.mmapSq != nil {
		if err := syscall.Munmap(r.mmapSq); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if r.sqeRing != nil && &r.sqeRing[0] != &r.mmapSq[0] {
		if err := syscall.Munmap(r.sqeRing); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if r.mmapCq != nil && &r.mmapCq[0] != &r.mmapSq[0] {
		if err := syscall.Munmap(r.mmapCq); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := syscall.Close(r.fd); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// Fd returns the underlying io_uring fd.
func (r *Ring) Fd() int { return r.fd }

// SubmitWait submits pending SQEs and waits for at least
// `want` completions. Returns the number of completions reaped.
func (r *Ring) SubmitWait(want int) (int, error) {
	if r.closed.Load() {
		return 0, errors.New("uring: ring is closed")
	}
	tail := atomic.LoadUint32(r.sqTail)
	head := atomic.LoadUint32(r.sqHead)
	need := tail - head
	if need == 0 && want == 0 {
		return 0, nil
	}
	var flags uintptr
	if want > 0 {
		flags = uintptr(IORING_ENTER_GETEVENTS)
	}
	_, _, errno := syscall.Syscall6(
		sysIoUringEnter, uintptr(r.fd),
		uintptr(need), uintptr(want),
		flags, 0, 0,
	)
	if errno != 0 {
		return 0, fmt.Errorf("uring: enter: %w", syscall.Errno(errno))
	}
	return r.cqAvailable(), nil
}

// Sqe returns a pointer to an SQE slot. Populate it then call
// SubmitWait.
func (r *Ring) Sqe() (*uring_sqe, error) {
	if r.closed.Load() {
		return nil, errors.New("uring: ring is closed")
	}
	for {
		tail := atomic.LoadUint32(r.sqTail)
		head := atomic.LoadUint32(r.sqHead)
		if tail-head >= r.entries {
			if _, err := r.SubmitWait(1); err != nil {
				return nil, err
			}
			continue
		}
		if atomic.CompareAndSwapUint32(r.sqTail, tail, tail+1) {
			idx := tail & r.ringMask
			base := uintptr(unsafe.Pointer(&r.sqeRing[0]))
			entry := (*uring_sqe)(unsafe.Pointer(base + uintptr(idx)*sqeSize))
			*entry = uring_sqe{}
			return entry, nil
		}
	}
}

// PrepRead prepares a pread SQE.
func (s *uring_sqe) PrepRead(fd int, buf []byte, offset int64) {
	s.opcode = IORING_OP_READV
	s.fd = int32(fd)
	s.off = uint64(offset)
	s.addr = uint64(uintptr(unsafe.Pointer(&buf[0])))
	s.len = uint32(len(buf))
}

// PrepWrite prepares a pwrite SQE.
func (s *uring_sqe) PrepWrite(fd int, buf []byte, offset int64) {
	s.opcode = IORING_OP_WRITEV
	s.fd = int32(fd)
	s.off = uint64(offset)
	s.addr = uint64(uintptr(unsafe.Pointer(&buf[0])))
	s.len = uint32(len(buf))
}

// PrepFsync prepares an fsync SQE.
func (s *uring_sqe) PrepFsync(fd int) {
	s.opcode = IORING_OP_FSYNC
	s.fd = int32(fd)
	s.len = IORING_FSYNC_DATASYNC
}

// SetUserData sets the user data for the SQE.
func (s *uring_sqe) SetUserData(n uint64) { s.userData = n }

// SetFlags sets SQE flags (e.g. IOSQE_IO_LINK).
func (s *uring_sqe) SetFlags(f uint8) { s.flags = f }

// Cqe is a single completion entry.
type Cqe struct {
	UserData uint64
	Res      int32
	Flags    uint32
}

// CqeCount returns the number of pending completions.
func (r *Ring) CqeCount() int {
	return int(atomic.LoadUint32(r.cqTail)) - int(atomic.LoadUint32(r.cqHead))
}

// PeekCqe returns the next completion entry without consuming.
func (r *Ring) PeekCqe() (Cqe, bool) {
	head := atomic.LoadUint32(r.cqHead)
	tail := atomic.LoadUint32(r.cqTail)
	if head == tail {
		return Cqe{}, false
	}
	idx := head & r.cqMask
	base := uintptr(unsafe.Pointer(&r.cqRing[0]))
	entry := (*uring_cqe)(unsafe.Pointer(base + uintptr(idx)*cqeSize))
	return Cqe{
		UserData: entry.userData,
		Res:      entry.res,
		Flags:    entry.flags,
	}, true
}

// ConsumeCqe advances the CQ head by one.
func (r *Ring) ConsumeCqe() {
	atomic.AddUint32(r.cqHead, 1)
}

// RegisterFixedFile registers a process fd with the io_uring
// instance. Returns the assigned slot index.
func (r *Ring) RegisterFixedFile(fd int) (int, error) {
	if r.closed.Load() {
		return -1, errors.New("uring: ring is closed")
	}
	var index uint32
	_, _, errno := syscall.Syscall6(
		sysIoUringRegister, uintptr(r.fd),
		uintptr(IORING_REGISTER_FIXED_FILE),
		uintptr(unsafe.Pointer(&fd)),
		uintptr(1),
		uintptr(unsafe.Pointer(&index)),
		0,
	)
	if errno != 0 {
		return -1, fmt.Errorf("uring: register fixed file: %w", syscall.Errno(errno))
	}
	return int(index), nil
}

// UnregisterFixedFile removes a previously-registered fixed file.
func (r *Ring) UnregisterFixedFile(index int) error {
	if r.closed.Load() {
		return errors.New("uring: ring is closed")
	}
	var idx uint32 = uint32(index)
	_, _, errno := syscall.Syscall6(
		sysIoUringRegister, uintptr(r.fd),
		uintptr(IORING_UNREGISTER_FIXED_FILE),
		uintptr(unsafe.Pointer(&idx)),
		uintptr(1),
		0, 0,
	)
	if errno != 0 {
		return fmt.Errorf("uring: unregister fixed file: %w", syscall.Errno(errno))
	}
	return nil
}

func (r *Ring) cqAvailable() int {
	h := atomic.LoadUint32(r.cqHead)
	t := atomic.LoadUint32(r.cqTail)
	return int(t - h)
}

// ErrUnsupported is returned by Ring methods on non-Linux
// platforms.
var ErrUnsupported = errors.New("uring: io_uring not supported on this platform")

//go:build !linux

// Package uring provides a non-Linux fallback that exposes the
// same interface as the Linux io_uring wrapper. On non-Linux
// platforms, the methods return ErrUnsupported.
// REQ000295 (iter-27).
package uring

import "errors"

const (
	IORING_OP_NOP         = 0
	IORING_OP_READV       = 1
	IORING_OP_WRITEV      = 2
	IORING_OP_FSYNC       = 3
	IORING_OP_READ_FIXED  = 7
	IORING_OP_WRITE_FIXED = 8
	IORING_FSYNC_DATASYNC = 1
)

const (
	IOSQE_FIXED_FILE  = 1 << 0
	IOSQE_IO_DRAIN    = 1 << 1
	IOSQE_IO_LINK     = 1 << 2
	IOSQE_IO_HARDLINK = 1 << 3
)

const (
	IORING_ENTER_GETEVENTS       = 1
	IORING_REGISTER_FIXED_FILE   = 4
	IORING_UNREGISTER_FIXED_FILE = 5
	IORING_UNREGISTER_FILES      = 11
)

type uring_sqe struct {
	opcode      uint8
	flags       uint8
	ioprio      uint16
	fd          int32
	off         uint64
	addr        uint64
	len         uint32
	rwFlags     uint32
	userData    uint64
	bufIndex    uint16
	personality uint16
	spliceFd    int32
	pad2        uint64
}

type Cqe struct {
	UserData uint64
	Res      int32
	Flags    uint32
}

type Ring struct{}

func New(entries int) (*Ring, error) {
	if entries <= 0 {
		return nil, errors.New("uring: entries must be > 0")
	}
	return &Ring{}, nil
}

func (r *Ring) Close() error                     { return nil }
func (r *Ring) Fd() int                          { return -1 }
func (r *Ring) SubmitWait(want int) (int, error) { return 0, ErrUnsupported }
func (r *Ring) Sqe() (*uring_sqe, error)         { return nil, ErrUnsupported }
func (r *Ring) CqeCount() int                    { return 0 }
func (r *Ring) PeekCqe() (Cqe, bool)             { return Cqe{}, false }
func (r *Ring) ConsumeCqe()                      {}

func (r *Ring) RegisterFixedFile(fd int) (int, error) {
	return -1, ErrUnsupported
}
func (r *Ring) UnregisterFixedFile(index int) error {
	return ErrUnsupported
}

func (s *uring_sqe) PrepRead(fd int, buf []byte, offset int64)  {}
func (s *uring_sqe) PrepWrite(fd int, buf []byte, offset int64) {}
func (s *uring_sqe) PrepFsync(fd int)                           {}
func (s *uring_sqe) SetUserData(n uint64)                       {}
func (s *uring_sqe) SetFlags(f uint8)                           {}

var ErrUnsupported = errors.New("uring: io_uring not supported on this platform")

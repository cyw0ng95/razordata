package df

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/cyw0ng95/razordata/internal/FIL/IO"
	"github.com/cyw0ng95/razordata/internal/LOG/EC"
	"github.com/cyw0ng95/razordata/internal/LOG/LG"
	"golang.org/x/sys/unix"
)

const (
	DefaultBlockSize = 4096
	ChecksumLen      = 4
	DataLen          = DefaultBlockSize - ChecksumLen
)

var (
	ErrCorrupt         = errors.New("block checksum mismatch: data corrupted")
	ErrIO              = errors.New("I/O error")
	ErrBigBlock        = errors.New("data exceeds block capacity")
	ErrClosed          = errors.New("block device is closed")
	ErrDiskFull        = errors.New("disk full (ENOSPC)")
	errMmapUnsupported = errors.New("mmap not supported on this platform")
)

// wrapWriteError checks for ENOSPC and returns ErrDiskFull; otherwise
// wraps the error as ErrIO.
func wrapWriteError(err error) error {
	if err == nil {
		return nil
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == unix.ENOSPC {
		return fmt.Errorf("%w: %v", ErrDiskFull, err)
	}
	return fmt.Errorf("%w: %v", ErrIO, err)
}

var bufPool = sync.Pool{
	New: func() any {
		// Allocate extra padding bytes for alignment.
		data := make([]byte, DefaultBlockSize+4096)
		// O(1) alignment: compute the offset to the first 4096-aligned
		// address within the slice. This replaces the O(n) byte-scan
		// loop (REQ000604).
		base := uintptr(unsafe.Pointer(&data[0]))
		alignOffset := (uintptr(4096) - (base & 4095)) & 4095
		if base&4095 != 0 {
			alignOffset = 4096 - (base & 4095)
		}
		buf := data[alignOffset : alignOffset+DefaultBlockSize]
		return &buf
	},
}

type BlockDevice struct {
	fd      int
	direct  bool
	mmap    bool // true: reads via mmap; writes still pwrite
	mmapSz  int
	mmapBuf []byte
	log     lg.Logger
	mu      sync.RWMutex // protects fd: concurrent-close protection (REQ000600)
	Ring    *uring.Ring  // io_uring ring (REQ001202), nil on unsupported platforms
	ringMu  sync.Mutex   // serializes ring I/O operations
}

func Open(path string, log ...lg.Logger) (*BlockDevice, error) {
	return openFile(path, false, false, log)
}
func Create(path string, log ...lg.Logger) (*BlockDevice, error) {
	return openFile(path, false, true, log)
}

func OpenReadOnly(path string, log ...lg.Logger) (*BlockDevice, error) {
	return openFile(path, true, false, log)
}

// OpenMmap opens a file with mmap'd reads and pwrite writes.
func OpenMmap(path string, log ...lg.Logger) (*BlockDevice, error) {
	bd, err := Open(path, log...)
	if err != nil {
		return nil, err
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(bd.fd, &st); err != nil {
		bd.Close()
		return nil, err
	}
	if st.Size == 0 {
		return bd, nil
	}
	mapped, err := mmapBlock(bd.fd, 0, int(st.Size))
	if err != nil {
		return bd, nil
	}
	bd.mmap = true
	bd.mmapSz = int(st.Size)
	bd.mmapBuf = mapped
	return bd, nil
}

func openFile(path string, readOnly, create bool, logs []lg.Logger) (*BlockDevice, error) {
	log := lg.FirstLogger(logs)

	flags := unix.O_RDWR
	if readOnly {
		flags = unix.O_RDONLY
	}
	if create {
		flags |= unix.O_CREAT | unix.O_EXCL
	}

	fd, err := unix.Open(path, flags, 0600)
	if err != nil {
		return nil, err
	}

	bd := &BlockDevice{fd: fd, log: log}

	if !readOnly && supportsODirect() {
		// REQ001139: use fcntl to set O_DIRECT on the existing fd
		// atomically instead of close+reopen (avoids race window).
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags|unix.O_DIRECT); err == nil {
			bd.direct = true
		} else {
			if log != nil {
				log.Info("df.open", "path", path, "msg", "O_DIRECT not supported via fcntl, using buffered I/O", "err", err)
			}
		}
	}

	if runtime.GOOS == "linux" {
		ring, err := uring.New(256)
		if err != nil {
			if log != nil {
				log.Info("df.open", "path", path, "msg", "io_uring unavailable, using synchronous I/O", "err", err)
			}
		} else if err := ring.Validate(); err != nil {
			ring.Close()
			if log != nil {
				log.Info("df.open", "path", path, "msg", "io_uring validation failed, using synchronous I/O", "err", err)
			}
		} else {
			bd.Ring = ring
		}
	}

	return bd, nil
}

func supportsODirect() bool {
	fd, err := unix.Open("/dev/null", unix.O_RDWR|unix.O_DIRECT, 0)
	if err == nil {
		unix.Close(fd)
		return true
	}
	return false
}

func (d *BlockDevice) readAt(fd int, buf []byte, offset int64) (int, error) {
	if d.Ring == nil {
		return unix.Pread(fd, buf, offset)
	}
	d.ringMu.Lock()
	defer d.ringMu.Unlock()

	sqe, err := d.Ring.Sqe()
	if err != nil {
		d.Ring.Close()
		d.Ring = nil
		return unix.Pread(fd, buf, offset)
	}
	sqe.PrepRead(fd, buf, offset)

	if _, err := d.Ring.SubmitWait(1); err != nil {
		d.Ring.Close()
		d.Ring = nil
		return unix.Pread(fd, buf, offset)
	}

	cqe, ok := d.Ring.PeekCqe()
	if !ok {
		d.Ring.Close()
		d.Ring = nil
		return unix.Pread(fd, buf, offset)
	}
	d.Ring.ConsumeCqe()
	if cqe.Res < 0 {
		return 0, syscall.Errno(-cqe.Res)
	}
	return int(cqe.Res), nil
}

func (d *BlockDevice) writeAt(fd int, buf []byte, offset int64) (int, error) {
	if d.Ring == nil {
		return unix.Pwrite(fd, buf, offset)
	}
	d.ringMu.Lock()
	defer d.ringMu.Unlock()

	sqe, err := d.Ring.Sqe()
	if err != nil {
		d.Ring.Close()
		d.Ring = nil
		return unix.Pwrite(fd, buf, offset)
	}
	sqe.PrepWrite(fd, buf, offset)

	if _, err := d.Ring.SubmitWait(1); err != nil {
		d.Ring.Close()
		d.Ring = nil
		return unix.Pwrite(fd, buf, offset)
	}

	cqe, ok := d.Ring.PeekCqe()
	if !ok {
		d.Ring.Close()
		d.Ring = nil
		return unix.Pwrite(fd, buf, offset)
	}
	d.Ring.ConsumeCqe()
	if cqe.Res < 0 {
		return 0, syscall.Errno(-cqe.Res)
	}
	return int(cqe.Res), nil
}

// SubmitBatch submits pending SQEs and waits for count completions with a
// single io_uring_enter syscall. Callers pre-stage SQEs via d.Ring.Sqe()
// before calling SubmitBatch.
func (d *BlockDevice) SubmitBatch(count int) (int, error) {
	if d.Ring == nil {
		return 0, uring.ErrUnsupported
	}
	d.ringMu.Lock()
	defer d.ringMu.Unlock()
	return d.Ring.SubmitWait(count)
}

// ReadBlock reads block blockID into buf.
func (d *BlockDevice) ReadBlock(_ context.Context, blockID uint64, n int, buf []byte) error {
	if len(buf) < DataLen {
		return ErrBigBlock
	}
	if n < 0 || n > DataLen-ChecksumLen {
		return ErrBigBlock
	}

	offset := blockID * uint64(DefaultBlockSize)

	tmp := borrowTempBuf()
	defer returnTempBuf(tmp)

	d.mu.RLock()
	fd := d.fd
	d.mu.RUnlock()

	if d.mmap && d.mmapBuf != nil {
		off := int64(offset)
		if off+int64(len(tmp)) > int64(d.mmapSz) {
			return ErrIO
		}
		copy(tmp, d.mmapBuf[off:off+int64(len(tmp))])
	} else {
		nn, err := d.readAt(fd, tmp, int64(offset))
		if err != nil {
			if d.log != nil {
				d.log.Error("df.read_block", "blockID", blockID, "err", err)
			}
			return err
		}
		if nn < DefaultBlockSize {
			return ErrIO
		}
	}

	storedSum := binary.LittleEndian.Uint32(tmp[DataLen-ChecksumLen:])
	computedSum := crc32.ChecksumIEEE(tmp[:n])
	EC.BUG_ON(storedSum != computedSum, "df.ReadBlock: CRC mismatch block %d, stored=%08x computed=%08x", blockID, storedSum, computedSum)
	if storedSum != computedSum {
		return ErrCorrupt
	}

	copy(buf, tmp[:n])
	return nil
}

// WriteBlock writes data as block blockID.
func (d *BlockDevice) WriteBlock(_ context.Context, blockID uint64, data []byte) error {
	n := len(data)
	if n > DataLen-ChecksumLen {
		return ErrBigBlock
	}

	offset := blockID * uint64(DefaultBlockSize)

	d.mu.RLock()
	fd := d.fd
	d.mu.RUnlock()

	if d.direct {
		poolBuf := *bufPool.Get().(*[]byte)
		defer bufPool.Put(&poolBuf)

		for i := n; i < DataLen-ChecksumLen; i++ {
			poolBuf[i] = 0
		}
		copy(poolBuf, data)
		sum := crc32.ChecksumIEEE(poolBuf[:n])
		binary.LittleEndian.PutUint32(poolBuf[DataLen-ChecksumLen:DataLen], sum)

		written, err := d.writeAt(fd, poolBuf[:], int64(offset))
		EC.BUG_ON(err == nil && written != DefaultBlockSize, "df.WriteBlock: short write block %d, wrote %d bytes, want %d", blockID, written, DefaultBlockSize)
		if err != nil {
			if d.log != nil {
				d.log.Error("df.write_block", "blockID", blockID, "err", err)
			}
			return wrapWriteError(err)
		}
		return nil
	}

	tmp := borrowTempBuf()
	defer returnTempBuf(tmp)

	for i := range tmp[:DataLen-ChecksumLen] {
		tmp[i] = 0
	}
	copy(tmp, data)
	sum := crc32.ChecksumIEEE(tmp[:n])
	binary.LittleEndian.PutUint32(tmp[DataLen-ChecksumLen:DataLen], sum)

	written, err := d.writeAt(fd, tmp[:], int64(offset))
	EC.BUG_ON(err == nil && written != DefaultBlockSize, "df.WriteBlock: short write block %d, wrote %d bytes, want %d", blockID, written, DefaultBlockSize)
	if err != nil {
		if d.log != nil {
			d.log.Error("df.write_block", "blockID", blockID, "err", err)
		}
		return wrapWriteError(err)
	}
	return nil
}

// ReadBlockFull reads a full block with checksum verification.
func (d *BlockDevice) ReadBlockFull(blockID uint64, buf []byte) error {
	if len(buf) < DataLen {
		return ErrBigBlock
	}

	offset := blockID * uint64(DefaultBlockSize)

	d.mu.RLock()
	fd := d.fd
	d.mu.RUnlock()

	tmp := borrowTempBuf()
	defer returnTempBuf(tmp)

	nn, err := d.readAt(fd, tmp, int64(offset))
	if err != nil {
		if d.log != nil {
			d.log.Error("df.read_block_full", "blockID", blockID, "err", err)
		}
		return err
	}
	if nn < DefaultBlockSize {
		return ErrIO
	}

	storedSum := binary.LittleEndian.Uint32(tmp[DataLen-ChecksumLen:])
	computedSum := crc32.ChecksumIEEE(tmp[:DataLen-ChecksumLen])
	if storedSum != computedSum {
		return ErrCorrupt
	}

	copy(buf, tmp[:DataLen-ChecksumLen])
	return nil
}

func (d *BlockDevice) Sync() error {
	d.mu.RLock()
	fd := d.fd
	d.mu.RUnlock()

	if fd == -1 {
		return nil
	}
	err := unix.Fsync(fd)
	if err != nil && d.log != nil {
		d.log.Error("df.sync", "err", err)
	}
	return err
}

func (d *BlockDevice) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.mmapBuf != nil {
		_ = munmapBlock(d.mmapBuf)
		d.mmapBuf = nil
	}
	if d.Ring != nil {
		_ = d.Ring.Close()
		d.Ring = nil
	}
	if d.fd == -1 {
		return nil
	}
	err := unix.Close(d.fd)
	d.fd = -1
	if err != nil && d.log != nil {
		d.log.Error("df.close", "err", err)
	}
	return err
}

func (d *BlockDevice) Size() (int64, error) {
	d.mu.RLock()
	fd := d.fd
	d.mu.RUnlock()

	if fd == -1 {
		return 0, ErrClosed
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		if d.log != nil {
			d.log.Error("df.size", "err", err)
		}
		return 0, err
	}
	return stat.Size, nil
}

var tempBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, DefaultBlockSize)
		return &b
	},
}

func borrowTempBuf() []byte { return *tempBufPool.Get().(*[]byte) }
func returnTempBuf(b []byte) {
	tempBufPool.Put(&b)
}

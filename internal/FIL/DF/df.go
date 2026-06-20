package df

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"sync"
	"syscall"
	"unsafe"

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
	errMmapUnsupported = errors.New("mmap not supported on this platform")
)

var bufPool = sync.Pool{
	New: func() any {
		// Allocate extra padding bytes for alignment.
		data := make([]byte, DefaultBlockSize+4096)
		// O(1) alignment: compute the offset to the first 4096-aligned
		// address within the slice. This replaces the O(n) byte-scan
		// loop (REQ000604).
		base := uintptr(unsafe.Pointer(&data[0]))
		alignMask := ^uintptr(4095)
		alignOffset := (uintptr(4096) - (base & 4095)) & 4095
		alignOffset = alignOffset & alignMask // handle base already aligned
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
	mmap    bool   // true: reads via mmap; writes still pwrite
	mmapSz  int
	mmapBuf []byte
	log     lg.Logger
	mu      sync.RWMutex // protects fd: concurrent-close protection (REQ000600)
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
		unix.Close(fd)
		fd2, err := unix.Open(path, flags|unix.O_DIRECT, 0600)
		if err == nil {
			bd.fd = fd2
			bd.direct = true
		} else {
			if log != nil {
				log.Info("df.open", "path", path, "msg", "O_DIRECT not supported, using buffered I/O", "err", err)
			}
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
		nn, err := unix.Pread(fd, tmp, int64(offset))
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

		_, err := unix.Pwrite(fd, poolBuf[:], int64(offset))
		if err != nil && d.log != nil {
			d.log.Error("df.write_block", "blockID", blockID, "err", err)
		}
		return err
	}

	tmp := borrowTempBuf()
	defer returnTempBuf(tmp)

	for i := range tmp[:DataLen-ChecksumLen] {
		tmp[i] = 0
	}
	copy(tmp, data)
	sum := crc32.ChecksumIEEE(tmp[:n])
	binary.LittleEndian.PutUint32(tmp[DataLen-ChecksumLen:DataLen], sum)

	_, err := unix.Pwrite(fd, tmp[:], int64(offset))
	if err != nil && d.log != nil {
		d.log.Error("df.write_block", "blockID", blockID, "err", err)
	}
	return err
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

	nn, err := unix.Pread(fd, tmp, int64(offset))
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

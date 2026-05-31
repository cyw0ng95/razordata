package df

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"sync"
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
	ErrCorrupt  = errors.New("block checksum mismatch: data corrupted")
	ErrIO       = errors.New("I/O error")
	ErrBigBlock = errors.New("data exceeds block capacity")
)

var bufPool = sync.Pool{
	New: func() any {
		data := make([]byte, DefaultBlockSize+4096)
		for uintptr(unsafe.Pointer(&data[0]))%4096 != 0 {
			data = data[1:]
		}
		return data[:DefaultBlockSize]
	},
}

type BlockDevice struct {
	fd     int
	direct bool
	log    lg.Logger
}

func Open(path string, log ...lg.Logger) (*BlockDevice, error) {
	return openFile(path, false, false, log)
}
func Create(path string, log ...lg.Logger) (*BlockDevice, error) {
	return openFile(path, false, true, log)
}

func openFile(path string, readOnly, create bool, logs []lg.Logger) (*BlockDevice, error) {
	log := firstLogger(logs)

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

func firstLogger(logs []lg.Logger) lg.Logger {
	if len(logs) > 0 {
		return logs[0]
	}
	return nil
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
// n is the number of actual data bytes written (must match what was passed to WriteBlock).
// buf must be at least DataLen bytes.
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

	_, err := unix.Pread(d.fd, tmp, int64(offset))
	if err != nil {
		if d.log != nil {
			d.log.Error("df.read_block", "blockID", blockID, "err", err)
		}
		return err
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
// data must be <= DataLen - ChecksumLen (4092 bytes).
// The checksum covers the first len(data) bytes; remaining data bytes are zeros.
func (d *BlockDevice) WriteBlock(_ context.Context, blockID uint64, data []byte) error {
	n := len(data)
	if n > DataLen-ChecksumLen {
		return ErrBigBlock
	}

	offset := blockID * uint64(DefaultBlockSize)

	if d.direct {
		poolBuf := bufPool.Get().([]byte)
		defer bufPool.Put(poolBuf)

		for i := range poolBuf[:DataLen-ChecksumLen] {
			poolBuf[i] = 0
		}
		copy(poolBuf, data)
		sum := crc32.ChecksumIEEE(poolBuf[:n])
		binary.LittleEndian.PutUint32(poolBuf[DataLen-ChecksumLen:DataLen], sum)

		_, err := unix.Pwrite(d.fd, poolBuf[:], int64(offset))
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

	_, err := unix.Pwrite(d.fd, tmp[:], int64(offset))
	if err != nil && d.log != nil {
		d.log.Error("df.write_block", "blockID", blockID, "err", err)
	}
	return err
}

func (d *BlockDevice) Sync() error {
	if d.fd == -1 {
		return nil
	}
	err := unix.Fsync(d.fd)
	if err != nil && d.log != nil {
		d.log.Error("df.sync", "err", err)
	}
	return err
}

// Close closes the block device. Safe to call multiple times.
func (d *BlockDevice) Close() error {
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
	var stat unix.Stat_t
	if err := unix.Fstat(d.fd, &stat); err != nil {
		if d.log != nil {
			d.log.Error("df.size", "err", err)
		}
		return 0, err
	}
	return stat.Size, nil
}

var tempBufPool = sync.Pool{
	New: func() any {
		return make([]byte, DefaultBlockSize)
	},
}

func borrowTempBuf() []byte { return tempBufPool.Get().([]byte) }
func returnTempBuf(b []byte) {
	for i := range b {
		b[i] = 0
	}
	tempBufPool.Put(b)
}

//go:build debug

package df

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"os/exec"
	"testing"
)

// TestFIL_Assert_Checksum verifies that ReadBlock fires BUG_ON when CRC
// mismatch is detected. The child process writes a valid block, corrupts it
// on disk, and re-reads. BUG_ON(os.Exit(1)) kills the child; parent asserts
// on exit code 1.
func TestFIL_Assert_Checksum(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		dir := t.TempDir()
		path := dir + "/data"

		// Write a valid block 0 manually (full 4096 bytes).
		block := make([]byte, DefaultBlockSize)
		for i := 0; i < DataLen-ChecksumLen; i++ {
			block[i] = byte(i)
		}
		sum := crc32.ChecksumIEEE(block[:DataLen-ChecksumLen])
		binary.LittleEndian.PutUint32(block[DataLen-ChecksumLen:], sum)
		if err := os.WriteFile(path, block, 0o644); err != nil {
			return
		}

		// Open, read once to verify clean path works.
		bd, err := OpenReadOnly(path)
		if err != nil {
			return
		}
		buf := make([]byte, DataLen)
		if err := bd.ReadBlock(nil, 0, DataLen-ChecksumLen, buf); err != nil {
			return
		}
		bd.Close()

		// Corrupt byte 100 of block 0 on disk.
		block[100] ^= 0xFF
		if err := os.WriteFile(path, block, 0o644); err != nil {
			return
		}

		// Re-open and read back — BUG_ON fires, child dies with exit(1).
		bd, err = OpenReadOnly(path)
		if err != nil {
			return
		}
		bd.ReadBlock(nil, 0, DataLen-ChecksumLen, buf)
		bd.Close()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestFIL_Assert_Checksum")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return
	}
	t.Fatal("expected BUG_ON to exit(1), but it didn't")
}

// TestFIL_Assert_ShortWrite verifies that a normal write does NOT trigger
// the short-write BUG_ON (positive path coverage).
func TestFIL_Assert_ShortWrite(t *testing.T) {
	if os.Getenv("TEST_BUG_ON") == "1" {
		dir := t.TempDir()
		path := dir + "/data"
		bd, err := Create(path)
		if err != nil {
			return
		}
		data := make([]byte, DataLen-ChecksumLen)
		if err := bd.WriteBlock(nil, 1, data); err != nil {
			return
		}
		bd.Close()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestFIL_Assert_ShortWrite")
	cmd.Env = append(os.Environ(), "TEST_BUG_ON=1")
	err := cmd.Run()
	if err != nil {
		t.Fatalf("unexpected BUG_ON exit: %v", err)
	}
}
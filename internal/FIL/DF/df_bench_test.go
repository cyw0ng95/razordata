package df

import (
	"context"
	"os"
	"testing"
)

func BenchmarkWriteBlock(b *testing.B) {
	tmp := b.TempDir()
	path := tmp + "/bench.bin"

	bd, err := Create(path)
	if err != nil {
		b.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	data := make([]byte, 1024) // 1 KB of data
	for i := range data {
		data[i] = byte(i % 256)
	}

	ctx := context.Background()
	b.ResetTimer()
	b.SetBytes(1024)

	for i := 0; i < b.N; i++ {
		if err := bd.WriteBlock(ctx, uint64(i%256), data); err != nil {
			b.Fatalf("WriteBlock: %v", err)
		}
	}
}

func BenchmarkWriteBlockFull(b *testing.B) {
	tmp := b.TempDir()
	path := tmp + "/bench.bin"

	bd, err := Create(path)
	if err != nil {
		b.Fatalf("Create: %v", err)
	}
	defer bd.Close()

	data := make([]byte, DataLen-ChecksumLen) // max data per block
	for i := range data {
		data[i] = byte(i % 256)
	}

	ctx := context.Background()
	b.ResetTimer()
	b.SetBytes(int64(DataLen - ChecksumLen))

	for i := 0; i < b.N; i++ {
		if err := bd.WriteBlock(ctx, uint64(i%256), data); err != nil {
			b.Fatalf("WriteBlock: %v", err)
		}
	}
}

func BenchmarkReadBlock(b *testing.B) {
	tmp := b.TempDir()
	path := tmp + "/bench.bin"

	bd, err := Create(path)
	if err != nil {
		b.Fatalf("Create: %v", err)
	}

	data := make([]byte, 1024)
	ctx := context.Background()
	for i := 0; i < 256; i++ {
		bd.WriteBlock(ctx, uint64(i), data)
	}
	bd.Close()

	bd, err = Open(path)
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	buf := make([]byte, DataLen)
	b.ResetTimer()
	b.SetBytes(1024)

	for i := 0; i < b.N; i++ {
		if err := bd.ReadBlock(ctx, uint64(i%256), 1024, buf); err != nil {
			b.Fatalf("ReadBlock: %v", err)
		}
	}
}

func BenchmarkReadBlockFull(b *testing.B) {
	tmp := b.TempDir()
	path := tmp + "/bench.bin"

	bd, err := Create(path)
	if err != nil {
		b.Fatalf("Create: %v", err)
	}

	data := make([]byte, DataLen-ChecksumLen)
	ctx := context.Background()
	for i := 0; i < 256; i++ {
		bd.WriteBlock(ctx, uint64(i), data)
	}
	bd.Close()

	bd, err = Open(path)
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer bd.Close()

	buf := make([]byte, DataLen)
	b.ResetTimer()
	b.SetBytes(int64(DataLen - ChecksumLen))

	for i := 0; i < b.N; i++ {
		if err := bd.ReadBlock(ctx, uint64(i%256), DataLen-ChecksumLen, buf); err != nil {
			b.Fatalf("ReadBlock: %v", err)
		}
	}
}

func BenchmarkWriteReadOnce(b *testing.B) {
	tmp := b.TempDir()
	path := tmp + "/bench.bin"

	ctx := context.Background()
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	b.SetBytes(2048)

	for i := 0; i < b.N; i++ {
		os.RemoveAll(tmp)
		os.MkdirAll(tmp, 0700)

		bd, err := Create(path)
		if err != nil {
			b.Fatalf("Create: %v", err)
		}
		if err := bd.WriteBlock(ctx, 0, data); err != nil {
			b.Fatalf("WriteBlock: %v", err)
		}
		bd.Sync()
		bd.Close()

		bd, err = Open(path)
		if err != nil {
			b.Fatalf("Open: %v", err)
		}
		buf := make([]byte, DataLen)
		if err := bd.ReadBlock(ctx, 0, 1024, buf); err != nil {
			b.Fatalf("ReadBlock: %v", err)
		}
		bd.Close()
	}
}

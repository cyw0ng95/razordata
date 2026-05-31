package mf

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestMarshalUnmarshal(t *testing.T) {
	p := &MetaPage{
		Magic:            MagicValue,
		Version:          1,
		BlockSize:        4096,
		CatalogRootPtr:   0xDEADBEEFCAFEBABE,
		ManifestChecksum: 0x12345678,
	}

	data, err := p.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	if len(data) != metaPageSize {
		t.Fatalf("expected %d bytes, got %d", metaPageSize, len(data))
	}

	p2 := &MetaPage{}
	if err := p2.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}

	if p2.Magic != p.Magic {
		t.Fatalf("Magic: got %x, want %x", p2.Magic, p.Magic)
	}
	if p2.Version != p.Version {
		t.Fatalf("Version: got %d, want %d", p2.Version, p.Version)
	}
	if p2.BlockSize != p.BlockSize {
		t.Fatalf("BlockSize: got %d, want %d", p2.BlockSize, p.BlockSize)
	}
	if p2.CatalogRootPtr != p.CatalogRootPtr {
		t.Fatalf("CatalogRootPtr: got %x, want %x", p2.CatalogRootPtr, p.CatalogRootPtr)
	}
}

func TestDefaultMetaPage(t *testing.T) {
	p := DefaultMetaPage()
	if p.Magic != MagicValue {
		t.Fatalf("Magic: got %x, want %x", p.Magic, MagicValue)
	}
	if p.Version != CurrentVersion {
		t.Fatalf("Version: got %d, want %d", p.Version, CurrentVersion)
	}
	if p.BlockSize != 4096 {
		t.Fatalf("BlockSize: got %d, want 4096", p.BlockSize)
	}
}

func TestMetaReaderWriter(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "meta.razor")

	p := &MetaPage{
		Magic:            MagicValue,
		Version:          CurrentVersion,
		BlockSize:        4096,
		CatalogRootPtr:   0x12345678,
		ManifestChecksum: 0xABCDEF00,
	}

	w := NewMetaWriter(path)
	if err := w.Write(p); err != nil {
		t.Fatalf("Write: %v", err)
	}

	r := NewMetaReader(path)
	p2, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if p2.Magic != p.Magic {
		t.Fatalf("Magic mismatch: got %x, want %x", p2.Magic, p.Magic)
	}
	if p2.Version != p.Version {
		t.Fatalf("Version mismatch: got %d, want %d", p2.Version, p.Version)
	}
	if p2.CatalogRootPtr != p.CatalogRootPtr {
		t.Fatalf("CatalogRootPtr mismatch: got %x, want %x", p2.CatalogRootPtr, p.CatalogRootPtr)
	}
}

func TestMetaReaderCreateIfAbsent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "meta.razor")

	r := NewMetaReader(path)
	p, err := r.Read()
	if err != nil {
		t.Fatalf("Read (create): %v", err)
	}
	if p.Magic != MagicValue {
		t.Fatalf("expected default magic %x, got %x", MagicValue, p.Magic)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("meta.razor should exist after Read: %v", err)
	}
}

func TestMetaReaderBadMagic(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "meta.razor")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	f.Write(make([]byte, metaPageSize))
	f.Close()

	r := NewMetaReader(path)
	_, err = r.Read()
	if err != ErrBadMagic {
		t.Fatalf("expected ErrBadMagic, got %v", err)
	}
}

func TestMetaReaderBadVersion(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "meta.razor")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var raw metaPageRaw
	binary.LittleEndian.PutUint32(raw[0:4], MagicValue)
	binary.LittleEndian.PutUint32(raw[4:8], 9999) // future version
	binary.LittleEndian.PutUint32(raw[8:12], 4096)
	f.Write(raw[:])
	f.Close()

	r := NewMetaReader(path)
	_, err = r.Read()
	if err != ErrUpgradeRequired {
		t.Fatalf("expected ErrUpgradeRequired, got %v", err)
	}
}

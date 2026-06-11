package BK

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackup_Basic(t *testing.T) {
	srcDir := t.TempDir()

	// Create some test files
	if err := os.WriteFile(filepath.Join(srcDir, "meta.razor"), []byte("test meta"), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "data"), 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "data", "sst.razor"), []byte("test sst data"), 0o644); err != nil {
		t.Fatalf("write sst: %v", err)
	}

	dstDir := filepath.Join(t.TempDir(), "backup")

	stats, err := Backup(context.Background(), srcDir, dstDir, BackupOptions{LSNMarker: 42})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	if stats.FilesCopied != 2 {
		t.Errorf("FilesCopied: got %d, want 2", stats.FilesCopied)
	}
	if stats.BytesCopied == 0 {
		t.Error("BytesCopied should be > 0")
	}
	if stats.LSN != 42 {
		t.Errorf("LSN: got %d, want 42", stats.LSN)
	}

	// Verify files exist in backup
	if _, err := os.Stat(filepath.Join(dstDir, "meta.razor")); err != nil {
		t.Errorf("meta.razor missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "data", "sst.razor")); err != nil {
		t.Errorf("data/sst.razor missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, "backup.marker")); err != nil {
		t.Errorf("backup.marker missing: %v", err)
	}
}

func TestBackup_RefuseNonEmptyDest(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	dstDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dstDir, "exists.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Backup(context.Background(), srcDir, dstDir, BackupOptions{})
	if err == nil {
		t.Fatal("expected error for non-empty destination")
	}
}

func TestBackup_RefuseSameDir(t *testing.T) {
	dir := t.TempDir()
	_, err := Backup(context.Background(), dir, dir, BackupOptions{})
	if err == nil {
		t.Fatal("expected error for same source and destination")
	}
}

func TestBackup_RefuseMissingSource(t *testing.T) {
	_, err := Backup(context.Background(), "/nonexistent", t.TempDir(), BackupOptions{})
	if err == nil {
		t.Fatal("expected error for missing source")
	}
}

func TestBackup_ContextCancellation(t *testing.T) {
	srcDir := t.TempDir()
	// Create many files to ensure walk takes some time
	for i := 0; i < 100; i++ {
		path := filepath.Join(srcDir, "f"+string(rune('0'+i%10))+string(rune('0'+i/10)))
		if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := Backup(ctx, srcDir, filepath.Join(t.TempDir(), "backup"), BackupOptions{})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestRestore_Basic(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "meta.razor"), []byte("test meta"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	backupDir := filepath.Join(t.TempDir(), "backup")
	_, err := Backup(context.Background(), srcDir, backupDir, BackupOptions{LSNMarker: 100})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	restoreDir := t.TempDir()
	stats, err := Restore(context.Background(), backupDir, restoreDir)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if stats.LSN != 100 {
		t.Errorf("LSN: got %d, want 100", stats.LSN)
	}
	if stats.FilesCopied < 1 {
		t.Errorf("FilesCopied: got %d, want >= 1", stats.FilesCopied)
	}

	// Verify meta.razor was restored
	data, err := os.ReadFile(filepath.Join(restoreDir, "meta.razor"))
	if err != nil {
		t.Fatalf("read restored meta: %v", err)
	}
	if string(data) != "test meta" {
		t.Errorf("restored content: got %q, want %q", string(data), "test meta")
	}
}

func TestRestore_RefuseLiveEngine(t *testing.T) {
	backupDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(backupDir, "backup.marker"), []byte("Version=0.20.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	restoreDir := t.TempDir()
	// Pre-existing meta.razor signals a live engine
	if err := os.WriteFile(filepath.Join(restoreDir, "meta.razor"), []byte("live"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Restore(context.Background(), backupDir, restoreDir)
	if err == nil {
		t.Fatal("expected error when restore directory contains live engine")
	}
}

func TestRestore_MissingMarker(t *testing.T) {
	backupDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(backupDir, "data.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Restore(context.Background(), backupDir, t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing backup marker")
	}
}

func TestBackupRestore_RoundTrip(t *testing.T) {
	srcDir := t.TempDir()
	files := map[string]string{
		"meta.razor":     "metadata content",
		"wal.razor":      "wal content",
		"data/sst1.razor": "sst file 1",
		"data/sst2.razor": "sst file 2",
	}
	for path, content := range files {
		fullPath := filepath.Join(srcDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	backupDir := filepath.Join(t.TempDir(), "backup")
	_, err := Backup(context.Background(), srcDir, backupDir, BackupOptions{LSNMarker: 999})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	restoreDir := t.TempDir()
	_, err = Restore(context.Background(), backupDir, restoreDir)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Verify all files match
	for path, content := range files {
		data, err := os.ReadFile(filepath.Join(restoreDir, path))
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		if string(data) != content {
			t.Errorf("%s: got %q, want %q", path, string(data), content)
		}
	}
}

func TestBackup_Duration(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	dstDir := filepath.Join(t.TempDir(), "backup")
	stats, err := Backup(context.Background(), srcDir, dstDir, BackupOptions{})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	if stats.DurationSeconds < 0 {
		t.Errorf("DurationSeconds: got %f, want >= 0", stats.DurationSeconds)
	}
	// Should complete quickly
	if stats.DurationSeconds > 5*time.Second.Seconds() {
		t.Errorf("Backup took too long: %fs", stats.DurationSeconds)
	}
}

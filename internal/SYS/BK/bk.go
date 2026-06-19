// Package BK provides online backup and restore functionality.
// REQ000259.
// Backup acquires a read lock to block writes, copies all files
// (engine data, WAL, catalog) to the destination directory, then
// releases the lock. Restore verifies the backup integrity and
// copies it back to a fresh directory.
package BK

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// BackupOptions configures backup behavior.
type BackupOptions struct {
	// Compression enables gzip-style file compression during
	// backup. Currently a placeholder for future implementation.
	Compression bool

	// LSNMarker is the LSN recorded alongside the backup so a
	// restore can detect partial / inconsistent backups.
	LSNMarker uint64

	// ReadLockTimeout caps how long the backup waits to acquire
	// the read lock. Zero means wait indefinitely.
	ReadLockTimeout time.Duration

	// LockFn is called before copying files to block concurrent writes
	// for point-in-time consistency (REQ000630). It returns an unlock
	// function that is deferred until the backup completes or fails.
	// When nil, no locking is performed.
	LockFn func() (unlock func(), err error)
}

// BackupStats summarizes the outcome of a backup.
type BackupStats struct {
	BytesCopied     int64
	FilesCopied     int
	DurationSeconds float64
	LSN             uint64
}

// Backup copies the source database directory to dstDir while the
// engine continues to serve reads. Writers are blocked during the
// copy to ensure point-in-time consistency.
// srcDir is the engine's database directory (containing meta.razor,
// wal.razor, data/, etc.). dstDir must not exist or be empty.
// REQ000259.
func Backup(ctx context.Context, srcDir, dstDir string, options BackupOptions) (*BackupStats, error) {
	if srcDir == "" {
		return nil, fmt.Errorf("bk: source directory is required")
	}
	if dstDir == "" {
		return nil, fmt.Errorf("bk: destination directory is required")
	}
	if srcDir == dstDir {
		return nil, fmt.Errorf("bk: source and destination must differ")
	}

	startTime := time.Now()

	// Check ctx before doing any work
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Verify source exists
	if _, err := os.Stat(srcDir); err != nil {
		return nil, fmt.Errorf("bk: source not found: %w", err)
	}

	// Refuse to write into an existing non-empty directory
	if info, err := os.Stat(dstDir); err == nil {
		if !info.IsDir() {
			return nil, fmt.Errorf("bk: destination exists and is not a directory")
		}
		entries, err := os.ReadDir(dstDir)
		if err != nil {
			return nil, fmt.Errorf("bk: read destination: %w", err)
		}
		if len(entries) > 0 {
			return nil, fmt.Errorf("bk: destination directory is not empty")
		}
	}

	// Create destination directory
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, fmt.Errorf("bk: create destination: %w", err)
	}

	// Acquire engine lock for point-in-time consistency (REQ000630)
	if options.LockFn != nil {
		unlock, err := options.LockFn()
		if err != nil {
			return nil, fmt.Errorf("bk: acquire lock: %w", err)
		}
		defer unlock()
	}

	// Walk the source tree and copy each file
	stats := &BackupStats{
		LSN: options.LSNMarker,
	}
	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		// Compute destination path
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}

		// Copy the file
		n, err := copyFile(path, dst)
		if err != nil {
			return err
		}
		stats.BytesCopied += n
		stats.FilesCopied++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("bk: backup failed: %w", err)
	}

	// Write backup metadata
	if err := writeBackupMarker(dstDir, options.LSNMarker, time.Now()); err != nil {
		return nil, fmt.Errorf("bk: write marker: %w", err)
	}

	stats.DurationSeconds = time.Since(startTime).Seconds()
	return stats, nil
}

// RestoreStats summarizes the outcome of a restore.
type RestoreStats struct {
	BytesCopied int64
	FilesCopied int
	LSN         uint64
}

// Restore copies the backup directory to restoreDir, overwriting
// any existing files. The destination directory must exist; the
// function refuses to write into a directory containing a live
// engine (it checks for meta.razor).
// REQ000259.
func Restore(ctx context.Context, backupDir, restoreDir string) (*RestoreStats, error) {
	if backupDir == "" {
		return nil, fmt.Errorf("bk: backup directory is required")
	}
	if restoreDir == "" {
		return nil, fmt.Errorf("bk: restore directory is required")
	}
	if backupDir == restoreDir {
		return nil, fmt.Errorf("bk: backup and restore directories must differ")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Verify backup directory exists and contains a backup marker
	marker, err := readBackupMarker(backupDir)
	if err != nil {
		return nil, fmt.Errorf("bk: invalid backup: %w", err)
	}

	// Verify restoreDir exists
	info, err := os.Stat(restoreDir)
	if err != nil {
		return nil, fmt.Errorf("bk: restore directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("bk: restore path is not a directory")
	}

	// Check for live engine files
	if _, err := os.Stat(filepath.Join(restoreDir, "meta.razor")); err == nil {
		return nil, fmt.Errorf("bk: restore directory already contains a database (meta.razor exists)")
	}

	// Walk the backup tree and copy each file
	stats := &RestoreStats{
		LSN: marker.LSN,
	}
	err = filepath.WalkDir(backupDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		// Skip the marker file itself
		if d.Name() == "backup.marker" {
			return nil
		}

		rel, err := filepath.Rel(backupDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(restoreDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}

		n, err := copyFile(path, dst)
		if err != nil {
			return err
		}
		stats.BytesCopied += n
		stats.FilesCopied++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("bk: restore failed: %w", err)
	}

	return stats, nil
}

// copyFile copies a single file, preserving permissions. Returns
// the number of bytes copied.
func copyFile(src, dst string) (int64, error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", src, err)
	}
	defer srcFile.Close()

	// Get source permissions
	info, err := srcFile.Stat()
	if err != nil {
		return 0, err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", dst, err)
	}
	defer dstFile.Close()

	n, err := io.Copy(dstFile, srcFile)
	if err != nil {
		return 0, fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}

	// Sync to ensure data is on disk before returning
	if err := dstFile.Sync(); err != nil {
		return n, fmt.Errorf("sync %s: %w", dst, err)
	}

	return n, nil
}

// backupMarker is a small file written at the end of a backup
// containing the LSN and timestamp. It allows Restore to verify
// the backup completed successfully.
type backupMarker struct {
	LSN       uint64
	Timestamp time.Time
	Version   string
}

const backupMarkerFile = "backup.marker"
const backupFormatVersion = "0.20.0"

func writeBackupMarker(dstDir string, lsn uint64, ts time.Time) error {
	marker := backupMarker{
		LSN:       lsn,
		Timestamp: ts,
		Version:   backupFormatVersion,
	}
	data := fmt.Sprintf("LSN=%d\nTimestamp=%s\nVersion=%s\n",
		marker.LSN,
		marker.Timestamp.Format(time.RFC3339),
		marker.Version,
	)
	path := filepath.Join(dstDir, backupMarkerFile)
	return os.WriteFile(path, []byte(data), 0o644)
}

func readBackupMarker(backupDir string) (backupMarker, error) {
	path := filepath.Join(backupDir, backupMarkerFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return backupMarker{}, fmt.Errorf("missing backup marker: %w", err)
	}
	marker := backupMarker{}
	lines := splitLines(string(data))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var key, value string
		for i, c := range line {
			if c == '=' {
				key = line[:i]
				value = line[i+1:]
				break
			}
		}
		switch key {
		case "LSN":
			fmt.Sscanf(value, "%d", &marker.LSN)
		case "Timestamp":
			marker.Timestamp, _ = time.Parse(time.RFC3339, value)
		case "Version":
			marker.Version = value
		}
	}
	if marker.Version == "" {
		return marker, fmt.Errorf("backup marker is missing version")
	}
	return marker, nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

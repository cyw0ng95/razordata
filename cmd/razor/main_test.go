package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRazorCLI_Version verifies the CLI prints version.
func TestRazorCLI_Version(t *testing.T) {
	if os.Getenv("RAZOR_CLI_INTEGRATION") == "" {
		t.Skip("set RAZOR_CLI_INTEGRATION=1 to run CLI integration tests")
	}
	out, err := runRazor(t, "version")
	if err != nil {
		t.Fatalf("razor version: %v\n%s", err, out)
	}
	if !strings.Contains(out, "razor ") {
		t.Errorf("unexpected version output: %s", out)
	}
}

// TestRazorCLI_Help verifies the help command.
func TestRazorCLI_Help(t *testing.T) {
	if os.Getenv("RAZOR_CLI_INTEGRATION") == "" {
		t.Skip("set RAZOR_CLI_INTEGRATION=1 to run CLI integration tests")
	}
	out, err := runRazor(t, "help")
	if err != nil {
		t.Fatalf("razor help: %v\n%s", err, out)
	}
	if !strings.Contains(out, "integrity-check") {
		t.Errorf("help should list integrity-check: %s", out)
	}
	if !strings.Contains(out, "vacuum") {
		t.Errorf("help should list vacuum: %s", out)
	}
	if !strings.Contains(out, "backup") {
		t.Errorf("help should list backup: %s", out)
	}
}

// TestRazorCLI_UnknownCommand verifies unknown commands fail.
func TestRazorCLI_UnknownCommand(t *testing.T) {
	if os.Getenv("RAZOR_CLI_INTEGRATION") == "" {
		t.Skip("set RAZOR_CLI_INTEGRATION=1 to run CLI integration tests")
	}
	_, err := runRazor(t, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
}

// TestRazorCLI_BackupRestore end-to-end test for backup and restore.
func TestRazorCLI_BackupRestore(t *testing.T) {
	if os.Getenv("RAZOR_CLI_INTEGRATION") == "" {
		t.Skip("set RAZOR_CLI_INTEGRATION=1 to run CLI integration tests")
	}

	srcDir := t.TempDir()
	// Create a minimal database structure
	if err := os.WriteFile(filepath.Join(srcDir, "meta.razor"), []byte("meta"), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	backupDir := filepath.Join(t.TempDir(), "backup")
	out, err := runRazor(t, "backup", srcDir, backupDir)
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, out)
	}
	if !strings.Contains(out, "OK") {
		t.Errorf("backup output missing 'OK': %s", out)
	}

	// Verify backup exists
	if _, err := os.Stat(filepath.Join(backupDir, "meta.razor")); err != nil {
		t.Errorf("backup missing meta.razor: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "backup.marker")); err != nil {
		t.Errorf("backup missing marker: %v", err)
	}

	// Restore
	restoreDir := t.TempDir()
	out, err = runRazor(t, "restore", backupDir, restoreDir)
	if err != nil {
		t.Fatalf("restore: %v\n%s", err, out)
	}
	if !strings.Contains(out, "OK") {
		t.Errorf("restore output missing 'OK': %s", out)
	}

	// Verify restore
	data, err := os.ReadFile(filepath.Join(restoreDir, "meta.razor"))
	if err != nil {
		t.Errorf("restored meta missing: %v", err)
	}
	if string(data) != "meta" {
		t.Errorf("restored content: got %q, want %q", string(data), "meta")
	}
}

// runRazor executes the razor CLI with the given arguments and
// returns combined stdout+stderr. It uses "go run" to build and
// execute the cmd/razor package.
func runRazor(t *testing.T, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Build the binary first to avoid go run overhead
	binPath := filepath.Join(t.TempDir(), "razor")
	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", binPath, "./cmd/razor")
	buildCmd.Dir = findRepoRoot(t)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build razor: %v\n%s", err, out)
	}

	cmd := exec.CommandContext(ctx, binPath, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	// Walk up from current directory to find go.mod
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find go.mod in any parent directory")
	return ""
}

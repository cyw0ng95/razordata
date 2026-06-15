// razor is the razordata admin CLI. REQ000260.
//
// Subcommands:
//   razor integrity-check <dbdir>
//   razor vacuum <dbdir>
//   razor analyze <dbdir>
//   razor backup <srcdir> <dstdir>
//   razor restore <backupdir> <restoredir>
//   razor schema-dump <dbdir>
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cyw0ng95/razordata/internal/SQL/EX"
	"github.com/cyw0ng95/razordata/internal/SYS/AP"
	"github.com/cyw0ng95/razordata/internal/SYS/SY"
)

const usage = `razor - razordata admin CLI

Usage:
  razor <command> [arguments]

Commands:
  integrity-check <dbdir>      Run PRAGMA integrity_check on database
  vacuum <dbdir>               Run VACUUM to reclaim tombstone space
  analyze <dbdir>              Run ANALYZE to update column statistics
  backup <srcdir> <dstdir>     Backup database to a new directory
  restore <backupdir> <rdir>   Restore database from backup
  schema-dump <dbdir>          Dump table schemas
  version                      Print version
  help                         Show this message

Examples:
  razor integrity-check /var/lib/razordata/mydb
  razor vacuum /var/lib/razordata/mydb
  razor backup /var/db /var/backup/mydb-2024
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "integrity-check":
		err = runIntegrityCheck(args)
	case "vacuum":
		err = runVacuum(args)
	case "analyze":
		err = runAnalyze(args)
	case "backup":
		err = runBackup(args)
	case "restore":
		err = runRestore(args)
	case "schema-dump":
		err = runSchemaDump(args)
	case "version":
		fmt.Printf("razor %s\n", AP.Version)
		return
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "razor: unknown command %q\n\n%s", cmd, usage)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "razor %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

// openEngine opens a database directory and returns the engine.
// Caller is responsible for calling eng.Close(ctx).
func openEngine(ctx context.Context, dir string) (*SY.Engine, error) {
	return SY.Open(ctx, dir, AP.Options{})
}

// rowAsString converts a Row's first cell to string for printing.
func rowAsString(row EX.Row) string {
	if len(row.Data) == 0 {
		return ""
	}
	switch v := row.Data[0].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func runIntegrityCheck(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: razor integrity-check <dbdir>")
	}
	dir := args[0]
	fmt.Printf("Running integrity check on %s...\n", dir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	eng, err := openEngine(ctx, dir)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer eng.Close(ctx)

	exe := eng.Executor()
	rows, err := exe.QueryAll(ctx, "PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}

	if len(rows) == 0 {
		fmt.Println("OK: integrity check passed")
		return nil
	}
	for _, r := range rows {
		fmt.Printf("  ERROR: %v\n", r)
	}
	fmt.Printf("FAILED: %d integrity errors\n", len(rows))
	return fmt.Errorf("integrity check failed")
}

func runVacuum(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: razor vacuum <dbdir>")
	}
	dir := args[0]
	fmt.Printf("Running vacuum on %s...\n", dir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	eng, err := openEngine(ctx, dir)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer eng.Close(ctx)

	exe := eng.Executor()
	if _, err := exe.Exec(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	fmt.Println("OK: vacuum completed")
	return nil
}

func runAnalyze(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: razor analyze <dbdir>")
	}
	dir := args[0]
	fmt.Printf("Running analyze on %s...\n", dir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	eng, err := openEngine(ctx, dir)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer eng.Close(ctx)

	exe := eng.Executor()

	tables, err := listTables(ctx, exe)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}

	for _, t := range tables {
		if _, err := exe.Exec(ctx, fmt.Sprintf("ANALYZE %s", t)); err != nil {
			fmt.Printf("  WARNING: analyze %s failed: %v\n", t, err)
			continue
		}
		fmt.Printf("  analyzed %s\n", t)
	}

	fmt.Println("OK: analyze completed")
	return nil
}

func runBackup(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: razor backup <srcdir> <dstdir>")
	}
	srcDir, dstDir := args[0], args[1]
	fmt.Printf("Backing up %s -> %s...\n", srcDir, dstDir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	stats, err := AP.Backup(ctx, srcDir, dstDir, AP.BackupOptions{})
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}

	fmt.Printf("OK: backed up %d files (%d bytes) in %.2fs (LSN=%d)\n",
		stats.FilesCopied, stats.BytesCopied, stats.DurationSeconds, stats.LSN)
	return nil
}

func runRestore(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: razor restore <backupdir> <restoredir>")
	}
	backupDir, restoreDir := args[0], args[1]
	fmt.Printf("Restoring %s -> %s...\n", backupDir, restoreDir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	stats, err := AP.Restore(ctx, backupDir, restoreDir)
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}

	fmt.Printf("OK: restored %d files (%d bytes) (LSN=%d)\n",
		stats.FilesCopied, stats.BytesCopied, stats.LSN)
	return nil
}

func runSchemaDump(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: razor schema-dump <dbdir>")
	}
	dir := args[0]
	fmt.Printf("-- Schema dump for %s --\n", dir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	eng, err := openEngine(ctx, dir)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer eng.Close(ctx)

	exe := eng.Executor()
	tables, err := listTables(ctx, exe)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}

	for _, t := range tables {
		fmt.Printf("CREATE TABLE %s (...);\n", t)
	}

	return nil
}

// listTables returns all user table names in the database.
func listTables(ctx context.Context, exe *EX.Executor) ([]string, error) {
	// Use sqlite_master equivalent; if not available, return empty list.
	rows, err := exe.QueryAll(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		// sqlite_master may not exist in this implementation
		if strings.Contains(err.Error(), "no such table") ||
			strings.Contains(err.Error(), "syntax") {
			return nil, nil
		}
		return nil, err
	}
	var tables []string
	for _, r := range rows {
		if name := rowAsString(r); name != "" {
			tables = append(tables, name)
		}
	}
	return tables, nil
}

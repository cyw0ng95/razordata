//go:build debug

package sk

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSocket_Commands(t *testing.T) {
	dir := t.TempDir()
	srv := NewServer(dir)
	if err := srv.Serve(); err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer srv.Close()

	sockPath := filepath.Join(dir, "debug.sock")
	time.Sleep(50 * time.Millisecond)

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "help\n")
	scanner := bufio.NewScanner(conn)
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "END" {
			break
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		t.Error("expected help response")
	}
	if !strings.Contains(lines[0], "commands:") {
		t.Errorf("expected help text, got: %s", lines[0])
	}
}

func TestSocket_Stats(t *testing.T) {
	dir := t.TempDir()
	srv := NewServer(dir)
	if err := srv.Serve(); err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer srv.Close()

	sockPath := filepath.Join(dir, "debug.sock")
	time.Sleep(50 * time.Millisecond)

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "stats\n")
	scanner := bufio.NewScanner(conn)
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "END" {
			break
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		t.Error("expected stats output")
	}
}

func TestSocket_Close(t *testing.T) {
	dir := t.TempDir()
	srv := NewServer(dir)
	if err := srv.Serve(); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	sockPath := filepath.Join(dir, "debug.sock")
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Error("expected socket file to be removed")
	}
}

//go:build debug

package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func debugCommand(dbdir string, args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "usage: razor debug <dbdir> <command> [args]\n")
		os.Exit(1)
	}

	sockPath := filepath.Join(dbdir, ".debug", "debug.sock")
	conn, err := net.DialTimeout("unix", sockPath, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect to debug socket: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	cmd := strings.Join(args, " ")
	fmt.Fprintf(conn, "%s\n", cmd)

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "END" {
			break
		}
		fmt.Println(line)
	}
}

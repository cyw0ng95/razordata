//go:build debug

package sk

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// Server is a UNIX domain socket command server.
type Server struct {
	dir      string
	listener net.Listener
	done     chan struct{}
	closed   atomic.Bool
	wg       sync.WaitGroup
}

// NewServer creates a socket server in the given directory.
func NewServer(dir string) *Server {
	return &Server{dir: dir, done: make(chan struct{})}
}

// Serve starts listening on <dir>/debug.sock.
func (s *Server) Serve() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	sockPath := filepath.Join(s.dir, "debug.sock")
	os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.listener = ln

	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				continue
			}
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		response := Dispatch(line)
		fmt.Fprintf(conn, "%s\nEND\n", response)
	}
}

// Close stops the server and removes the socket file.
func (s *Server) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	close(s.done)
	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()
	os.Remove(filepath.Join(s.dir, "debug.sock"))
	return nil
}

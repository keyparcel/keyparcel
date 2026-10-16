// Package sftptest provides an in-memory SFTP fixture with request failure injection.
package sftptest

import (
	"io"
	"net"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
)

type failure struct {
	method, path string
	err          error
}

type Server struct {
	Client   *sftp.Client
	wire     net.Conn
	handlers sftp.Handlers
	fault    atomic.Pointer[failure]
}

// Fail affects only the named request and path. io.ErrClosedPipe drops the wire.
func (s *Server) Fail(method, path string, err error) {
	s.fault.Store(&failure{method, path, err})
}

func (s *Server) check(method, path string) error {
	if f := s.fault.Load(); f != nil && f.method == method && f.path == path {
		if f.err == io.ErrClosedPipe {
			s.wire.Close()
		}
		return f.err
	}
	return nil
}

func New(t testing.TB) *Server {
	t.Helper()
	clientWire, serverWire := net.Pipe()
	s := &Server{wire: serverWire, handlers: sftp.InMemHandler()}
	handlers := s.handlers
	handlers.FileList, handlers.FileGet = s, s
	server := sftp.NewRequestServer(serverWire, handlers)
	done := make(chan struct{})
	go func() { defer close(done); server.Serve(); server.Close() }()
	client, err := sftp.NewClientPipe(clientWire, &requestWriter{Conn: clientWire, server: s})
	if err != nil {
		t.Fatal(err)
	}
	s.Client = client
	t.Cleanup(func() { client.Close(); clientWire.Close(); serverWire.Close(); <-done })
	return s
}

type requestWriter struct {
	net.Conn
	server *Server
}

func (w *requestWriter) Write(p []byte) (int, error) {
	// SSH_FXP_LSTAT (7) has a length, type, request ID, and length-prefixed path.
	if len(p) >= 13 && p[4] == 7 {
		if err := w.server.check("WriteLstat", string(p[13:])); err != nil {
			return 0, err
		}
	}
	return w.Conn.Write(p)
}

func (s *Server) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	if err := s.check(r.Method, r.Filepath); err != nil {
		return nil, err
	}
	return s.handlers.FileList.Filelist(r)
}

func (s *Server) Lstat(r *sftp.Request) (sftp.ListerAt, error) {
	if err := s.check("Lstat", r.Filepath); err != nil {
		return nil, err
	}
	return s.handlers.FileList.(sftp.LstatFileLister).Lstat(r)
}

func (s *Server) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	if err := s.check("Open", r.Filepath); err != nil {
		return nil, err
	}
	rd, err := s.handlers.FileGet.Fileread(r)
	if err != nil {
		return nil, err
	}
	return &reader{ReaderAt: rd, server: s, path: r.Filepath}, nil
}

type reader struct {
	io.ReaderAt
	server *Server
	path   string
}

func (r *reader) ReadAt(p []byte, off int64) (int, error) {
	if err := r.server.check("Read", r.path); err != nil {
		return 0, err
	}
	return r.ReaderAt.ReadAt(p, off)
}

func (r *reader) Close() error {
	if err := r.server.check("Close", r.path); err != nil {
		return err
	}
	if closer, ok := r.ReaderAt.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

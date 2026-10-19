// Package sftptest provides an in-memory SFTP fixture with request failure injection.
package sftptest

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
)

type failure struct {
	method, path string
	err          error
}

type ioHook func(method, path string, offset int64) error

type Server struct {
	Client   *sftp.Client
	wire     net.Conn
	handlers sftp.Handlers
	fault    atomic.Pointer[failure]
	ioHook   atomic.Pointer[ioHook]
	mu       sync.Mutex
	calls    map[string]int
}

// OnIO installs a concurrent read/write hook for deterministic transfer tests.
func (s *Server) OnIO(fn func(method, path string, offset int64) error) {
	hook := ioHook(fn)
	s.ioHook.Store(&hook)
}

func (s *Server) checkIO(method, path string, offset int64) error {
	if err := s.check(method, path); err != nil {
		return err
	}
	if hook := s.ioHook.Load(); hook != nil {
		return (*hook)(method, path, offset)
	}
	return nil
}

func (s *Server) Calls(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

// Fail affects the named request/path (empty path matches all). io.ErrClosedPipe drops the wire.
func (s *Server) Fail(method, path string, err error) {
	s.fault.Store(&failure{method, path, err})
}

func (s *Server) check(method, path string) error {
	s.mu.Lock()
	s.calls[method]++
	s.mu.Unlock()
	if f := s.fault.Load(); f != nil && f.method == method && (f.path == path || f.path == "") {
		if f.err == io.ErrClosedPipe {
			s.wire.Close()
		}
		return f.err
	}
	return nil
}

func New(t testing.TB, options ...sftp.ClientOption) *Server {
	t.Helper()
	clientWire, serverWire := net.Pipe()
	s := &Server{wire: serverWire, handlers: sftp.InMemHandler(), calls: map[string]int{}}
	handlers := s.handlers
	handlers.FileList, handlers.FileGet, handlers.FileCmd = s, s, s
	handlers.FilePut = s
	server := sftp.NewRequestServer(serverWire, handlers)
	done := make(chan struct{})
	go func() { defer close(done); server.Serve(); server.Close() }()
	client, err := sftp.NewClientPipe(clientWire, &requestWriter{Conn: clientWire, server: s}, options...)
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

func (s *Server) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	if err := s.check("Create", r.Filepath); err != nil {
		return nil, err
	}
	w, err := s.handlers.FilePut.Filewrite(r)
	if err != nil {
		return nil, err
	}
	return &writer{WriterAt: w, server: s, path: r.Filepath}, nil
}

type writer struct {
	io.WriterAt
	server *Server
	path   string
}

func (w *writer) WriteAt(p []byte, off int64) (int, error) {
	if err := w.server.checkIO("Write", w.path, off); err != nil {
		return 0, err
	}
	return w.WriterAt.WriteAt(p, off)
}

func (w *writer) Close() error {
	if closer, ok := w.WriterAt.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

type reader struct {
	io.ReaderAt
	server *Server
	path   string
}

func (r *reader) ReadAt(p []byte, off int64) (int, error) {
	if err := r.server.checkIO("Read", r.path, off); err != nil {
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

func (s *Server) Filecmd(r *sftp.Request) error {
	if err := s.check(r.Method, r.Filepath); err != nil {
		return err
	}
	return s.handlers.FileCmd.Filecmd(r)
}

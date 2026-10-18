package core

import (
	"net"
	"sync"

	"github.com/pkg/sftp"
)

// A channel can fail or close without retiring its authenticated SSH owner.
// Connection's category admission guarantees only one operation uses each slot.
type sftpChannel struct {
	client *sftp.Client
	done   chan struct{}
	close  func() error
}

func newSFTPChannel(client *sftp.Client, closeChannel func() error) *sftpChannel {
	var once sync.Once
	var err error
	channel := &sftpChannel{client: client, done: make(chan struct{})}
	channel.close = func() error { once.Do(func() { err = closeChannel() }); return err }
	go func() {
		client.Wait()
		close(channel.done)
		channel.close()
	}()
	return channel
}

// Opening does not hold an owner lock across network I/O. A late opener must
// close its channel rather than publishing it after owner retirement.
func (s *Session) openChannel() (*sftpChannel, error) {
	channel, err := s.openSFTP()
	if err != nil {
		return nil, err
	}
	s.channelMu.Lock()
	closed := s.channelsClosed
	if !closed {
		s.channels = append(s.channels, channel)
	}
	s.channelMu.Unlock()
	if closed {
		channel.close()
		return nil, net.ErrClosed
	}
	return channel, nil
}

func (s *Session) forgetChannel(channel *sftpChannel) {
	s.channelMu.Lock()
	defer s.channelMu.Unlock()
	for i, active := range s.channels {
		if active == channel {
			s.channels = append(s.channels[:i], s.channels[i+1:]...)
			return
		}
	}
}

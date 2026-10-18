package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// EmbeddedOptions are explicit connection settings; no SSH configuration is read.
type EmbeddedOptions struct {
	Address   string
	Port      int
	User      string
	Identity  *Identity
	HostKey   ssh.PublicKey
	Root      string
	Discovery string
	Timeout   time.Duration
	InboxMode string
}

// ProbeHostKey observes a server key without authentication or a trusted session.
func ProbeHostKey(ctx context.Context, address string, port int, timeout time.Duration) (ssh.PublicKey, error) {
	if address == "" || port < 1 || port > 65535 || timeout < 0 {
		return nil, errors.New("invalid server-key discovery settings")
	}
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	endpoint := net.JoinHostPort(address, strconv.Itoa(port))
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	observed := errors.New("server key observed; approval required")
	var key ssh.PublicKey
	// Observe ordinary keys; certificate trust and renewal need a separate workflow.
	var algorithms []string
	for _, algorithm := range ssh.SupportedAlgorithms().HostKeys {
		if !strings.Contains(algorithm, "-cert-") {
			algorithms = append(algorithms, algorithm)
		}
	}
	config := &ssh.ClientConfig{HostKeyAlgorithms: algorithms, HostKeyCallback: func(_ string, _ net.Addr, presented ssh.PublicKey) error {
		key = presented
		return observed
	}}
	_, _, _, err = ssh.NewClientConn(raw, endpoint, config)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !errors.Is(err, observed) {
		return nil, fmt.Errorf("server-key discovery: %w", err)
	}
	return key, nil
}

// ConnectEmbedded keeps the connection bound to ctx until Close is called.
func ConnectEmbedded(ctx context.Context, options EmbeddedOptions) (_ *Session, err error) {
	if !validInboxMode(options.InboxMode) || options.Address == "" || options.Port < 1 || options.Port > 65535 || options.User == "" || options.Identity == nil || options.HostKey == nil || options.Root == "" || options.Discovery == "" || options.Timeout < 0 {
		return nil, errors.New("incomplete or invalid embedded SSH settings (including host-key trust)")
	}
	id, err := ParseIdentity(options.Identity.private)
	if err != nil {
		return nil, err
	}
	if options.Identity.Public == nil || !bytes.Equal(id.Public.Marshal(), options.Identity.Public.Marshal()) {
		return nil, errors.New("selected identity fingerprint changed")
	}
	signer, err := ssh.ParsePrivateKey(id.private)
	if err != nil {
		return nil, err
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	setup, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() {
		if err != nil && setup.Err() != nil {
			err = fmt.Errorf("SSH/SFTP setup: %w", setup.Err())
		}
	}()
	address := net.JoinHostPort(options.Address, strconv.Itoa(options.Port))
	raw, err := (&net.Dialer{}).DialContext(setup, "tcp", address)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	stopSetup := context.AfterFunc(setup, func() { raw.Close() })
	defer stopSetup()
	ok := false
	defer func() {
		if !ok {
			stop()
			raw.Close()
		}
	}()
	hostKeyAlgorithms := []string{options.HostKey.Type()}
	if options.HostKey.Type() == ssh.KeyAlgoRSA {
		hostKeyAlgorithms = []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	}
	config := &ssh.ClientConfig{User: options.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyAlgorithms: hostKeyAlgorithms, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if !bytes.Equal(key.Marshal(), options.HostKey.Marshal()) {
			return fmt.Errorf("SSH host key does not match trusted key (expected %s; received %s). Verify the server independently before resetting its approval.", ssh.FingerprintSHA256(options.HostKey), ssh.FingerprintSHA256(key))
		}
		return nil
	}}
	connection, channels, requests, err := ssh.NewClientConn(raw, address, config)
	if err != nil {
		return nil, fmt.Errorf("SSH connection: %w", err)
	}
	client := ssh.NewClient(connection, channels, requests)
	ownerDone := make(chan struct{})
	var ownerErr error
	go func() { ownerErr = client.Wait(); close(ownerDone) }()
	s := &Session{Identity: id, ctx: ctx}
	s.inbox = &inboxAcceleration{mode: options.InboxMode, run: func(request context.Context, command string, consume func(io.Reader) error) error {
		return runEmbeddedInbox(request, client, ownerDone, command, consume)
	}}
	s.openSFTP = func() (*sftpChannel, error) {
		return openEmbeddedSFTP(client)
	}
	s.waitTransport = func() error { <-ownerDone; return ownerErr }
	s.channel, err = s.openChannel()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("SFTP connection: %w", err)
	}
	s.Client = s.channel.client
	s.closeTransport = func() error {
		stop()
		// Close SSH first to unblock requests; SFTP's subsequent EOF error is expected.
		client.Close()
		return nil
	}
	cwd, err := s.Client.Getwd()
	if err != nil {
		s.Close()
		return nil, err
	}
	s.Root = remotePath(cwd, options.Root)
	s.DiscoveryPath = remotePath(cwd, options.Discovery)
	if !stopSetup() {
		s.Close()
		return nil, setup.Err()
	}
	if err := ctx.Err(); err != nil {
		s.Close()
		return nil, err
	}
	ok = true
	return s, nil
}

// Keep the SSH channel handle so failed subsystem setup and channel-only close
// release it without closing the authenticated client.
func openEmbeddedSFTP(owner *ssh.Client) (_ *sftpChannel, err error) {
	session, err := owner.NewSession()
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			session.Close()
		}
	}()
	in, err := session.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := session.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return nil, err
	}
	go io.Copy(io.Discard, stderr)
	if err = session.RequestSubsystem("sftp"); err != nil {
		return nil, err
	}
	client, err := sftp.NewClientPipe(out, in, sftp.UseConcurrentReads(false), sftp.UseConcurrentWrites(false))
	if err != nil {
		return nil, err
	}
	ok = true
	return newSFTPChannel(client, func() error {
		session.Close()
		return client.Close()
	}), nil
}

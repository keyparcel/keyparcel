// Package bridge exposes the small Android-facing surface of the shared core.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"keyparcel/core"
	"keyparcel/internal/display"
)

// Initialize must run once before transfers; Android has no writable default temp directory.
func Initialize(cacheDir string) error {
	info, err := os.Stat(cacheDir)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(cacheDir) || !info.IsDir() {
		return errors.New("temporary storage must be an absolute app-private directory")
	}
	return os.Setenv("TMPDIR", cacheDir)
}

// GenerateIdentity is used only by the app's internal encrypted identity store.
func GenerateIdentity() ([]byte, error) { return core.GenerateIdentity() }

func FormatSize(bytes int64) string { return display.Size(bytes) }

func PublicKey(privateKey []byte) (string, error) {
	id, err := core.ParseIdentity(privateKey)
	if err != nil {
		return "", err
	}
	return core.PublicText(id.Public), nil
}

func Fingerprint(publicKey string) (string, error) {
	key, err := core.ParsePublic(publicKey)
	if err != nil {
		return "", err
	}
	return ssh.FingerprintSHA256(key), nil
}

// ServerFingerprint also accepts server key types that age cannot use as peers.
func ServerFingerprint(publicKey string) (string, error) {
	key, err := parseServerKey(publicKey)
	if err != nil {
		return "", err
	}
	return ssh.FingerprintSHA256(key), nil
}

// ProbeServerKey returns a candidate only; it never authenticates or stores trust.
func ProbeServerKey(address string, port, timeoutMillis int) (string, error) {
	if timeoutMillis <= 0 || int64(timeoutMillis) > int64((1<<63-1)/time.Millisecond) {
		return "", errors.New("request timeout must be a positive duration in milliseconds")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMillis)*time.Millisecond)
	defer cancel()
	key, err := core.ProbeHostKey(ctx, address, port, 30*time.Second)
	if err != nil {
		return "", requestError(ctx, err)
	}
	return core.PublicText(key), nil
}

func parseServerKey(text string) (ssh.PublicKey, error) {
	key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(text))
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("expected one trusted server key")
	}
	return key, nil
}

func ValidatePeer(name, publicKey string) error {
	if err := core.ValidateName(name); err != nil {
		return err
	}
	_, err := core.ParsePublic(publicKey)
	return err
}

// Client operations are synchronous. Use separate NewRequest snapshots for
// concurrent GUI operations; legacy mutable clients remain sequential. Close
// interrupts the shared SSH transport, including every request snapshot.
type Client struct {
	session        *core.Session
	config         *core.Config
	ctx            context.Context
	cancel         context.CancelFunc
	connection     *core.Connection
	requestTimeout time.Duration
	background     bool
	immutable      bool
}

func Connect(privateKey []byte, address string, port int, user, hostKey, root, discovery string) (*Client, error) {
	return connect(privateKey, address, port, user, hostKey, root, discovery, 0)
}

// ConnectWithTimeout bounds the entire connection lifetime, not just SSH setup.
func ConnectWithTimeout(privateKey []byte, address string, port int, user, hostKey, root, discovery string, timeoutMillis int) (*Client, error) {
	if timeoutMillis <= 0 || int64(timeoutMillis) > int64((1<<63-1)/time.Millisecond) {
		return nil, errors.New("request timeout must be a positive duration in milliseconds")
	}
	return connect(privateKey, address, port, user, hostKey, root, discovery, time.Duration(timeoutMillis)*time.Millisecond)
}

func connect(privateKey []byte, address string, port int, user, hostKey, root, discovery string, timeout time.Duration) (*Client, error) {
	id, err := core.ParseIdentity(privateKey)
	if err != nil {
		return nil, err
	}
	key, err := parseServerKey(hostKey)
	if err != nil {
		return nil, err
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	s, err := core.ConnectEmbedded(ctx, core.EmbeddedOptions{
		Address: address, Port: port, User: user, HostKey: key, Identity: id,
		Root: root, Discovery: discovery, Timeout: 30 * time.Second,
	})
	if err != nil {
		err = requestError(ctx, err)
		cancel()
		return nil, err
	}
	return &Client{session: s, config: &core.Config{Peers: map[string]string{}}, ctx: ctx, cancel: cancel}, nil
}

// NewPersistentClient defers SSH setup until the first operation. Close can
// therefore cancel both setup and subsequent requests, including from Android stop.
func NewPersistentClient(privateKey []byte, address string, port int, user, hostKey, root, discovery string, timeoutMillis int) (*Client, error) {
	return NewPersistentClientWithAccessMethod(privateKey, address, port, user, hostKey, root, discovery, timeoutMillis, "")
}

// NewPersistentClientWithAccessMethod selects the read-only remote access policy.
func NewPersistentClientWithAccessMethod(privateKey []byte, address string, port int, user, hostKey, root, discovery string, timeoutMillis int, accessMethod string) (*Client, error) {
	if err := core.ValidateAccessMethod(accessMethod); err != nil {
		return nil, err
	}

	id, err := core.ParseIdentity(privateKey)
	if err != nil {
		return nil, err
	}
	key, err := parseServerKey(hostKey)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{ctx: ctx, cancel: cancel, config: &core.Config{Peers: map[string]string{}}}
	if err := c.SetRequestTimeout(timeoutMillis); err != nil {
		cancel()
		return nil, err
	}
	c.connection = core.NewConnection(ctx, func(ctx context.Context) (*core.Session, error) {
		return core.ConnectEmbedded(ctx, core.EmbeddedOptions{Address: address, Port: port, User: user, HostKey: key, Identity: id, Root: root, Discovery: discovery, Timeout: 30 * time.Second, AccessMethod: accessMethod})
	})
	return c, nil
}

// NewRequest captures immutable approvals and timeout for one GUI operation.
// It shares this owner's transport; Close intentionally disconnects all views.
// Request settings cannot be changed; construct another snapshot instead.
// Call CancelRequest when finished to release its context.
func (c *Client) NewRequest(peersJSON string, timeoutMillis int, background bool) (*Client, error) {
	if c.connection == nil {
		return nil, errors.New("request snapshots require a persistent client")
	}
	request := &Client{session: c.session, config: &core.Config{}, ctx: c.ctx, cancel: c.cancel, connection: c.connection, background: background}
	if err := request.SetPeers(peersJSON); err != nil {
		return nil, err
	}
	if err := request.SetRequestTimeout(timeoutMillis); err != nil {
		return nil, err
	}
	request.ctx, request.cancel = context.WithCancel(c.ctx)
	request.immutable = true
	return request, nil
}

// CancelRequest cancels a snapshot's context. Connection decides whether it has
// admitted I/O to disconnect; a canceled waiter cannot close a healthy owner.
func (c *Client) CancelRequest() {
	if c.immutable {
		c.cancel()
	}
}

func (c *Client) SetRequestTimeout(timeoutMillis int) error {
	if c.immutable {
		return errors.New("request settings are immutable; create a new request")
	}
	if timeoutMillis <= 0 || int64(timeoutMillis) > int64((1<<63-1)/time.Millisecond) {
		return errors.New("request timeout must be a positive duration in milliseconds")
	}
	c.requestTimeout = time.Duration(timeoutMillis) * time.Millisecond
	return nil
}

// SetPeers replaces the complete approval snapshot; removing trust must not
// leave an old key in a reused session. Call sequentially with other operations.
func (c *Client) SetPeers(peersJSON string) error {
	if c.immutable {
		return errors.New("request settings are immutable; create a new request")
	}
	var peers map[string]string
	if err := json.Unmarshal([]byte(peersJSON), &peers); err != nil {
		return err
	}
	canonical := map[string]string{}
	for name, text := range peers {
		if err := ValidatePeer(name, text); err != nil {
			return err
		}
		key, _ := core.ParsePublic(text)
		canonical[name] = core.PublicText(key)
	}
	c.config.Peers = canonical
	return nil
}

func clientRequest[T any](c *Client, action func(*core.Session) (T, error)) (T, error) {
	if c.connection == nil {
		result, err := action(c.session)
		return result, requestError(c.ctx, err)
	}
	ctx, cancel := context.WithTimeout(c.ctx, c.requestTimeout)
	defer cancel()
	var result T
	run := c.connection.Do
	if c.background {
		run = c.connection.Poll
	}
	err := run(ctx, func(s *core.Session) error { var err error; result, err = action(s); return err })
	return result, requestError(ctx, err)
}

// ConnectionState observes the persistent transport without starting a request.
func (c *Client) ConnectionState() string {
	if c.connection != nil {
		return c.connection.State()
	}
	if c.ctx.Err() != nil {
		return "Suspended"
	}
	return "Connected"
}

// CheckConnection authenticates the connection without fetching remote data.
func (c *Client) CheckConnection() error {
	_, err := clientRequest(c, func(*core.Session) (bool, error) { return true, nil })
	return err
}

func (c *Client) Close() error {
	c.cancel()
	if c.connection != nil {
		c.connection.Close()
		return nil
	}
	return c.session.Close()
}

type requestTimeoutError struct{}

func (requestTimeoutError) Error() string { return "Request timed out. Try again." }
func (requestTimeoutError) Unwrap() error { return context.DeadlineExceeded }

func requestError(ctx context.Context, err error) error {
	if err != nil && (errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded)) {
		return requestTimeoutError{}
	}
	return err
}

// Approve pins a public key already verified by the caller, independently of discovery.
func (c *Client) Approve(name, publicKey string) error {
	if c.immutable {
		return errors.New("request settings are immutable; create a new request")
	}
	if err := ValidatePeer(name, publicKey); err != nil {
		return err
	}
	key, err := core.ParsePublic(publicKey)
	if err != nil {
		return err
	}
	c.config.Peers[name] = core.PublicText(key)
	return nil
}

func (c *Client) Discover() (string, error) {
	devices, err := clientRequest(c, func(s *core.Session) ([]core.Device, error) { return s.Discover(c.config) })
	if err != nil {
		return "", requestError(c.ctx, err)
	}
	b, err := json.Marshal(devices)
	return string(b), err
}

func (c *Client) Delete(id string) error {
	_, err := clientRequest(c, func(s *core.Session) (int, error) {
		n, failures, err := s.Delete([]string{id}, false)
		if len(failures) > 0 {
			return n, fmt.Errorf("%s: %s", failures[0].ID, failures[0].Reason)
		}
		return n, err
	})
	return requestError(c.ctx, err)
}

func (c *Client) Inbox() (string, error) {
	return clientRequest(c, func(s *core.Session) (string, error) {
		items, rejected, _, err := s.List(c.config)
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(struct {
			Items    []core.Item     `json:"items"`
			Rejected []core.Rejected `json:"rejected"`
		}{items, rejected})
		return string(b), err
	})
}

func (c *Client) SendText(recipient, text string) (string, error) {
	result, err := clientRequest(c, func(s *core.Session) ([]core.Delivery, error) {
		return s.Send(c.config, []string{recipient}, "text", nil, strings.NewReader(text))
	})
	if err != nil {
		return "", requestError(c.ctx, err)
	}
	return result[0].ID, nil
}

func (c *Client) SendFile(recipient, file string) (string, error) {
	return c.SendNamedFile(recipient, file, filepath.Base(file))
}

// SendNamedFile preserves a document's display name independently of its staging path.
func (c *Client) SendNamedFile(recipient, file, name string) (string, error) {
	f, err := openSendFile(file, name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	result, err := clientRequest(c, func(s *core.Session) ([]core.Delivery, error) {
		return s.Send(c.config, []string{recipient}, "file", &name, f)
	})
	if err != nil {
		return "", requestError(c.ctx, err)
	}
	return result[0].ID, nil
}

// Batch sends preserve every per-recipient outcome, including partial failures.
// A preflight error has no deliveries; callers must not infer success from JSON alone.
func (c *Client) SendTextTo(recipientsJSON, text string) (string, error) {
	return c.sendTo(recipientsJSON, "text", nil, strings.NewReader(text), "")
}
func (c *Client) SendNamedFileTo(recipientsJSON, file, name string) (string, error) {
	return c.SendNamedFileToWithDiscovery(recipientsJSON, file, name, "")
}

// The GUI binds this discovery JSON to its captured server/identity context.
// Empty JSON preserves fresh discovery; "[]" is a successful empty snapshot.
func (c *Client) SendTextToWithDiscovery(recipientsJSON, text, discoveryJSON string) (string, error) {
	return c.sendTo(recipientsJSON, "text", nil, strings.NewReader(text), discoveryJSON)
}
func (c *Client) SendNamedFileToWithDiscovery(recipientsJSON, file, name, discoveryJSON string) (string, error) {
	f, err := openSendFile(file, name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return c.sendTo(recipientsJSON, "file", &name, f, discoveryJSON)
}
func openSendFile(file, name string) (*os.File, error) {
	if !core.SafeFilename(name) {
		return nil, errors.New("invalid filename")
	}
	info, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("file input must be a regular file")
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	info, err = f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("file input must be a regular file")
	}
	return f, nil
}
func (c *Client) sendTo(recipientsJSON, kind string, filename *string, input io.Reader, discoveryJSON string) (string, error) {
	var recipients []string
	if err := json.Unmarshal([]byte(recipientsJSON), &recipients); err != nil {
		return "", err
	}
	var discovery []core.Device
	if discoveryJSON != "" {
		if err := json.Unmarshal([]byte(discoveryJSON), &discovery); err != nil {
			return "", err
		}
		if discovery == nil {
			return "", errors.New("discovery snapshot must be an array")
		}
	}
	result, err := clientRequest(c, func(s *core.Session) ([]core.Delivery, error) {
		return s.SendWithDiscovery(c.config, recipients, kind, filename, input, discovery)
	})
	report := struct {
		Deliveries []core.Delivery `json:"deliveries"`
		Error      string          `json:"error,omitempty"`
	}{Deliveries: result}
	if err != nil {
		report.Error = err.Error()
	}
	data, marshalErr := json.Marshal(report)
	return string(data), marshalErr
}

// Delivery consumes an already-verified app-private staging file during Deliver.
// The core removes the staging file after the callback; errors retain the remote copy.
type Delivery interface {
	Deliver(manifestJSON, verifiedFile string) error
}

func (c *Client) Receive(id string, delivery Delivery, keep bool) error {
	if delivery == nil {
		return errors.New("delivery callback is required")
	}
	_, err := clientRequest(c, func(s *core.Session) (core.Manifest, error) {
		return s.Receive(c.config, id, func(m core.Manifest, f *os.File) error {
			metadata, err := json.Marshal(m)
			if err != nil {
				return err
			}
			return delivery.Deliver(string(metadata), f.Name())
		}, keep)
	})
	return requestError(c.ctx, err)
}

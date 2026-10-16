package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
	"keyparcel/core"
	"keyparcel/internal/buildinfo"
	"keyparcel/internal/display"
)

type names []string

func (n *names) String() string     { return strings.Join(*n, ",") }
func (n *names) Set(s string) error { *n = append(*n, s); return nil }

const usage = `keyparcel: encrypted asynchronous text/file transfers over SSH/SFTP.

Commands:
  version
  setup --host TARGET [--identity PATH] [--default] [--root PATH] [--discovery PATH]
  devices [--json]
  trust FINGERPRINT --name NAME
  untrust NAME
  send --to NAME [--to NAME] (--text TEXT | --stdin | --file PATH)
  inbox [--json | --watch --interval 2s]
  receive ID-OR-PREFIX (--stdout | --copy | --save-to PATH) [--keep] [--overwrite]
  delete (ID-OR-PREFIX [ID-OR-PREFIX ...] | --all)
  bench [--rounds 20]

Inbox UPLOADED times are approximate server file times, not verified metadata.
Times use the local timezone with seconds and a UTC offset; newest entries first.
Delete accepts multiple IDs or --all, reports a count, and continues past errors.
--all selects only completed transfers in this device's inbox on the selected host.
All commands accept --config PATH. Remote commands accept --host TARGET and
--ssh-config PATH (an optional OpenSSH configuration override).
Saving to a directory uses the original filename, or TRANSFER-ID.txt for text.
An exact output filename is also accepted. Use --overwrite with --save-to
to replace existing regular files; symlinks and nonregular outputs are rejected.
--copy accepts verified UTF-8 without NUL bytes, including file transfers, up to
16 MiB (16,777,216 bytes). Other text and file operations have no clipboard cap.
Private identities must be unencrypted ordinary Ed25519 or RSA keys (at least 2048 bits).
Interactive first connections use OpenSSH's host-key confirmation prompt.
Approval is stored in OpenSSH's known_hosts; changed keys are rejected.
Completion and unattended commands require an already trusted server key.
`

func parse(f *flag.FlagSet, args []string) error {
	flags := []string{}
	positionals := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		key, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		opt := f.Lookup(key)
		if opt == nil {
			return fmt.Errorf("unknown flag %s", arg)
		}
		flags = append(flags, arg)
		b, boolean := opt.Value.(interface{ IsBoolFlag() bool })
		if !hasValue && (!boolean || !b.IsBoolFlag()) {
			i++
			if i >= len(args) {
				return fmt.Errorf("flag %s needs a value", arg)
			}
			flags = append(flags, args[i])
		}
	}
	return f.Parse(append(flags, positionals...))
}

// Connector supplies a connection for normal operations or read-only completion.
type Connector func(context.Context, string, core.Host, string, bool) (*core.Session, error)

func openSSH(ctx context.Context, target string, host core.Host, config string, completion bool) (*core.Session, error) {
	if completion {
		return core.ConnectForCompletion(ctx, target, host, config)
	}
	return core.Connect(ctx, target, host, config)
}

func Run(ctx context.Context, args []string, in io.Reader, out, diag io.Writer) error {
	return RunWithConnector(ctx, args, in, out, diag, openSSH)
}

// RunWithConnector reuses CLI behavior with an explicitly supplied transport.
func RunWithConnector(ctx context.Context, args []string, in io.Reader, out, diag io.Writer, connect Connector) error {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		_, err := fmt.Fprintln(out, "keyparcel", buildinfo.String())
		return err
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		_, err := io.WriteString(out, usage)
		return err
	}
	if args[0] == "__complete" {
		return completeWithConnector(ctx, args[1:], out, connect)
	}
	command := args[0]
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(diag)
	configDefault, err := core.ConfigPath()
	if err != nil {
		return err
	}
	configPath := f.String("config", configDefault, "local configuration file")
	host := f.String("host", "", "saved SSH target")
	sshConfig := f.String("ssh-config", "", "OpenSSH configuration file override")
	var identity, root, discovery, name, text, file, save *string
	var setDefault, stdin, asJSON, watch, stdout, copyClip, keep, overwrite, all *bool
	var interval *time.Duration
	var rounds *int
	var to names
	switch command {
	case "setup":
		identity = f.String("identity", "~/.ssh/id_ed25519", "device identity")
		root = f.String("root", ".local/share/keyparcel", "shared remote root")
		discovery = f.String("discovery", ".ssh/authorized_keys", "remote discovery file")
		setDefault = f.Bool("default", false, "select default host")
	case "devices":
		asJSON = f.Bool("json", false, "JSON output")
	case "trust":
		name = f.String("name", "", "local name")
	case "untrust":
	case "delete":
		all = f.Bool("all", false, "delete all completed transfers in the selected inbox")
	case "send":
		f.Var(&to, "to", "approved recipient (repeatable)")
		text = f.String("text", "", "UTF-8 text")
		stdin = f.Bool("stdin", false, "text from stdin")
		file = f.String("file", "", "file to send")
	case "inbox":
		asJSON = f.Bool("json", false, "JSON output")
		watch = f.Bool("watch", false, "poll with a persistent connection")
		interval = f.Duration("interval", 2*time.Second, "poll interval")
	case "receive":
		stdout = f.Bool("stdout", false, "emit verified content")
		copyClip = f.Bool("copy", false, "copy verified UTF-8 without NUL bytes to clipboard (max 16 MiB)")
		save = f.String("save-to", "", "destination file or existing directory")
		keep = f.Bool("keep", false, "retain remote package")
		overwrite = f.Bool("overwrite", false, "replace an existing regular output file (requires --save-to)")
	case "bench":
		rounds = f.Int("rounds", 20, "measurement rounds")
	default:
		return fmt.Errorf("unknown command %q; run keyparcel help", command)
	}
	if err = parse(f, args[1:]); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	expected := 0
	if command == "trust" || command == "untrust" || command == "receive" {
		expected = 1
	}
	if command == "delete" {
		if *all && f.NArg() != 0 || !*all && f.NArg() == 0 {
			return errors.New("delete requires one or more IDs, or --all without IDs")
		}
	} else if f.NArg() != expected {
		return fmt.Errorf("%s expects %d positional arguments", command, expected)
	}
	cfg, err := core.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if command == "setup" {
		if err = core.ValidateTarget(*host); err != nil {
			return err
		}
		if *root == "" || *discovery == "" {
			return errors.New("remote root and discovery path must not be empty")
		}
		id, e := core.LoadIdentity(*identity)
		if e != nil {
			return e
		}
		cfg.Hosts[*host] = core.Host{Identity: id.Path, Fingerprint: ssh.FingerprintSHA256(id.Public), Root: *root, Discovery: *discovery}
		if *setDefault {
			cfg.DefaultHost = *host
		}
		if err = cfg.Save(*configPath); err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Configured %s\nIdentity: %s\nFingerprint: %s\n", escape(*host), escape(id.Path), ssh.FingerprintSHA256(id.Public))
		return err
	}
	if command == "untrust" {
		if _, ok := cfg.Peers[f.Arg(0)]; !ok {
			return errors.New("peer is not approved")
		}
		delete(cfg.Peers, f.Arg(0))
		return cfg.Save(*configPath)
	}
	if command == "trust" {
		supplied := false
		f.Visit(func(o *flag.Flag) {
			if o.Name == "name" {
				supplied = true
			}
		})
		if !supplied {
			return fmt.Errorf("--name is required; example: keyparcel trust %s --name bob", strconv.Quote(f.Arg(0)))
		}
		if err = core.ValidateName(*name); err != nil {
			return err
		}
		if _, exists := cfg.Peers[*name]; exists {
			return errors.New("peer name already approved; untrust it explicitly before replacement")
		}
	}
	var source *os.File
	if command == "send" {
		count := 0
		f.Visit(func(o *flag.Flag) {
			if o.Name == "text" || o.Name == "file" {
				count++
			}
		})
		if *stdin {
			count++
		}
		if count != 1 || len(to) == 0 {
			return errors.New("send requires recipients and exactly one of --text, --stdin, --file")
		}
		emptyFile := false
		f.Visit(func(o *flag.Flag) {
			if o.Name == "file" && *file == "" {
				emptyFile = true
			}
		})
		if emptyFile {
			return errors.New("--file requires a nonempty path")
		}
		if *file != "" {
			// Reject pipes before opening: a read-only open can wait for a writer.
			info, e := os.Stat(*file)
			if e != nil {
				return fmt.Errorf("input file: %w", e)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("input file %q is not a regular file", *file)
			}
			source, err = os.Open(*file)
			if err != nil {
				return fmt.Errorf("input file: %w", err)
			}
			defer source.Close()
			info, e = source.Stat()
			if e != nil {
				return fmt.Errorf("input file: %w", e)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("input file %q is not a regular file", *file)
			}
		}
	}
	if command == "receive" {
		count := 0
		if *stdout {
			count++
		}
		if *copyClip {
			count++
		}
		saveSupplied := false
		f.Visit(func(o *flag.Flag) {
			if o.Name == "save-to" {
				saveSupplied = true
			}
		})
		if *overwrite && !saveSupplied {
			return errors.New("--overwrite requires --save-to")
		}
		if saveSupplied {
			if *save == "" {
				return errors.New("--save-to requires a nonempty path")
			}
			count++
		}
		if count != 1 {
			return fmt.Errorf("receive requires exactly one output action:\n  --stdout          Print content to standard output\n  --copy            Copy text to the clipboard\n  --save-to PATH    Save content to a file or directory\n\nExample: keyparcel receive %s --stdout\n--keep retains the server copy after successful receipt.", escape(f.Arg(0)))
		}
	}
	if command == "inbox" {
		if *interval <= 0 {
			return errors.New("interval must be positive")
		}
		if *watch && *asJSON {
			return errors.New("--watch and --json cannot be combined")
		}
	}
	if command == "bench" && (*rounds < 1 || *rounds > 1000) {
		return errors.New("rounds must be between 1 and 1000")
	}
	target, h, err := cfg.Select(*host)
	if err != nil {
		return err
	}
	if command == "inbox" && *watch {
		return watchInbox(ctx, target, h, *sshConfig, cfg, *interval, out, diag, connect)
	}
	s, err := connect(ctx, target, h, *sshConfig, false)
	if err != nil {
		return err
	}
	defer s.Close()
	switch command {
	case "devices":
		devices, e := s.Discover(cfg)
		if e != nil {
			return e
		}
		if *asJSON {
			return json.NewEncoder(out).Encode(devices)
		}
		return printDevices(out, target, devices)
	case "trust":
		devices, e := s.Discover(cfg)
		if e != nil {
			return e
		}
		for _, d := range devices {
			if d.Discovered && d.Fingerprint == f.Arg(0) {
				if _, err := core.ParsePublic(d.PublicKey); err != nil {
					return err
				}
				for _, pub := range cfg.Peers {
					if pub == d.PublicKey {
						return errors.New("key is already approved under another name")
					}
				}
				cfg.Peers[*name] = d.PublicKey
				return cfg.Save(*configPath)
			}
		}
		return errors.New("fingerprint not present in current discovery")
	case "send":
		var input io.Reader = strings.NewReader(*text)
		kind := "text"
		var filename *string
		if *stdin {
			input = in
		}
		if source != nil {
			input = source
			kind = "file"
			base := filepath.Base(*file)
			filename = &base
		}
		results, e := s.Send(cfg, to, kind, filename, input)
		for _, d := range results {
			if d.Unconfirmed {
				fmt.Fprintf(diag, "%s: delivery unconfirmed; check the recipient before retrying: %s\n", escape(d.Recipient), escape(d.Error))
			} else if d.Error != "" {
				fmt.Fprintf(diag, "%s: delivery failed: %s\n", escape(d.Recipient), escape(d.Error))
			} else {
				fmt.Fprintf(out, "%s: stored %s\n", escape(d.Recipient), d.ID)
			}
		}
		return e
	case "inbox":
		return printInbox(s, cfg, target, *asJSON, out, diag)
	case "delete":
		count, failures, e := s.Delete(f.Args(), *all)
		for _, failure := range failures {
			fmt.Fprintf(diag, "%s: deletion failed: %s\n", escape(failure.ID), escape(failure.Reason))
		}
		if _, writeErr := fmt.Fprintf(out, "Deleted %d transfer(s)\n", count); writeErr != nil {
			return writeErr
		}
		return e
	case "receive":
		m, e := s.Receive(cfg, f.Arg(0), func(m core.Manifest, file *os.File) error {
			if *stdout {
				_, e := io.Copy(out, file)
				return e
			}
			if *copyClip {
				return clipboard(ctx, file)
			}
			dest, e := core.SaveContent(*save, m, file, *overwrite)
			if e == nil {
				_, e = fmt.Fprintf(diag, "Saved %s\n", escape(dest))
			}
			return e
		}, *keep)
		if e == nil && !*stdout {
			_, e = fmt.Fprintf(diag, "Received %s\n", m.ID)
		}
		return e
	case "bench":
		return benchmark(ctx, target, h, *sshConfig, s, cfg, *rounds, out, connect)
	}
	return nil
}
func escape(s string) string { q := strconv.Quote(s); return q[1 : len(q)-1] }
func printDevices(out io.Writer, target string, devices []core.Device) error {
	fmt.Fprintf(out, "Host: %s\n", escape(target))
	table := func(present bool) error {
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tFINGERPRINT\tTRUST\tREMOTE COMMENT")
		for _, d := range devices {
			if d.Discovered != present {
				continue
			}
			name := d.Name
			if name == "" {
				name = "-"
			}
			trust := "unapproved"
			if d.Trusted {
				trust = "approved"
			}
			if d.Self {
				trust = "self"
			}
			if !d.Supported {
				trust = "unsupported"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", escape(name), d.Fingerprint, trust, escape(d.Comment))
		}
		return w.Flush()
	}
	if err := table(true); err != nil {
		return err
	}
	for _, d := range devices {
		if !d.Discovered {
			fmt.Fprintln(out, "\nKnown peers not discovered on this host:")
			return table(false)
		}
	}
	return nil
}
func printInbox(s *core.Session, cfg *core.Config, target string, asJSON bool, out, diag io.Writer) error {
	items, bad, ids, err := s.List(cfg)
	if err != nil {
		return err
	}
	for _, r := range bad {
		fmt.Fprintf(diag, "Rejected %s: %s\n", r.ID, escape(r.Reason))
	}
	if asJSON {
		err = json.NewEncoder(out).Encode(items)
	} else {
		fmt.Fprintf(out, "Host: %s\n", escape(target))
		if len(items) == 0 {
			if len(ids) == 0 {
				_, err = fmt.Fprintln(out, "Inbox empty.")
			} else {
				_, err = fmt.Fprintln(out, "No accepted items.")
			}
		} else {
			w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tFROM\tTYPE\tNAME\tSIZE\tUPLOADED")
			for _, item := range items {
				name := "-"
				if item.Filename != nil {
					name = *item.Filename
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", core.ShortID(item.ID, ids), escape(item.From), item.Type, escape(name), display.Size(item.Size), item.UploadedAt.Local().Format("2006-01-02 15:04:05 -07:00"))
			}
			err = w.Flush()
		}
	}
	if err != nil {
		return err
	}
	if len(bad) > 0 {
		return fmt.Errorf("%d rejected package(s)", len(bad))
	}
	return nil
}

const maxClipboardBytes = 16 * 1024 * 1024

func clipboard(ctx context.Context, input *os.File) error {
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if info.Size() > maxClipboardBytes {
		return errors.New("transfer exceeds the 16 MiB clipboard limit; use --save-to or --stdout")
	}
	reader := bufio.NewReader(input)
	for {
		r, size, e := reader.ReadRune()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if r == 0 || r == utf8.RuneError && size == 1 {
			return errors.New("clipboard requires valid UTF-8 without NUL bytes; use --save-to or --stdout")
		}
	}
	if _, err = input.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		// Explicit text type and UTF-8 decoding avoid format sniffing and locale dependence.
		cmd = exec.CommandContext(ctx, "osascript", "-l", "JavaScript", "-e", `
ObjC.import('AppKit');
var data = $.NSFileHandle.fileHandleWithStandardInput.readDataToEndOfFile;
var text = $.NSString.alloc.initWithDataEncoding(data, $.NSUTF8StringEncoding);
if (text.isNil()) throw Error('invalid UTF-8');
var pasteboard = $.NSPasteboard.generalPasteboard;
pasteboard.clearContents;
if (!pasteboard.setStringForType(text, $.NSPasteboardTypeString))
    throw Error('cannot write plain-text pasteboard');
`)
	} else if os.Getenv("WAYLAND_DISPLAY") != "" {
		cmd = exec.CommandContext(ctx, "wl-copy", "--type", "text/plain;charset=utf-8")
	} else if os.Getenv("DISPLAY") != "" {
		cmd = exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-in")
	} else {
		return errors.New("no desktop clipboard session available")
	}
	cmd.Stdin = input
	// Clipboard tools fork an owner process; a captured stderr pipe can keep Wait blocked.
	log, err := os.CreateTemp("", "keyparcel-clipboard-*")
	if err != nil {
		return err
	}
	defer log.Close()
	defer os.Remove(log.Name())
	cmd.Stderr = log
	if err = cmd.Run(); err != nil {
		log.Seek(0, 0)
		diag, _ := io.ReadAll(io.LimitReader(log, 64*1024))
		return fmt.Errorf("clipboard: %w: %s", err, diag)
	}
	return nil
}
func watchInbox(ctx context.Context, target string, h core.Host, sshConfig string, cfg *core.Config, interval time.Duration, out, diag io.Writer, connect Connector) error {
	var s *core.Session
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if s == nil {
			var err error
			s, err = connect(ctx, target, h, sshConfig, false)
			if err != nil {
				fmt.Fprintf(diag, "Disconnected: %s\n", escape(err.Error()))
			} else {
				fmt.Fprintln(diag, "Connected")
			}
		}
		if s != nil {
			err := printInbox(s, cfg, target, false, out, diag)
			if err != nil {
				if _, e := s.Client.Getwd(); e != nil {
					fmt.Fprintf(diag, "Disconnected: %s\n", escape(e.Error()))
					s.Close()
					s = nil
				} else {
					fmt.Fprintln(diag, err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func benchmark(ctx context.Context, target string, h core.Host, sshConfig string, s *core.Session, cfg *core.Config, rounds int, out io.Writer, connect Connector) error {
	metrics := map[string][]time.Duration{"fresh SSH/SFTP": {}, "persistent inbox list": {}, "local signed age roundtrip": {}, "persistent verified 5-byte transfer": {}}
	benchCfg := &core.Config{Hosts: cfg.Hosts, Peers: map[string]string{"benchmark-self": core.PublicText(s.Identity.Public)}}
	for i := 0; i < rounds; i++ {
		start := time.Now()
		fresh, err := connect(ctx, target, h, sshConfig, false)
		if err != nil {
			return err
		}
		metrics["fresh SSH/SFTP"] = append(metrics["fresh SSH/SFTP"], time.Since(start))
		fresh.Close()
		start = time.Now()
		if _, err = s.IDs(); err != nil {
			return err
		}
		metrics["persistent inbox list"] = append(metrics["persistent inbox list"], time.Since(start))
		start = time.Now()
		f, m, err := core.NewPackage(ctx, s.Identity, []ssh.PublicKey{s.Identity.Public}, "text", nil, strings.NewReader("hello"))
		if err != nil {
			return err
		}
		_, plain, err := core.ReadPackage(ctx, f, s.Identity, benchCfg.Peers, m.ID)
		if err == nil {
			var verified *os.File
			verified, err = core.VerifyContent(plain, m)
			if err == nil {
				verified.Close()
				os.Remove(verified.Name())
			}
		}
		f.Close()
		os.Remove(f.Name())
		if err != nil {
			return err
		}
		metrics["local signed age roundtrip"] = append(metrics["local signed age roundtrip"], time.Since(start))
		start = time.Now()
		result, err := s.Send(benchCfg, []string{"benchmark-self"}, "text", nil, strings.NewReader("hello"))
		if err != nil {
			return err
		}
		_, err = s.Receive(benchCfg, result[0].ID, func(_ core.Manifest, f *os.File) error {
			b, e := io.ReadAll(f)
			if e == nil && string(b) != "hello" {
				e = errors.New("benchmark byte mismatch")
			}
			return e
		}, false)
		if err != nil {
			return err
		}
		metrics["persistent verified 5-byte transfer"] = append(metrics["persistent verified 5-byte transfer"], time.Since(start))
	}
	fmt.Fprintln(out, "Measurements (5-byte transfer payload):")
	for _, name := range []string{"fresh SSH/SFTP", "persistent inbox list", "local signed age roundtrip", "persistent verified 5-byte transfer"} {
		values := metrics[name]
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		median := values[len(values)/2]
		if len(values)%2 == 0 {
			median = values[len(values)/2-1] + (median-values[len(values)/2-1])/2
		}
		fmt.Fprintf(out, "%s: median=%s p95=%s rounds=%d\n", name, median, values[(len(values)*95+99)/100-1], rounds)
	}
	return nil
}

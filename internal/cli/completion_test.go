package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"keyparcel/core"
)

func TestLocalCompletion(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	// A valid public key is enough for local completion; no identity or SSH is needed.
	pub := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJDBUUygtqtyPOsA8u72ZVUWJHWfcPW043TqZMsnMg4B"
	cfg := &core.Config{Hosts: map[string]core.Host{"inbox": {Identity: "missing", Fingerprint: "fp", Root: "root", Discovery: "keys"}}, Peers: map[string]string{"bob": pub}, DefaultHost: "inbox"}
	if err := cfg.Save(config); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		words, want []string
	}{
		{"commands", []string{"tr"}, []string{"trust"}},
		{"inbox policy", []string{"inbox", "--access-method", ""}, []string{"auto", "sftp", "ssh"}},
		{"setup policy", []string{"setup", "--access-method", "sf"}, []string{"sftp"}},
		{"next name flag", []string{"trust", "SHA256:test", ""}, []string{"--name"}},
		{"hosts", []string{"devices", "--host", "in"}, []string{"inbox"}},
		{"inline hosts", []string{"devices", "--host=in"}, []string{"--host=inbox"}},
		{"recipients", []string{"send", "--to", ""}, []string{"bob", "self"}},
		{"untrust", []string{"untrust", "b"}, []string{"bob"}},
		{"paths delegated to Bash", []string{"send", "--file", dir}, nil},
		{"no text suggestions", []string{"send", "--text", ""}, nil},
		{"overwrite flag", []string{"receive", "12345678", "--over"}, []string{"--overwrite"}},
		{"delete all flag", []string{"delete", "--a"}, []string{"--access-method", "--all"}},
		{"delete all excludes IDs", []string{"delete", "--all", ""}, nil},
		{"delete selected excludes all flag", []string{"delete", "12345678", "--a"}, []string{"--access-method"}},
		{"malformed config", []string{"trust", "--config", filepath.Join(dir, "absent"), ""}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			words := append([]string{}, tc.words...)
			if len(words) > 1 {
				words = append(words[:1], append([]string{"--config", config}, words[1:]...)...)
			}
			var out bytes.Buffer
			if err := complete(context.Background(), words, &out); err != nil {
				t.Fatal(err)
			}
			var got []string
			if out.Len() > 0 {
				got = strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
func TestCompletionValuesCannotInjectLines(t *testing.T) {
	var out bytes.Buffer
	if err := emitCompletion(&out, []string{"good", "bad\ncommand", "bad\x1b[31m", "good"}, ""); err != nil {
		t.Fatal(err)
	}
	if out.String() != "good\n" {
		t.Fatal(out.String())
	}
}

func TestRemoteCompletionDeadlineAndSilentFailure(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config.json")
	cfg := &core.Config{Hosts: map[string]core.Host{"inbox": {Identity: "missing", Fingerprint: "fp", Root: "root", Discovery: "keys"}}, Peers: map[string]string{}, DefaultHost: "inbox"}
	if err := cfg.Save(config); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"trust", "receive", "delete"} {
		for _, timeout := range []time.Duration{5 * time.Second, 2 * time.Second, 0} {
			t.Run(command+"/"+timeout.String(), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					start := time.Now()
					ctx := context.Background()
					if timeout < 5*time.Second {
						var cancel context.CancelFunc
						ctx, cancel = context.WithTimeout(ctx, timeout)
						defer cancel()
					}
					calls := 0
					connect := func(ctx context.Context, target string, host core.Host, sshConfig string, completion bool) (*core.Session, error) {
						calls++
						expected := cfg.Hosts["inbox"]
						expected.AccessMethod = "ssh"
						if !completion || target != "inbox" || host != expected || sshConfig != "fixture-ssh-config" {
							t.Fatal("completion changed its selected host or noninteractive transport mode")
						}
						deadline, ok := ctx.Deadline()
						if !ok || !deadline.Equal(start.Add(timeout)) {
							t.Fatalf("completion deadline = %v, want %v", deadline, start.Add(timeout))
						}
						<-ctx.Done() // The bubble advances time without a real five-second wait.
						return nil, ctx.Err()
					}
					var out bytes.Buffer
					words := []string{command, "--config", config, "--ssh-config", "fixture-ssh-config", ""}
					if err := completeWithConnector(ctx, words, &out, connect); err != nil || out.Len() != 0 {
						t.Fatalf("completion timeout must remain silent: %q %v", out.String(), err)
					}
					if calls != 1 || time.Since(start) != timeout {
						t.Fatalf("completion ignored its deadline: calls=%d elapsed=%v want=%v", calls, time.Since(start), timeout)
					}
				})
			})
		}
	}
}

func TestActionableMissingArguments(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"trust", "SHA256:test"}, "--name is required"},
		{[]string{"trust", "SHA256:test", "--name", "self"}, "reserved"},
		{[]string{"receive", "deadbeef"}, "--save-to PATH"},
	} {
		var out bytes.Buffer
		args := append(tc.args, "--config", filepath.Join(t.TempDir(), "config"))
		err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: %v", tc.args, err)
		}
	}
}

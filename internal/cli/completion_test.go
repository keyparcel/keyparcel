package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
		{"next name flag", []string{"trust", "SHA256:test", ""}, []string{"--name"}},
		{"hosts", []string{"devices", "--host", "in"}, []string{"inbox"}},
		{"inline hosts", []string{"devices", "--host=in"}, []string{"--host=inbox"}},
		{"recipients", []string{"send", "--to", ""}, []string{"bob", "self"}},
		{"untrust", []string{"untrust", "b"}, []string{"bob"}},
		{"paths delegated to Bash", []string{"send", "--file", dir}, nil},
		{"no text suggestions", []string{"send", "--text", ""}, nil},
		{"overwrite flag", []string{"receive", "12345678", "--over"}, []string{"--overwrite"}},
		{"delete all flag", []string{"delete", "--a"}, []string{"--all"}},
		{"delete all excludes IDs", []string{"delete", "--all", ""}, nil},
		{"delete selected excludes all flag", []string{"delete", "12345678", "--a"}, nil},
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

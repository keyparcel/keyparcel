package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Stub only the shell's completion option and semantic helper; keep the real wrapper.
func TestBashCompletionWrapper(t *testing.T) {
	script, err := filepath.Abs("../../completions/keyparcel.bash")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		words []string
		path  bool
		want  string
	}{
		{"tilde", []string{"send", "--file", "~/Down"}, true, ""},
		{"spaces", []string{"send", "--file", "some file"}, true, ""},
		{"literal equals", []string{"send", "--file", "some", "=", "file"}, true, ""},
		{"assignment equals", []string{"send", "--file", "=", "some", "=", "file"}, true, ""},
		{"inline assignment", []string{"send", "--file=~/some file"}, true, ""},
		{"save", []string{"receive", "id", "--save-to", ""}, true, ""},
		{"identity", []string{"setup", "--identity", "~/.ssh/"}, true, ""},
		{"config", []string{"devices", "--config", "a=b"}, true, ""},
		{"ssh config", []string{"devices", "--ssh-config", "a b"}, true, ""},
		{"text stays semantic", []string{"send", "--text", "some"}, false, "some"},
		{"after completed path", []string{"send", "--file", "file", ""}, false, "some"},
		{"fingerprint", []string{"trust", "SHA256", ":", "abc"}, false, "def"},
		{"flag assignment", []string{"devices", "--host", "=", "in"}, false, "inbox"},
		{"next name flag", []string{"trust", "SHA256:abc", ""}, false, "some"},
		{"config equals before recipients", []string{"send", "--config", "/tmp/a", "=", "b.json", "--to", ""}, false, "some"},
		{"ssh config equals before remote", []string{"trust", "--ssh-config", "/tmp/ssh", "=", "config", "SHA256:abc", ""}, false, "def"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `source "$1"; shift
COMP_WORDS=(keyparcel "$@"); COMP_CWORD=$((${#COMP_WORDS[@]}-1))
original=$(declare -p COMP_WORDS); delegated=0
compgen() { [[ $1 == -f && $2 == -- ]] || exit 13; return 0; }
compopt() { [[ "$*" == '-o filenames' ]] && delegated=1; return 0; }
_keyparcel
[[ $(declare -p COMP_WORDS) == "$original" ]] || exit 10
if [[ $EXPECT_PATH == true ]]; then
 [[ $delegated == 1 && ${#COMPREPLY[@]} == 0 ]] || exit 11
else
 [[ $delegated == 0 && ${COMPREPLY[0]} == "$EXPECT_REPLY" ]] || exit 12
fi
`
			// command bypasses shell functions, so use a real disposable executable.
			dir := t.TempDir()
			helper := "#!/bin/bash\n"
			if tc.path {
				helper += "echo 'path helper must not run' >&2; touch \"$MARKER\"\n"
			} else {
				helper += "printf '%s\\n' \"$@\" > \"$ARG_LOG\"\n[[ -z $EXPECTED_QUERY || \"$*\" == \"$EXPECTED_QUERY\" ]] || exit 42\nif [[ \"$*\" == *--host=in* ]]; then echo --host=inbox; elif [[ \"$*\" == *--ssh-config* ]]; then echo def; elif [[ \"$*\" == *SHA256:abc ]]; then echo SHA256:def; else echo some; fi\n"
			}
			writeCompletionHelper(t, filepath.Join(dir, "keyparcel"), helper)
			args := append([]string{"--noprofile", "--norc", "-c", body, "bash", script}, tc.words...)
			expectedQuery := ""
			switch tc.name {
			case "fingerprint":
				expectedQuery = "__complete trust SHA256:abc"
			case "flag assignment":
				expectedQuery = "__complete devices --host=in"
			case "next name flag":
				expectedQuery = "__complete trust SHA256:abc "
			case "config equals before recipients":
				expectedQuery = "__complete send --config /tmp/a=b.json --to "
			case "ssh config equals before remote":
				expectedQuery = "__complete trust --ssh-config /tmp/ssh=config SHA256:abc "
			}
			cmd := exec.Command("bash", args...)
			cmd.Env = append(cmd.Environ(), "PATH="+dir+":/usr/bin:/bin", "EXPECT_PATH="+map[bool]string{true: "true", false: "false"}[tc.path], "EXPECT_REPLY="+tc.want, "EXPECTED_QUERY="+expectedQuery, "MARKER="+filepath.Join(dir, "called"), "ARG_LOG="+filepath.Join(dir, "args"))
			if out, err := cmd.CombinedOutput(); err != nil {
				argsSeen, _ := os.ReadFile(filepath.Join(dir, "args"))
				t.Fatalf("%v: %s; helper args %q", err, out, argsSeen)
			}
			if tc.path {
				if _, err := os.Stat(filepath.Join(dir, "called")); !os.IsNotExist(err) {
					t.Fatal("path completion invoked Go helper")
				}
			}
		})
	}
}
func writeCompletionHelper(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
}

func TestBashFilenameCompletion(t *testing.T) {
	script, err := filepath.Abs("../../completions/keyparcel.bash")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"space name.txt", "literal=name.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name  string
		words []string
		want  string
	}{
		{"tilde", []string{"--file", "~/space"}, "~/space name.txt"},
		{"space", []string{"--file", filepath.Join(dir, "space")}, filepath.Join(dir, "space name.txt")},
		{"literal equals", []string{"--file", filepath.Join(dir, "literal"), "=", "na"}, "name.txt"},
		{"trailing equals", []string{"--file", "=", filepath.Join(dir, "literal"), "="}, "name.txt"},
		{"inline", []string{"--file=" + filepath.Join(dir, "space")}, "--file=" + filepath.Join(dir, "space name.txt")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--noprofile", "--norc", "-c", `source "$1"; shift; compopt() { :; }; COMP_WORDS=(keyparcel send "$@"); COMP_CWORD=$((${#COMP_WORDS[@]}-1)); _keyparcel; printf '%s' "${COMPREPLY[@]}"`, "bash", script}, tc.words...)
			cmd := exec.Command("bash", args...)
			cmd.Env = append(cmd.Environ(), "HOME="+dir)
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != tc.want {
				t.Fatalf("got %q, error %v; want %q", out, err, tc.want)
			}
		})
	}
}

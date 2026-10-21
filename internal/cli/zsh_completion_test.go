package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the real completion system and line editor, including its quoting.
func TestZshCompletionInsertion(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	completionSource, err := os.ReadFile("../../completions/_keyparcel")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, line, candidates, want string
		path                         bool
	}{
		{"command", "keyparcel tr", "trust", "keyparcel trust ", false},
		{"quiet empty", "keyparcel receive dead", "", "keyparcel receive dead", false},
		{"option", "keyparcel receive id --ov", "--overwrite", "keyparcel receive id --overwrite ", false},
		{"inline option", "keyparcel devices --host=in", "--host=inbox", "keyparcel devices --host=inbox ", false},
		{"quoted config", "keyparcel send --config 'some config' --to b", "bob", "keyparcel send --config 'some config' --to bob ", false},
		{"fingerprint", "keyparcel trust SHA256:ab", "SHA256:abcdef", "keyparcel trust SHA256:abcdef ", false},
		{"ordered first", "keyparcel send --to ", "self\nbob", "keyparcel send --to self", false},
		{"ordered second", "keyparcel send --to \t", "self\nbob", "keyparcel send --to bob", false},
		{"quoted name", "keyparcel untrust ", "some name", `keyparcel untrust some\ name `, false},
		{"unicode", "keyparcel untrust 名", "名前", "keyparcel untrust 名前 ", false},
		{"literal shell syntax", "keyparcel untrust ", "$(touch${IFS}injected)", `keyparcel untrust \$\(touch\$\{IFS\}injected\) `, false},
		{"local config", "keyparcel devices --config literal=", "", "keyparcel devices --config literal=name.txt ", true},
		{"local SSH config", "keyparcel devices --ssh-config space", "", `keyparcel devices --ssh-config space\ name.txt `, true},
		{"local space", "keyparcel send --file space", "", `keyparcel send --file space\ name.txt `, true},
		{"local unicode", "keyparcel send --file 文", "", "keyparcel send --file 文書.txt ", true},
		{"local equals", "keyparcel send --file=literal=", "", "keyparcel send --file=literal=name.txt ", true},
		{"local quoted", `keyparcel receive id --save-to 'space`, "", `keyparcel receive id --save-to 'space name.txt' `, true},
		{"local tilde", "keyparcel setup --identity ~/space", "", `keyparcel setup --identity ~/space\ name.txt `, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			completion := filepath.Join(dir, "completions")
			if err := os.Mkdir(completion, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(completion, "_keyparcel"), completionSource, 0600); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{"space name.txt", "文書.txt", "literal=name.txt"} {
				if err := os.WriteFile(filepath.Join(dir, file), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			writeCompletionHelper(t, filepath.Join(dir, "keyparcel"), "#!/bin/sh\nprintf '%s\\n' \"$CANDIDATES\"\nprintf '%s\\n' \"$@\" > \"$QUERY_LOG\"\n")
			rc := `PROMPT=''
fpath=("$COMPLETIONS" $fpath)
autoload -Uz compinit
# Ignore insecure host additions; compaudit still checks our private fixture.
compinit -D -i </dev/null || { print -r -- KPFAILED; exit 1; }
bindkey '^I' menu-complete
capture() { print -r -- "KPBUFFER:$BUFFER:KPEND"; zle send-break }
zle -N capture
bindkey '^X' capture
print -r -- KPREADY
`
			if err := os.WriteFile(filepath.Join(dir, ".zshrc"), []byte(rc), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, zsh, "-f", "-c", `zmodload zsh/zpty
zpty child zsh -d -i
zpty -r child ready '*KP(READY|FAILED)*'
[[ $ready == *KPREADY* ]] || { print -r -- "$ready"; zpty -d child; exit 1; }
zpty -w -n child "$INPUT"$'\t\x18'
zpty -r child result '*KPBUFFER:*:KPEND*'
print -r -- "$result"
zpty -d child
`)
			cmd.Dir = dir
			cmd.Env = append(cmd.Environ(), "ZDOTDIR="+dir, "HOME="+dir, "PATH="+dir+":"+os.Getenv("PATH"), "TERM=xterm", "COMPLETIONS="+completion, "INPUT="+tc.line, "CANDIDATES="+tc.candidates, "QUERY_LOG="+filepath.Join(dir, "query"))
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "KPBUFFER:"+tc.want+":KPEND") {
				t.Fatalf("completion got %q (%v), want buffer %q", out, err, tc.want)
			}
			if tc.name == "literal shell syntax" {
				parsed := exec.Command(zsh, "-f", "-c", `eval "set -- $1"; print -r -- "$3"`, "zsh", tc.want)
				parsed.Dir = dir
				value, err := parsed.CombinedOutput()
				if err != nil || strings.TrimSpace(string(value)) != tc.candidates {
					t.Fatalf("literal insertion changed value: %q (%v)", value, err)
				}
				if _, err := os.Stat(filepath.Join(dir, "injected")); !os.IsNotExist(err) {
					t.Fatal("completion executed shell syntax")
				}
			}
			query, err := os.ReadFile(filepath.Join(dir, "query"))
			if tc.path {
				if !os.IsNotExist(err) {
					t.Fatalf("path completion called CLI: %q (%v)", query, err)
				}
			} else if err != nil || !strings.HasPrefix(string(query), "__complete\n") {
				t.Fatalf("missing semantic query: %q (%v)", query, err)
			}
			if tc.name == "quoted config" && string(query) != "__complete\nsend\n--config\nsome config\n--to\nb\n" {
				t.Fatalf("quoted config was split: %q", query)
			}
		})
	}
}

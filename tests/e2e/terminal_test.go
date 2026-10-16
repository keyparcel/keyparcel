//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// script gives the real CLI a controlling terminal while its data uses separate files/pipes.
func (w *world) terminal(c client, reply string, input []byte, args ...string) ([]byte, []byte, error) {
	w.t.Helper()
	base := filepath.Join(c.dir, "terminal-"+time.Now().Format("150405.000000000"))
	position := 1
	if args[0] == "__complete" {
		position = 2
	}
	commandArgs := append([]string{binaryPath}, args[:position]...)
	commandArgs = append(commandArgs, "--config", c.config, "--ssh-config", c.sshConfig)
	args = append(commandArgs, args[position:]...)
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	shell := strings.Join(quoted, " ") + " > " + shellQuote(base+".stdout")
	if input != nil {
		if err := os.WriteFile(base+".input", input, 0600); err != nil {
			w.t.Fatal(err)
		}
		shell = "cat " + shellQuote(base+".input") + " | " + shell
	}
	ctx, cancel := context.WithTimeout(w.ctx, 15*time.Second)
	defer cancel()
	params := append(docker[1:], "exec", "-i", c.id, "timeout", "12s", "script", "-qefc", shell, "/dev/null")
	cmd := exec.CommandContext(ctx, docker[0], params...)
	cmd.WaitDelay = 2 * time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		w.t.Fatal(err)
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		w.t.Fatal(err)
	}
	var diag, terminal bytes.Buffer
	cmd.Stderr = &diag
	if err := cmd.Start(); err != nil {
		w.t.Fatal(err)
	}
	buffer := make([]byte, 4096)
	answered := false
	for {
		n, readErr := out.Read(buffer)
		terminal.Write(buffer[:n])
		if !answered && reply != "" && strings.Contains(terminal.String(), "Are you sure you want to continue connecting") {
			if _, err := io.WriteString(in, reply); err != nil {
				w.t.Fatal(err)
			}
			answered = true
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			w.t.Fatal(readErr)
		}
	}
	err = cmd.Wait()
	data, readErr := os.ReadFile(base + ".stdout")
	if readErr != nil {
		w.t.Fatalf("missing terminal command output: %v; terminal: %s; docker: %s", readErr, terminal.Bytes(), diag.Bytes())
	}
	text := append(terminal.Bytes(), diag.Bytes()...)
	w.transcript.Write(text)
	return data, text, err
}

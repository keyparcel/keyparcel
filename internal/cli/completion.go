package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"keyparcel/core"
)

var completionFlags = map[string][]string{
	"setup":   {"--host", "--identity", "--default", "--root", "--discovery"},
	"devices": {"--json"}, "trust": {"--name"}, "untrust": {},
	"send":    {"--to", "--text", "--stdin", "--file"},
	"inbox":   {"--json", "--watch", "--interval"},
	"receive": {"--stdout", "--copy", "--save-to", "--keep", "--overwrite"}, "delete": {"--all"}, "bench": {"--rounds"},
}
var valueFlags = map[string]bool{"--host": true, "--config": true, "--ssh-config": true, "--identity": true, "--root": true, "--discovery": true, "--name": true, "--to": true, "--text": true, "--file": true, "--save-to": true, "--interval": true, "--rounds": true}

// Completion is read-only and failures intentionally produce no shell diagnostics.
func complete(ctx context.Context, words []string, out io.Writer) error {
	return completeWithConnector(ctx, words, out, openSSH)
}
func completeWithConnector(ctx context.Context, words []string, out io.Writer, connect Connector) error {
	if len(words) == 0 {
		return nil
	}
	command, cur := words[0], words[len(words)-1]
	if len(words) == 1 {
		candidates := []string{"help"}
		for c := range completionFlags {
			candidates = append(candidates, c)
		}
		return emitCompletion(out, candidates, cur)
	}
	previous := words[1 : len(words)-1]
	configPath, err := core.ConfigPath()
	if err != nil {
		return nil
	}
	host, sshConfig, pending := "", "", ""
	positionals := []string{}
	used := map[string]bool{}
	allSelected := false
	for _, word := range previous {
		if pending != "" {
			switch pending {
			case "--config":
				configPath = word
			case "--host":
				host = word
			case "--ssh-config":
				sshConfig = word
			}
			pending = ""
			continue
		}
		key, val, inline := strings.Cut(word, "=")
		used[key] = true
		if key == "--all" {
			allSelected = true
			if inline {
				allSelected, err = strconv.ParseBool(val)
				if err != nil {
					return nil
				}
			}
		}
		if valueFlags[key] {
			if inline {
				switch key {
				case "--config":
					configPath = val
				case "--host":
					host = val
				case "--ssh-config":
					sshConfig = val
				}
			} else {
				pending = key
			}
		} else if !strings.HasPrefix(word, "-") {
			positionals = append(positionals, word)
		}
	}
	inlinePrefix := ""
	if key, val, ok := strings.Cut(cur, "="); ok && valueFlags[key] {
		pending = key
		cur = val
		inlinePrefix = key + "="
	}
	emit := func(values []string) error {
		if inlinePrefix != "" {
			filtered := []string{}
			for _, v := range values {
				if strings.HasPrefix(v, cur) {
					filtered = append(filtered, inlinePrefix+v)
				}
			}
			return emitCompletion(out, filtered, inlinePrefix+cur)
		}
		return emitCompletion(out, values, cur)
	}
	if pending == "" && (strings.HasPrefix(cur, "-") || len(positionals) > 0 && command != "delete" || cur == "" && command != "trust" && command != "untrust" && command != "receive" && command != "delete") {
		if command == "trust" && len(positionals) > 0 && cur == "" && !used["--name"] {
			return emit([]string{"--name"})
		}
		flags, ok := completionFlags[command]
		if !ok {
			return nil
		}
		values := []string{}
		for _, flag := range append(append([]string{}, flags...), "--host", "--config", "--ssh-config") {
			if (flag != "--all" || len(positionals) == 0) && (!used[flag] || flag == "--to") {
				values = append(values, flag)
			}
		}
		return emit(values)
	}
	cfg, err := core.LoadConfig(configPath)
	if err != nil {
		return nil
	}
	if pending == "--host" {
		values := []string{}
		for h := range cfg.Hosts {
			values = append(values, h)
		}
		return emit(values)
	}
	if pending == "--to" || command == "untrust" && pending == "" {
		values := []string{}
		if pending == "--to" {
			values = append(values, "self")
		}
		for p := range cfg.Peers {
			values = append(values, p)
		}
		return emit(values)
	}
	if pending != "" && !(command == "trust" && pending == "--name") {
		return nil
	}
	if command != "trust" && command != "receive" && command != "delete" {
		return nil
	}
	if command == "receive" && len(positionals) > 0 || command == "delete" && allSelected {
		return nil
	}
	target, h, err := cfg.Select(host)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	session, err := connect(ctx, target, h, sshConfig, true)
	if err != nil {
		return nil
	}
	defer session.Close()
	if command == "trust" {
		devices, e := session.Discover(cfg)
		if e != nil {
			return nil
		}
		values := []string{}
		for _, d := range devices {
			if !d.Discovered || !d.Supported || d.Trusted || d.Self {
				continue
			}
			if pending == "--name" {
				if len(positionals) == 1 && positionals[0] == d.Fingerprint {
					if name := core.SuggestedPeerName(d.Comment, cfg.Peers); name != "" {
						values = append(values, name)
					}
				}
			} else {
				values = append(values, d.Fingerprint)
			}
		}
		return emit(values)
	}
	if command == "delete" {
		ids, e := session.IDs()
		if e != nil {
			return nil
		}
		selected := map[string]bool{}
		for _, arg := range positionals {
			if id, e := core.ResolveID(arg, ids); e == nil {
				selected[id] = true
			}
		}
		remaining := []string{}
		for _, id := range ids {
			if !selected[id] {
				remaining = append(remaining, id)
			}
		}
		return emit(remaining)
	}
	items, _, _, e := session.List(cfg)
	if e != nil {
		return nil
	}
	ids := []string{}
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return emit(ids)
}
func emitCompletion(out io.Writer, values []string, prefix string) error {
	sort.Strings(values)
	last := ""
	for _, value := range values {
		if value == "" || value == last || !strings.HasPrefix(value, prefix) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			continue
		}
		last = value
		if _, err := fmt.Fprintln(out, value); err != nil {
			return err
		}
	}
	return nil
}

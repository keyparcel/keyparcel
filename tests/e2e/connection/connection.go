//go:build e2e

// Package connection selects the fixture transport without adding a public CLI mode.
package connection

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
	"keyparcel/core"
)

type Settings struct {
	Address    string
	Port       int
	User       string
	KnownHosts string
}

func Connect(ctx context.Context, target string, host core.Host, config string, completion bool) (*core.Session, error) {
	if os.Getenv("KEYPARCEL_TEST_TRANSPORT") != "embedded" {
		if completion {
			return core.ConnectForCompletion(ctx, target, host, config)
		}
		return core.Connect(ctx, target, host, config)
	}
	data, err := os.ReadFile(config + ".embedded.json")
	if err != nil {
		return nil, err
	}
	var settings map[string]Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}
	selected, ok := settings[target]
	if !ok {
		return nil, errors.New("missing explicit embedded test connection settings")
	}
	id, err := core.LoadIdentity(host.Identity)
	if err != nil {
		return nil, err
	}
	if ssh.FingerprintSHA256(id.Public) != host.Fingerprint {
		return nil, errors.New("selected identity fingerprint changed; run setup explicitly")
	}
	id.Path = ""
	known, err := os.ReadFile(selected.KnownHosts)
	if err != nil {
		return nil, err
	}
	var key ssh.PublicKey
	for _, line := range strings.Split(string(known), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == selected.Address {
			key, _, _, _, err = ssh.ParseAuthorizedKey([]byte(strings.Join(fields[1:], " ")))
			if err != nil {
				return nil, err
			}
			break
		}
	}
	if key == nil {
		return nil, errors.New("unknown SSH host key")
	}
	return core.ConnectEmbedded(ctx, core.EmbeddedOptions{Address: selected.Address, Port: selected.Port, User: selected.User, Identity: id, HostKey: key, Root: host.Root, Discovery: host.Discovery})
}

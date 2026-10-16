//go:build e2e

// This fixture exercises the ordinary SFTP rename used to publish packages.
package main

import (
	"context"
	"fmt"
	"os"

	"keyparcel/core"
	"keyparcel/tests/e2e/connection"
)

func rename() error {
	cfg, err := core.LoadConfig(os.Args[1])
	if err != nil {
		return err
	}
	target, host, err := cfg.Select("")
	if err != nil {
		return err
	}
	session, err := connection.Connect(context.Background(), target, host, os.Args[2], false)
	if err != nil {
		return err
	}
	defer session.Close()
	if err := session.Client.Rename(os.Args[3], os.Args[4]); err != nil {
		return fmt.Errorf("publish rename: %w", err)
	}
	return nil
}

func main() {
	if len(os.Args) != 5 {
		panic("usage: sftp-probe CONFIG SSH_CONFIG SOURCE DESTINATION")
	}
	if err := rename(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

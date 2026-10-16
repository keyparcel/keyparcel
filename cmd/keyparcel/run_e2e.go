//go:build e2e

package main

import (
	"context"
	"io"
	"keyparcel/internal/cli"
	"keyparcel/tests/e2e/connection"
)

func run(ctx context.Context, args []string, in io.Reader, out, diag io.Writer) error {
	return cli.RunWithConnector(ctx, args, in, out, diag, connection.Connect)
}

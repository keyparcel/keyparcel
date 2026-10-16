//go:build !e2e

package main

import (
	"context"
	"io"
	"keyparcel/internal/cli"
)

func run(ctx context.Context, args []string, in io.Reader, out, diag io.Writer) error {
	return cli.Run(ctx, args, in, out, diag)
}

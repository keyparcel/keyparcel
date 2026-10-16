package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveCancellationInterruptsBlockedSource(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "output")
	os.WriteFile(dest, []byte("original"), 0600)
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := SaveContentContext(ctx, dest, Manifest{}, input, true); result <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		files, _ := filepath.Glob(filepath.Join(dir, ".keyparcel-save-*"))
		if len(files) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("staging did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked source remained stuck after cancellation")
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "original" {
		t.Fatal("cancelled save replaced destination")
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".keyparcel-save-*"))
	if len(files) != 0 {
		t.Fatal("cancelled save left staging")
	}
}

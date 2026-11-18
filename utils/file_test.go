package utils

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileWithContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFileWithContext(context.Background(), path)
	if err != nil || string(got) != "hello" {
		t.Fatalf("got %q %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ReadFileWithContext(ctx, path)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

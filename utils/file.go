package utils

import (
	"context"
	"io"
	"os"
)

func ReadFileWithContext(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	done := make(chan struct{})
	var data []byte
	var readErr error
	go func() {
		data, readErr = io.ReadAll(f)
		close(done)
	}()

	select {
	case <-ctx.Done():
		_ = f.Close()
		<-done
		return nil, ctx.Err()
	case <-done:
		return data, readErr
	}
}

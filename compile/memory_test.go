package compile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
)

func TestStateTimeoutIncludesQueue(t *testing.T) {
	rt := &Runtime{StateGate: NewGate(1), StateTimeout: 25 * time.Millisecond}
	if err := rt.StateGate.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer rt.StateGate.Release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	_, _, err := (&Config{Runtime: rt}).beginState(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("queue not bounded by state budget: %v, %s", err, time.Since(started))
	}
}

func TestStateOutputOverflowKillsReaderAndReleasesSlot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "register-runtime.mjs"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "overflow.mjs")
	if err := os.WriteFile(script, []byte(`process.stdout.write('x'.repeat(1024*1024));setInterval(()=>{},1000);`), 0600); err != nil {
		t.Fatal(err)
	}
	rt := &Runtime{StateGate: NewGate(1), StateTimeout: 3 * time.Second, StateOutputLimit: 1024, OutputLimit: 1024}
	conf := &Config{StaticDir: dir, Runtime: rt}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := conf.ExecTsScript(ctx, script)
	if !errors.Is(err, ErrStateOutputTooLarge) || output != "" {
		t.Fatalf("got %q %v", output, err)
	}
	if !rt.StateGate.TryAcquire() {
		t.Fatal("reader slot retained after overflow")
	}
	rt.StateGate.Release()
	if err := os.WriteFile(script, []byte(`console.log('{"ok":true}');`), 0600); err != nil {
		t.Fatal(err)
	}
	output, err = conf.ExecTsScript(ctx, script)
	if err != nil || output != "{\"ok\":true}\n" {
		t.Fatalf("following read: %q %v", output, err)
	}
}

func TestCompileClosesTemporaryFiles(t *testing.T) {
	runtime.GC()
	previous := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previous)
	t.Setenv("COMPACT_BIN", "/usr/bin/true")
	descriptors := func() int {
		path := "/dev/fd"
		if runtime.GOOS == "linux" {
			path = "/proc/self/fd"
		}
		dir, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer dir.Close()
		names, err := dir.Readdirnames(-1)
		if err != nil {
			t.Fatal(err)
		}
		return len(names)
	}
	for _, sourceType := range []SourceType{SingleFile, MultiFile} {
		t.Run(string(sourceType), func(t *testing.T) {
			c := &Config{StaticDir: t.TempDir(), TempDir: t.TempDir(), Runtime: NewRuntime()}
			source := "source"
			if sourceType == MultiFile {
				source = `{"contracts":{"a.compact":"source"},"entry-file":"a.compact"}`
			}
			if err := os.MkdirAll(c.GetStaticDir()+"/verified/audit", 0755); err != nil {
				t.Fatal(err)
			}
			before := descriptors()
			for i := 0; i < 25; i++ {
				if err := c.CompileContract(context.Background(), "0.31.1", "audit", source, sourceType); err != nil {
					t.Fatal(err)
				}
			}
			if delta := descriptors() - before; delta > 2 {
				t.Fatalf("temporary file descriptors retained: %d", delta)
			}
		})
	}
}

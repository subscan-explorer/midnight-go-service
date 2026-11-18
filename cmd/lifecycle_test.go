package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"midnight-go-sdk/compile"
	"midnight-go-sdk/logger"
)

type ownedCompile struct {
	*compile.Config
	entered  chan struct{}
	finish   chan struct{}
	calls    atomic.Int32
	compiles atomic.Int32
}

func (c *ownedCompile) IsContractVerified(context.Context, string) bool { c.calls.Add(1); return false }
func (c *ownedCompile) CompileContract(ctx context.Context, _ string, _ string, _ string, _ compile.SourceType) error {
	if c.compiles.Add(1) > 1 {
		<-ctx.Done()
		return ctx.Err()
	}
	path := filepath.Join(c.StaticDir, "verified", "same", "output")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte("owned output"), 0600); err != nil {
		return err
	}
	close(c.entered)
	<-c.finish
	return errors.New("test compile failed")
}
func TestCanceledVerificationWaiterPreservesOwnerFiles(t *testing.T) {
	logger.NewLogger()
	c := &ownedCompile{Config: &compile.Config{StaticDir: t.TempDir(), TempDir: t.TempDir()}, entered: make(chan struct{}), finish: make(chan struct{})}
	handler := compileHandler(c)
	request := func(ctx context.Context) *http.Request {
		return httptest.NewRequest("POST", "/verify", strings.NewReader(`{"contract_addr":"same","source_code":"code","compiler_version":"0.31.1","verification_keys":{"a":"b"},"source_type":"single_file"}`)).WithContext(ctx)
	}
	done := make(chan struct{})
	go func() { defer close(done); handler(httptest.NewRecorder(), request(context.Background())) }()
	<-c.entered
	defer func() { close(c.finish); <-done }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	response := httptest.NewRecorder()
	handler(response, request(ctx))
	if !strings.Contains(response.Body.String(), "context deadline exceeded") {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
	if c.calls.Load() != 1 {
		t.Fatal("waiter checked or modified owner's directory")
	}
	if _, err := os.Stat(filepath.Join(c.StaticDir, "verified", "same", "output")); err != nil {
		t.Fatalf("owner output deleted: %v", err)
	}
}

func TestRequestDeadlineCancelsQueuedWork(t *testing.T) {
	for _, name := range []string{"compile", "state"} {
		t.Run(name, func(t *testing.T) {
			rt := compile.NewRuntime()
			gate := rt.CompileGate
			if name == "state" {
				gate = rt.StateGate
			}
			if err := gate.Acquire(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer gate.Release()
			conf := &compile.Config{Runtime: rt, StaticDir: t.TempDir(), TempDir: t.TempDir()}
			result := make(chan error, 1)
			srv := httptest.NewUnstartedServer(requestDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var err error
				if name == "compile" {
					err = conf.CompileContract(r.Context(), "0.31.1", "a", "source", compile.SingleFile)
				} else {
					_, err = conf.ReadContractCMA(r.Context(), "a", "")
				}
				result <- err
				http.Error(w, "request expired", http.StatusServiceUnavailable)
			}), 2*time.Second))
			srv.Config.WriteTimeout = 2 * time.Second
			srv.Start()
			defer srv.Close()
			client := &http.Client{Timeout: 3 * time.Second}
			response, err := client.Get(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status %d", response.StatusCode)
			}
			if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestServerShutdownWaitsForActiveRequest(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-finish; io.WriteString(w, "completed") })}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closed := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- serveUntilShutdown(ctx, srv, listener, func() { close(closed) }, time.Second) }()
	response := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if err != nil {
			response <- err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		response <- string(b)
	}()
	<-entered
	cancel()
	select {
	case <-closed:
		t.Fatal("runtime closed before request completed")
	case <-time.After(30 * time.Millisecond):
	}
	close(finish)
	if got := <-response; got != "completed" {
		t.Fatalf("response %q", got)
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
	<-closed
}

func TestServerShutdownDeadlineCancelsActiveRequest(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(canceled) })}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closed := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- serveUntilShutdown(ctx, srv, listener, func() { close(closed) }, 30*time.Millisecond) }()
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		resp, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("request not canceled")
	}
	<-closed
	<-clientDone
}

func TestContractLockReleasesCanceledWaiters(t *testing.T) {
	locks := &contractLocks{entries: make(map[string]*contractLock)}
	release, err := locks.acquire(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := locks.acquire(ctx, "a"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	other, err := locks.acquire(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	if len(locks.entries) != 0 {
		t.Fatal("unused lock entries retained")
	}
}

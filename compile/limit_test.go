package compile

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGateWaitsForRequestContext(t *testing.T) {
	gate := NewGate(1)
	if err := gate.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := gate.Acquire(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	gate.Release()
	if err := gate.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestGateProceedsWhenSlotFrees(t *testing.T) {
	gate := NewGate(1)
	holding := make(chan struct{})
	releaseHold := make(chan struct{})
	go func() {
		if err := gate.Acquire(context.Background()); err != nil {
			t.Errorf("hold: %v", err)
			close(holding)
			return
		}
		close(holding)
		<-releaseHold
		gate.Release()
	}()
	<-holding

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got := make(chan error, 1)
	go func() {
		got <- gate.Acquire(ctx)
	}()
	time.Sleep(20 * time.Millisecond)
	close(releaseHold)
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not proceed")
	}
	gate.Release()
}

func TestCompileContractWaitsUntilRequestEnds(t *testing.T) {
	rt := &Runtime{
		CompileGate:    NewGate(1),
		CompileTimeout: time.Second,
		OutputLimit:    1024,
	}
	if err := rt.CompileGate.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.CompileGate.Release)
	conf := &Config{Runtime: rt, TempDir: t.TempDir(), StaticDir: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := conf.CompileContract(ctx, "0.31.1", "0xabc", "pragma language_version 0.23;", SingleFile)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestReadContractCMAWaitsUntilRequestEnds(t *testing.T) {
	rt := &Runtime{
		StateGate:    NewGate(1),
		StateTimeout: time.Second,
		OutputLimit:  1024,
	}
	if err := rt.StateGate.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.StateGate.Release)
	conf := &Config{Runtime: rt, StaticDir: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := conf.ReadContractCMA(ctx, "0xabc", "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestCappedBufferDropsOverflow(t *testing.T) {
	buf := &cappedBuffer{max: 4}
	if n, err := buf.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("write %d %v", n, err)
	}
	if n, err := buf.Write([]byte("zz")); err != nil || n != 2 {
		t.Fatalf("write %d %v", n, err)
	}
	if buf.String() != "abcd" {
		t.Fatalf("got %q", buf.String())
	}
}

func TestCommandContextKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := commandContext(ctx, "bash", "-c", "sleep 30 & echo $!; wait")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		cancel()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || child <= 0 {
		cancel()
		_ = cmd.Wait()
		t.Fatalf("child pid %q: %v", line, err)
	}
	cancel()
	_ = cmd.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(child) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("grandchild %d still alive", child)
}

func processAlive(pid int) bool {
	out, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(out))
	return state != "" && !strings.HasPrefix(state, "Z")
}

func TestGateCanceledContextCannotTakeFreeSlot(t *testing.T) {
	gate := NewGate(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 100; i++ {
		if err := gate.Acquire(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled acquisition: %v", err)
		}
	}
	if err := gate.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	gate.Release()
}

func TestRuntimeCloseKillsLateTrackedProcess(t *testing.T) {
	for _, late := range []bool{false, true} {
		rt := NewRuntime()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := commandContext(ctx, "sleep", "30")
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		if late {
			rt.Close()
			rt.track(cmd)
		} else {
			rt.track(cmd)
			rt.Close()
		}
		err := cmd.Wait()
		canceled := ctx.Err()
		cancel()
		if err == nil || canceled != nil {
			t.Fatalf("late=%v: process was not killed by Close: %v, %v", late, err, canceled)
		}
		rt.untrack(cmd.Process.Pid)
		rt.Close()
	}
}

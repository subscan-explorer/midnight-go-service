package compile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"midnight-go-sdk/logger"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Gate limits how many operations run at once. Extra callers wait until the
// operation context ends. State reads apply their budget before entering the queue.
type Gate struct {
	sem chan struct{}
}

func NewGate(n int) *Gate {
	if n < 1 {
		n = 1
	}
	return &Gate{sem: make(chan struct{}, n)}
}

func (g *Gate) Acquire(ctx context.Context) error {
	if g == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case g.sem <- struct{}{}:
		if err := ctx.Err(); err != nil {
			g.Release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *Gate) Release() {
	if g == nil {
		return
	}
	select {
	case <-g.sem:
	default:
	}
}

// Runtime holds process limits shared by every request copy of Config.
type Runtime struct {
	CompileGate          *Gate
	StateGate            *Gate
	CompileTimeout       time.Duration
	StateTimeout         time.Duration
	OutputLimit          int
	StateOutputLimit     int
	IndexerResponseLimit int
	RPCResponseLimit     int
	VerifyBodyLimit      int64
	VerifyRequests       *Gate
	StateRequests        *Gate

	HTTPReadHeaderTimeout time.Duration
	HTTPReadTimeout       time.Duration
	HTTPWriteTimeout      time.Duration
	HTTPIdleTimeout       time.Duration

	mu     sync.Mutex
	active map[int]*exec.Cmd
	closed bool
}

func NewRuntime() *Runtime {
	compileN := envInt("MIDNIGHT_COMPILE_CONCURRENCY", 1)
	stateN := envInt("MIDNIGHT_STATE_CONCURRENCY", 1)
	compileTimeout := envDuration("MIDNIGHT_COMPILE_TIMEOUT", 5*time.Minute)
	stateTimeout := envDuration("MIDNIGHT_STATE_TIMEOUT", 45*time.Second)
	// Cover one compile waiting behind another, then running its own compile.
	writeTimeout := envDuration("MIDNIGHT_HTTP_WRITE_TIMEOUT", 2*compileTimeout+30*time.Second)
	return &Runtime{
		CompileGate:           NewGate(compileN),
		StateGate:             NewGate(stateN),
		CompileTimeout:        compileTimeout,
		StateTimeout:          stateTimeout,
		OutputLimit:           envInt("MIDNIGHT_OUTPUT_LIMIT_BYTES", 1<<20),
		StateOutputLimit:      envInt("MIDNIGHT_STATE_OUTPUT_LIMIT_BYTES", 16<<20),
		IndexerResponseLimit:  envInt("MIDNIGHT_INDEXER_RESPONSE_LIMIT_BYTES", 32<<20),
		RPCResponseLimit:      envInt("MIDNIGHT_RPC_RESPONSE_LIMIT_BYTES", 32<<20),
		VerifyBodyLimit:       int64(envInt("MIDNIGHT_VERIFY_BODY_LIMIT_BYTES", 8<<20)),
		VerifyRequests:        NewGate(envInt("MIDNIGHT_VERIFY_MAX_REQUESTS", compileN+1)),
		StateRequests:         NewGate(envInt("MIDNIGHT_STATE_MAX_REQUESTS", stateN+7)),
		HTTPReadHeaderTimeout: envDuration("MIDNIGHT_HTTP_READ_HEADER_TIMEOUT", 10*time.Second),
		HTTPReadTimeout:       envDuration("MIDNIGHT_HTTP_READ_TIMEOUT", 5*time.Minute),
		HTTPWriteTimeout:      writeTimeout,
		HTTPIdleTimeout:       envDuration("MIDNIGHT_HTTP_IDLE_TIMEOUT", 90*time.Second),
	}
}

func (rt *Runtime) String() string {
	if rt == nil {
		return "runtime unset"
	}
	compileN, stateN := 0, 0
	if rt.CompileGate != nil {
		compileN = cap(rt.CompileGate.sem)
	}
	if rt.StateGate != nil {
		stateN = cap(rt.StateGate.sem)
	}
	return fmt.Sprintf("compile concurrency=%d timeout=%s; state concurrency=%d total timeout=%s; diagnostics=%dB state JSON=%dB indexer response=%dB RPC response=%dB",
		compileN, rt.CompileTimeout, stateN, rt.StateTimeout, rt.OutputLimit, rt.stateOutputLimit(), rt.indexerResponseLimit(), rt.rpcResponseLimit())
}

func (rt *Runtime) track(cmd *exec.Cmd) {
	if rt == nil || cmd == nil || cmd.Process == nil {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		_ = cancelProcessGroup(cmd)
		return
	}
	if rt.active == nil {
		rt.active = make(map[int]*exec.Cmd)
	}
	rt.active[cmd.Process.Pid] = cmd
}

func (rt *Runtime) untrack(pid int) {
	if rt == nil || pid <= 0 {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.active, pid)
}

// Close stops any compile or state-reader process still running.
func (rt *Runtime) Close() {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	rt.closed = true
	cmds := rt.active
	rt.active = nil
	rt.mu.Unlock()
	for _, cmd := range cmds {
		_ = cancelProcessGroup(cmd)
	}
}

var (
	defaultRuntimeOnce sync.Once
	sharedRuntime      *Runtime
)

func (c *Config) beginState(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	rt := c.runtime()
	stateCtx := ctx
	cancel := func() {}
	if rt.StateTimeout > 0 {
		stateCtx, cancel = context.WithTimeout(ctx, rt.StateTimeout)
	}
	if err := rt.StateGate.Acquire(stateCtx); err != nil {
		cancel()
		return nil, nil, err
	}
	return stateCtx, func() {
		cancel()
		rt.StateGate.Release()
	}, nil
}

func (c *Config) runtime() *Runtime {
	if c != nil && c.Runtime != nil {
		return c.Runtime
	}
	defaultRuntimeOnce.Do(func() {
		sharedRuntime = NewRuntime()
		if logger.Logger() != nil {
			logger.Logger().Info(sharedRuntime.String())
		}
	})
	return sharedRuntime
}

func commandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = procAttr()
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		return cancelProcessGroup(cmd)
	}
	return cmd
}

func cancelProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// cappedBuffer keeps at most max bytes and still consumes the rest of the stream.
type cappedBuffer struct {
	max int
	buf bytes.Buffer
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c == nil || c.max <= 0 {
		return len(p), nil
	}
	if remain := c.max - c.buf.Len(); remain > 0 {
		chunk := p
		if len(chunk) > remain {
			chunk = chunk[:remain]
		}
		_, _ = c.buf.Write(chunk)
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	if c == nil {
		return ""
	}
	return c.buf.String()
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// TryAcquire bounds total retained HTTP work, including queued and writing requests.
func (g *Gate) TryAcquire() bool {
	if g == nil {
		return true
	}
	select {
	case g.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (c *Config) RuntimeLimits() *Runtime { return c.runtime() }

func (rt *Runtime) stateOutputLimit() int {
	if rt.StateOutputLimit > 0 {
		return rt.StateOutputLimit
	}
	return 16 << 20
}
func (rt *Runtime) indexerResponseLimit() int {
	if rt.IndexerResponseLimit > 0 {
		return rt.IndexerResponseLimit
	}
	return 32 << 20
}

func (rt *Runtime) rpcResponseLimit() int {
	if rt.RPCResponseLimit > 0 {
		return rt.RPCResponseLimit
	}
	return 32 << 20
}

var ErrStateOutputTooLarge = errors.New("contract state reader output exceeds configured limit")

// Unlike diagnostic output, JSON must fail atomically rather than be truncated.
type stateOutputBuffer struct {
	buf      bytes.Buffer
	max      int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *stateOutputBuffer) Write(p []byte) (int, error) {
	if len(p) > b.max-b.buf.Len() {
		b.exceeded = true
		b.cancel()
		return 0, ErrStateOutputTooLarge
	}
	return b.buf.Write(p)
}

package logger

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"sync"
	"time"
)

// Fields must contain operational metadata only, never bodies, headers,
// upstream URLs, compiler output, or arbitrary error messages.
type Fields map[string]interface{}

type traceKey struct{}
type trace struct {
	mu     sync.Mutex
	fields Fields
}

func WithFields(ctx context.Context, fields Fields) context.Context {
	return context.WithValue(ctx, traceKey{}, &trace{fields: fields})
}

func AddFields(ctx context.Context, fields Fields) {
	if t, ok := ctx.Value(traceKey{}).(*trace); ok {
		t.mu.Lock()
		defer t.mu.Unlock()
		for k, v := range fields {
			t.fields[k] = v
		}
	}
}

func Outcome(ctx context.Context) string {
	if t, ok := ctx.Value(traceKey{}).(*trace); ok {
		t.mu.Lock()
		defer t.mu.Unlock()
		outcome, _ := t.fields["outcome"].(string)
		return outcome
	}
	return ""
}

// Event emits one bounded JSON line. Only explicitly selected metadata enters
// this record; request/response streams and subprocess output are never copied.
func Event(ctx context.Context, level, event string, fields Fields) {
	record := Fields{}
	if t, ok := ctx.Value(traceKey{}).(*trace); ok {
		t.mu.Lock()
		for k, v := range t.fields {
			record[k] = v
		}
		t.mu.Unlock()
	}
	for k, v := range fields {
		record[k] = v
	}
	record["time"] = time.Now().UTC().Format(time.RFC3339Nano)
	record["level"] = level
	record["event"] = event
	if data, err := json.Marshal(record); err == nil {
		Logger().eventLogger.Print(string(data))
	}
}

func ErrorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "subprocess_failed"
		}
		return "operation_failed"
	}
}

type Operation struct {
	ctx       context.Context
	name      string
	queuedAt  time.Time
	startedAt time.Time
	enabled   bool
}

func Queued(ctx context.Context, name string) *Operation {
	_, enabled := ctx.Value(traceKey{}).(*trace)
	return &Operation{ctx: ctx, name: name, queuedAt: time.Now(), enabled: enabled}
}

func (op *Operation) Started() {
	op.startedAt = time.Now()
	if op.enabled && op.name != "state_read" {
		Event(op.ctx, "INFO", "operation_started", Fields{"operation": op.name, "queue_ms": op.startedAt.Sub(op.queuedAt).Milliseconds()})
	}
}

func (op *Operation) Finished(err error, fields Fields) {
	if !op.enabled {
		return
	}
	if fields == nil {
		fields = Fields{}
	}
	queueTime, runTime := time.Since(op.queuedAt), time.Duration(0)
	if !op.startedAt.IsZero() {
		queueTime, runTime = op.startedAt.Sub(op.queuedAt), time.Since(op.startedAt)
	}
	AddFields(op.ctx, Fields{op.name + "_queue_ms": queueTime.Milliseconds(), op.name + "_run_ms": runTime.Milliseconds()})
	// Successful state reads are frequent: put their timings on the access log
	// rather than emitting another pair of process logs for every request.
	if op.name == "state_read" && err == nil {
		return
	}
	fields["operation"] = op.name
	fields["queue_ms"] = queueTime.Milliseconds()
	fields["run_ms"] = runTime.Milliseconds()
	fields["duration_ms"] = time.Since(op.queuedAt).Milliseconds()
	fields["outcome"] = "success"
	level := "INFO"
	if err != nil {
		fields["outcome"] = "failed"
		if _, set := fields["error_code"]; !set {
			fields["error_code"] = ErrorCode(err)
		}
		fields["phase"] = "running"
		if op.startedAt.IsZero() {
			fields["phase"] = "waiting"
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			fields["exit_code"] = exit.ExitCode()
		}
		level = "ERROR"
	}
	Event(op.ctx, level, "operation_completed", fields)
}

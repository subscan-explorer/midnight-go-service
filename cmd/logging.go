package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"midnight-go-sdk/compile"
	"midnight-go-sdk/logger"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

var (
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
	logTokenPattern  = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	fallbackID       uint64
)

// Restrict untrusted metadata in size and alphabet. Never log a raw URL, query
// string, request body, header collection, or subprocess diagnostics.
func logToken(value string) string {
	if value != "" && !logTokenPattern.MatchString(value) {
		return "[invalid]"
	}
	return value
}

func requestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(id) {
			var random [16]byte
			if _, err := rand.Read(random[:]); err == nil {
				id = hex.EncodeToString(random[:])
			} else {
				id = fmt.Sprintf("%x-%x", time.Now().UnixNano(), atomic.AddUint64(&fallbackID, 1))
			}
		}
		path := r.URL.Path
		if path != "/verify" && path != "/contract/state" {
			path = "unmatched"
		}
		fields := logger.Fields{"request_id": id, "method": logToken(r.Method), "path": path}
		if path == "/contract/state" {
			q := r.URL.Query()
			fields["contract_addr"] = logToken(q.Get("contract_addr"))
			fields["block_hash"] = logToken(firstQuery(q.Get("block_hash"), q.Get("hash")))
			fields["network"] = logToken(firstQuery(strings.TrimSpace(q.Get("networkid")), strings.TrimSpace(q.Get("network_id"))))
			fields["view"] = logToken(q.Get("view"))
		}
		ctx := logger.WithFields(r.Context(), fields)
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-ID", id)
		response := &loggedResponse{ResponseWriter: w}
		start := time.Now()
		logger.Event(ctx, "INFO", "request_started", nil)
		completed := false
		defer func() {
			fields := logger.Fields{"status": response.status, "response_bytes": response.bytes, "duration_ms": time.Since(start).Milliseconds()}
			outcome := logger.Outcome(ctx)
			level := "INFO"
			switch {
			case !completed:
				outcome, level = "panic", "ERROR"
				fields["error_code"] = "handler_panic"
			case ctx.Err() != nil:
				outcome, level = logger.ErrorCode(ctx.Err()), "WARN"
				fields["error_code"] = outcome
			case response.writeErr != nil:
				outcome, level = "write_failed", "WARN"
				fields["error_code"] = "response_write_failed"
			case response.status >= 500:
				outcome, level = "failed", "ERROR"
			case response.status >= 400:
				outcome, level = "rejected", "WARN"
			case outcome != "":
				if outcome != "success" {
					level = "WARN"
				}
			default:
				outcome = "success"
			}
			if completed && response.status == 0 && ctx.Err() == nil {
				fields["status"] = http.StatusOK // net/http's implicit empty response
			}
			fields["outcome"] = outcome
			logger.Event(ctx, level, "request_completed", fields)
		}()
		next.ServeHTTP(response, r)
		completed = true
	})
}

type loggedResponse struct {
	http.ResponseWriter
	status   int
	bytes    int64
	writeErr error
}

func (w *loggedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *loggedResponse) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *loggedResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	if err != nil {
		w.writeErr = err
	}
	return n, err
}

func requestFailed(r *http.Request, code string, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = logger.ErrorCode(err)
	} else if errors.Is(err, compile.ErrStateOutputTooLarge) {
		code = "state_output_limit"
	}
	logger.AddFields(r.Context(), logger.Fields{"outcome": "failed", "error_code": code})
}

func requestError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	requestFailed(r, code, nil)
	http.Error(w, message, status)
}

func requestWarning(r *http.Request, operation string, err error) {
	logger.Event(r.Context(), "WARN", "request_warning", logger.Fields{"operation": operation, "error_code": logger.ErrorCode(err)})
}

func firstQuery(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

func logNetwork(r *http.Request, c compile.ICompile) {
	if conf, ok := c.(*compile.Config); ok && conf.NetworkConf != nil {
		logger.AddFields(r.Context(), logger.Fields{"network": string(conf.NetworkConf.GetNetworkID())})
	}
}

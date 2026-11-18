package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"midnight-go-sdk/compile"
	"midnight-go-sdk/config"
	"midnight-go-sdk/logger"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func captureLogs(t *testing.T) *logBuffer {
	t.Helper()
	b := &logBuffer{}
	logger.SetLogger(b)
	t.Cleanup(func() { logger.SetLogger(os.Stdout) })
	return b
}

func logRecords(t *testing.T, b *logBuffer) []map[string]interface{} {
	t.Helper()
	var records []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		var record map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(line), &record), line)
		records = append(records, record)
	}
	return records
}

func completion(t *testing.T, b *logBuffer) map[string]interface{} {
	t.Helper()
	var found []map[string]interface{}
	for _, record := range logRecords(t, b) {
		if record["event"] == "request_completed" {
			found = append(found, record)
		}
	}
	require.Len(t, found, 1)
	return found[0]
}

func TestRequestLogsStateMetadataAndRedactsUnselectedData(t *testing.T) {
	b := captureLogs(t)
	m := &MockICompile{}
	address, hash := "0x"+strings.Repeat("ab", 32), "0x"+strings.Repeat("cd", 32)
	m.On("IsContractVerified", mock.Anything, address).Return(false)
	m.On("ReadContractCMA", mock.Anything, address, hash).Return(&compile.ContractStateResponse{}, nil)
	request := httptest.NewRequest("GET", "/contract/state?contract_addr="+address+"&network_id=preview&hash="+hash+"&project_id=QUERY_SECRET", nil)
	request.Header.Set("Authorization", "Bearer HEADER_SECRET")
	request.Header.Set("X-Request-ID", "caller-123")
	w := httptest.NewRecorder()
	requestLogging(stateHandler(m)).ServeHTTP(w, request)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "caller-123", w.Header().Get("X-Request-ID"))
	records := logRecords(t, b)
	require.Len(t, records, 2)
	for _, record := range records {
		require.Equal(t, "caller-123", record["request_id"])
		require.Equal(t, address, record["contract_addr"])
		require.Equal(t, hash, record["block_hash"])
		require.Equal(t, "preview", record["network"])
	}
	last := completion(t, b)
	require.Equal(t, "success", last["outcome"])
	require.Equal(t, "cma", last["state_mode"])
	require.Equal(t, float64(w.Body.Len()), last["response_bytes"])
	require.NotContains(t, b.String(), "QUERY_SECRET")
	require.NotContains(t, b.String(), "HEADER_SECRET")
	m.AssertExpectations(t)
}

func TestVerificationHTTP200FailureIsNotLoggedAsSuccess(t *testing.T) {
	b := captureLogs(t)
	m := &MockICompile{}
	m.On("GetTempDir").Return(t.TempDir())
	m.On("GetStaticDir").Return(t.TempDir())
	m.On("IsContractVerified", mock.Anything, "addr1").Return(false)
	m.On("CompileContract", mock.Anything, "0.31.1", "addr1", "SOURCE_SECRET", compile.SingleFile).
		Return(errors.New("compiler failure: SOURCE_SECRET https://example.test/?project_id=UPSTREAM_SECRET"))
	body := `{"contract_addr":"addr1","source_code":"SOURCE_SECRET","compiler_version":"0.31.1","source_type":"single_file","verification_keys":{"circuit":"KEY_SECRET"},"network_id":"preview"}`
	w := httptest.NewRecorder()
	requestLogging(compileHandler(m)).ServeHTTP(w, httptest.NewRequest("POST", "/verify", strings.NewReader(body)))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"verified_status":"mismatch"`)
	last := completion(t, b)
	require.Equal(t, "failed", last["outcome"])
	require.Equal(t, "WARN", last["level"])
	require.Equal(t, "compile_failed", last["error_code"])
	require.Equal(t, mismatch, last["verified_status"])
	require.Equal(t, "0.31.1", last["compiler_version"])
	require.Equal(t, float64(len("SOURCE_SECRET")), last["source_bytes"])
	for _, secret := range []string{"SOURCE_SECRET", "KEY_SECRET", "UPSTREAM_SECRET", "example.test"} {
		require.NotContains(t, b.String(), secret)
	}
	m.AssertExpectations(t)
}

func TestPartialVerificationWarningsPreserveBusinessSuccess(t *testing.T) {
	b := captureLogs(t)
	m := &MockICompile{}
	m.On("GetTempDir").Return(t.TempDir())
	m.On("GetStaticDir").Return(t.TempDir())
	m.On("IsContractVerified", mock.Anything, "addr1").Return(false)
	m.On("CompileContract", mock.Anything, "0.31.1", "addr1", mock.Anything, compile.SingleFile).Return(nil)
	m.On("CheckVerifyKeys", mock.Anything, "addr1", mock.Anything).Return(compile.VerifyKeyResult{Status: partial}, nil)
	m.On("ContractABIInfo", mock.Anything, "addr1").Return(map[string]interface{}{}, nil)
	m.On("ReadContractCMA", mock.Anything, "addr1", "").Return((*compile.ContractStateResponse)(nil), errors.New("UPSTREAM_SECRET"))
	body := `{"contract_addr":"addr1","source_code":"export ledger counter: Counter;","compiler_version":"0.31.1","source_type":"single_file","verification_keys":{"counter":"hash"}}`
	w := httptest.NewRecorder()
	requestLogging(compileHandler(m)).ServeHTTP(w, httptest.NewRequest("POST", "/verify", strings.NewReader(body)))
	require.Equal(t, 200, w.Code)
	last := completion(t, b)
	require.Equal(t, "success", last["outcome"])
	require.Equal(t, partial, last["verified_status"])
	var warned bool
	for _, record := range logRecords(t, b) {
		if record["event"] == "request_warning" && record["operation"] == "read_cma" {
			warned = true
		}
	}
	require.True(t, warned)
	require.NotContains(t, b.String(), "UPSTREAM_SECRET")
	m.AssertExpectations(t)
}

func TestRejectedRequestsAndQueueTimeoutAreVisible(t *testing.T) {
	for _, scenario := range []string{"invalid_json", "request_body_limit", "service_busy", "deadline_exceeded"} {
		t.Run(scenario, func(t *testing.T) {
			b := captureLogs(t)
			rt := compile.NewRuntime()
			conf := &compile.Config{StaticDir: t.TempDir(), TempDir: t.TempDir(), Runtime: rt}
			var handler http.Handler
			var request *http.Request
			status := 400
			switch scenario {
			case "invalid_json":
				handler, request = compileHandler(conf), httptest.NewRequest("POST", "/verify", strings.NewReader(`{`))
			case "request_body_limit":
				rt.VerifyBodyLimit = 32
				handler, request = compileHandler(conf), httptest.NewRequest("POST", "/verify", strings.NewReader(`{"source_code":"`+strings.Repeat("x", 100)+`"}`))
				status = 413
			case "service_busy":
				rt.StateRequests = compile.NewGate(1)
				require.True(t, rt.StateRequests.TryAcquire())
				defer rt.StateRequests.Release()
				handler, request = stateHandler(conf), httptest.NewRequest("GET", "/contract/state?contract_addr=addr1", nil)
				status = 503
			case "deadline_exceeded":
				rt.StateTimeout = 20 * time.Millisecond
				require.True(t, rt.StateGate.TryAcquire())
				defer rt.StateGate.Release()
				handler, request = stateHandler(conf), httptest.NewRequest("GET", "/contract/state?contract_addr=addr1", nil)
				status = 504
			}
			w := httptest.NewRecorder()
			requestLogging(handler).ServeHTTP(w, request)
			require.Equal(t, status, w.Code)
			last := completion(t, b)
			require.Equal(t, float64(status), last["status"])
			require.Equal(t, scenario, last["error_code"])
			require.NotEqual(t, "success", last["outcome"])
			if scenario == "deadline_exceeded" {
				var waiting bool
				for _, record := range logRecords(t, b) {
					if record["event"] == "operation_completed" {
						require.Equal(t, "waiting", record["phase"])
						require.Equal(t, "deadline_exceeded", record["error_code"])
						waiting = true
					}
				}
				require.True(t, waiting)
			}
		})
	}
}

func TestRealReaderFailureLogsSafeIndexerClassification(t *testing.T) {
	b := captureLogs(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "RESPONSE_SECRET", http.StatusUnauthorized)
	}))
	defer upstream.Close()
	conf := &compile.Config{StaticDir: "../static", Runtime: compile.NewRuntime(), NetworkConf: &config.NetworkConfig{
		NetworkID: config.Preview, Indexer: upstream.URL + "/?project_id=UPSTREAM_SECRET",
	}}
	w := httptest.NewRecorder()
	requestLogging(stateHandler(conf)).ServeHTTP(w, httptest.NewRequest("GET", "/contract/state?contract_addr="+strings.Repeat("ab", 32), nil))
	require.Equal(t, 502, w.Code)
	var classified bool
	for _, record := range logRecords(t, b) {
		if record["event"] == "operation_completed" {
			require.Equal(t, "indexer_http_failed", record["error_code"])
			require.Equal(t, float64(401), record["upstream_status"])
			require.Equal(t, float64(1), record["exit_code"])
			require.Equal(t, w.Header().Get("X-Request-ID"), record["request_id"])
			classified = true
		}
	}
	require.True(t, classified)
	require.NotContains(t, b.String(), "UPSTREAM_SECRET")
	require.NotContains(t, b.String(), "RESPONSE_SECRET")
	require.NotContains(t, b.String(), upstream.URL)
}

// TestStateErrorResponses keeps internal faults distinct from provider failures
// and preserves cancellation/output diagnostics without exposing error details.
func TestStateErrorResponses(t *testing.T) {
	secret := errors.New("INTERNAL_SECRET https://provider.example/?project_id=TOKEN_SECRET")
	reader := func(code string) error { return &compile.StateReadError{Code: code, Err: secret} }
	for _, tc := range []struct {
		name, code string
		err        error
		status     int
	}{
		{"missing state", "state_not_found", reader("state_not_found"), http.StatusNotFound},
		{"missing block", "rpc_block_not_found", reader("rpc_block_not_found"), http.StatusNotFound},
		{"invalid request", "rpc_invalid_request", reader("rpc_invalid_request"), http.StatusBadRequest},
		{"RPC failure", "rpc_network_failed", reader("rpc_network_failed"), http.StatusBadGateway},
		{"missing Indexer action", "indexer_state_unavailable", reader("indexer_state_unavailable"), http.StatusBadGateway},
		{"decode failure", "state_decode_failed", reader("state_decode_failed"), http.StatusBadGateway},
		{"reader timeout", "deadline_exceeded", reader("deadline_exceeded"), http.StatusGatewayTimeout},
		{"deadline precedence", "deadline_exceeded", errors.Join(context.DeadlineExceeded, reader("rpc_http_failed")), http.StatusGatewayTimeout},
		{"canceled", "canceled", context.Canceled, http.StatusInternalServerError},
		{"internal reader failure", "state_read_failed", reader("state_read_failed"), http.StatusInternalServerError},
		{"internal failure", "state_read_failed", secret, http.StatusInternalServerError},
		{"output limit", "state_output_limit", compile.ErrStateOutputTooLarge, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := captureLogs(t)
			w := httptest.NewRecorder()
			requestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				respondStateError(w, r, tc.err)
			})).ServeHTTP(w, httptest.NewRequest("GET", "/contract/state", nil))
			require.Equal(t, tc.status, w.Code)
			require.Equal(t, tc.code, completion(t, b)["error_code"])
			for _, private := range []string{"INTERNAL_SECRET", "TOKEN_SECRET", "provider.example"} {
				require.NotContains(t, w.Body.String(), private)
				require.NotContains(t, b.String(), private)
			}
		})
	}
}

func TestSuccessfulReaderAddsTimingsWithoutExtraProcessLogs(t *testing.T) {
	b := captureLogs(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "register-runtime.mjs"), nil, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "read-state.mjs"), []byte(`console.log('{"maintenanceAuthority":{"committee":[],"threshold":1,"counter":0},"operations":[]}');`), 0600))
	conf := &compile.Config{StaticDir: dir, Runtime: compile.NewRuntime(), NetworkConf: &config.NetworkConfig{NetworkID: config.Preview}}
	w := httptest.NewRecorder()
	requestLogging(stateHandler(conf)).ServeHTTP(w, httptest.NewRequest("GET", "/contract/state?contract_addr=addr1", nil))
	require.Equal(t, 200, w.Code)
	require.Len(t, logRecords(t, b), 2)
	last := completion(t, b)
	require.Equal(t, "success", last["outcome"])
	require.Equal(t, "preview", last["network"])
	require.Contains(t, last, "state_read_queue_ms")
	require.Contains(t, last, "state_read_run_ms")
	require.NotContains(t, b.String(), "committee")
}

func TestRequestIDsStayBoundedAndConcurrentRequestsRemainSeparate(t *testing.T) {
	b := captureLogs(t)
	handler := requestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("GET", "/private/PATH_SECRET?token=QUERY_SECRET", nil)
			r.Header.Set("X-Request-ID", strings.Repeat("UNTRUSTED", 1000))
			handler.ServeHTTP(httptest.NewRecorder(), r)
		}()
	}
	wg.Wait()
	counts := map[string]int{}
	for _, record := range logRecords(t, b) {
		id := record["request_id"].(string)
		require.Len(t, id, 32)
		counts[id]++
		require.Equal(t, "unmatched", record["path"])
	}
	require.Len(t, counts, 32)
	for _, count := range counts {
		require.Equal(t, 2, count)
	}
	for _, secret := range []string{"PATH_SECRET", "QUERY_SECRET", "UNTRUSTED"} {
		require.NotContains(t, b.String(), secret)
	}
}

func TestRequestCancellationAndPanicNeverLookSuccessful(t *testing.T) {
	for _, scenario := range []string{"canceled", "panic"} {
		t.Run(scenario, func(t *testing.T) {
			b := captureLogs(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			handler := requestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "panic" {
					panic("PANIC_SECRET")
				}
				cancel()
			}))
			func() {
				defer func() {
					if scenario == "panic" {
						require.Equal(t, "PANIC_SECRET", recover())
					}
				}()
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/contract/state", nil).WithContext(ctx))
			}()
			last := completion(t, b)
			require.Equal(t, scenario, last["outcome"])
			if scenario == "panic" {
				require.Equal(t, float64(0), last["status"])
			}
			require.NotContains(t, b.String(), "PANIC_SECRET")
		})
	}
}

type failedResponse struct{ header http.Header }

func (w *failedResponse) Header() http.Header    { return w.header }
func (w *failedResponse) WriteHeader(status int) {}
func (w *failedResponse) Write(p []byte) (int, error) {
	return 2, errors.New("WRITE_SECRET")
}

func TestAccessLogsActualWrittenBytesAndWriteFailure(t *testing.T) {
	b := captureLogs(t)
	handler := requestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("long response")) }))
	handler.ServeHTTP(&failedResponse{header: http.Header{}}, httptest.NewRequest("GET", "/contract/state", nil))
	last := completion(t, b)
	require.Equal(t, "write_failed", last["outcome"])
	require.Equal(t, float64(2), last["response_bytes"])
	require.NotContains(t, b.String(), "WRITE_SECRET")
}

func BenchmarkRequestLogging(b *testing.B) {
	logger.SetLogger(io.Discard)
	defer logger.SetLogger(os.Stdout)
	handler := requestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest("GET", "/contract/state?contract_addr="+strings.Repeat("ab", 32)+"&network_id=preview", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), r)
	}
}

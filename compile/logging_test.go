package compile

import (
	"bytes"
	"context"
	"encoding/json"
	"midnight-go-sdk/logger"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCompileLogsTimeoutAndExitCodeWithoutCompilerOutput(t *testing.T) {
	for _, tc := range []struct {
		name, script, code string
		timeout            time.Duration
		exit               int
	}{
		{"timeout", "#!/bin/sh\necho COMPILER_SECRET >&2\nexec sleep 5\n", "deadline_exceeded", 200 * time.Millisecond, -1},
		{"compiler_error", "#!/bin/sh\necho COMPILER_SECRET >&2\nexit 9\n", "subprocess_failed", 5 * time.Second, 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			logger.SetLogger(&output)
			t.Cleanup(func() { logger.SetLogger(os.Stdout) })
			bin := filepath.Join(t.TempDir(), "compiler")
			require.NoError(t, os.WriteFile(bin, []byte(tc.script), 0700))
			t.Setenv("COMPACT_BIN", bin)
			rt := NewRuntime()
			rt.CompileTimeout = tc.timeout
			conf := &Config{StaticDir: t.TempDir(), TempDir: t.TempDir(), Runtime: rt}
			ctx := logger.WithFields(context.Background(), logger.Fields{"request_id": "compile-check"})
			err := conf.CompileContract(ctx, "0.31.1", "addr1", "SOURCE_SECRET", SingleFile)
			require.Error(t, err)
			require.NotContains(t, output.String(), "COMPILER_SECRET")
			require.NotContains(t, output.String(), "SOURCE_SECRET")
			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			require.Len(t, lines, 2)
			var record map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(lines[1]), &record))
			require.Equal(t, "operation_completed", record["event"])
			require.Equal(t, tc.code, record["error_code"])
			require.Equal(t, float64(tc.exit), record["exit_code"])
			require.Equal(t, "running", record["phase"])
			require.Equal(t, "compile-check", record["request_id"])
		})
	}
}

func TestReaderDiagnosticsRejectArbitraryStderr(t *testing.T) {
	for _, raw := range []string{
		"https://provider/?project_id=SECRET", `{"error_code":"SECRET"}`,
		`{"error_code":"indexer_http_failed","upstream_status":"SECRET"}`,
		strings.Repeat("x", 4097),
	} {
		require.Empty(t, stateReaderDiagnostics(raw))
	}
	require.Equal(t, logger.Fields{"error_code": "indexer_http_failed", "upstream_status": 429},
		stateReaderDiagnostics(`{"error_code":"indexer_http_failed","upstream_status":429,"message":"SECRET"}`))
	require.Equal(t, logger.Fields{"error_code": "rpc_http_failed", "upstream_status": 403},
		stateReaderDiagnostics(`{"error_code":"rpc_http_failed","upstream_status":403,"message":"SECRET"}`))
	require.Equal(t, logger.Fields{"error_code": "rpc_call_failed"},
		stateReaderDiagnostics(`{"error_code":"rpc_call_failed","message":"SECRET"}`))
}

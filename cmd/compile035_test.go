package main

import (
	"bytes"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"midnight-go-sdk/compile"
	"midnight-go-sdk/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompile035HTTP runs the actual compiler and Node reader through /verify.
// The Indexer serves a recorded state; reference keys are independently compiled
// fixtures, so this test does not claim verification against a deployed contract.
func TestCompile035HTTP(t *testing.T) {
	if os.Getenv("RUN_COMPACT_035_TESTS") != "1" {
		t.Skip("set RUN_COMPACT_035_TESTS=1 with Compact 0.35.0 installed")
	}
	source, err := os.ReadFile("../static/fixtures/reader-035.compact")
	require.NoError(t, err)
	raw, err := os.ReadFile("../static/fixtures/stagenet-state-v8.json")
	require.NoError(t, err)
	var fixture struct{ State string }
	require.NoError(t, json.Unmarshal(raw, &fixture))
	indexer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"contractAction": map[string]string{"state": fixture.State}}})
		if err != nil {
			t.Errorf("write fixture: %v", err)
		}
	}))
	defer indexer.Close()
	t.Setenv("MIDNIGHT_STAGENET_EPOCH", "test035")
	for _, tc := range []struct {
		name, key string
		feature   bool
	}{
		{"v2", "6eec870aac7d7e031ffcdf5113c932e1", false},
		{"v3", "6abf60563424aeafcaa23b89459f1415", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, file := range []string{"read-state.mjs", "state.mjs", "runtime-loader.mjs", "register-runtime.mjs"} {
				original, err := filepath.Abs(filepath.Join("../static", file))
				require.NoError(t, err)
				require.NoError(t, os.Symlink(original, filepath.Join(root, file)))
			}
			conf := &compile.Config{StaticDir: root, TempDir: t.TempDir(), NetworkConf: &config.NetworkConfig{NetworkID: config.Stagenet, Indexer: indexer.URL}, Runtime: compile.NewRuntime()}
			request := CompileRequest{ContractAddr: "0x" + strings.Repeat("a", 64), SourceCode: string(source), CompilerVersion: "0.35.0", SourceType: compile.SingleFile, CompilerOptions: compile.CompilerOptions{FeatureZKIRV3: tc.feature}, VerificationKeys: map[string]string{"set": tc.key}}
			body, err := json.Marshal(request)
			require.NoError(t, err)
			response := httptest.NewRecorder()
			compileHandler(conf).ServeHTTP(response, httptest.NewRequest("POST", "/verify", bytes.NewReader(body)))
			require.Equal(t, http.StatusOK, response.Code)
			var got VerificationResponse
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &got))
			require.Equal(t, perfect, got.VerifiedStatus, got.Message)
			require.NotNil(t, got.ABI)
			require.NotNil(t, got.Viewer)
			require.Len(t, got.CMA, 1)
			require.Equal(t, "56577e67c12c49a7305c33e9f9220e94bb42a0fe47cf26fd3d7d19fd8bc4603d", got.CMA[0])
		})
	}
}

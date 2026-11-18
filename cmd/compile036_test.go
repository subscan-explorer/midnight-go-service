package main

import (
	"bytes"
	"encoding/json"
	"midnight-go-sdk/compile"
	"midnight-go-sdk/config"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCompile036HTTP verifies source -> real compiler -> independent key ->
// persisted artifact -> ledger reader. The provider is local and no chain writes occur.
func TestCompile036HTTP(t *testing.T) {
	if os.Getenv("RUN_COMPACT_036_TESTS") != "1" {
		t.Skip("set RUN_COMPACT_036_TESTS=1 with Compact 0.36.0 installed")
	}
	source, err := os.ReadFile("../static/fixtures/reader-036.compact")
	require.NoError(t, err)
	command := exec.CommandContext(t.Context(), "node", "--import", "../static/register-runtime.mjs", "--input-type=module", "--eval", `
 import * as runtime from '@midnight-ntwrk/compact-runtime-0.21';
 import * as contract from '../static/fixtures/reader-036.js';
 const state=(await new contract.Contract({}).initialState(runtime.createConstructorContext({},'00'.repeat(32)))).currentContractState;
 state.setOperation('set',new runtime.ContractOperation());
 console.log(JSON.stringify({state:Buffer.from(state.serialize()).toString('hex')}));`)
	raw, err := command.Output()
	require.NoError(t, err)
	var fixture struct{ State string }
	require.NoError(t, json.Unmarshal(raw, &fixture))
	hash := "0x" + strings.Repeat("b", 64)
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			ID     int      `json:"id"`
			Method string   `json:"method"`
			Params []string `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&q))
		var value interface{} = fixture.State
		if q.Method == "chain_getFinalizedHead" {
			value = hash
		} else {
			require.Equal(t, "midnight_contractState", q.Method)
			require.Equal(t, hash, q.Params[1])
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": q.ID, "result": value}))
	}))
	defer rpc.Close()
	for _, network := range []config.NetworkID{config.MainNet, config.Preview} {
		t.Run(string(network), func(t *testing.T) {
			root := t.TempDir()
			for _, file := range []string{"read-state.mjs", "state.mjs", "runtime-loader.mjs", "register-runtime.mjs"} {
				original, err := filepath.Abs(filepath.Join("../static", file))
				require.NoError(t, err)
				require.NoError(t, os.Symlink(original, filepath.Join(root, file)))
			}
			conf := &compile.Config{StaticDir: root, TempDir: t.TempDir(), NetworkConf: &config.NetworkConfig{NetworkID: network, Node: rpc.URL}, Runtime: compile.NewRuntime()}
			// Preview retains its current Indexer transport; the local provider returns
			// the same official V9 snapshot for that path.
			indexer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"contractAction": map[string]string{"state": fixture.State}}}))
			}))
			defer indexer.Close()
			conf.NetworkConf.(*config.NetworkConfig).Indexer = indexer.URL
			address := "0x" + strings.Repeat("a", 64)
			request := CompileRequest{ContractAddr: address, SourceCode: string(source), CompilerVersion: "0.36.0", SourceType: compile.SingleFile, VerificationKeys: map[string]string{"set": "6abf60563424aeafcaa23b89459f1415"}}
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
			// The new artifact imports runtime 0.21.0. It must decode V9 state without
			// colliding with the retained runtime 0.16.0 SDK dependency.
			response = httptest.NewRecorder()
			stateHandler(conf).ServeHTTP(response, httptest.NewRequest("GET", "/contract/state?contract_addr="+address+"&block_hash="+hash, nil))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var state compile.ContractStateResponse
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &state))
			require.NotEmpty(t, state.Data)
			manifest, err := os.ReadFile(filepath.Join(root, "verified", address, "manifest.json"))
			require.NoError(t, err)
			require.Contains(t, string(manifest), "0.36.0")
		})
	}
}

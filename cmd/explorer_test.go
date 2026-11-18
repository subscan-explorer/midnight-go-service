package main

import (
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"midnight-go-sdk/compile"
	"midnight-go-sdk/config"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func TestExplorerProofHTTPIntegration(t *testing.T) {
	// Exercise the real Go -> Node -> native Ledger decoder path without chain writes.
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", `import {Transaction, ContractState, ContractDeploy, Intent} from '@midnightntwrk/ledger-v9'; const deploy = new ContractDeploy(new ContractState()); const intent = Intent.new(new Date(Date.now()+300000)).addDeploy(deploy); console.log(JSON.stringify({address:deploy.address, raw:Buffer.from(Transaction.fromParts('preview',undefined,undefined,intent).mockProve().serialize()).toString('hex')}));`)
	output, err := cmd.Output()
	require.NoError(t, err)
	var fixture struct{ Address, Raw string }
	require.NoError(t, json.Unmarshal(output, &fixture))
	raw, hash := fixture.Raw, strings.Repeat("ab", 32)
	indexer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Variables struct{ Offset struct{ Hash string } }
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode proof query: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		require.NotEmpty(t, request.Variables.Offset.Hash)
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprintf(w, `{"data":{"transactions":[{"hash":%q,"raw":%q,"protocolVersion":2000000}]}}`, hash, raw); err != nil {
			t.Errorf("write proof fixture: %v", err)
		}
	}))
	defer indexer.Close()
	conf := &compile.Config{StaticDir: "../static", ArtifactDir: t.TempDir(), NetworkConf: &config.NetworkConfig{
		NetworkID: config.Preview, Indexer: indexer.URL, IndexerWS: strings.Replace(indexer.URL, "http:", "ws:", 1), Node: indexer.URL,
	}}
	handler := explorerHandler(conf)
	for _, valid := range []bool{true, false} {
		requested := hash
		if !valid {
			requested = strings.Repeat("cd", 32)
		}
		body := fmt.Sprintf(`{"operation":"proof","contract":"0x%s","transaction_hash":"0x%s"}`, hash, requested)
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodPost, "/contract/explorer", strings.NewReader(body)))
		if valid {
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			var result struct {
				HasZKProof bool   `json:"has_zk_proof"`
				Era        string `json:"era"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
			require.False(t, result.HasZKProof)
			require.Equal(t, "v9", result.Era)
		} else {
			require.Equal(t, http.StatusUnprocessableEntity, response.Code)
			require.Equal(t, "transaction_snapshot_unavailable", response.Header().Get("X-Midnight-Error-Code"))
			require.NotContains(t, response.Body.String(), indexer.URL)
		}
	}
	for _, address := range []string{fixture.Address, strings.Repeat("ef", 32)} {
		body := fmt.Sprintf(`{"operation":"deployment","contract":"0x%s","transaction_hash":"0x%s"}`, address, hash)
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodPost, "/contract/explorer", strings.NewReader(body)))
		if address == fixture.Address {
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var result struct {
				Source           string `json:"source"`
				TransactionBytes string `json:"transaction_bytes"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
			require.Equal(t, "contract_deploy", result.Source)
			require.Equal(t, raw, result.TransactionBytes)
		} else {
			require.Equal(t, http.StatusUnprocessableEntity, response.Code)
			require.Equal(t, "deployment_not_found", response.Header().Get("X-Midnight-Error-Code"))
		}
	}
}

func TestExplorerRejectsMalformedHTTPBeforeIO(t *testing.T) {
	handler := explorerHandler(&compile.Config{StaticDir: t.TempDir()})
	for _, body := range []string{`{}`, `{"operation":"inspect","contract":"../../secret"}`, `{"private_state":"secret"}`, strings.Repeat("a", 65537), `{} {}`} {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest("POST", "/contract/explorer", strings.NewReader(body)))
		require.Equal(t, 400, response.Code)
		require.NotContains(t, response.Body.String(), "secret")
	}
	response := httptest.NewRecorder()
	handler(response, httptest.NewRequest("GET", "/contract/explorer", nil))
	require.Equal(t, 405, response.Code)
}

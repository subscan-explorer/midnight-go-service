package compile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"midnight-go-sdk/config"

	"github.com/stretchr/testify/require"
)

func TestStateReaderProcessIntegration(t *testing.T) {
	conf := &Config{StaticDir: "../static", NetworkConf: &config.NetworkConfig{
		NetworkID: config.MainNet, Indexer: "https://example.test/graphql?project_id=private-token", IndexerWS: "wss://example.test/graphql/ws?project_id=private-token", Node: "https://rpc.example.test/?token=private-node-token",
	}}
	dir, err := os.MkdirTemp("../static", ".reader-test-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	script := filepath.Join(dir, "reader.mjs")
	require.NoError(t, os.WriteFile(script, []byte(`console.log(JSON.stringify({network: process.env.MIDNIGHT_NETWORK_ID, indexer: process.env.INDEXER_RPC, websocket: process.env.INDEXER_WS, node: process.env.RPC_NODE, rpc_limit: process.env.MIDNIGHT_RPC_RESPONSE_LIMIT_BYTES}));`), 0600))
	output, err := conf.ExecTsScript(context.Background(), script)
	require.NoError(t, err)
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(output), &got))
	require.Equal(t, "mainnet", got["network"])
	require.Equal(t, conf.NetworkConf.GetIndexer(), got["indexer"])
	require.Equal(t, conf.NetworkConf.GetIndexerWS(), got["websocket"])
	require.Equal(t, conf.NetworkConf.GetNode(), got["node"])
	require.Equal(t, "33554432", got["rpc_limit"])

	// A shared helper resolves Preprod's default RPC from its own project ID
	// before passing the authenticated URL to the request's reader process.
	t.Setenv("MIDNIGHT_PREPROD_RPC_NODE", "")
	t.Setenv("MIDNIGHT_PREPROD_BLOCKFROST_PROJECT_ID", "preprod-private-project")
	t.Setenv("NETWORK_ID", "preview")
	conf.NetworkConf, err = config.NewNetworkConfigByID("preprod")
	require.NoError(t, err)
	output, err = conf.ExecTsScript(context.Background(), script)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(output), &got))
	require.Equal(t, "preprod", got["network"])
	require.Equal(t, "https://rpc.midnight-preprod.blockfrost.io?project_id=preprod-private-project", got["node"])

	require.NoError(t, conf.Generate(context.Background(), "0x1234", "./contract", dir))
	reader, err := os.ReadFile(filepath.Join(dir, "generated_demo.ts"))
	require.NoError(t, err)
	require.NotContains(t, string(reader), "private-token")
	require.NotContains(t, string(reader), "preprod-private-project")
	require.NotContains(t, string(reader), "example.test")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, os.WriteFile(script, []byte(`setInterval(() => {}, 1000);`), 0600))
	_, err = conf.ExecTsScript(ctx, script)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = os.Stat(filepath.Join(dir, "generated_demo.ts"))
	require.NoError(t, err)
	require.ErrorIs(t, conf.Generate(ctx, "0x1234", "./contract", dir), context.DeadlineExceeded)
}

func TestFixedStateReaderIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "--import", "../static/register-runtime.mjs", "--input-type=module", "--eval", `import {stateFixture} from '../static/fixtures/state-fixture.mjs'; console.log(JSON.stringify({latest:stateFixture(2n), historical:stateFixture(1n)}));`)
	output, err := cmd.Output()
	require.NoError(t, err)
	var fixtures struct{ Latest, Historical string }
	require.NoError(t, json.Unmarshal(output, &fixtures))
	// Deliver request parameters only after the HTTP handler is finished reading
	// them, so assertions do not race with GraphQL handling.
	requests := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Variables struct {
				Address string `json:"address"`
				Offset  *struct {
					BlockOffset struct{ Hash string } `json:"blockOffset"`
				} `json:"offset"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode GraphQL request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		state, hash := fixtures.Latest, ""
		if request.Variables.Offset != nil {
			state = fixtures.Historical
			hash = request.Variables.Offset.BlockOffset.Hash
		}
		requests <- request.Variables.Address + ":" + hash
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprintf(w, `{"data":{"contractAction":{"__typename":"ContractUpdate","state":%q}}}`, state); err != nil {
			t.Errorf("write GraphQL response: %v", err)
		}
	}))
	defer server.Close()

	conf := newFixedReaderConfig(t, server.URL)
	address, hash := "0x"+strings.Repeat("ab", 32), "0x"+strings.Repeat("cd", 32)
	dir := filepath.Join(conf.StaticDir, "verified", address)
	contractPath := filepath.Join(dir, "src", "managed", "contract", "index.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(contractPath), 0755))
	legacyPath := filepath.Join(dir, "generated_demo.ts")
	require.NoError(t, os.WriteFile(legacyPath, []byte("old reader must stay untouched"), 0600))
	for _, version := range []string{"029", "031"} {
		contract, err := os.ReadFile("../static/fixtures/reader-" + version + ".js")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(contractPath, contract, 0600))
		for _, blockHash := range []string{"", hash} {
			state, err := conf.ReadContract(ctx, address, blockHash)
			require.NoError(t, err)
			require.Equal(t, float64(1), state.Data["counter"])
			counter := uint32(2)
			if blockHash != "" {
				counter = 1
			}
			require.Equal(t, counter, state.MaintenanceAuthority.Counter)
			require.Len(t, state.Operations, 1)
			require.Equal(t, "fixture", state.Operations[0].Operation)
			require.Equal(t, strings.TrimPrefix(address, "0x")+":"+strings.TrimPrefix(blockHash, "0x"), <-requests)
		}
		unchanged, err := os.ReadFile(contractPath)
		require.NoError(t, err)
		require.Equal(t, contract, unchanged)
	}
	state, err := conf.ReadContractCMA(ctx, address, hash)
	require.NoError(t, err)
	require.Nil(t, state.Data)
	require.Equal(t, uint32(1), state.MaintenanceAuthority.Counter)
	require.Equal(t, strings.TrimPrefix(address, "0x")+":"+strings.TrimPrefix(hash, "0x"), <-requests)
	legacy, err := os.ReadFile(legacyPath)
	require.NoError(t, err)
	require.Equal(t, "old reader must stay untouched", string(legacy))

	canceled, stop := context.WithCancel(ctx)
	stop()
	_, err = conf.ReadContract(canceled, address, hash)
	require.ErrorIs(t, err, context.Canceled)
	_, err = conf.ReadContractCMA(canceled, address, hash)
	require.ErrorIs(t, err, context.Canceled)
}

func TestFixedStateReaderCancelsIndexerRequest(t *testing.T) {
	requested := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.Body.Close(); err != nil {
			t.Errorf("close GraphQL request: %v", err)
		}
		requested <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	conf := newFixedReaderConfig(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := conf.ReadContractCMA(ctx, strings.Repeat("ab", 32), "")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case <-requested:
	default:
		t.Fatal("reader did not reach the stalled Indexer")
	}
}

func newFixedReaderConfig(t *testing.T, endpoint string) *Config {
	t.Helper()
	dir, err := os.MkdirTemp("../static", ".reader-test-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	for _, name := range []string{"read-state.mjs", "state.mjs", "runtime-loader.mjs", "register-runtime.mjs"} {
		content, err := os.ReadFile(filepath.Join("../static", name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), content, 0600))
	}
	return &Config{StaticDir: dir, NetworkConf: &config.NetworkConfig{
		NetworkID: config.Preview, Indexer: endpoint, IndexerWS: strings.Replace(endpoint, "http:", "ws:", 1),
	}}
}

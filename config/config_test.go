package config

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewNetworkConfigByIDUndeployed(t *testing.T) {
	clearNetworkEnv(t)
	cfg, err := NewNetworkConfigByID("undeployed")
	require.NoError(t, err)
	assert.Equal(t, Undeployed, cfg.GetNetworkID())
	assert.Equal(t, "http://127.0.0.1:9944", cfg.GetNode())
}

func TestMainnetRequiresTokenAndPreservesPerRequestOverrides(t *testing.T) {
	clearNetworkEnv(t)
	t.Setenv("NETWORK_ID", "preview")
	t.Setenv("BLOCKFROST_PROJECT_ID", "")
	_, err := NewNetworkConfigByID("mainnet")
	require.ErrorContains(t, err, "BLOCKFROST_PROJECT_ID")
	t.Setenv("BLOCKFROST_PROJECT_ID", "token with + query & characters")
	cfg, err := NewNetworkConfigByID("mainnet")
	require.NoError(t, err)
	for _, endpoint := range []string{cfg.Node} {
		u, err := url.Parse(endpoint)
		require.NoError(t, err)
		assert.Equal(t, "token with + query & characters", u.Query().Get("project_id"))
	}
	t.Setenv("MIDNIGHT_MAINNET_INDEXER_RPC", "https://private.example/graphql")
	t.Setenv("MIDNIGHT_MAINNET_INDEXER_WS", "wss://private.example/graphql/ws")
	t.Setenv("MIDNIGHT_MAINNET_RPC_NODE", "https://private.example/rpc")
	t.Setenv("BLOCKFROST_PROJECT_ID", "")
	cfg, err = NewNetworkConfigByID("mainnet")
	require.NoError(t, err)
	assert.Equal(t, "https://private.example/graphql", cfg.Indexer)
	assert.NotContains(t, cfg.Indexer, "project_id")
}

func TestNetworkOverridesDoNotLeakAcrossNetworks(t *testing.T) {
	clearNetworkEnv(t)
	t.Setenv("NETWORK_ID", "preview")
	t.Setenv("INDEXER_RPC", "https://preview.example/graphql")
	t.Setenv("MIDNIGHT_PREPROD_INDEXER_RPC", "https://preprod.example/graphql")
	t.Setenv("MIDNIGHT_PREPROD_RPC_NODE", "wss://preprod.example/rpc?token=preprod-secret")
	t.Setenv("RPC_NODE", "https://preview.example/rpc")
	preview, err := NewNetworkConfigByID("preview")
	require.NoError(t, err)
	preprod, err := NewNetworkConfigByID("preprod")
	require.NoError(t, err)
	assert.Equal(t, "https://preview.example/graphql", preview.Indexer)
	assert.Equal(t, "https://preprod.example/graphql", preprod.Indexer)
	assert.Equal(t, "wss://midnight-preprod.blockfrost.io/api/v0/ws", preprod.IndexerWS)
	assert.Equal(t, "wss://preprod.example/rpc?token=preprod-secret", preprod.Node)
	assert.Equal(t, "https://preview.example/rpc", preview.Node)
}

func TestPreprodCustomRPCDoesNotRequireBlockfrost(t *testing.T) {
	clearNetworkEnv(t)
	t.Setenv("NETWORK_ID", "preview")
	t.Setenv("RPC_NODE", "https://preview.example/rpc")
	_, err := NewNetworkConfigByID("preprod")
	require.ErrorContains(t, err, "MIDNIGHT_PREPROD_BLOCKFROST_PROJECT_ID")

	t.Setenv("NETWORK_ID", "preprod")
	t.Setenv("RPC_NODE", "http://existing-node.example/rpc?token=private-node-token")
	t.Setenv("BLOCKFROST_PROJECT_ID", "")
	t.Setenv("MIDNIGHT_PREPROD_INDEXER_RPC", "unused")
	t.Setenv("MIDNIGHT_PREPROD_INDEXER_WS", "unused")
	cfg, err := NewNetworkConfigByID("preprod")
	require.NoError(t, err)
	assert.Equal(t, "http://existing-node.example/rpc?token=private-node-token", cfg.Node)

	t.Setenv("MIDNIGHT_PREPROD_RPC_NODE", "wss://preprod.example/rpc")
	cfg, err = NewNetworkConfigByID("preprod")
	require.NoError(t, err)
	assert.Equal(t, "wss://preprod.example/rpc", cfg.Node)
}

func TestBlockfrostProjectIDsStayWithinTheirNetwork(t *testing.T) {
	clearNetworkEnv(t)
	t.Setenv("NETWORK_ID", "preview")
	t.Setenv("BLOCKFROST_PROJECT_ID", "mainnet + legacy & token")
	t.Setenv("MIDNIGHT_PREPROD_BLOCKFROST_PROJECT_ID", " preprod + project & token ")
	mainnet, err := NewNetworkConfigByID("mainnet")
	require.NoError(t, err)
	for _, endpoint := range []string{mainnet.Node} {
		u, err := url.Parse(endpoint)
		require.NoError(t, err)
		assert.Equal(t, "mainnet + legacy & token", u.Query().Get("project_id"))
	}
	preprod, err := NewNetworkConfigByID("preprod")
	require.NoError(t, err)
	u, err := url.Parse(preprod.Node)
	require.NoError(t, err)
	assert.Equal(t, "https", u.Scheme)
	assert.Equal(t, "rpc.midnight-preprod.blockfrost.io", u.Host)
	assert.Equal(t, "preprod + project & token", u.Query().Get("project_id"))

	preview, err := NewNetworkConfigByID("preview")
	require.NoError(t, err)
	for _, endpoint := range []string{preview.Indexer, preview.IndexerWS, preview.Node} {
		assert.NotContains(t, endpoint, "project_id")
	}

	t.Setenv("MIDNIGHT_MAINNET_BLOCKFROST_PROJECT_ID", "mainnet-explicit-project")
	mainnet, err = NewNetworkConfigByID("mainnet")
	require.NoError(t, err)
	u, err = url.Parse(mainnet.Node)
	require.NoError(t, err)
	assert.Equal(t, "mainnet-explicit-project", u.Query().Get("project_id"))
}

func TestBlockfrostProjectIDCannotFallBackToAnotherNetwork(t *testing.T) {
	for _, networkID := range []string{"mainnet", "preprod"} {
		t.Run(networkID, func(t *testing.T) {
			clearNetworkEnv(t)
			// Even a dedicated Preprod process must not reuse the legacy Mainnet ID.
			t.Setenv("NETWORK_ID", "preprod")
			if networkID == "preprod" {
				t.Setenv("BLOCKFROST_PROJECT_ID", "MAINNET_PRIVATE_PROJECT")
				t.Setenv("MIDNIGHT_MAINNET_BLOCKFROST_PROJECT_ID", "MAINNET_EXPLICIT_PROJECT")
			} else {
				t.Setenv("MIDNIGHT_PREPROD_BLOCKFROST_PROJECT_ID", "PREPROD_PRIVATE_PROJECT")
			}
			_, err := NewNetworkConfigByID(networkID)
			require.ErrorContains(t, err, "PROJECT_ID is required")
			assert.NotContains(t, err.Error(), "PRIVATE_PROJECT")
			assert.NotContains(t, err.Error(), "EXPLICIT_PROJECT")
		})
	}
}

func TestPreprodBlockfrostOverridePreservesQueryAndNetwork(t *testing.T) {
	clearNetworkEnv(t)
	t.Setenv("MIDNIGHT_PREPROD_BLOCKFROST_PROJECT_ID", "preprod-project")
	t.Setenv("MIDNIGHT_PREPROD_RPC_NODE", "wss://rpc.midnight-preprod.blockfrost.io?mode=archive")
	cfg, err := NewNetworkConfigByID("preprod")
	require.NoError(t, err)
	u, err := url.Parse(cfg.Node)
	require.NoError(t, err)
	assert.Equal(t, "wss", u.Scheme)
	assert.Equal(t, "archive", u.Query().Get("mode"))
	assert.Equal(t, "preprod-project", u.Query().Get("project_id"))

	t.Setenv("MIDNIGHT_PREPROD_RPC_NODE", "https://rpc.midnight-preprod.blockfrost.io?project_id=url-project")
	t.Setenv("MIDNIGHT_PREPROD_BLOCKFROST_PROJECT_ID", "")
	cfg, err = NewNetworkConfigByID("preprod")
	require.NoError(t, err)
	u, err = url.Parse(cfg.Node)
	require.NoError(t, err)
	assert.Equal(t, "url-project", u.Query().Get("project_id"))

	t.Setenv("MIDNIGHT_PREPROD_RPC_NODE", "https://rpc.midnight-mainnet.blockfrost.io?project_id=PRIVATE_PROJECT")
	_, err = NewNetworkConfigByID("preprod")
	require.ErrorContains(t, err, "cannot serve network preprod")
	assert.NotContains(t, err.Error(), "PRIVATE_PROJECT")
}

func TestCustomProvidersReceiveNoBlockfrostProjectID(t *testing.T) {
	clearNetworkEnv(t)
	t.Setenv("BLOCKFROST_PROJECT_ID", "MAINNET_PRIVATE_PROJECT")
	t.Setenv("MIDNIGHT_PREPROD_BLOCKFROST_PROJECT_ID", "PREPROD_PRIVATE_PROJECT")
	for _, endpoint := range []string{
		"https://custom.example/rpc?token=existing-node-token",
		"https://rpc.midnight-preprod.blockfrost.io.custom.example/rpc",
	} {
		t.Setenv("MIDNIGHT_PREPROD_RPC_NODE", endpoint)
		cfg, err := NewNetworkConfigByID("preprod")
		require.NoError(t, err)
		assert.Equal(t, endpoint, cfg.Node)
	}
}

func clearNetworkEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"NETWORK_ID", "INDEXER_RPC", "INDEXER_WS", "RPC_NODE", "BLOCKFROST_PROJECT_ID"} {
		t.Setenv(key, "")
	}
	for _, network := range []string{"MAINNET", "PREPROD", "PREVIEW"} {
		for _, key := range []string{"INDEXER_RPC", "INDEXER_WS", "RPC_NODE", "BLOCKFROST_PROJECT_ID"} {
			t.Setenv("MIDNIGHT_"+network+"_"+key, "")
		}
	}
}

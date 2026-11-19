package config

import (
	"fmt"
	"midnight-go-sdk/utils"
	"net/url"
	"os"
	"strings"
)

// NetworkID represents the network identifier
type NetworkID string

const (
	TestNet    NetworkID = "testnet-02"
	Preview    NetworkID = "preview"
	Stagenet   NetworkID = "stagenet"
	DevNet     NetworkID = "devnet"
	MainNet    NetworkID = "mainnet"
	Preprod    NetworkID = "preprod"
	Undeployed NetworkID = "undeployed"
)

type networkEndpoints struct {
	indexer   string
	indexerWS string
	node      string
}

var defaultNetworkEndpoints = map[NetworkID]networkEndpoints{
	Stagenet: {
		indexer:   "https://indexer.stagenet.shielded.tools/api/v4/graphql",
		indexerWS: "wss://indexer.stagenet.shielded.tools/api/v4/graphql/ws",
		node:      "https://rpc.stagenet.shielded.tools",
	},
	Preview: {
		indexer:   "https://indexer.preview.midnight.network/api/v4/graphql",
		indexerWS: "wss://indexer.preview.midnight.network/api/v4/graphql/ws",
		node:      "https://rpc.preview.midnight.network",
	},
	TestNet: {
		indexer:   "https://indexer.testnet-02.midnight.network/api/v4/graphql",
		indexerWS: "wss://indexer.testnet-02.midnight.network/api/v4/graphql/ws",
		node:      "https://rpc.testnet-02.midnight.network",
	},
	DevNet: {
		indexer:   "https://indexer.devnet.midnight.network/api/v4/graphql",
		indexerWS: "wss://indexer.devnet.midnight.network/api/v4/graphql/ws",
		node:      "https://rpc.devnet.midnight.network",
	},
	MainNet: {
		indexer:   "https://midnight-mainnet.blockfrost.io/api/v0",
		indexerWS: "wss://midnight-mainnet.blockfrost.io/api/v0/ws",
		node:      "https://rpc.midnight-mainnet.blockfrost.io",
	},
	Preprod: {
		indexer:   "https://midnight-preprod.blockfrost.io/api/v0",
		indexerWS: "wss://midnight-preprod.blockfrost.io/api/v0/ws",
		node:      "https://rpc.midnight-preprod.blockfrost.io",
	},
	Undeployed: {
		indexer:   "http://127.0.0.1:8088/api/v4/graphql",
		indexerWS: "ws://127.0.0.1:8088/api/v4/graphql/ws",
		node:      "http://127.0.0.1:9944",
	},
}

// Config interface defines the configuration structure
type Config interface {
	GetIndexer() string
	GetIndexerWS() string
	GetNode() string
	GetNetworkID() NetworkID
}

// NetworkConfig implements Config for the Midnight network
type NetworkConfig struct {
	LogDir    string
	Indexer   string
	IndexerWS string
	Node      string
	NetworkID NetworkID
}

// NewNetworkConfig resolves the configured network and validates its endpoints before serving requests.
func NewNetworkConfig() (*NetworkConfig, error) {
	return NewNetworkConfigByID(utils.GetEnv("NETWORK_ID", string(Preview)))
}

// NewNetworkConfigByID keeps per-network overrides when requests select a network.
// Blockfrost project IDs stay within their network; BLOCKFROST_PROJECT_ID remains
// the legacy Mainnet setting. Custom providers receive no Blockfrost credentials.
func NewNetworkConfigByID(networkID string) (*NetworkConfig, error) {
	id := NetworkID(strings.ToLower(strings.TrimSpace(networkID)))
	endpoints, ok := defaultNetworkEndpoints[id]
	if !ok {
		return nil, fmt.Errorf("unsupported networkid: %s", networkID)
	}
	prefix := "MIDNIGHT_" + strings.ToUpper(strings.ReplaceAll(string(id), "-", "_")) + "_"
	configuredID := NetworkID(strings.ToLower(utils.GetEnv("NETWORK_ID", string(Preview))))
	resolve := func(key, fallback string) string {
		if value := strings.TrimSpace(os.Getenv(prefix + key)); value != "" {
			return value
		}
		if id == configuredID {
			return utils.GetEnv(key, fallback)
		}
		return fallback
	}
	cfg := &NetworkConfig{Indexer: resolve("INDEXER_RPC", endpoints.indexer), IndexerWS: resolve("INDEXER_WS", endpoints.indexerWS), Node: resolve("RPC_NODE", endpoints.node), NetworkID: id}
	validate := []struct {
		name  string
		value *string
	}{{"indexer", &cfg.Indexer}, {"indexer websocket", &cfg.IndexerWS}, {"node", &cfg.Node}}
	if id == Preprod || id == MainNet {
		// Native snapshot readers must not depend on unused Indexer settings.
		validate = validate[2:]
	}
	for _, endpoint := range validate {
		resolved, err := networkEndpoint(id, endpoint.name, *endpoint.value)
		if err != nil {
			return nil, err
		}
		*endpoint.value = resolved
	}
	return cfg, nil
}

// WithExplorerIndexer resolves only the indexer transports used by this operation.
func WithExplorerIndexer(c Config, websocket bool) (Config, error) {
	if c == nil || (c.GetNetworkID() != MainNet && c.GetNetworkID() != Preprod) {
		return c, nil
	}
	id := c.GetNetworkID()
	indexer, err := networkEndpoint(id, "indexer", c.GetIndexer())
	if err != nil {
		return nil, err
	}
	ws := c.GetIndexerWS()
	if websocket {
		ws, err = networkEndpoint(id, "indexer websocket", ws)
		if err != nil {
			return nil, err
		}
	}
	return &NetworkConfig{NetworkID: id, Node: c.GetNode(), Indexer: indexer, IndexerWS: ws}, nil
}

func networkEndpoint(id NetworkID, name, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("invalid %s endpoint for %s", name, id)
	}
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ws" && u.Scheme != "wss" {
		return "", fmt.Errorf("unsupported %s endpoint scheme for %s", name, id)
	}
	var providerNetwork NetworkID
	switch u.Hostname() {
	case "midnight-mainnet.blockfrost.io", "rpc.midnight-mainnet.blockfrost.io":
		providerNetwork = MainNet
	case "midnight-preprod.blockfrost.io", "rpc.midnight-preprod.blockfrost.io":
		providerNetwork = Preprod
	}
	if providerNetwork != "" {
		if providerNetwork != id {
			return "", fmt.Errorf("Blockfrost %s endpoint cannot serve network %s", providerNetwork, id)
		}
		if u.Scheme != "https" && u.Scheme != "wss" {
			return "", fmt.Errorf("insecure Blockfrost %s endpoint", name)
		}
		q := u.Query()
		if q.Get("project_id") == "" {
			projectIDKey := "MIDNIGHT_" + strings.ToUpper(string(id)) + "_BLOCKFROST_PROJECT_ID"
			projectID := strings.TrimSpace(os.Getenv(projectIDKey))
			if projectID == "" && id == MainNet {
				// Existing shared helpers already use this key for Mainnet, even when
				// NETWORK_ID is Preview. Never apply it to a Preprod request.
				projectIDKey = "BLOCKFROST_PROJECT_ID"
				projectID = strings.TrimSpace(os.Getenv(projectIDKey))
			}
			if projectID == "" {
				return "", fmt.Errorf("%s is required for %s Blockfrost endpoints", projectIDKey, id)
			}
			q.Set("project_id", projectID)
		}
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	return raw, nil
}

// GetLogDir returns the log directory
func (c *NetworkConfig) GetLogDir() string {
	return c.LogDir
}

// GetIndexer returns the indexer URL
func (c *NetworkConfig) GetIndexer() string {
	return c.Indexer
}

// GetIndexerWS returns the indexer WebSocket URL
func (c *NetworkConfig) GetIndexerWS() string {
	return c.IndexerWS
}

// GetNode returns the node URL
func (c *NetworkConfig) GetNode() string {
	return c.Node
}

// GetNetworkID returns the network ID
func (c *NetworkConfig) GetNetworkID() NetworkID {
	return c.NetworkID
}

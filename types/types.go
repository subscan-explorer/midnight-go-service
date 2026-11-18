package types

// NetworkId represents the network identifier
type NetworkId string

const (
	TestNet    NetworkId = "testnet-02"
	Preview    NetworkId = "preview"
	DevNet     NetworkId = "devnet"
	MainNet    NetworkId = "mainnet"
	Undeployed NetworkId = "undeployed"
)

// ContractAddress represents a contract address
type ContractAddress string

// TokenType represents a token type
type TokenType string

// Nonce represents a nonce value
type Nonce string

// StateValue represents a state value
type StateValue interface{}

type JsonRPCResponse struct {
	Result any `json:"result,omitempty"`
}

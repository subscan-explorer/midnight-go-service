package compile

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ExplorerRequest contains only public circuit parameters and wallet public keys.
// Neither private witnesses nor wallet signing material are accepted.
type ExplorerRequest struct {
	Page                    int             `json:"page,omitempty"`
	Operation               string          `json:"operation"`
	Contract                string          `json:"contract"`
	NetworkID               string          `json:"network_id"`
	Hash                    string          `json:"hash,omitempty"`
	Circuit                 string          `json:"circuit,omitempty"`
	Arguments               json.RawMessage `json:"arguments,omitempty"`
	CoinPublicKey           string          `json:"coin_public_key,omitempty"`
	EncryptionPublicKey     string          `json:"encryption_public_key,omitempty"`
	Artifact                string          `json:"artifact,omitempty"`
	Field                   string          `json:"field,omitempty"`
	Leaf                    json.RawMessage `json:"leaf,omitempty"`
	LeafIndex               string          `json:"leaf_index,omitempty"`
	LeafHash                string          `json:"leaf_hash,omitempty"`
	Root                    string          `json:"root,omitempty"`
	Proof                   json.RawMessage `json:"proof,omitempty"`
	Verified                bool            `json:"verified"`
	TransactionHash         string          `json:"transaction_hash,omitempty"`
	BlockHash               string          `json:"block_hash,omitempty"`
	Outcome                 string          `json:"outcome,omitempty"`
	TransactionBytes        string          `json:"transaction_bytes,omitempty"`
	PreviousTransactionHash string          `json:"previous_transaction_hash,omitempty"`
}

var explorerAddress = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
var explorerIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,127}$`)
var explorerRoot = regexp.MustCompile(`^(0x)?[0-9a-fA-F]{1,64}$`)

// Validate rejects path components and unbounded inputs before filesystem or RPC access.
func (r ExplorerRequest) Validate() error {
	if !explorerAddress.MatchString(r.Contract) || (r.Hash != "" && !explorerAddress.MatchString(r.Hash)) {
		return fmt.Errorf("invalid contract or block hash")
	}
	if r.Hash != "" && r.TransactionHash != "" {
		return fmt.Errorf("provide one block or transaction snapshot, not both")
	}
	switch r.Operation {
	case "index_contract_events", "executed_contract_events":
		if !explorerAddress.MatchString(r.BlockHash) || !explorerAddress.MatchString(r.TransactionHash) ||
			(r.Outcome != "success" && r.Outcome != "partial_success" && r.Outcome != "failure" && r.Outcome != "unknown") {
			return fmt.Errorf("invalid transaction execution inputs")
		}
		if r.TransactionBytes != "" {
			if !strings.HasPrefix(r.TransactionBytes, "0x") || len(r.TransactionBytes) > (12<<20) {
				return fmt.Errorf("invalid transaction bytes")
			}
			if _, err := hex.DecodeString(r.TransactionBytes[2:]); err != nil {
				return fmt.Errorf("invalid transaction bytes: %w", err)
			}
		}
	case "inspect":
		if r.TransactionHash != "" && !explorerAddress.MatchString(r.TransactionHash) {
			return fmt.Errorf("invalid deployment transaction")
		}
	case "changes":
		if !explorerAddress.MatchString(r.TransactionHash) || (r.PreviousTransactionHash != "" && !explorerAddress.MatchString(r.PreviousTransactionHash)) {
			return fmt.Errorf("invalid transaction snapshot")
		}
	case "contract_events":
		if r.Page < 0 || r.Page > 2147483647/20 {
			return fmt.Errorf("invalid event page")
		}
	case "proof", "deployment", "transaction_events", "transaction_zswap":
		if !explorerAddress.MatchString(r.TransactionHash) {
			return fmt.Errorf("invalid transaction hash")
		}
	case "prepare":
		if !explorerIdentifier.MatchString(r.Circuit) || len(r.Arguments) == 0 || len(r.Arguments) > 32768 {
			return fmt.Errorf("invalid public circuit arguments")
		}
		if len(r.CoinPublicKey) < 64 || len(r.CoinPublicKey) > 256 || len(r.EncryptionPublicKey) < 64 || len(r.EncryptionPublicKey) > 256 {
			return fmt.Errorf("invalid wallet public keys")
		}
		if r.Hash != "" {
			return fmt.Errorf("transactions must be prepared against current state")
		}
	case "artifact":
		if !explorerIdentifier.MatchString(r.Circuit) {
			return fmt.Errorf("invalid circuit")
		}
		if r.Artifact != "prover" && r.Artifact != "verifier" && r.Artifact != "zkir" {
			return fmt.Errorf("invalid artifact kind")
		}
	case "merkle":
		index, err := strconv.ParseUint(r.LeafIndex, 10, 32)
		if err != nil || index > 1<<32-1 || strconv.FormatUint(index, 10) != r.LeafIndex || !explorerIdentifier.MatchString(r.Field) {
			return fmt.Errorf("invalid Merkle inclusion request")
		}
		if (len(r.Leaf) == 0) == (r.LeafHash == "") || len(r.Leaf) > 32768 || (len(r.Leaf) > 0 && !json.Valid(r.Leaf)) {
			return fmt.Errorf("provide exactly one public leaf or leaf hash")
		}
		if r.LeafHash != "" && !explorerAddress.MatchString("0x"+strings.TrimPrefix(r.LeafHash, "0x")) {
			return fmt.Errorf("invalid Merkle leaf hash")
		}
		if r.Root != "" && !explorerRoot.MatchString(r.Root) {
			return fmt.Errorf("invalid Merkle root")
		}
		if len(r.Proof) > 16384 || (len(r.Proof) > 0 && !json.Valid(r.Proof)) {
			return fmt.Errorf("invalid or oversized Merkle proof")
		}
		if r.TransactionHash != "" && !explorerAddress.MatchString(r.TransactionHash) {
			return fmt.Errorf("invalid transaction snapshot")
		}
	default:
		return fmt.Errorf("unsupported explorer operation")
	}
	return nil
}

// Explore executes an isolated, bounded public-data operation using verified artifacts.
func (c *Config) Explore(ctx context.Context, request ExplorerRequest) (json.RawMessage, error) {
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("validate explorer request: %w", err)
	}
	if request.Operation == "index_contract_events" || request.Operation == "executed_contract_events" {
		encoded, err := json.Marshal(request)
		if err != nil {
			return nil, fmt.Errorf("encode index request: %w", err)
		}
		// Raw finalized transactions can exceed the operating system argv limit.
		return c.execTsInput(ctx, bytes.NewReader(encoded), filepath.Join(c.StaticDir, "index-contract-events.mjs"))
	}
	request.Contract = strings.ToLower(request.Contract)
	switch request.Operation {
	case "proof", "transaction_events", "transaction_zswap", "contract_events":
		request.Verified = false // Public ledger reads are independent of local contract artifacts.
	default:
		request.Verified = c.IsContractVerified(ctx, request.Contract)
		if !request.Verified && request.Operation != "inspect" && request.Operation != "deployment" {
			return nil, fmt.Errorf("verified contract artifacts required")
		}
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode explorer request: %w", err)
	}
	directory, err := filepath.Abs(filepath.Join(c.GetStaticDir(), "verified", request.Contract, "src", "managed"))
	if err != nil {
		return nil, fmt.Errorf("resolve explorer artifacts: %w", err)
	}
	output, err := c.execTsBytes(ctx, filepath.Join(c.StaticDir, "explorer.mjs"), string(encoded), directory)
	if err != nil {
		return nil, fmt.Errorf("execute explorer operation: %w", err)
	}
	if !json.Valid(output) {
		return nil, fmt.Errorf("invalid explorer response")
	}
	return output, nil
}

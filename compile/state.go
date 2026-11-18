package compile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"midnight-go-sdk/logger"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ContractState represents the state of a contract in Go, equivalent to the Rust ContractState struct.
type ContractState struct {
	Data                 ChargedState                        `json:"data"` // The primary state of the contract.
	Operations           map[EntryPointBuf]ContractOperation // Map of contract operations by entry point.
	MaintenanceAuthority ContractMaintenanceAuthority        `json:"maintenanceAuthority"` // Contract maintenance authority details.
	Balance              map[TokenType]*big.Int              // Public balances held by the contract.
}

// ChargedState represents the underlying data of the contract.
type ChargedState struct {
	State       map[string]interface{} // Generic state values.
	ChargedKeys map[string]interface{} // Charged keys associated with the state.
}

// EntryPointBuf represents an entry point in the contract.
type EntryPointBuf struct {
	EntryHash string // A hash representing the entry point.
}

// ContractOperation represents an operation that can be performed on the contract.
type ContractOperation struct {
	OperationType string                 // Type of operation (e.g., "call", "transfer").
	Data          map[string]interface{} // Additional data for the operation.
}

// ContractMaintenanceAuthority represents the authority managing the contract.
type ContractMaintenanceAuthority struct {
	Committee []VerifyingKey // List of committee keys.
	Threshold uint32         // Minimum number of keys required for rule changes.
	Counter   uint32         // Replay protection counter.
}

// TokenType represents a token type used in the contract.
type TokenType struct {
	Symbol   string // Token symbol (e.g., "ETH", "BTC").
	Decimals int    // Number of decimals for the token.
}

// VerifyingKey represents a cryptographic key used for verification.
type VerifyingKey struct {
	Key string // The public key in string format.
}

type ContractStateResponse struct {
	Data                 map[string]interface{} `json:"data,omitempty"`
	MaintenanceAuthority struct {
		Committee CMACommittee `json:"committee"`
		Threshold uint32       `json:"threshold"`
		Counter   uint32       `json:"counter"`
	} `json:"maintenanceAuthority,omitempty"`
	Operations []struct {
		Operation   string `json:"operation"`
		VerifierKey string `json:"verifierKey"`
	} `json:"operations"`
}

// ExecTsScript runs an isolated reader with request cancellation and network-specific endpoints.
// JavaScript readers skip TypeScript loading; legacy .ts scripts remain supported.
func (c *Config) ExecTsScript(ctx context.Context, scriptPath string, args ...string) (string, error) {
	output, err := c.execTsBytes(ctx, scriptPath, args...)
	return string(output), err
}

func (c *Config) execTsBytes(ctx context.Context, scriptPath string, args ...string) (output []byte, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	op := logger.Queued(ctx, "state_read")
	diagnostics := logger.Fields{}
	defer func() {
		if errors.Is(resultErr, ErrStateOutputTooLarge) {
			diagnostics["error_code"] = "state_output_limit"
		}
		op.Finished(resultErr, diagnostics)
	}()
	loader, err := filepath.Abs(filepath.Join(c.StaticDir, "register-runtime.mjs"))
	if err != nil {
		return nil, fmt.Errorf("resolve runtime loader: %w", err)
	}
	runCtx, release, err := c.beginState(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	op.Started()
	runCtx, cancel := context.WithCancel(runCtx)
	defer cancel()

	cmdArgs := []string{"--import", loader}
	if filepath.Ext(scriptPath) == ".ts" {
		cmdArgs = append(cmdArgs, "--experimental-specifier-resolution=node", "--loader", "ts-node/esm")
	}
	cmdArgs = append(cmdArgs, scriptPath)
	cmdArgs = append(cmdArgs, args...)
	cmd := commandContext(runCtx, "node", cmdArgs...)
	if c.NetworkConf != nil {
		cmd.Env = append(os.Environ(), fmt.Sprintf("MIDNIGHT_NETWORK_ID=%s", c.NetworkConf.GetNetworkID()), fmt.Sprintf("INDEXER_RPC=%s", c.NetworkConf.GetIndexer()), fmt.Sprintf("INDEXER_WS=%s", c.NetworkConf.GetIndexerWS()), fmt.Sprintf("RPC_NODE=%s", c.NetworkConf.GetNode()), "TS_NODE_TRANSPILE_ONLY=true", `TS_NODE_COMPILER_OPTIONS={"module":"NodeNext","moduleResolution":"NodeNext"}`)
	}
	rt := c.runtime()
	stdout := &stateOutputBuffer{max: rt.stateOutputLimit(), cancel: cancel}
	timeout := rt.StateTimeout
	if deadline, ok := runCtx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	cmd.Env = append(cmd.Environ(), "MIDNIGHT_INDEXER_RESPONSE_LIMIT_BYTES="+strconv.Itoa(rt.indexerResponseLimit()), "MIDNIGHT_RPC_RESPONSE_LIMIT_BYTES="+strconv.Itoa(rt.rpcResponseLimit()), "MIDNIGHT_STATE_OUTPUT_LIMIT_BYTES="+strconv.Itoa(rt.stateOutputLimit()))
	if timeout > 0 {
		cmd.Env = append(cmd.Env, "MIDNIGHT_STATE_TIMEOUT_MS="+strconv.FormatInt(timeout.Milliseconds()+1, 10))
	}
	stderr := &cappedBuffer{max: rt.OutputLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		if runCtx.Err() != nil {
			return nil, fmt.Errorf("contract state reader canceled: %w", runCtx.Err())
		}
		return nil, fmt.Errorf("contract state reader failed: %w", err)
	}
	rt.track(cmd)
	defer rt.untrack(cmd.Process.Pid)
	err = cmd.Wait()
	if stdout.exceeded {
		return nil, ErrStateOutputTooLarge
	}
	if err != nil {
		diagnostics = stateReaderDiagnostics(stderr.String())
		if runCtx.Err() != nil {
			diagnostics = logger.Fields{} // cancellation takes precedence over child diagnostics
			return nil, fmt.Errorf("contract state reader canceled: %w", runCtx.Err())
		}
		if code, ok := diagnostics["error_code"].(string); ok {
			return nil, fmt.Errorf("contract state reader: %w", &StateReadError{Code: code, Err: err})
		}
		return nil, fmt.Errorf("contract state reader failed: %w", err)
	}
	return stdout.buf.Bytes(), nil
}

// StateReadError carries a fixed reader classification without provider credentials or payloads.
type StateReadError struct {
	Code string
	Err  error
}

// Error returns only the safe diagnostic classification.
func (e *StateReadError) Error() string { return e.Code }

// Unwrap preserves exit-status diagnostics without exposing subprocess output.
func (e *StateReadError) Unwrap() error { return e.Err }

func stateReaderDiagnostics(stderr string) logger.Fields {
	fields := logger.Fields{}
	if len(stderr) > 4096 {
		return fields
	}
	var diagnostic struct {
		Code   string `json:"error_code"`
		Status int    `json:"upstream_status"`
	}
	if json.Unmarshal([]byte(stderr), &diagnostic) != nil {
		return fields
	}
	switch diagnostic.Code {
	case "indexer_response_limit", "indexer_invalid_response", "indexer_network_failed",
		"indexer_http_failed", "indexer_graphql_failed", "indexer_state_unavailable", "state_decode_failed",
		"rpc_response_limit", "rpc_invalid_response", "rpc_network_failed",
		"rpc_http_failed", "rpc_call_failed", "rpc_invalid_request", "rpc_config_invalid", "rpc_block_not_found",
		"state_not_found", "state_output_limit", "deadline_exceeded", "state_read_failed",
		"private_witness_required", "unsupported_runtime", "public_tree_unavailable", "invalid_merkle_leaf", "invalid_merkle_proof", "merkle_root_unavailable", "historical_proof_required", "deployment_not_found", "explorer_failed", "transaction_snapshot_unavailable":
		fields["error_code"] = diagnostic.Code
		if (diagnostic.Code == "indexer_http_failed" || diagnostic.Code == "rpc_http_failed") && diagnostic.Status >= 400 && diagnostic.Status <= 599 {
			fields["upstream_status"] = diagnostic.Status
		}
	}
	return fields
}

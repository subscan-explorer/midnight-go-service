package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"midnight-go-sdk/compile"
	"midnight-go-sdk/config"
	"midnight-go-sdk/logger"
	"midnight-go-sdk/network"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var validBlockHash = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)

const (
	mismatch = "mismatch"
	perfect  = compile.VerifyStatusPerfect
	partial  = compile.VerifyStatusPartial
)

// systemError is a generic error returned to API consumers; detailed/internal errors are logged separately
var systemError = fmt.Errorf("system error")

type VerificationResponse struct {
	VerifiedStatus string   `json:"verified_status"`
	Message        string   `json:"message"`
	Uncovered      []string `json:"uncovered_circuits,omitempty"`
	// data if verified
	ABI    interface{}          `json:"abi,omitempty"`
	Viewer *compile.ContractABI `json:"viewer,omitempty"`
	CMA    []string             `json:"cma,omitempty"`
}

func respondError(w http.ResponseWriter, r *http.Request, code string, err error) {
	requestFailed(r, code, err)
	logger.AddFields(r.Context(), logger.Fields{"verified_status": mismatch})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(VerificationResponse{VerifiedStatus: mismatch, Message: err.Error()})
}

type CompileRequest struct {
	CompilerOptions  compile.CompilerOptions `json:"compiler_options"`
	ContractAddr     string                  `json:"contract_addr"`
	SourceCode       string                  `json:"source_code"`
	CompilerVersion  string                  `json:"compiler_version"`
	VerificationKeys map[string]string       `json:"verification_keys"`
	SourceType       compile.SourceType      `json:"source_type"`
	NetworkID        string                  `json:"network_id"`
}

func (r CompileRequest) resolveNetworkID() string {
	return strings.TrimSpace(r.NetworkID)
}

func resolveCompileByNetwork(ctx context.Context, c compile.ICompile, networkID string) (compile.ICompile, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("resolve network canceled: %w", err)
	}
	var networkConf config.Config
	if networkID == "" {
		base, ok := c.(*compile.Config)
		if !ok || base.NetworkConf == nil {
			return c, nil
		}
		networkConf = base.NetworkConf
	} else {
		selected, err := config.NewNetworkConfigByID(networkID)
		if err != nil {
			return nil, fmt.Errorf("resolve network: %w", err)
		}
		networkConf = selected
	}
	base, ok := c.(*compile.Config)
	if !ok {
		return c, nil
	}
	perRequest := *base
	perRequest.NetworkConf = networkConf
	perRequest.Client = network.NewClient(networkConf.GetNode())
	if networkConf.GetNetworkID() == config.Stagenet {
		epoch := strings.TrimSpace(os.Getenv("MIDNIGHT_STAGENET_EPOCH"))
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`).MatchString(epoch) {
			return nil, fmt.Errorf("MIDNIGHT_STAGENET_EPOCH is required for isolated Stagenet artifacts")
		}
		// Keep epoch artifacts under the existing verified PVC mount. The hidden
		// namespace cannot collide with a hexadecimal contract address.
		perRequest.ArtifactDir = filepath.Join(base.StaticDir, "verified", ".networks", "stagenet", epoch)
		perRequest.TempDir = filepath.Join(base.TempDir, "stagenet", epoch)
		if err := os.MkdirAll(perRequest.TempDir, 0755); err != nil {
			return nil, fmt.Errorf("create isolated temp directory: %w", err)
		}
	}

	return &perRequest, nil
}

// compileHandler creates an HTTP handler for compiling contracts.
// todo: 1. verify contract has not been verified
//  2. if verified, return verified status directly
//  3. remove verified contract keys and zkir dir
//
// Keep ownership through verification and cleanup, not just the compiler process.
func compileHandler(c compile.ICompile) http.HandlerFunc {
	locks := &contractLocks{entries: make(map[string]*contractLock)}
	limits := handlerLimits(c)

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			requestError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST method is allowed")
			return
		}

		if !limits.VerifyRequests.TryAcquire() {
			respondBusy(w, r)
			return
		}
		defer limits.VerifyRequests.Release()
		bodyLimit := limits.VerifyBodyLimit
		if bodyLimit <= 0 {
			bodyLimit = 8 << 20
		}
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		// parse request body
		var req CompileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				requestError(w, r, http.StatusRequestEntityTooLarge, "request_body_limit", "Request body too large")
			} else {
				requestError(w, r, http.StatusBadRequest, "invalid_json", "Invalid request body")
			}
			return
		}
		logger.AddFields(r.Context(), logger.Fields{"contract_addr": logToken(req.ContractAddr), "network": logToken(req.resolveNetworkID()), "compiler_version": logToken(req.CompilerVersion), "source_type": logToken(string(req.SourceType)), "source_bytes": len(req.SourceCode)})
		// validate request
		if req.ContractAddr == "" || req.SourceCode == "" || req.CompilerVersion == "" || len(req.VerificationKeys) == 0 || (req.SourceType != compile.SingleFile && req.SourceType != compile.MultiFile) {
			respondError(w, r, "missing_required_fields", fmt.Errorf("missing required fields"))
			return
		}
		activeCompile, err := resolveCompileByNetwork(r.Context(), c, req.resolveNetworkID())
		if err != nil {
			respondError(w, r, "invalid_network", err)
			return
		}

		if conf, ok := activeCompile.(*compile.Config); ok {
			selected := *conf
			selected.CompilerOptions = req.CompilerOptions
			activeCompile = &selected
			if err := compile.ValidateCompilerOptions(compileNetworkID(conf), req.CompilerVersion, req.CompilerOptions); err != nil {
				respondError(w, r, "invalid_compiler_options", err)
				return
			}
		} else if req.CompilerOptions.FeatureZKIRV3 {
			respondError(w, r, "invalid_compiler_options", fmt.Errorf("compiler does not support feature options"))
			return
		}

		logNetwork(r, activeCompile)
		ctx := r.Context()
		lockStarted := time.Now()
		logger.Event(ctx, "INFO", "verification_waiting", logger.Fields{"phase": "contract_lock"})
		release, err := locks.acquire(ctx, filepath.Clean(req.ContractAddr))
		if err != nil {
			respondError(w, r, "contract_lock_failed", err)
			return
		}
		defer release()
		logger.Event(ctx, "INFO", "verification_started", logger.Fields{"lock_wait_ms": time.Since(lockStarted).Milliseconds()})

		defer func() {
			// clean up temp dir if not verified
			if err := os.RemoveAll(fmt.Sprintf("%s/%s", activeCompile.GetTempDir(), req.ContractAddr)); err != nil {
				log.Printf("Failed to clean up temp dir: %v", err)
			}
		}()

		cleanCompileCache := func() {
			if err := os.RemoveAll(fmt.Sprintf("%s/verified/%s", activeCompile.GetStaticDir(), req.ContractAddr)); err != nil {
				log.Printf("Failed to clean up compile cache dir: %v", err)
			}
		}
		cleanZkirDir := func() {
			if err := os.RemoveAll(fmt.Sprintf("%s/zkir/%s/src/managed/keys", activeCompile.GetStaticDir(), req.ContractAddr)); err != nil {
				log.Printf("Failed to clean up zkir dir: %v", err)
			}
			if err := os.RemoveAll(fmt.Sprintf("%s/zkir/%s/src/managed/zkir", activeCompile.GetStaticDir(), req.ContractAddr)); err != nil {
				log.Printf("Failed to clean up zkir dir: %v", err)
			}
		}

		// check if contract is verified
		verified := activeCompile.IsContractVerified(r.Context(), req.ContractAddr)
		if verified {
			requestError(w, r, http.StatusBadRequest, "already_verified", "Contract has verified")
			return
		}

		// compile contact
		err = activeCompile.CompileContract(ctx, req.CompilerVersion, req.ContractAddr, req.SourceCode, req.SourceType)
		if err != nil {
			cleanCompileCache()
			respondError(w, r, "compile_failed", err)
			return
		}

		// verify by verified keys
		match, err := activeCompile.CheckVerifyKeys(ctx, req.ContractAddr, req.VerificationKeys)

		if err != nil {
			cleanCompileCache()
			respondError(w, r, "verification_failed", err)
			return
		}

		// remove verified contract keys and zkir dir
		defer cleanZkirDir()

		logger.AddFields(ctx, logger.Fields{"verified_status": match.Status})
		res := VerificationResponse{VerifiedStatus: match.Status, Message: match.Message(), Uncovered: match.Uncovered}
		if match.Status != perfect && match.Status != partial {
			cleanCompileCache()
			respondError(w, r, "invalid_verification_result", fmt.Errorf("unexpected verification status %q", match.Status))
			return
		}
		// move compiled contract to static dir

		contractInfo, err := activeCompile.ContractABIInfo(ctx, req.ContractAddr)
		if err != nil {
			requestWarning(r, "read_abi", err)
		}

		// read ABI & CMA & Viewer
		res.ABI = contractInfo
		res.Viewer, err = compile.ParseCompactContract(req.SourceType, req.SourceCode)
		if err != nil {
			requestWarning(r, "parse_viewer", err)
		}

		// Read public state for CMA; compilation and key matching already succeeded.
		state, err := activeCompile.ReadContractCMA(ctx, req.ContractAddr, "")
		if err != nil {
			requestWarning(r, "read_cma", err)
		} else if state != nil {
			res.CMA = state.MaintenanceAuthority.Committee.Values()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(res)
	}
}

func stateHandler(c compile.ICompile) http.HandlerFunc {
	limits := handlerLimits(c)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			requestError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Only GET method is allowed")
			return
		}

		if !limits.StateRequests.TryAcquire() {
			respondBusy(w, r)
			return
		}
		defer limits.StateRequests.Release()
		q := r.URL.Query()
		view := q.Get("view")
		if view != "" && view != "cma" {
			requestError(w, r, http.StatusBadRequest, "invalid_view", "Invalid view")
			return
		}

		contractAddr := q.Get("contract_addr")
		blockHash := q.Get("block_hash")
		if blockHash == "" {
			blockHash = q.Get("hash")
		}
		if blockHash != "" && !validBlockHash.MatchString(blockHash) {
			requestError(w, r, http.StatusBadRequest, "invalid_block_hash", "Invalid block hash")
			return
		}
		networkID := strings.TrimSpace(q.Get("networkid"))
		if networkID == "" {
			networkID = strings.TrimSpace(q.Get("network_id"))
		}
		if contractAddr == "" {
			requestError(w, r, http.StatusBadRequest, "missing_contract", "Missing contract_addr parameter")
			return
		}
		activeCompile, err := resolveCompileByNetwork(r.Context(), c, networkID)
		if err != nil {
			requestError(w, r, http.StatusBadRequest, "invalid_network", err.Error())
			return
		}

		logNetwork(r, activeCompile)
		// check if contract is verified
		verified := activeCompile.IsContractVerified(r.Context(), contractAddr)
		if !verified || view == "cma" {
			logger.AddFields(r.Context(), logger.Fields{"state_mode": "cma"})
			// if contract not verified, return cma info
			cma, err := activeCompile.ReadContractCMA(r.Context(), contractAddr, blockHash)
			if err != nil {
				respondStateError(w, r, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(cma)
			return
		}
		logger.AddFields(r.Context(), logger.Fields{"state_mode": "ledger"})
		state, err := activeCompile.ReadContract(r.Context(), contractAddr, blockHash)
		if err != nil {
			respondStateError(w, r, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(state)
	}
}

// respondStateError separates missing snapshots, invalid requests and provider failures.
// Provider URLs and free-form child-process messages must not reach clients.
func respondStateError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := http.StatusInternalServerError, "state_read_failed", "Failed to read contract state"
	var reader *compile.StateReadError
	if errors.Is(err, context.DeadlineExceeded) {
		status, code, message = http.StatusGatewayTimeout, "deadline_exceeded", "Contract state request timed out"
	} else if errors.As(err, &reader) {
		code = reader.Code
		switch code {
		case "private_witness_required", "unsupported_runtime", "public_tree_unavailable", "invalid_merkle_leaf", "invalid_merkle_proof", "merkle_root_unavailable", "historical_proof_required", "deployment_not_found", "transaction_snapshot_unavailable":
			status, message = http.StatusUnprocessableEntity, "Public circuit or transaction snapshot unavailable"
		case "state_not_found", "rpc_block_not_found":
			status, message = http.StatusNotFound, "Contract or block not found at the requested snapshot"
		case "rpc_invalid_request", "rpc_config_invalid":
			status, message = http.StatusBadRequest, "Invalid contract state request or RPC configuration"
		case "deadline_exceeded":
			status, message = http.StatusGatewayTimeout, "Contract state request timed out"
		case "state_read_failed":
			// Unclassified reader failures can be internal bugs, not provider failures.
		default:
			status, message = http.StatusBadGateway, "Contract state provider could not supply a valid snapshot"
		}
	}
	requestFailed(r, code, err)
	http.Error(w, message, status)
}

// References include waiters so an entry cannot be replaced while still in use.
type contractLock struct {
	gate *compile.Gate
	refs int
}
type contractLocks struct {
	mu      sync.Mutex
	entries map[string]*contractLock
}

func (l *contractLocks) acquire(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	entry := l.entries[key]
	if entry == nil {
		entry = &contractLock{gate: compile.NewGate(1)}
		l.entries[key] = entry
	}
	entry.refs++
	l.mu.Unlock()
	drop := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.entries, key)
		}
	}
	if err := entry.gate.Acquire(ctx); err != nil {
		drop()
		return nil, err
	}
	return func() { entry.gate.Release(); drop() }, nil
}

func handlerLimits(c compile.ICompile) *compile.Runtime {
	if conf, ok := c.(*compile.Config); ok {
		return conf.RuntimeLimits()
	}
	return compile.NewRuntime()
}
func respondBusy(w http.ResponseWriter, r *http.Request) {
	requestFailed(r, "service_busy", nil)
	w.Header().Set("Retry-After", "1")
	http.Error(w, "Service busy; retry later", http.StatusServiceUnavailable)
}

func compileNetworkID(c *compile.Config) config.NetworkID {
	if c.NetworkConf == nil {
		return config.Preview
	}
	return c.NetworkConf.GetNetworkID()
}

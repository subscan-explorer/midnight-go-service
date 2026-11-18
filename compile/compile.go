package compile

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"midnight-go-sdk/config"
	"midnight-go-sdk/logger"
	"midnight-go-sdk/network"
	"midnight-go-sdk/utils"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var CompactVersions = []string{
	"0.22.0",
	"0.23.0",
	"0.24.0",
	"0.25.0",
	"0.26.0",
	"0.28.0",
	"0.29.0",
	"0.30.0",
	"0.31.0",
	"0.31.1",
	"0.35.0",
	"0.36.0",
}

type SourceType string

const (
	SingleFile SourceType = "single_file"
	MultiFile  SourceType = "multi_file"
)

// error

var (
	ErrSystemFail = errors.New("system error, please try again later")
)

type ICompile interface {
	CompileContract(_ context.Context, compilerVersion string, contractAddr, sourceCode string, sourceType SourceType) error
	// MoveCompiledContract(contractTempDir string, dest string) error
	CheckVerifyKeys(ctx context.Context, ContractAddr string, VerificationKeys map[string]string) (VerifyKeyResult, error)
	ContractABIInfo(_ context.Context, ContractAddr string) (map[string]interface{}, error)
	Generate(ctx context.Context, contractAddress, ContractImportPath, destPath string) error
	GenerateContractAndReadState(ctx context.Context, contractAddress string) (*ContractStateResponse, error)
	IsContractVerified(ctx context.Context, contractAddr string) bool
	ReadContract(ctx context.Context, contractAddress, hash string) (*ContractStateResponse, error)
	ReadContractCMA(ctx context.Context, contractAddress, hash string) (*ContractStateResponse, error)

	GetClient() *network.Client
	GetStaticDir() string
	GetTempDir() string
}

type Config struct {
	CompilerOptions CompilerOptions
	ArtifactDir     string // Empty preserves historical on-disk paths.
	StaticDir       string
	TempDir         string
	CompilerVersion string

	Client      *network.Client
	NetworkConf config.Config
	// Runtime is shared by per-request copies of Config. Nil uses the process default.
	Runtime *Runtime
}

func (c *Config) GetStaticDir() string {
	if c.ArtifactDir != "" {
		return c.ArtifactDir
	}
	return c.StaticDir
}

func (c *Config) GetTempDir() string {
	return c.TempDir
}

func (c *Config) GetClient() *network.Client {
	return c.Client
}

func NewCompileConfig() ICompile {
	return &Config{}
}

type StandardInputJson struct {
	Contracts map[string]string `json:"contracts"`
	EntryFile string            `json:"entry-file"`
}

// CompileContract compile compact contract source code
// 1. compile source code to tmp dir
// 2. verify by verified keys
// 3. if verified, save to static dir
// 4. return verified status
func (c *Config) CompileContract(ctx context.Context, compilerVersion string, contractAddr, sourceCode string, sourceType SourceType) (resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	networkID := config.Preview
	if c.NetworkConf != nil {
		networkID = c.NetworkConf.GetNetworkID()
	}
	if err := ValidateCompilerOptions(networkID, compilerVersion, c.CompilerOptions); err != nil {
		return err
	}
	rt := c.runtime()
	compileCtx := ctx
	op := logger.Queued(ctx, "compile")
	logFields := logger.Fields{}
	defer func() {
		op.Finished(resultErr, logFields)
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := rt.CompileGate.Acquire(ctx); err != nil {
		return err
	}
	defer rt.CompileGate.Release()
	op.Started()

	if rt.CompileTimeout > 0 {
		var cancel context.CancelFunc
		compileCtx, cancel = context.WithTimeout(ctx, rt.CompileTimeout)
		defer cancel()
	}

	// create temp file to save source code
	var (
		tempFile *os.File
		err      error
	)
	switch sourceType {
	case MultiFile:
		stdInput, err := parseStandardInputJSON(sourceCode)
		if err != nil {
			return fmt.Errorf("file is not valid standard-input-json format: %w", err)
		}
		// create temp dir
		tempDir, err := os.MkdirTemp(c.TempDir, fmt.Sprintf("%s-*.compact", contractAddr))
		if err != nil {
			logger.Logger().Error(fmt.Errorf("create temp %s dir fail", contractAddr))
			return ErrSystemFail
		}
		defer os.RemoveAll(tempDir)
		// write all files to temp dir
		// create sub dirs if necessary
		for filePath, fileContent := range stdInput.Contracts {
			fullPath := filepath.Join(tempDir, filePath)
			dir := filepath.Dir(fullPath)
			if err = os.MkdirAll(dir, os.ModePerm); err != nil {
				logger.Logger().Error(fmt.Errorf("create sub dir %s fail: %v", dir, err))
				return ErrSystemFail
			}
			if err = os.WriteFile(fullPath, []byte(fileContent), 0644); err != nil {
				logger.Logger().Error(fmt.Errorf("write file %s fail: %v", fullPath, err))
				return ErrSystemFail
			}
			if filePath == stdInput.EntryFile {
				tempFile, err = os.Open(fullPath)
				if err != nil {
					logger.Logger().Error(fmt.Errorf("open entry file %s fail: %v", fullPath, err))
					return ErrSystemFail
				}
				defer tempFile.Close()
			}
		}
	case SingleFile:
		tempFile, err = os.CreateTemp(c.TempDir, fmt.Sprintf("%s-*.compact", contractAddr))

		if err != nil {
			return fmt.Errorf("create temp file fail: %v", err)
		}
		defer os.Remove(tempFile.Name())
		defer tempFile.Close()

		if _, err = io.WriteString(tempFile, sourceCode); err != nil {
			return fmt.Errorf("write source code to temp file fail: %v", err)
		}
	}
	destDir := fmt.Sprintf("%s/verified/%s/src/managed", c.GetStaticDir(), contractAddr)
	cmd := c.buildCompileCommand(compileCtx, tempFile, destDir, compilerVersion)
	stdoutBuf := &cappedBuffer{max: rt.OutputLimit}
	stderrBuf := &cappedBuffer{max: rt.OutputLimit}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf
	stdinPipe, err := cmd.StdinPipe()

	if err != nil {
		return fmt.Errorf("create pipe fail: %v", err)
	}

	if err = cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(compileCtx.Err(), context.DeadlineExceeded) {
			logFields["error_code"] = "deadline_exceeded"
			return fmt.Errorf("compile timed out after %s", rt.CompileTimeout)
		}
		return fmt.Errorf("start cmd fail %v", err)
	}
	rt.track(cmd)
	defer rt.untrack(cmd.Process.Pid)

	_ = stdinPipe.Close()

	if err = cmd.Wait(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			logFields["exit_code"] = exit.ExitCode()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(compileCtx.Err(), context.DeadlineExceeded) {
			logFields["error_code"] = "deadline_exceeded"
			return fmt.Errorf("compile timed out after %s", rt.CompileTimeout)
		}
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			return err
		}
		logFields["error_code"] = "subprocess_failed"
		return errors.New(msg)
	}
	digest := sha256.Sum256([]byte(sourceCode))
	manifest := struct {
		Compiler     string           `json:"compiler_version"`
		Options      CompilerOptions  `json:"compiler_options"`
		SourceSHA256 string           `json:"source_sha256"`
		Network      config.NetworkID `json:"network_id"`
	}{compilerVersion, c.CompilerOptions, fmt.Sprintf("%x", digest), networkID}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode compilation manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(c.GetStaticDir(), "verified", contractAddr, "manifest.json"), encoded, 0644); err != nil {
		return fmt.Errorf("write compilation manifest: %w", err)
	}
	return nil
}

// func (c *Config) MoveCompiledContract(contractTempDir string, dest string) error {
// 	// move all files from contractTempDir to dest,including sub dirs
// 	// if dest not exist, create it
//
// 	if err := os.Rename(contractTempDir, dest); err != nil {
// 		return fmt.Errorf("move dir from %s to %s fail: %v", contractTempDir, dest, err)
// 	}
// 	defer os.Remove(contractTempDir)
// 	return nil
// }

const (
	// VerifyStatusPerfect means every on-chain circuit matches the compiled source.
	VerifyStatusPerfect = "perfect"
	// VerifyStatusPartial means every compiled circuit matches, and the chain has additional circuits.
	VerifyStatusPartial = "partial"
)

// VerifyKeyResult is the circuit-set comparison against the caller-supplied on-chain key fingerprints.
type VerifyKeyResult struct {
	Status    string
	Uncovered []string
}

// Message describes a partial match. A perfect match has an empty message.
func (r VerifyKeyResult) Message() string {
	if r.Status != VerifyStatusPartial || len(r.Uncovered) == 0 {
		return ""
	}
	return fmt.Sprintf("partial verification: remaining on-chain circuits: %s", strings.Join(r.Uncovered, ", "))
}

func (c *Config) CheckVerifyKeys(ctx context.Context, ContractAddr string, VerificationKeys map[string]string) (VerifyKeyResult, error) {
	// checkout scriptPath keys files
	scriptPath := fmt.Sprintf("%s/verified/%s", c.GetStaticDir(), ContractAddr)
	keysPath := fmt.Sprintf("%s/src/managed/keys", scriptPath)
	// read all files in keysPath
	dirs, err := os.ReadDir(keysPath)

	if err != nil {
		logger.Logger().Error(fmt.Errorf("read keys dir %s fail: %v", keysPath, err))
		return VerifyKeyResult{}, ErrSystemFail
	}

	compiled := make(map[string]struct{})
	var circuits []string
	for _, v := range dirs {
		name := v.Name()
		if strings.HasSuffix(name, ".verifier") {
			circuit := strings.TrimSuffix(name, ".verifier")
			circuits = append(circuits, circuit)
			compiled[circuit] = struct{}{}
		}
	}
	if len(circuits) == 0 {
		return VerifyKeyResult{}, fmt.Errorf("no verifier keys found in %s", keysPath)
	}

	for _, circuit := range circuits {
		verifyKey, err := utils.ReadFileWithContext(ctx, filepath.Join(keysPath, circuit+".verifier"))
		if err != nil {
			return VerifyKeyResult{}, fmt.Errorf("get verify keys fail")
		}
		// A compiled circuit that is missing or different on chain is a hard mismatch.
		// Extra on-chain circuits are reported only after every compiled circuit matches.
		if fmt.Sprintf("%x", md5.Sum(verifyKey)) != VerificationKeys[circuit] {
			return VerifyKeyResult{}, fmt.Errorf("verify key mismatch for circuit %s", circuit)
		}
	}

	var uncovered []string
	for circuit := range VerificationKeys {
		if _, ok := compiled[circuit]; !ok {
			uncovered = append(uncovered, circuit)
		}
	}
	sort.Strings(uncovered)
	if len(uncovered) > 0 {
		return VerifyKeyResult{Status: VerifyStatusPartial, Uncovered: uncovered}, nil
	}
	return VerifyKeyResult{Status: VerifyStatusPerfect}, nil
}

func (c *Config) ContractABIInfo(_ context.Context, ContractAddr string) (map[string]interface{}, error) {
	scriptPath := fmt.Sprintf("%s/verified/%s", c.GetStaticDir(), ContractAddr)
	complerPath := fmt.Sprintf("%s/src/managed/compiler/contract-info.json", scriptPath)
	data, err := os.ReadFile(complerPath)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	return result, json.Unmarshal(data, &result)
}

// GenerateContractAndReadState reads public state without changing compiled contract code.
func (c *Config) GenerateContractAndReadState(ctx context.Context, contractAddress string) (*ContractStateResponse, error) {
	return c.ReadContract(ctx, contractAddress, "")
}

// ReadContract reads the ledger at the requested block using its compiled runtime.
func (c *Config) ReadContract(ctx context.Context, contractAddress string, hash string) (*ContractStateResponse, error) {
	contractPath, err := filepath.Abs(filepath.Join(c.GetStaticDir(), "verified", contractAddress, "src", "managed", "contract", "index.js"))
	if err != nil {
		return nil, fmt.Errorf("resolve compiled contract: %w", err)
	}
	output, err := c.execTsBytes(ctx, filepath.Join(c.StaticDir, "read-state.mjs"), utils.TrimHex(utils.ConvertWithNetworkIdAccount(contractAddress)), hash, contractPath)
	if err != nil {
		return nil, fmt.Errorf("read contract state: %w", err)
	}
	var state ContractStateResponse
	if err := json.Unmarshal(output, &state); err != nil {
		return nil, fmt.Errorf("decode contract state reader output: %w", err)
	}
	return &state, nil
}

// ReadContractCMA reads maintenance authority and verifier keys at the requested block.
func (c *Config) ReadContractCMA(ctx context.Context, contractAddress, hash string) (*ContractStateResponse, error) {
	output, err := c.execTsBytes(ctx, filepath.Join(c.StaticDir, "read-state.mjs"), utils.TrimHex(utils.ConvertWithNetworkIdAccount(contractAddress)), hash)
	if err != nil {
		return nil, fmt.Errorf("read contract state: %w", err)
	}
	var state ContractStateResponse
	if err := json.Unmarshal(output, &state); err != nil {
		return nil, fmt.Errorf("decode contract state reader output: %w", err)
	}
	return &state, nil
}

func (c *Config) IsContractVerified(_ context.Context, contractAddr string) bool {
	// check if contractAddr dir exist in staticDir/verified
	verifiedPath := filepath.Join(c.GetStaticDir(), "verified", contractAddr)
	info, err := os.Stat(verifiedPath)
	if os.IsNotExist(err) {
		return false
	}
	return info.IsDir()
}

func (c *Config) buildCompileCommand(ctx context.Context, tempFile *os.File, destDir, compilerVersion string) *exec.Cmd {
	var compactBin = os.Getenv("COMPACT_BIN")
	if compactBin == "" {
		compactBin = "compact"
	}
	cmdArgs := []string{
		"compile",                           // compile command
		fmt.Sprintf("+%s", compilerVersion), // compiler version
	}
	if c.CompilerOptions.FeatureZKIRV3 {
		cmdArgs = append(cmdArgs, "--feature-zkir-v3")
	}
	// source-pathname &  target-directory-pathname
	cmdArgs = append(cmdArgs, tempFile.Name(), destDir)
	return commandContext(ctx, compactBin, cmdArgs...)
}

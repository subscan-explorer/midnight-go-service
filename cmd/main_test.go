package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"midnight-go-sdk/compile"
	"midnight-go-sdk/network"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"midnight-go-sdk/types"
)

// MockMidnightClient is a mock implementation of the client needed by ICompile.
type MockMidnightClient struct {
	mock.Mock
}

func (m *MockMidnightClient) GetContractState(ctx context.Context, contractAddr types.ContractAddress) (*types.StateValue, error) {
	args := m.Called(ctx, contractAddr)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.StateValue), args.Error(1)
}

// MockICompile is a mock implementation of compile.ICompile for testing.
type MockICompile struct {
	mock.Mock
}

func (m *MockICompile) ReadContractCMA(ctx context.Context, contractAddress, hash string) (*compile.ContractStateResponse, error) {
	args := m.Called(ctx, contractAddress, hash)
	return args.Get(0).(*compile.ContractStateResponse), args.Error(1)
}

func (m *MockICompile) Generate(ctx context.Context, contractAddress, ContractImportPath, destPath string) error {
	args := m.Called(ctx, contractAddress, ContractImportPath, destPath)
	return args.Error(0)
}

func (m *MockICompile) GenerateContractAndReadState(ctx context.Context, contractAddress string) (*compile.ContractStateResponse, error) {
	args := m.Called(ctx, contractAddress)
	return args.Get(0).(*compile.ContractStateResponse), args.Error(1)
}

func (m *MockICompile) IsContractVerified(ctx context.Context, contractAddr string) bool {
	args := m.Called(ctx, contractAddr)
	return args.Bool(0)
}

func (m *MockICompile) ReadContract(ctx context.Context, contractAddress, hash string) (*compile.ContractStateResponse, error) {
	args := m.Called(ctx, contractAddress, hash)
	return args.Get(0).(*compile.ContractStateResponse), args.Error(1)
}

func (m *MockICompile) ContractABIInfo(ctx context.Context, ContractAddr string) (map[string]interface{}, error) {
	args := m.Called(ctx, ContractAddr)
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockICompile) CompileContract(ctx context.Context, compilerVersion, contractAddr, sourceCode string, sourceType compile.SourceType) error {
	args := m.Called(ctx, compilerVersion, contractAddr, sourceCode, sourceType)
	return args.Error(0)
}

func (m *MockICompile) CheckVerifyKeys(ctx context.Context, contractAddr string, stateValue map[string]string) (compile.VerifyKeyResult, error) {
	args := m.Called(ctx, contractAddr, stateValue)
	result, _ := args.Get(0).(compile.VerifyKeyResult)
	return result, args.Error(1)
}

func (m *MockICompile) MoveCompiledContract(src, dest string) error {
	args := m.Called(src, dest)
	return args.Error(0)
}

func (m *MockICompile) GetClient() *network.Client {
	return network.NewClient("")
}

func (m *MockICompile) GetStaticDir() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockICompile) GetTempDir() string {
	args := m.Called()
	return args.String(0)
}

func TestCompileHandler(t *testing.T) {
	tests := []struct {
		name               string
		method             string
		body               interface{}
		setupMock          func(m *MockICompile, mc *MockMidnightClient)
		expectedStatus     int
		expectedResponse   VerificationResponse
		expectBodyContains string
	}{
		{
			name:   "Successful verification",
			method: http.MethodPost,
			body:   CompileRequest{ContractAddr: "addr1", SourceCode: "code", CompilerVersion: "v1", SourceType: compile.SingleFile, VerificationKeys: map[string]string{"key1": "value1"}},
			setupMock: func(m *MockICompile, mc *MockMidnightClient) {
				m.On("CompileContract", mock.Anything, "v1", "addr1", "code", compile.SingleFile).Return(nil)
				m.On("GetClient").Return(mock.Anything)
				m.On("IsContractVerified", mock.Anything, "addr1").Return(false)
				mc.On("GetContractState", mock.Anything, types.ContractAddress("addr1")).Return("", nil)
				m.On("CheckVerifyKeys", mock.Anything, "addr1", mock.Anything).Return(compile.VerifyKeyResult{Status: compile.VerifyStatusPerfect}, nil)
				m.On("GetStaticDir").Return("static")
				m.On("ContractABIInfo", mock.Anything, "addr1").Return(map[string]interface{}{"abi": "data"}, nil)
				m.On("ReadContractCMA", mock.Anything, "addr1", "").Return(&compile.ContractStateResponse{}, nil)
				m.On("GetTempDir").Return("tmp")
				m.On("MoveCompiledContract", "tmp/addr1", "static/verified/addr1").Return(nil)
			},
			expectedStatus:   http.StatusOK,
			expectedResponse: VerificationResponse{VerifiedStatus: perfect, ABI: map[string]interface{}{"abi": "data"}, Viewer: nil, CMA: nil},
		},
		{
			name:   "Partial verification",
			method: http.MethodPost,
			body:   CompileRequest{ContractAddr: "addr1", SourceCode: "code", CompilerVersion: "v1", SourceType: compile.SingleFile, VerificationKeys: map[string]string{"key1": "value1"}},
			setupMock: func(m *MockICompile, mc *MockMidnightClient) {
				m.On("CompileContract", mock.Anything, "v1", "addr1", "code", compile.SingleFile).Return(nil)
				m.On("IsContractVerified", mock.Anything, "addr1").Return(false)
				m.On("CheckVerifyKeys", mock.Anything, "addr1", mock.Anything).Return(compile.VerifyKeyResult{Status: compile.VerifyStatusPartial, Uncovered: []string{"hidden"}}, nil)
				m.On("GetStaticDir").Return("static")
				m.On("GetTempDir").Return("tmp")
				m.On("ContractABIInfo", mock.Anything, "addr1").Return(map[string]interface{}{"abi": "data"}, nil)
				m.On("ReadContractCMA", mock.Anything, "addr1", "").Return(&compile.ContractStateResponse{}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedResponse: VerificationResponse{
				VerifiedStatus: partial,
				Message:        "partial verification: remaining on-chain circuits: hidden",
				Uncovered:      []string{"hidden"},
				ABI:            map[string]interface{}{"abi": "data"},
			},
		},
		{
			name:   "Invalid networkid",
			method: http.MethodPost,
			body: CompileRequest{
				ContractAddr:     "addr1",
				SourceCode:       "code",
				CompilerVersion:  "v1",
				SourceType:       compile.SingleFile,
				VerificationKeys: map[string]string{"key1": "value1"},
				NetworkID:        "unknown-network",
			},
			setupMock:        func(m *MockICompile, mc *MockMidnightClient) {},
			expectedStatus:   http.StatusOK,
			expectedResponse: VerificationResponse{VerifiedStatus: mismatch, Message: "resolve network: unsupported networkid: unknown-network"},
		},
		{
			name:   "CMA read failure still returns perfect",
			method: http.MethodPost,
			body:   CompileRequest{ContractAddr: "addr1", SourceCode: "code", CompilerVersion: "v1", SourceType: compile.SingleFile, VerificationKeys: map[string]string{"key1": "value1"}},
			setupMock: func(m *MockICompile, mc *MockMidnightClient) {
				m.On("CompileContract", mock.Anything, "v1", "addr1", "code", compile.SingleFile).Return(nil)
				m.On("IsContractVerified", mock.Anything, "addr1").Return(false)
				m.On("CheckVerifyKeys", mock.Anything, "addr1", mock.Anything).Return(compile.VerifyKeyResult{Status: compile.VerifyStatusPerfect}, nil)
				m.On("GetStaticDir").Return("static")
				m.On("GetTempDir").Return("tmp")
				m.On("ContractABIInfo", mock.Anything, "addr1").Return(map[string]interface{}{"abi": "data"}, nil)
				m.On("ReadContractCMA", mock.Anything, "addr1", "").Return((*compile.ContractStateResponse)(nil), errors.New("ts failed"))
			},
			expectedStatus:   http.StatusOK,
			expectedResponse: VerificationResponse{VerifiedStatus: perfect, ABI: map[string]interface{}{"abi": "data"}, Viewer: nil, CMA: nil},
		},
		{
			name:   "Mismatch verification",
			method: http.MethodPost,
			body:   CompileRequest{ContractAddr: "addr1", SourceCode: "code", CompilerVersion: "v1", SourceType: compile.SingleFile, VerificationKeys: map[string]string{"key1": "value1"}},
			setupMock: func(m *MockICompile, mc *MockMidnightClient) {
				tempDir, _ := os.MkdirTemp("", "test")
				t.Cleanup(func() { os.RemoveAll(tempDir) })
				// Create a dummy file/dir to be removed
				_ = os.MkdirAll(fmt.Sprintf("%s/addr1", tempDir), 0755)
				m.On("IsContractVerified", mock.Anything, "addr1").Return(false)
				m.On("CompileContract", mock.Anything, "v1", "addr1", "code", compile.SingleFile).Return(nil)
				m.On("GetClient").Return(mc)
				m.On("GetStaticDir").Return("static")
				mc.On("GetContractState", mock.Anything, types.ContractAddress("addr1")).Return("", nil)
				m.On("CheckVerifyKeys", mock.Anything, "addr1", mock.Anything).Return(compile.VerifyKeyResult{}, errors.New("verification keys do not match"))
				m.On("GetTempDir").Return(tempDir)
			},
			expectedStatus:   http.StatusOK,
			expectedResponse: VerificationResponse{VerifiedStatus: mismatch, Message: "verification keys do not match"},
		},
		{
			name:           "Invalid method",
			method:         http.MethodGet,
			body:           nil,
			setupMock:      func(m *MockICompile, mc *MockMidnightClient) {},
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:               "Invalid request body",
			method:             http.MethodPost,
			body:               "invalid json",
			setupMock:          func(m *MockICompile, mc *MockMidnightClient) {},
			expectedStatus:     http.StatusBadRequest,
			expectBodyContains: "Invalid request body",
		},
		{
			name:             "Missing required fields",
			method:           http.MethodPost,
			body:             CompileRequest{ContractAddr: "addr1"},
			setupMock:        func(m *MockICompile, mc *MockMidnightClient) {},
			expectedStatus:   http.StatusOK,
			expectedResponse: VerificationResponse{VerifiedStatus: mismatch, Message: "missing required fields"},
		},
		{
			name:   "CompileContract error",
			method: http.MethodPost,
			body:   CompileRequest{ContractAddr: "addr1", SourceCode: "code", CompilerVersion: "v1", SourceType: compile.SingleFile, VerificationKeys: map[string]string{"key1": "value1"}},
			setupMock: func(m *MockICompile, mc *MockMidnightClient) {
				tempDir, _ := os.MkdirTemp("", "test")
				t.Cleanup(func() { os.RemoveAll(tempDir) })
				m.On("IsContractVerified", mock.Anything, "addr1").Return(false)
				m.On("GetTempDir").Return(tempDir)
				m.On("GetStaticDir").Return("static")
				m.On("CompileContract", mock.Anything, "v1", "addr1", "code", compile.SingleFile).Return(errors.New("compile error"))
			},
			expectedStatus:   http.StatusOK,
			expectedResponse: VerificationResponse{VerifiedStatus: mismatch, Message: "compile error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCompile := new(MockICompile)
			mockClient := new(MockMidnightClient)
			if tt.setupMock != nil {
				tt.setupMock(mockCompile, mockClient)
			}

			var reqBody []byte
			if tt.body != nil {
				if s, ok := tt.body.(string); ok {
					reqBody = []byte(s)
				} else {
					reqBody, _ = json.Marshal(tt.body)
				}
			}

			req := httptest.NewRequest(tt.method, "/verify", bytes.NewBuffer(reqBody))
			rr := httptest.NewRecorder()

			handler := compileHandler(mockCompile)
			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code, "handler returned wrong status code")

			if tt.expectBodyContains != "" {
				assert.Contains(t, rr.Body.String(), tt.expectBodyContains, "handler returned unexpected body")
				return
			}

			if rr.Header().Get("Content-Type") == "application/json" && (tt.expectedResponse.VerifiedStatus != "" || tt.expectedResponse.Message != "") {
				var resp VerificationResponse
				err := json.NewDecoder(rr.Body).Decode(&resp)
				assert.NoError(t, err, "could not decode response")
				assert.Equal(t, tt.expectedResponse, resp, "handler returned unexpected body")
			}

		})
	}
}

func TestStateHandler_InvalidNetworkID(t *testing.T) {
	mockCompile := new(MockICompile)
	req := httptest.NewRequest(http.MethodGet, "/contract/state?contract_addr=addr1&networkid=bad-network", nil)
	rr := httptest.NewRecorder()

	handler := stateHandler(mockCompile)
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "unsupported networkid: bad-network")
}

func TestStateHandlerHistoricalHashAliases(t *testing.T) {
	for _, key := range []string{"hash", "block_hash"} {
		t.Run(key, func(t *testing.T) {
			m := new(MockICompile)
			hash := "0x" + strings.Repeat("a", 64)
			m.On("IsContractVerified", mock.Anything, "addr").Return(false)
			m.On("ReadContractCMA", mock.Anything, "addr", hash).Return(&compile.ContractStateResponse{}, nil)
			req := httptest.NewRequest(http.MethodGet, "/contract/state?contract_addr=addr&"+key+"="+hash, nil)
			recorder := httptest.NewRecorder()
			stateHandler(m)(recorder, req)
			assert.Equal(t, http.StatusOK, recorder.Code)
			m.AssertExpectations(t)
		})
	}
}

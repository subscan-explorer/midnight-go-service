package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"midnight-go-sdk/utils"
	"net/http"
	"time"

	"midnight-go-sdk/types"
)

// Client represents a network client for interacting with Midnight services
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient creates a new network client
func NewClient(baseURL string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: baseURL,
	}
}

type JSONRPCRequest struct {
	Id      int    `json:"id"`
	Jsonrpc string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// GetContractState fetches the state of a contract
func (c *Client) GetContractState(ctx context.Context, contractAddress types.ContractAddress) (string, error) {
	requestParams := JSONRPCRequest{
		Id:      1,
		Jsonrpc: "2.0",
		Method:  "state_call",
		Params:  []string{"MidnightRuntimeApi_get_contract_state", fmt.Sprintf("0x8c%s", utils.TrimHex(string(contractAddress)))}, // 8c is the length of address scale encoded
	}
	paramsBytes, _ := json.Marshal(requestParams)
	fmt.Println(string(paramsBytes))
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewReader(paramsBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	var state types.JsonRPCResponse
	if err = json.Unmarshal(body, &state); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	stateValue, ok := state.Result.(string)
	if !ok {
		return "", fmt.Errorf("invalid response format")
	}
	if len(stateValue) < 4 || stateValue[:2] != "0x" {
		return "", fmt.Errorf("invalid state value format")
	}

	resultPrefix := stateValue[2:4]
	if resultPrefix == "00" { // result is Ok
		return stateValue[4:], nil // scale byte type
	}
	return "", fmt.Errorf("contract not found or not a valid contract address")
}

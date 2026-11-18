package network

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"midnight-go-sdk/types"
)

func TestNewClient(t *testing.T) {
	client := NewClient("http://localhost:8080")
	assert.NotNil(t, client)
	assert.NotNil(t, client.httpClient)
	assert.Equal(t, "http://localhost:8080", client.baseURL)
}

func TestGetContractState(t *testing.T) {
	rpcURL := os.Getenv("MIDNIGHT_RPC_URL")
	contractAddr := os.Getenv("MIDNIGHT_CONTRACT_ADDRESS")
	if rpcURL == "" || contractAddr == "" {
		t.Skip("set MIDNIGHT_RPC_URL and MIDNIGHT_CONTRACT_ADDRESS to run this integration test against the Preview network")
	}

	client := NewClient(rpcURL)

	ctx := context.Background()
	state, err := client.GetContractState(ctx, types.ContractAddress(contractAddr))

	assert.NoError(t, err)
	assert.NotNil(t, state)
	fmt.Println(state)
}

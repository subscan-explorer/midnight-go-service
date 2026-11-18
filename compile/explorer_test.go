package compile

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExplorerRequestBoundaries(t *testing.T) {
	request := ExplorerRequest{Contract: "0x" + strings.Repeat("ab", 32), Operation: "inspect"}
	require.NoError(t, request.Validate())
	request.Contract = "../../keys"
	require.Error(t, request.Validate())
	request.Contract = "0x" + strings.Repeat("ab", 32)
	request.Operation = "prepare"
	request.Circuit = "set"
	request.Arguments = json.RawMessage(`["18446744073709551615"]`)
	request.CoinPublicKey = strings.Repeat("a", 64)
	request.EncryptionPublicKey = strings.Repeat("b", 64)
	require.NoError(t, request.Validate())
	request.Circuit = "../private"
	require.Error(t, request.Validate())
	request.Circuit = "set"
	request.Arguments = make([]byte, 32769)
	require.Error(t, request.Validate())
	request.Operation = "artifact"
	request.Artifact = "source"
	require.Error(t, request.Validate())
	request.Artifact = "prover"
	require.NoError(t, request.Validate())
	request.Operation = "merkle"
	request.Field = "tree"
	request.Leaf = json.RawMessage(`"42"`)
	request.LeafIndex = "4294967295"
	require.NoError(t, request.Validate())
	request.LeafIndex = "4294967296"
	require.Error(t, request.Validate())
	request.LeafIndex = "-1"
	require.Error(t, request.Validate())
	request.LeafIndex = "0"
	request.Leaf = make([]byte, 32769)
	require.Error(t, request.Validate())
	request.Operation = "deployment"
	require.Error(t, request.Validate())
	request.TransactionHash = "0x" + strings.Repeat("cd", 32)
	require.NoError(t, request.Validate())
}

func TestExplorerRefusesMissingArtifactsBeforeStartingReader(t *testing.T) {
	conf := &Config{StaticDir: t.TempDir()}
	_, err := conf.Explore(context.Background(), ExplorerRequest{Contract: "0x" + strings.Repeat("a", 64), Operation: "artifact", Circuit: "set", Artifact: "prover"})
	require.ErrorContains(t, err, "verified contract artifacts required")
}

func TestMerkleHashAndProofBoundaries(t *testing.T) {
	r := ExplorerRequest{Contract: "0x" + strings.Repeat("ab", 32), Operation: "merkle", Field: "tree", LeafIndex: "0", LeafHash: strings.Repeat("cd", 32), Root: "1", Proof: json.RawMessage(`{"path":[]}`)}
	require.NoError(t, r.Validate())
	r.Leaf = json.RawMessage(`"private preimage"`)
	require.ErrorContains(t, r.Validate(), "exactly one")
	r.Leaf = nil
	r.LeafHash = "ab"
	require.ErrorContains(t, r.Validate(), "leaf hash")
	r.LeafHash = strings.Repeat("cd", 32)
	r.LeafIndex = "00"
	require.ErrorContains(t, r.Validate(), "inclusion request")
	r.LeafIndex = "0"
	r.Proof = json.RawMessage(strings.Repeat(" ", 16385))
	require.ErrorContains(t, r.Validate(), "oversized")
	r.Proof = nil
	r.Root = "not-hex"
	require.ErrorContains(t, r.Validate(), "root")
}

package compile

import (
	"context"
	"crypto/md5"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckVerifyKeysCircuitSet(t *testing.T) {
	dir := t.TempDir()
	conf := &Config{StaticDir: dir}
	addr := "0xabc"
	keysDir := filepath.Join(dir, "verified", addr, "src", "managed", "keys")
	require.NoError(t, os.MkdirAll(keysDir, 0o755))

	writeKey := func(circuit string, body []byte) string {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(keysDir, circuit+".verifier"), body, 0o644))
		sum := md5.Sum(body)
		return string(hexEncode(sum[:]))
	}
	require.NoError(t, os.WriteFile(filepath.Join(keysDir, "post.prover"), []byte("prover"), 0o644))
	post := writeKey("post", []byte("post-key"))
	takeDown := writeKey("takeDown", []byte("take-down-key"))
	ctx := context.Background()

	t.Run("exact circuit set is perfect", func(t *testing.T) {
		got, err := conf.CheckVerifyKeys(ctx, addr, map[string]string{
			"post":     post,
			"takeDown": takeDown,
		})
		require.NoError(t, err)
		assert.Equal(t, VerifyKeyResult{Status: VerifyStatusPerfect}, got)
		assert.Empty(t, got.Message())
	})

	t.Run("extra on-chain circuit is partial", func(t *testing.T) {
		got, err := conf.CheckVerifyKeys(ctx, addr, map[string]string{
			"hidden":   "abcd",
			"post":     post,
			"takeDown": takeDown,
			"insert":   "ffff",
		})
		require.NoError(t, err)
		assert.Equal(t, VerifyStatusPartial, got.Status)
		assert.Equal(t, []string{"hidden", "insert"}, got.Uncovered)
		assert.Equal(t, "partial verification: remaining on-chain circuits: hidden, insert", got.Message())
	})

	t.Run("hash mismatch stays a failure when extra circuits exist", func(t *testing.T) {
		_, err := conf.CheckVerifyKeys(ctx, addr, map[string]string{
			"post":     "deadbeef",
			"takeDown": takeDown,
			"hidden":   "abcd",
		})
		assert.EqualError(t, err, "verify key mismatch for circuit post")
	})

	t.Run("compiled circuit missing on chain is a failure", func(t *testing.T) {
		_, err := conf.CheckVerifyKeys(ctx, addr, map[string]string{
			"post": post,
		})
		assert.EqualError(t, err, "verify key mismatch for circuit takeDown")
	})
}

func hexEncode(b []byte) []byte {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return out
}

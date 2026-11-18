package compile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompactVersionsIncludeLedger8(t *testing.T) {
	require.NotEmpty(t, CompactVersions)
	assert.Contains(t, CompactVersions, "0.28.0")
	assert.Contains(t, CompactVersions, "0.29.0")
	assert.Contains(t, CompactVersions, "0.30.0")
	assert.Contains(t, CompactVersions, "0.31.0")
	assert.Contains(t, CompactVersions, "0.31.1")
	assert.NotContains(t, CompactVersions, "0.34.0")
}

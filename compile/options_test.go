package compile

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"midnight-go-sdk/config"
	"os"
	"testing"
)

func TestCompilerOptions(t *testing.T) {
	for _, network := range []config.NetworkID{config.MainNet, config.Preview} {
		require.NoError(t, ValidateCompilerOptions(network, "0.36.0", CompilerOptions{}))
		require.Error(t, ValidateCompilerOptions(network, "0.36.0", CompilerOptions{FeatureZKIRV3: true}))
	}
	require.Error(t, ValidateCompilerOptions(config.Preprod, "0.36.0", CompilerOptions{}))
	require.Error(t, ValidateCompilerOptions(config.Stagenet, "0.36.0", CompilerOptions{}))
	options := CompilerOptions{FeatureZKIRV3: true}
	require.NoError(t, ValidateCompilerOptions(config.Stagenet, "0.35.0", options))
	require.Error(t, ValidateCompilerOptions(config.Preview, "0.35.0", options))
	require.Error(t, ValidateCompilerOptions(config.Stagenet, "0.31.1", options))
	require.NoError(t, ValidateCompilerOptions(config.Preview, "0.31.1", CompilerOptions{}))
	require.Error(t, json.Unmarshal([]byte(`{"flags":["--unsafe"]}`), &options))
	file, err := os.CreateTemp(t.TempDir(), "source-*.compact")
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	c := &Config{CompilerOptions: options}
	cmd := c.buildCompileCommand(context.Background(), file, "output", "0.35.0")
	require.Equal(t, []string{"compile", "+0.35.0", "--feature-zkir-v3", file.Name(), "output"}, cmd.Args[1:])
	c.CompilerOptions = CompilerOptions{}
	require.NotContains(t, c.buildCompileCommand(context.Background(), file, "output", "0.31.1").Args, "--feature-zkir-v3")
}

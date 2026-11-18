package compile

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"midnight-go-sdk/config"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCompile035Modes uses the real compiler, including key generation. The
// fingerprints come from independent CLI compilations of the checked-in source;
// this is a reproducibility check, not evidence of a chain deployment.
func TestCompile035Modes(t *testing.T) {
	if os.Getenv("RUN_COMPACT_035_TESTS") != "1" {
		t.Skip("set RUN_COMPACT_035_TESTS=1 with Compact 0.35.0 installed")
	}
	source, err := os.ReadFile("../static/fixtures/reader-035.compact")
	require.NoError(t, err)
	for _, tc := range []struct {
		name, fingerprint string
		feature           bool
	}{
		{"v2", "6eec870aac7d7e031ffcdf5113c932e1", false},
		{"v3", "6abf60563424aeafcaa23b89459f1415", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			c := &Config{StaticDir: t.TempDir(), TempDir: t.TempDir(), NetworkConf: &config.NetworkConfig{NetworkID: config.Stagenet}, CompilerOptions: CompilerOptions{FeatureZKIRV3: tc.feature}}
			require.NoError(t, c.CompileContract(ctx, "0.35.0", "fixture", string(source), SingleFile))
			result, err := c.CheckVerifyKeys(ctx, "fixture", map[string]string{"set": tc.fingerprint})
			require.NoError(t, err)
			require.Equal(t, VerifyStatusPerfect, result.Status)
			result, err = c.CheckVerifyKeys(ctx, "fixture", map[string]string{"set": tc.fingerprint, "extra": "1234"})
			require.NoError(t, err)
			require.Equal(t, VerifyStatusPartial, result.Status)
			_, err = c.CheckVerifyKeys(ctx, "fixture", map[string]string{"set": "bad"})
			require.ErrorContains(t, err, "mismatch")
			manifest, err := os.ReadFile(filepath.Join(c.StaticDir, "verified", "fixture", "manifest.json"))
			require.NoError(t, err)
			var saved struct {
				Compiler string          `json:"compiler_version"`
				Options  CompilerOptions `json:"compiler_options"`
				Digest   string          `json:"source_sha256"`
			}
			require.NoError(t, json.Unmarshal(manifest, &saved))
			require.Equal(t, "0.35.0", saved.Compiler)
			require.Equal(t, tc.feature, saved.Options.FeatureZKIRV3)
			require.Len(t, saved.Digest, 64)
		})
	}
}

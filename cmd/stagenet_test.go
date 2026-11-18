package main

import (
	"github.com/stretchr/testify/require"
	"midnight-go-sdk/compile"
	"midnight-go-sdk/config"
	"os"
	"path/filepath"
	"testing"
)

func TestStagenetArtifactIsolation(t *testing.T) {
	root := t.TempDir()
	base := &compile.Config{StaticDir: root, TempDir: filepath.Join(root, "tmp"), NetworkConf: &config.NetworkConfig{NetworkID: config.Preview}}
	t.Setenv("MIDNIGHT_STAGENET_EPOCH", "")
	_, err := resolveCompileByNetwork(t.Context(), base, "stagenet")
	require.ErrorContains(t, err, "EPOCH")
	t.Setenv("MIDNIGHT_STAGENET_EPOCH", "e1")
	stage, err := resolveCompileByNetwork(t.Context(), base, "stagenet")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "verified", ".networks", "stagenet", "e1"), stage.GetStaticDir())
	require.Equal(t, root, base.GetStaticDir())
	legacy, err := resolveCompileByNetwork(t.Context(), base, "preview")
	require.NoError(t, err)
	require.Equal(t, root, legacy.GetStaticDir())
	address := "0123456789abcdef"
	oldArtifact := filepath.Join(legacy.GetStaticDir(), "verified", address, "manifest.json")
	stageArtifact := filepath.Join(stage.GetStaticDir(), "verified", address, "manifest.json")
	for file, value := range map[string]string{oldArtifact: "legacy", stageArtifact: "stagenet-e1"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0755))
		require.NoError(t, os.WriteFile(file, []byte(value), 0644))
	}
	for file, value := range map[string]string{oldArtifact: "legacy", stageArtifact: "stagenet-e1"} {
		contents, readErr := os.ReadFile(file)
		require.NoError(t, readErr)
		require.Equal(t, value, string(contents))
	}
	t.Setenv("MIDNIGHT_STAGENET_EPOCH", "e2")
	reset, err := resolveCompileByNetwork(t.Context(), base, "stagenet")
	require.NoError(t, err)
	require.NotEqual(t, stage.GetStaticDir(), reset.GetStaticDir())
	t.Setenv("MIDNIGHT_STAGENET_EPOCH", "../preview")
	_, err = resolveCompileByNetwork(t.Context(), base, "stagenet")
	require.Error(t, err)
}

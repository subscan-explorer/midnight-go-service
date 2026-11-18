package compile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompileHelloWorld0311(t *testing.T) {
	if _, err := exec.LookPath("compact"); err != nil {
		t.Skip("compact CLI not on PATH")
	}
	list, err := exec.Command("compact", "list").CombinedOutput()
	require.NoError(t, err)
	if !strings.Contains(string(list), "0.31.1") {
		t.Skip("compact 0.31.1 is not installed")
	}

	src, err := os.ReadFile(filepath.Join("..", "e2e", "contracts", "hello-world.compact"))
	require.NoError(t, err)

	conf := InitConf()
	conf.CompilerVersion = "0.31.1"
	conf.StaticDir = t.TempDir()
	conf.TempDir = t.TempDir()

	err = conf.CompileContract(context.Background(), "0.31.1", "e2e-hello", string(src), SingleFile)
	require.NoError(t, err, "compact compile +0.31.1")

	key := filepath.Join(conf.StaticDir, "verified", "e2e-hello", "src", "managed", "keys", "storeMessage.verifier")
	_, err = os.Stat(key)
	require.NoError(t, err, "expected storeMessage.verifier after compile")
}

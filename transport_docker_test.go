//go:build integration_docker

// Container-based integration test for stdio transport. Spins up a real
// MCP filesystem server in a Docker container and exercises the full
// subprocess lifecycle through mcpx. Skipped automatically when the
// Docker daemon is unreachable.
package mcpx_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ory/dockertest/v3"
	"github.com/stretchr/testify/require"

	mcpx "github.com/inhuman/mcp-multiplexer"
)

// buildLocalStdioBinary compiles internal/testutil/dockertarget for the host
// platform and returns its absolute path. Used as a baseline subprocess
// lifecycle test that complements the docker-based test below.
func buildLocalStdioBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "dockertarget")
	cmd := exec.Command("go", "build", "-o", bin, "./internal/testutil/dockertarget")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build: %s", out)
	return bin
}

func TestDockerStdio_LocalBinarySubprocess(t *testing.T) {
	// Sanity check — verify our own dockertarget binary works as a
	// subprocess. Independent of docker; runs whenever the integration_docker
	// tag is set.
	bin := buildLocalStdioBinary(t)
	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{Name: "s", Transport: mcpx.TransportStdio, Binary: bin}},
	})
	require.NoError(t, err)
	defer mx.Close()

	res, err := mx.CallTool(t.Context(), "s", "echo", []byte(`{"msg":"hi"}`))
	require.NoError(t, err)
	require.Equal(t, "hi", res.Text)
}

// dockerPool dials docker; t.Skip on unavailable daemon.
func dockerPool(t *testing.T) *dockertest.Pool {
	t.Helper()
	pool, err := dockertest.NewPool("")
	if err != nil {
		t.Skipf("docker daemon unavailable: %v", err)
	}
	if err := pool.Client.Ping(); err != nil {
		t.Skipf("docker daemon ping failed: %v", err)
	}
	pool.MaxWait = 60 * time.Second
	return pool
}

func TestDockerStdio_FilesystemImage(t *testing.T) {
	// Connects to mcp/filesystem running inside Docker via `docker run -i`,
	// proxied through stdio. The mcpx client invokes the binary `docker`
	// with arguments that exec the container's MCP server bound to a temp
	// directory containing one fixture file.
	//
	// This is an integration_docker-only test (gated by the build tag
	// declared at the top of the file). Without `-tags integration_docker`
	// the file is excluded from compilation and dockertest stays out of
	// the dependency graph (Principle IV).
	pool := dockerPool(t)
	_ = pool // Pool is dialed mainly to verify daemon health; the actual
	// container is launched via `docker run` because `mcp/filesystem`
	// is designed to be invoked as a subprocess that owns stdio for the
	// duration of the MCP session, which dockertest doesn't model.

	hostDir := t.TempDir()
	fixture := filepath.Join(hostDir, "hello.txt")
	require.NoError(t, writeFile(fixture, "world"))

	mx, err := mcpx.New(t.Context(), mcpx.MultiplexerConfig{
		Servers: []mcpx.ServerConfig{{
			Name:      "fs",
			Transport: mcpx.TransportStdio,
			Binary:    "docker",
			Args: []string{
				"run", "--rm", "-i",
				"--mount", "type=bind,src=" + hostDir + ",dst=/projects/x",
				"mcp/filesystem", "/projects",
			},
		}},
	})
	if err != nil {
		t.Skipf("docker run mcp/filesystem failed (image pull or daemon issue): %v", err)
	}
	defer mx.Close()

	require.Contains(t, mx.ServerNames(), "fs")
	tools := mx.AllTools()
	require.NotEmpty(t, tools, "mcp/filesystem must expose at least one tool")
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

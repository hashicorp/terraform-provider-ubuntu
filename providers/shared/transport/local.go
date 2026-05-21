package transport

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Compile-time interface check.
var _ Transport = (*LocalTransport)(nil)

// LocalTransport implements Transport for the local machine. Useful for testing
// or same-host management without SSH overhead.
type LocalTransport struct {
	config TransportConfig
}

// NewLocalTransport creates a new local transport with the given configuration.
func NewLocalTransport(config TransportConfig) *LocalTransport {
	return &LocalTransport{
		config: config,
	}
}

// Connect is a no-op for local transport.
func (t *LocalTransport) Connect(_ context.Context) error {
	return nil
}

func (t *LocalTransport) HostKeyFingerprint() string {
	return ""
}

// PushFile writes data directly to the local filesystem.
func (t *LocalTransport) PushFile(_ context.Context, remotePath string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(remotePath)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("local mkdir %s: %w", dir, err)
	}

	if err := os.WriteFile(remotePath, data, mode); err != nil {
		return fmt.Errorf("local write %s: %w", remotePath, err)
	}

	return nil
}

// StartProcess starts a local process and returns piped stdin/stdout.
func (t *LocalTransport) StartProcess(_ context.Context, command string) (io.WriteCloser, io.ReadCloser, error) {
	cmd := exec.Command("sh", "-c", command) //nolint:gosec

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("local stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("local stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("local start %q: %w", command, err)
	}

	// Wrap stdout so that Close also waits for the process.
	rc := &processReadCloser{
		Reader: stdout,
		cmd:    cmd,
	}

	return stdin, rc, nil
}

// Close is a no-op for local transport.
func (t *LocalTransport) Close() error {
	return nil
}

// TargetArch returns the architecture of the local machine.
func (t *LocalTransport) TargetArch(_ context.Context) (string, error) {
	return runtime.GOARCH, nil
}

// processReadCloser wraps a stdout pipe and waits for the process to exit
// when Close is called.
type processReadCloser struct {
	io.Reader
	cmd *exec.Cmd
}

func (p *processReadCloser) Close() error {
	return p.cmd.Wait()
}

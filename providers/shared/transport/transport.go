package transport

import (
	"context"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	TransportSSH   = "ssh"
	TransportLocal = "local"
	DefaultSSHPort = 22
)

// Transport abstracts how commands are executed and files are transferred
// to a target host. Implementations include SSH (remote) and local (for testing
// or same-host management).
type Transport interface {
	// Connect establishes a connection to the target host.
	Connect(ctx context.Context) error

	// HostKeyFingerprint returns the remote host key fingerprint accepted during
	// connection establishment, when the transport verifies a host key.
	HostKeyFingerprint() string

	// PushFile sends a file from local bytes to a remote path with the given mode.
	PushFile(ctx context.Context, remotePath string, data []byte, mode os.FileMode) error

	// StartProcess starts a long-lived process on the target and returns
	// handles to its stdin and stdout. The caller is responsible for closing
	// the returned writers/readers when done.
	StartProcess(ctx context.Context, command string) (io.WriteCloser, io.ReadCloser, error)

	// Close tears down the connection and releases any resources.
	Close() error

	// TargetArch returns the Go-style architecture name of the target
	// (e.g. "amd64", "arm64").
	TargetArch(ctx context.Context) (string, error)
}

// TransportConfig holds all configuration needed to establish a transport
// connection to a target host.
type TransportConfig struct {
	// SSH config (from provider-level ssh block)
	SSHUser               string // remote username
	SSHPrivateKey         string // PEM-encoded private key
	SSHCertificate        string // PEM-encoded signed certificate
	SSHAgent              bool   // use SSH agent for authentication
	SSHKnownHostsFile     string // path to known_hosts data loaded into provider memory
	SSHHostKeyTrust       *HostKeyTrustStore
	SSHHostKeyFingerprint string // expected pinned host key fingerprint for this target

	// Target-level config (from provider default_target or resource target attrs)
	Target    string
	Port      int
	Transport string
}

func (c TransportConfig) NormalizedTransport() string {
	transportName := strings.ToLower(strings.TrimSpace(c.Transport))
	if transportName != "" {
		return transportName
	}
	if strings.EqualFold(strings.TrimSpace(c.Target), TransportLocal) {
		return TransportLocal
	}
	return TransportSSH
}

func (c TransportConfig) IsLocal() bool {
	return c.NormalizedTransport() == TransportLocal
}

func (c TransportConfig) NormalizedTarget() string {
	target := strings.TrimSpace(c.Target)
	if strings.HasPrefix(target, "[") && strings.HasSuffix(target, "]") {
		return strings.TrimSuffix(strings.TrimPrefix(target, "["), "]")
	}
	return target
}

func (c TransportConfig) ResolvedPort() int {
	if c.IsLocal() {
		return 0
	}
	if c.Port > 0 {
		return c.Port
	}
	return DefaultSSHPort
}

func (c TransportConfig) Endpoint() string {
	if c.IsLocal() {
		return TransportLocal
	}
	target := c.NormalizedTarget()
	if target == "" {
		return ""
	}
	return net.JoinHostPort(target, strconv.Itoa(c.ResolvedPort()))
}

func (c TransportConfig) DisplayTarget() string {
	if c.IsLocal() {
		return TransportLocal
	}
	if endpoint := c.Endpoint(); endpoint != "" {
		return endpoint
	}
	return c.NormalizedTarget()
}

func (c TransportConfig) CacheKey() string {
	if c.IsLocal() {
		return TransportLocal
	}
	return c.NormalizedTransport() + "|" + strings.ToLower(c.Endpoint())
}

package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Compile-time interface check.
var _ Transport = (*SSHTransport)(nil)

const (
	defaultSSHDialTimeout      = 10 * time.Second
	defaultSSHHandshakeTimeout = 15 * time.Second
	sshShellUploadChunkSize    = 16 << 10
	sshStderrTailSize          = 16 << 10
)

// SSHTransport implements Transport over an SSH connection.
type SSHTransport struct {
	config             TransportConfig
	client             *ssh.Client
	hostKeyFingerprint string
	dialTimeout        time.Duration
	handshakeTimeout   time.Duration
	dialContext        func(context.Context, string, string) (net.Conn, error)
	newClientConn      func(net.Conn, string, *ssh.ClientConfig) (ssh.Conn, <-chan ssh.NewChannel, <-chan *ssh.Request, error)
	newSFTPClient      func() (sftpUploadClient, error)
	runCommandFunc     func(context.Context, string) error
}

// SSHTransportOptions configures per-connection SSH transport behavior. Zero
// values preserve the provider defaults.
type SSHTransportOptions struct {
	DialTimeout      time.Duration
	HandshakeTimeout time.Duration
}

// NewSSHTransport creates a new SSH transport with the given configuration.
func NewSSHTransport(config TransportConfig) *SSHTransport {
	return NewSSHTransportWithOptions(config, SSHTransportOptions{})
}

// NewSSHTransportWithOptions creates a new SSH transport with custom timeout
// settings.
func NewSSHTransportWithOptions(config TransportConfig, options SSHTransportOptions) *SSHTransport {
	return &SSHTransport{
		config:           config,
		dialTimeout:      normalizeSSHTimeout(options.DialTimeout, defaultSSHDialTimeout),
		handshakeTimeout: normalizeSSHTimeout(options.HandshakeTimeout, defaultSSHHandshakeTimeout),
	}
}

func normalizeSSHTimeout(value, defaultValue time.Duration) time.Duration {
	if value <= 0 {
		return defaultValue
	}
	return value
}

// Connect establishes an SSH connection to the target host using certificate
// auth or SSH agent.
func (t *SSHTransport) Connect(ctx context.Context) error {
	t.hostKeyFingerprint = ""

	authMethods, err := t.buildAuthMethods()
	if err != nil {
		return fmt.Errorf("ssh auth: %w", err)
	}

	endpoint := t.resolveEndpoint()
	hostKeyCallback, err := t.hostKeyCallback()
	if err != nil {
		return fmt.Errorf("ssh host verification: %w", err)
	}

	clientConfig := &ssh.ClientConfig{
		User:            t.config.SSHUser,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         t.dialTimeout,
	}

	transportLogDebug(ctx, "starting SSH transport connect", t.config, map[string]interface{}{
		"dial_timeout_ms":      t.dialTimeout.Milliseconds(),
		"handshake_timeout_ms": t.handshakeTimeout.Milliseconds(),
	})

	dialStarted := time.Now()
	conn, err := t.dial(ctx, endpoint)
	if err != nil {
		dialDuration := time.Since(dialStarted)
		transportLogWarn(ctx, "SSH dial failed", t.config, map[string]interface{}{
			"dial_ms": dialDuration.Milliseconds(),
			"error":   err.Error(),
		})
		return fmt.Errorf("ssh dial %s after %s: %w", endpoint, dialDuration.Round(time.Millisecond), err)
	}
	dialDuration := time.Since(dialStarted)

	clearHandshakeDeadline, err := t.applyHandshakeDeadline(ctx, conn)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("ssh handshake %s: %w", endpoint, err)
	}

	handshakeStarted := time.Now()
	sshConn, chans, reqs, err := t.clientConn(conn, endpoint, clientConfig)
	if err != nil {
		clearHandshakeDeadline(false)
		conn.Close()
		handshakeDuration := time.Since(handshakeStarted)
		transportLogWarn(ctx, "SSH handshake failed", t.config, map[string]interface{}{
			"dial_ms":      dialDuration.Milliseconds(),
			"handshake_ms": handshakeDuration.Milliseconds(),
			"error":        err.Error(),
		})
		return fmt.Errorf("ssh handshake %s after %s: %w", endpoint, handshakeDuration.Round(time.Millisecond), err)
	}
	clearHandshakeDeadline(true)

	t.client = ssh.NewClient(sshConn, chans, reqs)
	transportLogDebug(ctx, "SSH transport connect complete", t.config, map[string]interface{}{
		"dial_ms":      dialDuration.Milliseconds(),
		"handshake_ms": time.Since(handshakeStarted).Milliseconds(),
	})

	return nil
}

func (t *SSHTransport) HostKeyFingerprint() string {
	return strings.TrimSpace(t.hostKeyFingerprint)
}

func (t *SSHTransport) dial(ctx context.Context, addr string) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, t.dialTimeout)
	defer cancel()

	if t.dialContext != nil {
		return t.dialContext(dialCtx, "tcp", addr)
	}

	dialer := net.Dialer{Timeout: t.dialTimeout}
	return dialer.DialContext(dialCtx, "tcp", addr)
}

func (t *SSHTransport) clientConn(conn net.Conn, addr string, config *ssh.ClientConfig) (ssh.Conn, <-chan ssh.NewChannel, <-chan *ssh.Request, error) {
	if t.newClientConn != nil {
		return t.newClientConn(conn, addr, config)
	}
	return ssh.NewClientConn(conn, addr, config)
}

func (t *SSHTransport) applyHandshakeDeadline(ctx context.Context, conn net.Conn) (func(bool), error) {
	deadline := time.Now().Add(t.handshakeTimeout)
	if outerDeadline, ok := ctx.Deadline(); ok && outerDeadline.Before(deadline) {
		deadline = outerDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set SSH handshake deadline: %w", err)
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-done:
		}
	}()

	return func(clear bool) {
		close(done)
		if clear {
			_ = conn.SetDeadline(time.Time{})
		}
	}, nil
}

func (t *SSHTransport) hostKeyCallback() (ssh.HostKeyCallback, error) {
	store := t.config.SSHHostKeyTrust
	if store == nil {
		store = NewHostKeyTrustStore()
		t.config.SSHHostKeyTrust = store
	}

	if err := store.LoadKnownHostsFile(strings.TrimSpace(t.config.SSHKnownHostsFile)); err != nil {
		return nil, err
	}

	targetAddress := t.resolveEndpoint()
	expectedFingerprint := strings.TrimSpace(t.config.SSHHostKeyFingerprint)

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fingerprint := hostKeyFingerprint(key)
		t.hostKeyFingerprint = fingerprint

		if store.IsRevokedFingerprint(fingerprint) {
			return fmt.Errorf("ssh host verification: key is revoked for %s", describeHostVerificationTarget(hostname, remote, targetAddress))
		}

		addresses := knownHostAddresses(hostname, remote)
		if strings.TrimSpace(targetAddress) != "" {
			addresses = append(addresses, targetAddress)
		}
		trustedFingerprints := store.TrustedFingerprints(addresses)

		if expectedFingerprint != "" && fingerprint != expectedFingerprint {
			return fmt.Errorf("ssh host key fingerprint mismatch for %s: expected %s, got %s", describeHostVerificationTarget(hostname, remote, targetAddress), expectedFingerprint, fingerprint)
		}

		if len(trustedFingerprints) > 0 {
			if _, ok := trustedFingerprints[fingerprint]; !ok {
				return fmt.Errorf("ssh host key fingerprint mismatch for %s: expected one of %s, got %s", describeHostVerificationTarget(hostname, remote, targetAddress), formatTrustedFingerprints(trustedFingerprints), fingerprint)
			}
		}

		store.TrustFingerprintAliases(addresses, fingerprint)
		return nil
	}, nil
}

func knownHostAddresses(hostname string, remote net.Addr) []string {
	seen := map[string]struct{}{}
	var addresses []string
	appendAddress := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		addresses = append(addresses, value)
	}

	appendAddress(hostname)
	if remote != nil {
		appendAddress(remote.String())
	}

	return addresses
}

func describeHostVerificationTarget(hostname string, remote net.Addr, targetAddress string) string {
	for _, candidate := range []string{hostname, targetAddress} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			return candidate
		}
	}
	if remote != nil {
		return remote.String()
	}
	return "target"
}

func formatTrustedFingerprints(fingerprints map[string]struct{}) string {
	values := make([]string, 0, len(fingerprints))
	for fingerprint := range fingerprints {
		values = append(values, fingerprint)
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}

// PushFile writes data to a remote path, creating parent directories as needed.
func (t *SSHTransport) PushFile(ctx context.Context, remotePath string, data []byte, mode os.FileMode) error {
	if t.client == nil {
		return fmt.Errorf("ssh: not connected")
	}

	sftpErr := t.pushFileSFTP(remotePath, data, mode)
	if sftpErr == nil {
		return nil
	}

	transportLogWarn(ctx, "SFTP upload failed; falling back to shell upload", t.config, map[string]interface{}{
		"remote_path": remotePath,
		"error":       sftpErr.Error(),
	})

	if err := pushFileWithShellRunner(ctx, t.runCommand, remotePath, data, mode); err != nil {
		return fmt.Errorf("ssh upload %s via sftp failed: %v; shell fallback failed: %w", remotePath, sftpErr, err)
	}

	return nil
}

func (t *SSHTransport) pushFileSFTP(remotePath string, data []byte, mode os.FileMode) error {
	client, err := t.openSFTPClient()
	if err != nil {
		return err
	}
	defer client.Close()

	return pushFileWithSFTP(client, remotePath, data, mode)
}

func (t *SSHTransport) openSFTPClient() (sftpUploadClient, error) {
	if t.newSFTPClient != nil {
		return t.newSFTPClient()
	}
	client, err := sftp.NewClient(t.client)
	if err != nil {
		return nil, err
	}
	return &realSFTPUploadClient{client: client}, nil
}

func pushFileWithSFTP(client sftpUploadClient, remotePath string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(remotePath)
	if err := client.MkdirAll(dir); err != nil {
		return fmt.Errorf("sftp mkdir %s: %w", dir, err)
	}

	file, err := client.OpenFile(remotePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("sftp open %s: %w", remotePath, err)
	}

	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		_ = file.Close()
		return fmt.Errorf("sftp write %s: %w", remotePath, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("sftp close %s: %w", remotePath, err)
	}

	if err := client.Chmod(remotePath, mode); err != nil {
		return fmt.Errorf("sftp chmod %s: %w", remotePath, err)
	}

	return nil
}

func pushFileWithShellRunner(ctx context.Context, run func(context.Context, string) error, remotePath string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(remotePath)
	if err := run(ctx, fmt.Sprintf("mkdir -p %s", shellQuote(dir))); err != nil {
		return fmt.Errorf("ssh mkdir %s: %w", dir, err)
	}

	if err := run(ctx, fmt.Sprintf(": > %s", shellQuote(remotePath))); err != nil {
		return fmt.Errorf("ssh create %s: %w", remotePath, err)
	}

	for offset := 0; offset < len(data); offset += sshShellUploadChunkSize {
		end := offset + sshShellUploadChunkSize
		if end > len(data) {
			end = len(data)
		}
		chunkCommand := fmt.Sprintf("printf %s >> %s", shellQuote(shellEscapeBytesForPrintf(data[offset:end])), shellQuote(remotePath))
		if err := run(ctx, chunkCommand); err != nil {
			return fmt.Errorf("ssh write %s chunk %d: %w", remotePath, offset/sshShellUploadChunkSize, err)
		}
	}

	if err := run(ctx, fmt.Sprintf("chmod %04o %s", mode, shellQuote(remotePath))); err != nil {
		return fmt.Errorf("ssh chmod %s: %w", remotePath, err)
	}

	return nil
}

// StartProcess starts a long-lived command over SSH, returning stdin/stdout
// pipes. If Become is enabled, the command is wrapped in the configured
// privilege escalation method.
func (t *SSHTransport) StartProcess(ctx context.Context, command string) (io.WriteCloser, io.ReadCloser, error) {
	if t.client == nil {
		return nil, nil, fmt.Errorf("ssh: not connected")
	}

	session, err := t.client.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("ssh session: %w", err)
	}

	return startSSHProcess(session, command)
}

func startSSHProcess(session sshProcessSession, command string) (io.WriteCloser, io.ReadCloser, error) {
	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()

		return nil, nil, fmt.Errorf("ssh stdin pipe: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()

		return nil, nil, fmt.Errorf("ssh stdout pipe: %w", err)
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()

		return nil, nil, fmt.Errorf("ssh stderr pipe: %w", err)
	}

	if err := session.Start(command); err != nil {
		session.Close()

		return nil, nil, fmt.Errorf("ssh start %q: %w", command, err)
	}

	// The remote executor writes structured logs to stderr. Drain that stream so
	// SSH channel backpressure cannot stall an otherwise-complete RPC. Keep a
	// bounded tail so session failures can report the most recent remote logs.
	stderrTail := newBoundedTailBuffer(sshStderrTailSize)
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(stderrTail, stderr)
	}()

	// Wrap stdout in a ReadCloser that also closes the session when done.
	rc := &sessionReadCloser{
		Reader:     stdout,
		session:    session,
		stderrDone: stderrDone,
		stderrTail: stderrTail,
	}

	return stdin, rc, nil
}

// Close tears down the SSH connection.
func (t *SSHTransport) Close() error {
	if t.client == nil {
		return nil
	}

	err := t.client.Close()
	t.client = nil

	return err
}

// TargetArch runs uname -m on the remote host and maps it to a Go arch name.
func (t *SSHTransport) TargetArch(ctx context.Context) (string, error) {
	if t.client == nil {
		return "", fmt.Errorf("ssh: not connected")
	}

	session, err := t.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()

	out, err := session.Output("uname -m")
	if err != nil {
		return "", fmt.Errorf("ssh uname -m: %w", err)
	}

	return mapArch(strings.TrimSpace(string(out)))
}

// buildAuthMethods constructs SSH auth methods from the transport config.
func (t *SSHTransport) buildAuthMethods() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// Certificate-based auth: requires both a private key and a signed certificate.
	if t.config.SSHCertificate != "" && t.config.SSHPrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(t.config.SSHPrivateKey))
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}

		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(t.config.SSHCertificate))
		if err != nil {
			return nil, fmt.Errorf("parse certificate: %w", err)
		}

		cert, ok := pub.(*ssh.Certificate)
		if !ok {
			return nil, fmt.Errorf("parsed key is not a certificate")
		}

		certSigner, err := ssh.NewCertSigner(cert, signer)
		if err != nil {
			return nil, fmt.Errorf("create cert signer: %w", err)
		}

		methods = append(methods, ssh.PublicKeys(certSigner))
	} else if t.config.SSHPrivateKey != "" {
		// Plain private key auth (no certificate).
		signer, err := ssh.ParsePrivateKey([]byte(t.config.SSHPrivateKey))
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}

		methods = append(methods, ssh.PublicKeys(signer))
	}

	// SSH agent auth.
	if t.config.SSHAgent {
		agentConn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
		if err != nil {
			return nil, fmt.Errorf("ssh agent connection: %w", err)
		}

		agentClient := agent.NewClient(agentConn)
		methods = append(methods, ssh.PublicKeysCallback(agentClient.Signers))
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no SSH authentication method configured")
	}

	return methods, nil
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func isRetryableConnectError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}

	var keyErr *knownhosts.KeyError
	if errors.As(err, &keyErr) {
		return len(keyErr.Want) == 0
	}

	var revokedErr *knownhosts.RevokedError
	if errors.As(err, &revokedErr) {
		return false
	}

	if isTimeoutError(err) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	message := strings.ToLower(err.Error())
	for _, nonRetryable := range []string{
		"unable to authenticate",
		"no supported methods remain",
		"parse private key",
		"no ssh authentication method configured",
		"key mismatch",
		"key is revoked",
		"parse known_hosts",
		"host verification",
		"adopt new host key",
	} {
		if strings.Contains(message, nonRetryable) {
			return false
		}
	}

	for _, retryable := range []string{
		"dial tcp",
		"handshake failed",
		"connection refused",
		"connection reset",
		"connection closed",
		"broken pipe",
		"no route to host",
		"network is unreachable",
		"resource temporarily unavailable",
		"eof",
	} {
		if strings.Contains(message, retryable) {
			return true
		}
	}

	return false
}

// resolveEndpoint ensures the SSH transport always dials a concrete endpoint.
func (t *SSHTransport) resolveEndpoint() string {
	if endpoint := t.config.Endpoint(); endpoint != "" {
		return endpoint
	}
	return net.JoinHostPort("localhost", "22")
}

// runCommand is a convenience for executing a command and discarding output.
func (t *SSHTransport) runCommand(ctx context.Context, cmd string) error {
	if t.runCommandFunc != nil {
		return t.runCommandFunc(ctx, cmd)
	}

	session, err := t.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	return session.Run(cmd)
}

type sftpUploadClient interface {
	MkdirAll(string) error
	OpenFile(string, int) (sftpUploadFile, error)
	Chmod(string, os.FileMode) error
	Close() error
}

type realSFTPUploadClient struct {
	client *sftp.Client
}

func (c *realSFTPUploadClient) MkdirAll(path string) error {
	return c.client.MkdirAll(path)
}

func (c *realSFTPUploadClient) OpenFile(path string, flags int) (sftpUploadFile, error) {
	return c.client.OpenFile(path, flags)
}

func (c *realSFTPUploadClient) Chmod(path string, mode os.FileMode) error {
	return c.client.Chmod(path, mode)
}

func (c *realSFTPUploadClient) Close() error {
	return c.client.Close()
}

type sftpUploadFile interface {
	io.WriteCloser
}

type sshSession interface {
	Wait() error
	Close() error
}

type sshProcessSession interface {
	sshSession
	StdinPipe() (io.WriteCloser, error)
	StdoutPipe() (io.Reader, error)
	StderrPipe() (io.Reader, error)
	Start(string) error
}

type boundedTailBuffer struct {
	mu   sync.Mutex
	max  int
	data []byte
}

func newBoundedTailBuffer(max int) *boundedTailBuffer {
	return &boundedTailBuffer{max: max}
}

func (b *boundedTailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.max <= 0 {
		return len(p), nil
	}

	if len(p) >= b.max {
		b.data = append(b.data[:0], p[len(p)-b.max:]...)
		return len(p), nil
	}

	needed := len(b.data) + len(p) - b.max
	if needed > 0 {
		if needed >= len(b.data) {
			b.data = b.data[:0]
		} else {
			copy(b.data, b.data[needed:])
			b.data = b.data[:len(b.data)-needed]
		}
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *boundedTailBuffer) Snapshot() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.data))
}

// sessionReadCloser wraps an io.Reader and closes the underlying SSH session
// when Close is called.
type sessionReadCloser struct {
	io.Reader
	session    sshSession
	stderrDone <-chan struct{}
	stderrTail *boundedTailBuffer
}

func (s *sessionReadCloser) Close() error {
	waitErr := normalizeSSHSessionCloseError(s.session.Wait())
	closeErr := normalizeSSHSessionCloseError(s.session.Close())
	if s.stderrDone != nil {
		<-s.stderrDone
	}
	if waitErr != nil {
		return annotateRemoteStderr(waitErr, s.stderrTail)
	}
	return annotateRemoteStderr(closeErr, s.stderrTail)
}

func normalizeSSHSessionCloseError(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func annotateRemoteStderr(err error, tail *boundedTailBuffer) error {
	if err == nil {
		return nil
	}
	stderrTail := tail.Snapshot()
	if stderrTail == "" {
		return err
	}
	return fmt.Errorf("%w; remote stderr tail: %s", err, stderrTail)
}

// shellQuote wraps a string in single quotes, escaping embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func shellEscapeBytesForPrintf(data []byte) string {
	var builder strings.Builder
	builder.Grow(len(data) * 4)
	for _, value := range data {
		builder.WriteByte('\\')
		builder.WriteByte('0' + ((value >> 6) & 0x03))
		builder.WriteByte('0' + ((value >> 3) & 0x07))
		builder.WriteByte('0' + (value & 0x07))
	}
	return builder.String()
}

// mapArch maps uname -m output to Go architecture names.
func mapArch(uname string) (string, error) {
	switch uname {
	case "x86_64":
		return "amd64", nil
	case "aarch64":
		return "arm64", nil
	case "armv7l":
		return "arm", nil
	case "i686", "i386":
		return "386", nil
	case "s390x":
		return "s390x", nil
	case "ppc64le":
		return "ppc64le", nil
	default:
		return "", fmt.Errorf("unknown architecture: %s", uname)
	}
}

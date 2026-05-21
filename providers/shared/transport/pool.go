package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMaxConns              = 50
	defaultConnectRetryAttempts  = 30
	defaultConnectInitialBackoff = 2 * time.Second
	defaultConnectMaxBackoff     = 10 * time.Second
	defaultConnectTimeout        = 8 * time.Minute
	defaultReconnectTimeout      = 8 * time.Minute
)

type ConnectRetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	TotalTimeout   time.Duration
}

// ConnectionPoolOptions configures SSH session pooling and connection retry
// behavior. Zero values preserve the provider defaults.
type ConnectionPoolOptions struct {
	MaxConnections      int
	ConnectRetry        ConnectRetryPolicy
	ReconnectRetry      ConnectRetryPolicy
	SSHDialTimeout      time.Duration
	SSHHandshakeTimeout time.Duration
}

// SSHConfig holds provider-level SSH defaults that apply to all connections.
type SSHConfig struct {
	User           string
	PrivateKey     string
	Certificate    string
	Agent          bool
	KnownHostsFile string
	HostKeyTrust   *HostKeyTrustStore
}

// Session represents an active transport session to a host, including the
// running executor process's stdin/stdout.
type Session struct {
	Transport          Transport
	Config             TransportConfig
	Stdin              io.WriteCloser
	Stdout             io.ReadCloser
	LastUsed           time.Time
	HostKeyFingerprint string
	BootstrapMu        sync.Mutex
	inUse              atomic.Int32
}

func (s *Session) AcquireUse() {
	if s == nil {
		return
	}
	s.inUse.Add(1)
}

func (s *Session) ReleaseUse() {
	if s == nil {
		return
	}
	if s.inUse.Add(-1) < 0 {
		s.inUse.Store(0)
	}
}

func (s *Session) InUse() bool {
	if s == nil {
		return false
	}
	return s.inUse.Load() > 0
}

type connectionAttempt struct {
	done    chan struct{}
	op      string
	started time.Time
	session *Session
	err     error
}

func newConnectionAttempt(op string) *connectionAttempt {
	return &connectionAttempt{
		done:    make(chan struct{}),
		op:      op,
		started: time.Now(),
	}
}

func (a *connectionAttempt) wait(ctx context.Context) (*Session, error) {
	select {
	case <-a.done:
		return a.session, a.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ConnectionPool manages a pool of transport sessions keyed by transport identity.
// It is safe for concurrent use.
type ConnectionPool struct {
	mu                  sync.Mutex
	sessions            map[string]*Session
	inflight            map[string]*connectionAttempt
	maxConns            int
	connectRetry        ConnectRetryPolicy
	reconnectRetry      ConnectRetryPolicy
	sshDialTimeout      time.Duration
	sshHandshakeTimeout time.Duration
	config              *SSHConfig
	transportFactory    func(TransportConfig) Transport
}

// NewConnectionPool creates a new connection pool with the given SSH defaults.
func NewConnectionPool(config *SSHConfig) *ConnectionPool {
	return NewConnectionPoolWithOptions(config, ConnectionPoolOptions{})
}

// NewConnectionPoolWithMax creates a new connection pool with a custom max
// connection limit.
func NewConnectionPoolWithMax(config *SSHConfig, maxConns int) *ConnectionPool {
	return NewConnectionPoolWithOptions(config, ConnectionPoolOptions{MaxConnections: maxConns})
}

// NewConnectionPoolWithOptions creates a new connection pool with custom
// pooling, retry, and SSH transport settings.
func NewConnectionPoolWithOptions(config *SSHConfig, options ConnectionPoolOptions) *ConnectionPool {
	options = normalizeConnectionPoolOptions(options)
	pool := &ConnectionPool{
		sessions:            make(map[string]*Session),
		inflight:            make(map[string]*connectionAttempt),
		maxConns:            options.MaxConnections,
		connectRetry:        options.ConnectRetry,
		reconnectRetry:      options.ReconnectRetry,
		sshDialTimeout:      options.SSHDialTimeout,
		sshHandshakeTimeout: options.SSHHandshakeTimeout,
		config:              config,
	}
	pool.transportFactory = pool.newTransport
	return pool
}

func normalizeConnectionPoolOptions(options ConnectionPoolOptions) ConnectionPoolOptions {
	if options.MaxConnections <= 0 {
		options.MaxConnections = defaultMaxConns
	}
	options.ConnectRetry = normalizeConnectRetryPolicy(options.ConnectRetry, defaultConnectRetryPolicy())
	options.ReconnectRetry = normalizeConnectRetryPolicy(options.ReconnectRetry, defaultReconnectRetryPolicy())
	options.SSHDialTimeout = normalizeSSHTimeout(options.SSHDialTimeout, defaultSSHDialTimeout)
	options.SSHHandshakeTimeout = normalizeSSHTimeout(options.SSHHandshakeTimeout, defaultSSHHandshakeTimeout)
	return options
}

func defaultConnectRetryPolicy() ConnectRetryPolicy {
	return ConnectRetryPolicy{
		MaxAttempts:    defaultConnectRetryAttempts,
		InitialBackoff: defaultConnectInitialBackoff,
		MaxBackoff:     defaultConnectMaxBackoff,
		TotalTimeout:   defaultConnectTimeout,
	}
}

func defaultReconnectRetryPolicy() ConnectRetryPolicy {
	policy := defaultConnectRetryPolicy()
	policy.TotalTimeout = defaultReconnectTimeout
	return policy
}

func normalizeConnectRetryPolicy(policy, defaults ConnectRetryPolicy) ConnectRetryPolicy {
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = defaults.MaxAttempts
	}
	if policy.InitialBackoff <= 0 {
		policy.InitialBackoff = defaults.InitialBackoff
	}
	if policy.MaxBackoff <= 0 {
		policy.MaxBackoff = defaults.MaxBackoff
	}
	if policy.MaxBackoff < policy.InitialBackoff {
		policy.MaxBackoff = policy.InitialBackoff
	}
	if policy.TotalTimeout <= 0 {
		policy.TotalTimeout = defaults.TotalTimeout
	}
	return policy
}

func (p *ConnectionPool) retryPolicyForOperation(op string) ConnectRetryPolicy {
	switch op {
	case "reconnect":
		return normalizeConnectRetryPolicy(p.reconnectRetry, defaultReconnectRetryPolicy())
	default:
		return normalizeConnectRetryPolicy(p.connectRetry, defaultConnectRetryPolicy())
	}
}

// GetOrCreate returns an existing session for the given target config, or
// creates a new one using the provided overrides. The pool merges provider-level
// SSH defaults with target-level overrides.
func (p *ConnectionPool) GetOrCreate(ctx context.Context, hostConfig TransportConfig) (*Session, error) {
	merged := p.mergeConfig(hostConfig)
	key := sessionCacheKey(merged)

	for {
		p.mu.Lock()
		if attempt := p.inflight[key]; attempt != nil {
			p.mu.Unlock()

			waitStarted := time.Now()
			sess, err := attempt.wait(ctx)
			transportLogDebug(ctx, "waited for in-flight SSH session operation", merged, map[string]interface{}{
				"cache_key": attemptKey(key),
				"operation": attempt.op,
				"wait_ms":   time.Since(waitStarted).Milliseconds(),
			})
			if err != nil {
				return nil, err
			}
			if sess != nil {
				if err := p.validateSessionTrust(sess, merged); err != nil {
					return nil, err
				}
				sess.LastUsed = time.Now()
				return sess, nil
			}
			continue
		}

		if sess, ok := p.sessions[key]; ok {
			if err := p.validateSessionTrust(sess, merged); err != nil {
				p.mu.Unlock()
				return nil, err
			}
			sess.LastUsed = time.Now()
			p.mu.Unlock()
			transportLogDebug(ctx, "reused cached SSH session", merged, map[string]interface{}{
				"cache_key": attemptKey(key),
			})
			return sess, nil
		}

		attempt := newConnectionAttempt("connect")
		p.inflight[key] = attempt
		p.mu.Unlock()

		transportLogDebug(ctx, "starting SSH session connect", merged, map[string]interface{}{
			"cache_key": attemptKey(key),
			"operation": attempt.op,
		})

		sess, err := p.connectSession(ctx, merged)
		p.finishConnectAttempt(key, attempt, sess, err)
		if err != nil {
			return nil, err
		}
		return sess, nil
	}
}

// Remove removes and closes a session for the given transport config.
func (p *ConnectionPool) Remove(config TransportConfig) error {
	merged := p.mergeConfig(config)
	key := sessionCacheKey(merged)

	p.mu.Lock()
	sess := p.sessions[key]
	delete(p.sessions, key)
	p.mu.Unlock()

	if sess == nil {
		return nil
	}
	return p.closeSession(sess)
}

// Close closes all sessions in the pool.
func (p *ConnectionPool) Close() error {
	p.mu.Lock()
	sessions := make([]*Session, 0, len(p.sessions))

	for key, sess := range p.sessions {
		sessions = append(sessions, sess)
		delete(p.sessions, key)
	}
	p.mu.Unlock()

	var firstErr error
	for _, sess := range sessions {
		if err := p.closeSession(sess); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close session %s: %w", sess.Config.DisplayTarget(), err)
		}
	}
	return firstErr
}

// Len returns the number of active sessions.
func (p *ConnectionPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.sessions)
}

func (p *ConnectionPool) Reconnect(ctx context.Context, session *Session) error {
	return p.ReconnectWithReason(ctx, session, "")
}

// ReconnectWithReason tears down and recreates the transport backing an
// existing session.
func (p *ConnectionPool) ReconnectWithReason(ctx context.Context, session *Session, reason string) error {
	if session == nil {
		return fmt.Errorf("nil session")
	}

	key := sessionCacheKey(session.Config)
	p.mu.Lock()
	if attempt := p.inflight[key]; attempt != nil {
		p.mu.Unlock()

		waitStarted := time.Now()
		_, err := attempt.wait(ctx)
		transportLogDebug(ctx, "waited for in-flight SSH reconnect", session.Config, map[string]interface{}{
			"cache_key": attemptKey(key),
			"operation": attempt.op,
			"reason":    reason,
			"wait_ms":   time.Since(waitStarted).Milliseconds(),
		})
		return err
	}

	attempt := newConnectionAttempt("reconnect")
	p.inflight[key] = attempt
	p.mu.Unlock()

	transportLogDebug(ctx, "starting SSH session reconnect", session.Config, map[string]interface{}{
		"cache_key": attemptKey(key),
		"operation": attempt.op,
		"reason":    reason,
	})

	err := p.reconnectSession(ctx, session)
	p.finishReconnectAttempt(key, attempt, session, err)
	return err
}

func (p *ConnectionPool) reconnectSession(ctx context.Context, session *Session) error {
	if session == nil {
		return fmt.Errorf("nil session")
	}

	oldTransport := session.Transport
	transport, err := p.connectTransportWithRetry(ctx, session.Config, "reconnect")
	if err != nil {
		return err
	}

	session.Transport = transport
	session.Stdin = nil
	session.Stdout = nil
	session.LastUsed = time.Now()
	session.HostKeyFingerprint = strings.TrimSpace(transport.HostKeyFingerprint())
	if session.HostKeyFingerprint != "" {
		session.Config.SSHHostKeyFingerprint = session.HostKeyFingerprint
	}

	if oldTransport != nil {
		if err := oldTransport.Close(); err != nil {
			transportLogWarn(ctx, "closing stale SSH transport returned an error after successful reconnect", session.Config, map[string]interface{}{
				"error": err.Error(),
			})
		}
	}

	return nil
}

func (p *ConnectionPool) finishReconnectAttempt(key string, attempt *connectionAttempt, session *Session, err error) {
	p.mu.Lock()
	if current := p.inflight[key]; current == attempt {
		if err != nil {
			delete(p.sessions, key)
		} else {
			p.sessions[key] = session
		}
		delete(p.inflight, key)
		attempt.session = session
		attempt.err = err
	}
	p.mu.Unlock()
	close(attempt.done)
}

func (p *ConnectionPool) connectSession(ctx context.Context, config TransportConfig) (*Session, error) {
	transport, err := p.connectTransportWithRetry(ctx, config, "connect")
	if err != nil {
		return nil, err
	}
	fingerprint := strings.TrimSpace(transport.HostKeyFingerprint())
	if fingerprint != "" {
		config.SSHHostKeyFingerprint = fingerprint
	}
	return &Session{
		Transport:          transport,
		Config:             config,
		LastUsed:           time.Now(),
		HostKeyFingerprint: fingerprint,
	}, nil
}

func (p *ConnectionPool) connectTransportWithRetry(ctx context.Context, config TransportConfig, op string) (Transport, error) {
	policy := p.retryPolicyForOperation(op)
	cacheKey := sessionCacheKey(config)
	started := time.Now()
	retryCtx := ctx
	cancel := func() {}
	if policy.TotalTimeout > 0 {
		retryCtx, cancel = context.WithTimeout(ctx, policy.TotalTimeout)
	}
	defer cancel()
	var lastErr error

	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
		if err := retryCtx.Err(); err != nil {
			return nil, wrapConnectContextError(op, cacheKey, started, lastErr, err)
		}

		transport := p.transportFactory(config)
		err := transport.Connect(retryCtx)
		if err == nil {
			return transport, nil
		}
		_ = transport.Close()

		if !isRetryableConnectError(err) {
			return nil, fmt.Errorf("%s %s: %w", op, cacheKey, err)
		}

		lastErr = err
		if attempt == policy.MaxAttempts-1 {
			break
		}

		backoff := connectRetryBackoff(policy, attempt)
		transportLogWarn(ctx, "SSH session connect failed; retrying", config, map[string]interface{}{
			"cache_key":    attemptKey(cacheKey),
			"operation":    op,
			"attempt":      attempt + 1,
			"max_attempts": policy.MaxAttempts,
			"backoff_ms":   backoff.Milliseconds(),
			"timeout_ms":   policy.TotalTimeout.Milliseconds(),
			"error":        err.Error(),
		})
		if err := sleepWithContext(retryCtx, backoff); err != nil {
			return nil, wrapConnectContextError(op, cacheKey, started, lastErr, err)
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("ssh connection failed")
	}

	return nil, fmt.Errorf("%s %s: retry budget exhausted after %d attempts over %s: %w", op, cacheKey, policy.MaxAttempts, formatRetryDuration(policy.TotalTimeout), lastErr)
}

func wrapConnectContextError(op, cacheKey string, started time.Time, lastErr, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	elapsed := formatRetryDuration(time.Since(started))
	if lastErr != nil {
		return fmt.Errorf("%s %s: timed out after %s waiting for SSH availability: last error: %w", op, cacheKey, elapsed, lastErr)
	}
	return fmt.Errorf("%s %s: timed out after %s waiting for SSH availability: %w", op, cacheKey, elapsed, err)
}

func formatRetryDuration(value time.Duration) string {
	if value <= 0 {
		return "0s"
	}
	if value < time.Second {
		return value.Round(time.Millisecond).String()
	}
	return value.Round(time.Second).String()
}

func connectRetryBackoff(policy ConnectRetryPolicy, attempt int) time.Duration {
	backoff := policy.InitialBackoff
	for i := 0; i < attempt; i++ {
		backoff *= 2
		if backoff >= policy.MaxBackoff {
			return policy.MaxBackoff
		}
	}
	if backoff > policy.MaxBackoff {
		return policy.MaxBackoff
	}
	return backoff
}

func sleepWithContext(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p *ConnectionPool) finishConnectAttempt(key string, attempt *connectionAttempt, session *Session, err error) {
	var victim *Session

	p.mu.Lock()
	if current := p.inflight[key]; current == attempt {
		if err == nil && session != nil {
			if len(p.sessions) >= p.maxConns {
				victim = p.evictLRULocked()
			}
			p.sessions[key] = session
		}
		delete(p.inflight, key)
		attempt.session = session
		attempt.err = err
	}
	p.mu.Unlock()

	if victim != nil {
		_ = p.closeSession(victim)
	}
	close(attempt.done)
}

// evictLRULocked removes and returns the least recently used session. Must be
// called with mu held.
func (p *ConnectionPool) evictLRULocked() *Session {
	var (
		oldestKey  string
		oldestTime time.Time
		found      bool
	)

	for key, sess := range p.sessions {
		if sess.InUse() {
			continue
		}
		if !found || sess.LastUsed.Before(oldestTime) {
			oldestKey = key
			oldestTime = sess.LastUsed
			found = true
		}
	}

	if !found {
		return nil
	}

	sess := p.sessions[oldestKey]
	delete(p.sessions, oldestKey)
	return sess
}

// mergeConfig combines provider-level SSH defaults with host-level overrides.
func (p *ConnectionPool) mergeConfig(hostConfig TransportConfig) TransportConfig {
	if p.config == nil {
		return hostConfig
	}

	if hostConfig.SSHUser == "" {
		hostConfig.SSHUser = p.config.User
	}

	if hostConfig.SSHPrivateKey == "" {
		hostConfig.SSHPrivateKey = p.config.PrivateKey
	}

	if hostConfig.SSHCertificate == "" {
		hostConfig.SSHCertificate = p.config.Certificate
	}

	if !hostConfig.SSHAgent && p.config.Agent {
		hostConfig.SSHAgent = true
	}

	if hostConfig.SSHHostKeyTrust == nil {
		hostConfig.SSHHostKeyTrust = p.config.HostKeyTrust
	}

	if hostConfig.SSHKnownHostsFile == "" {
		hostConfig.SSHKnownHostsFile = p.config.KnownHostsFile
	}

	return hostConfig
}

func (p *ConnectionPool) HostKeyFingerprint(config TransportConfig) string {
	merged := p.mergeConfig(config)
	key := sessionCacheKey(merged)

	p.mu.Lock()
	defer p.mu.Unlock()

	if sess := p.sessions[key]; sess != nil {
		return strings.TrimSpace(sess.HostKeyFingerprint)
	}
	return ""
}

func (p *ConnectionPool) validateSessionTrust(session *Session, config TransportConfig) error {
	if session == nil {
		return nil
	}

	expected := strings.TrimSpace(config.SSHHostKeyFingerprint)
	observed := strings.TrimSpace(session.HostKeyFingerprint)
	if expected == "" || observed == "" {
		if expected != "" && session.Config.SSHHostKeyFingerprint == "" {
			session.Config.SSHHostKeyFingerprint = expected
		}
		return nil
	}
	if expected == observed {
		session.Config.SSHHostKeyFingerprint = expected
		return nil
	}

	return fmt.Errorf("ssh host key fingerprint mismatch for %s: expected %s, got %s", config.Endpoint(), expected, observed)
}

// closeSession closes a session's process handles and transport.
func (p *ConnectionPool) closeSession(sess *Session) error {
	var firstErr error

	if sess.Stdin != nil {
		err := sess.Stdin.Close()
		if err != nil {
			firstErr = preserveFirstErr(firstErr, err)
		}
	}

	if sess.Stdout != nil {
		err := sess.Stdout.Close()
		if err != nil {
			firstErr = preserveFirstErr(firstErr, err)
		}
	}

	if err := sess.Transport.Close(); err != nil {
		firstErr = preserveFirstErr(firstErr, err)
	}

	return firstErr
}

func newTransport(config TransportConfig) Transport {
	return newTransportWithOptions(config, SSHTransportOptions{})
}

func (p *ConnectionPool) newTransport(config TransportConfig) Transport {
	return newTransportWithOptions(config, SSHTransportOptions{
		DialTimeout:      p.sshDialTimeout,
		HandshakeTimeout: p.sshHandshakeTimeout,
	})
}

func newTransportWithOptions(config TransportConfig, options SSHTransportOptions) Transport {
	if config.IsLocal() || strings.TrimSpace(config.Target) == "" {
		return NewLocalTransport(config)
	}

	return NewSSHTransportWithOptions(config, options)
}

func preserveFirstErr(current, next error) error {
	if current != nil {
		return current
	}

	return next
}

func sessionCacheKey(config TransportConfig) string {
	return config.CacheKey()
}

func attemptKey(key string) string {
	return key
}

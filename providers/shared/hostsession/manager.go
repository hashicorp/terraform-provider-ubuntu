package hostsession

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assets"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/supportpolicy"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

const (
	// defaultExecutorBasePath is the default directory on the target where the
	// executor binary is cached, keyed by the hash of the binary.
	defaultExecutorBasePath         = "/tmp/tf-unix"
	executorBinName                 = "executor"
	executorCacheKeyLength          = 12
	defaultMutationCallTimeout      = 15 * time.Minute
	defaultRetryAttempts            = 3
	defaultRetryInitialBackoff      = 250 * time.Millisecond
	defaultRetryMaxBackoff          = 3 * time.Second
	operationAcquirePollDelay       = 200 * time.Millisecond
	defaultOperationReleaseTimeout  = 20 * time.Second
	terminalOperationReleaseTimeout = 5 * time.Second
)

type RetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

type ExecutorLayout struct {
	BasePath string
}

type pluginLoadAttempt struct {
	done chan struct{}
	err  error
}

func newPluginLoadAttempt() *pluginLoadAttempt {
	return &pluginLoadAttempt{done: make(chan struct{})}
}

func (a *pluginLoadAttempt) wait(ctx context.Context) error {
	select {
	case <-a.done:
		return a.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ExecutorManager handles pushing the executor binary and streaming WASM
// plugins to target hosts via their transport sessions.
type ExecutorManager struct {
	pool                *transport.ConnectionPool
	assets              assets.Store
	rpcClients          map[string]*jrpc2.Client
	encryptedTunnel     bool
	hostLockTimeout     time.Duration
	retryPolicy         RetryPolicy
	executorLayout      ExecutorLayout
	mutationCallTimeout time.Duration
	usePostQuantum      bool
	dualVerification    bool
	journalKey          []byte
	supportPolicy       supportpolicy.Policy
	runtimeMu           sync.Mutex
	sessionRuntimes     map[string]*sessionRuntime

	// sentPlugins tracks which plugins have already been sent to which
	// sessions (keyed by host address) during this provider run.
	mu           sync.Mutex
	sentPlugins  map[string]map[string]bool // address → plugin name → sent
	pluginLoads  map[string]map[string]*pluginLoadAttempt
	hostProfiles map[string]hostrpc.HostProfile // session key -> discovered host profile
}

// NewExecutorManager creates a new ExecutorManager with the given connection
// pool and runtime asset store.
func NewExecutorManager(pool *transport.ConnectionPool, assetStore assets.Store) *ExecutorManager {
	if assetStore == nil {
		assetStore = assets.NewMemoryStore(assets.Spec{}, nil, nil)
	}

	return &ExecutorManager{
		pool:                pool,
		assets:              assetStore,
		rpcClients:          make(map[string]*jrpc2.Client),
		encryptedTunnel:     true,
		sentPlugins:         make(map[string]map[string]bool),
		pluginLoads:         make(map[string]map[string]*pluginLoadAttempt),
		hostProfiles:        make(map[string]hostrpc.HostProfile),
		retryPolicy:         defaultRetryPolicy(),
		executorLayout:      defaultExecutorLayout(),
		mutationCallTimeout: defaultMutationCallTimeout,
		journalKey:          newJournalKey(),
		supportPolicy:       supportpolicy.Policy{ID: "unspecified", AllowAny: true},
		sessionRuntimes:     make(map[string]*sessionRuntime),
	}
}

func newJournalKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Sprintf("generate journal key: %v", err))
	}
	return key
}

func defaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:    defaultRetryAttempts,
		InitialBackoff: defaultRetryInitialBackoff,
		MaxBackoff:     defaultRetryMaxBackoff,
	}
}

func normalizeRetryPolicy(policy RetryPolicy) RetryPolicy {
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = defaultRetryAttempts
	}
	if policy.InitialBackoff <= 0 {
		policy.InitialBackoff = defaultRetryInitialBackoff
	}
	if policy.MaxBackoff <= 0 {
		policy.MaxBackoff = defaultRetryMaxBackoff
	}
	if policy.MaxBackoff < policy.InitialBackoff {
		policy.MaxBackoff = policy.InitialBackoff
	}
	return policy
}

func defaultExecutorLayout() ExecutorLayout {
	return ExecutorLayout{BasePath: defaultExecutorBasePath}
}

func normalizeExecutorLayout(layout ExecutorLayout) ExecutorLayout {
	layout.BasePath = strings.TrimSpace(layout.BasePath)
	if layout.BasePath == "" {
		return defaultExecutorLayout()
	}
	if layout.BasePath != "/" {
		layout.BasePath = strings.TrimRight(layout.BasePath, "/")
	}
	return layout
}

func (m *ExecutorManager) SetRetryPolicy(policy RetryPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retryPolicy = normalizeRetryPolicy(policy)
}

func (m *ExecutorManager) SetExecutorLayout(layout ExecutorLayout) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executorLayout = normalizeExecutorLayout(layout)
}

func (m *ExecutorManager) SetHostLockTimeout(timeout time.Duration) {
	m.hostLockTimeout = timeout
}

func (m *ExecutorManager) SetEncryptedTunnelEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.encryptedTunnel = enabled
}

func (m *ExecutorManager) SetUsePostQuantumDigests(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usePostQuantum = enabled
}

func (m *ExecutorManager) SetDualPluginVerification(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dualVerification = enabled
}

func (m *ExecutorManager) SetSupportPolicy(policy supportpolicy.Policy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.supportPolicy = policy
}

func (m *ExecutorManager) getRetryPolicy() RetryPolicy {
	m.mu.Lock()
	defer m.mu.Unlock()
	return normalizeRetryPolicy(m.retryPolicy)
}

func (m *ExecutorManager) getExecutorLayout() ExecutorLayout {
	m.mu.Lock()
	defer m.mu.Unlock()
	return normalizeExecutorLayout(m.executorLayout)
}

func (m *ExecutorManager) encryptedTunnelEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.encryptedTunnel
}

func (m *ExecutorManager) pluginVerificationOptions() (bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usePostQuantum, m.dualVerification
}

// executorHash returns the SHA-256 used to verify the cached executor binary on
// the host before the executor itself is running.
func (m *ExecutorManager) executorHash(arch string) (string, error) {
	asset, err := m.assets.ExecutorBinary(arch)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(asset.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

// executorDir returns the cache directory path on the target for a given hash.
func (m *ExecutorManager) executorDir(hash string) string {
	layout := m.getExecutorLayout()
	return fmt.Sprintf("%s-%s", layout.BasePath, executorCacheKey(hash))
}

// executorPath returns the full path to the executor binary on the target.
func (m *ExecutorManager) executorPath(hash string) string {
	return fmt.Sprintf("%s/%s", m.executorDir(hash), executorBinName)
}

func executorCacheKey(digest string) string {
	token := strings.TrimSpace(digest)
	if _, encoded, ok := strings.Cut(token, ":"); ok && encoded != "" {
		token = encoded
	}
	token = assets.DigestToken(token)
	if len(token) > executorCacheKeyLength {
		return token[:executorCacheKeyLength]
	}
	return token
}

// EnsureExecutor pushes the executor binary to the target if it is not
// already cached, starts the executor process, and stores the stdin/stdout
// handles on the session.
func (m *ExecutorManager) EnsureExecutor(ctx context.Context, session *transport.Session) error {
	session.AcquireUse()
	defer session.ReleaseUse()

	addr := sessionKey(session)
	policy := m.currentSupportPolicy()
	if session.Stdin == nil || session.Stdout == nil {
		m.resetSession(addr, session, nil)
	}
	if client := m.getRPCClient(addr); client != nil {
		if policy.AllowAny {
			return nil
		}
		if _, ok := m.getHostProfile(addr); ok {
			return nil
		}
		_, err := m.discover(ctx, session, client)
		return err
	}

	runtime := m.sessionRuntime(session)
	leader, waitErr, finish := runtime.beginEnsure(ctx)
	if !leader {
		if waitErr != nil {
			return waitErr
		}
		if m.getRPCClient(addr) != nil {
			return nil
		}
		return fmt.Errorf("executor ensure completed without an RPC client")
	}

	var ensureErr error
	defer func() {
		finish(ensureErr)
	}()

	if m.getRPCClient(addr) != nil {
		if policy.AllowAny {
			return nil
		}
		if _, ok := m.getHostProfile(addr); ok {
			return nil
		}
		client := m.getRPCClient(addr)
		if client == nil {
			return fmt.Errorf("executor ensure completed without an RPC client")
		}
		_, err := m.discover(ctx, session, client)
		return err
	}

	if session.Stdin == nil || session.Stdout == nil {
		arch, err := session.Transport.TargetArch(ctx)
		if err != nil {
			ensureErr = fmt.Errorf("detect target arch: %w", err)
			return ensureErr
		}

		asset, err := m.assets.ExecutorBinary(arch)
		if err != nil {
			ensureErr = err
			return err
		}

		hash, err := m.executorHash(arch)
		if err != nil {
			ensureErr = err
			return err
		}

		binPath := m.executorPath(hash)

		if !m.isExecutorCached(ctx, session, binPath, hash) {
			if err := session.Transport.PushFile(ctx, binPath, asset.Bytes, 0755); err != nil {
				ensureErr = fmt.Errorf("push executor binary: %w", err)
				return ensureErr
			}
		}

		stdin, stdout, err := session.Transport.StartProcess(ctx, fmt.Sprintf("%s --serve --encrypted-tunnel=%t", binPath, m.encryptedTunnelEnabled()))
		if err != nil {
			ensureErr = fmt.Errorf("start executor: %w", err)
			return ensureErr
		}

		session.Stdin = stdin
		session.Stdout = stdout
	}

	client, err := newRPCClient(session, m.encryptedTunnelEnabled())
	if err != nil {
		ensureErr = fmt.Errorf("create executor RPC client: %w", err)
		return ensureErr
	}
	if !policy.AllowAny {
		if _, err := m.discover(ctx, session, client); err != nil {
			_ = client.Close()
			ensureErr = fmt.Errorf("executor startup failed: %w", err)
			return ensureErr
		}
	}
	if err := m.configureJournalKey(ctx, client); err != nil {
		_ = client.Close()
		ensureErr = fmt.Errorf("configure executor journal key: %w", err)
		return ensureErr
	}

	m.setRPCClient(addr, client)

	return nil
}

// isExecutorCached checks whether the executor binary at the given path on the
// target matches the expected hash.
func (m *ExecutorManager) isExecutorCached(ctx context.Context, session *transport.Session, binPath, expectedHash string) bool {
	// Run sha256sum on the target and compare output.
	checkCmd := fmt.Sprintf("sha256sum %s 2>/dev/null", binPath)
	stdin, stdout, err := session.Transport.StartProcess(ctx, checkCmd)
	if err != nil {
		return false
	}
	defer stdin.Close()

	scanner := bufio.NewScanner(stdout)
	if scanner.Scan() {
		line := scanner.Text()
		// sha256sum output format: "<hash>  <path>"
		parts := strings.Fields(line)
		if len(parts) >= 1 && parts[0] == expectedHash {
			stdout.Close()
			return true
		}
	}

	stdout.Close()
	return false
}

// SendPlugin streams a WASM plugin to the executor if it has not already been
// sent during this session.
func (m *ExecutorManager) SendPlugin(ctx context.Context, session *transport.Session, pluginName string) error {
	session.AcquireUse()
	defer session.ReleaseUse()

	if session.Stdin == nil {
		return fmt.Errorf("executor not running on session; call EnsureExecutor first")
	}

	addr := sessionKey(session)
	for {
		m.mu.Lock()
		if m.sentPlugins[addr] != nil && m.sentPlugins[addr][pluginName] {
			m.mu.Unlock()
			return nil
		}
		if attempts := m.pluginLoads[addr]; attempts != nil {
			if attempt := attempts[pluginName]; attempt != nil {
				m.mu.Unlock()
				if err := attempt.wait(ctx); err != nil {
					return err
				}
				continue
			}
		}
		if m.pluginLoads[addr] == nil {
			m.pluginLoads[addr] = make(map[string]*pluginLoadAttempt)
		}
		attempt := newPluginLoadAttempt()
		m.pluginLoads[addr][pluginName] = attempt
		m.mu.Unlock()

		err := m.sendPluginOnce(ctx, session, pluginName)
		m.finishPluginLoad(addr, pluginName, attempt, err)
		return err
	}
}

func (m *ExecutorManager) sendPluginOnce(ctx context.Context, session *transport.Session, pluginName string) error {
	addr := sessionKey(session)

	asset, err := m.assets.PluginModule(pluginName)
	if err != nil {
		return err
	}

	client, err := m.clientForSession(session)
	if err != nil {
		return err
	}
	usePostQuantum, dualVerification := m.pluginVerificationOptions()

	params := hostrpc.ModuleLoadParams{
		Name:                   pluginName,
		UsePostQuantumDigests:  usePostQuantum,
		DualPluginVerification: dualVerification,
		WasmCompression:        asset.Compression,
		Wasm:                   asset.Bytes,
	}

	var result hostrpc.ModuleLoadResult
	if err := client.CallResult(ctx, hostrpc.MethodModuleLoad, params, &result); err != nil {
		return fmt.Errorf("send plugin %q: %w", pluginName, err)
	}

	// Mark as sent.
	m.mu.Lock()
	if m.sentPlugins[addr] == nil {
		m.sentPlugins[addr] = make(map[string]bool)
	}
	m.sentPlugins[addr][pluginName] = true
	m.mu.Unlock()

	return nil
}

func (m *ExecutorManager) finishPluginLoad(addr, pluginName string, attempt *pluginLoadAttempt, err error) {
	m.mu.Lock()
	if attempts := m.pluginLoads[addr]; attempts != nil {
		if current := attempts[pluginName]; current == attempt {
			delete(attempts, pluginName)
		}
		if len(attempts) == 0 {
			delete(m.pluginLoads, addr)
		}
	}
	attempt.err = err
	close(attempt.done)
	m.mu.Unlock()
}

// SendOperation sends an operation to the executor and returns the result.
func (m *ExecutorManager) SendOperation(ctx context.Context, session *transport.Session, op OperationMessage) (*ResultMessage, error) {
	method, err := hostrpc.MethodForAction(op.Action)
	if err != nil {
		return nil, err
	}

	var rpcResult hostrpc.OperationResult
	callErr := m.withRetries(ctx, session, op.ModuleName, m.timeoutForOperationAction(op.Action), nil, func(callCtx context.Context, client *jrpc2.Client) error {
		switch method {
		case hostrpc.MethodResourceValidate:
			configRaw, marshalError := marshalJSON(op.Config)
			if marshalError != nil {
				return marshalError
			}
			params := hostrpc.ResourceValidateParams{
				ModuleName:   op.ModuleName,
				ResourceType: op.ResourceType,
				Config:       configRaw,
				Execution:    op.Execution,
			}
			return client.CallResult(callCtx, method, params, &rpcResult)
		case hostrpc.MethodResourceRead:
			stateRaw, marshalError := marshalJSON(op.State)
			if marshalError != nil {
				return marshalError
			}
			params := hostrpc.ResourceReadParams{
				ModuleName:   op.ModuleName,
				ResourceType: op.ResourceType,
				State:        stateRaw,
				Execution:    op.Execution,
			}
			return client.CallResult(callCtx, method, params, &rpcResult)
		case hostrpc.MethodResourceCreate:
			planRaw, marshalError := marshalJSON(op.Plan)
			if marshalError != nil {
				return marshalError
			}
			params := hostrpc.ResourceCreateParams{
				ModuleName:   op.ModuleName,
				ResourceType: op.ResourceType,
				Plan:         planRaw,
				Execution:    op.Execution,
			}
			return client.CallResult(callCtx, method, params, &rpcResult)
		case hostrpc.MethodResourceUpdate:
			stateRaw, stateErr := marshalJSON(op.State)
			planRaw, planErr := marshalJSON(op.Plan)
			if stateErr != nil {
				return stateErr
			}
			if planErr != nil {
				return planErr
			}
			params := hostrpc.ResourceUpdateParams{
				ModuleName:   op.ModuleName,
				ResourceType: op.ResourceType,
				State:        stateRaw,
				Plan:         planRaw,
				Execution:    op.Execution,
			}
			return client.CallResult(callCtx, method, params, &rpcResult)
		case hostrpc.MethodResourceDelete:
			stateRaw, marshalError := marshalJSON(op.State)
			if marshalError != nil {
				return marshalError
			}
			params := hostrpc.ResourceDeleteParams{
				ModuleName:   op.ModuleName,
				ResourceType: op.ResourceType,
				State:        stateRaw,
				Execution:    op.Execution,
			}
			return client.CallResult(callCtx, method, params, &rpcResult)
		case hostrpc.MethodResourceImport:
			params := hostrpc.ResourceImportParams{
				ModuleName:   op.ModuleName,
				ResourceType: op.ResourceType,
				ImportID:     op.ImportID,
				Execution:    op.Execution,
			}
			return client.CallResult(callCtx, method, params, &rpcResult)
		case hostrpc.MethodDataSourceRead:
			configRaw, marshalError := marshalJSON(op.Config)
			if marshalError != nil {
				return marshalError
			}
			params := hostrpc.DataSourceReadParams{
				ModuleName:   op.ModuleName,
				ResourceType: op.ResourceType,
				Config:       configRaw,
				Execution:    op.Execution,
			}
			return client.CallResult(callCtx, method, params, &rpcResult)
		default:
			return fmt.Errorf("unsupported RPC method %q", method)
		}
	})
	if callErr != nil {
		return nil, fmt.Errorf("%s: %w", method, callErr)
	}

	state, err := decodeState(rpcResult.State)
	if err != nil {
		return nil, err
	}

	return &ResultMessage{State: state}, nil
}

func (m *ExecutorManager) timeoutForOperationAction(action string) time.Duration {
	switch action {
	case "create", "update", "delete":
		return m.mutationCallTimeout
	default:
		return 0
	}
}

func (m *ExecutorManager) SendOperationLocked(ctx context.Context, session *transport.Session, op OperationMessage, lockSet []LockDescriptor) (*ResultMessage, error) {
	lease, err := m.acquireOperationLease(ctx, session, OperationMetadata{
		ModuleName:   op.ModuleName,
		ResourceType: op.ResourceType,
		Action:       op.Action,
	}, lockSet)
	if err != nil {
		return nil, err
	}

	result, callErr := m.SendOperation(ctx, session, op)
	if releaseErr := lease.Complete(callErr); releaseErr != nil {
		if callErr == nil {
			return result, fmt.Errorf("release operation lease: %w", releaseErr)
		}
		return result, fmt.Errorf("%w; release operation lease: %v", callErr, releaseErr)
	}

	return result, callErr
}

func (m *ExecutorManager) InvokeAction(ctx context.Context, session *transport.Session, params hostrpc.ActionInvokeParams) (map[string]interface{}, error) {
	var rpcResult hostrpc.OperationResult
	callErr := m.withRetries(ctx, session, params.ModuleName, 0, nil, func(callCtx context.Context, client *jrpc2.Client) error {
		return client.CallResult(callCtx, hostrpc.MethodActionInvoke, params, &rpcResult)
	})
	if callErr != nil {
		return nil, fmt.Errorf("%s: %w", hostrpc.MethodActionInvoke, callErr)
	}

	state, err := decodeState(rpcResult.State)
	if err != nil {
		return nil, err
	}

	return state, nil
}

func (m *ExecutorManager) InvokeActionLocked(ctx context.Context, session *transport.Session, params hostrpc.ActionInvokeParams, lockSet []LockDescriptor) (map[string]interface{}, error) {
	lease, err := m.acquireOperationLease(ctx, session, OperationMetadata{
		ModuleName:   params.ModuleName,
		ResourceType: params.ResourceType,
		Action:       hostrpc.MethodActionInvoke,
	}, lockSet)
	if err != nil {
		return nil, err
	}

	result, callErr := m.InvokeAction(ctx, session, params)
	if releaseErr := lease.Complete(callErr); releaseErr != nil {
		if callErr == nil {
			return result, fmt.Errorf("release operation lease: %w", releaseErr)
		}
		return result, fmt.Errorf("%w; release operation lease: %v", callErr, releaseErr)
	}

	return result, callErr
}

// Shutdown sends a shutdown message to the executor process.
func (m *ExecutorManager) Shutdown(ctx context.Context, session *transport.Session) error {
	if session.Stdin == nil {
		return nil
	}

	client, err := m.clientForSession(session)
	if err == nil {
		err = client.Notify(ctx, hostrpc.MethodExecutorShutdown, nil)
	}

	if resetErr := m.resetSessionSafely(ctx, session, nil, false, false, ""); resetErr != nil && err == nil {
		err = resetErr
	}
	if err != nil {
		return fmt.Errorf("send shutdown: %w", err)
	}

	return nil
}

// RestartProcess invokes the executor-side restart action for a process or service.
func (m *ExecutorManager) RestartProcess(ctx context.Context, session *transport.Session, params hostrpc.RestartProcessParams) (*hostrpc.CommandResult, error) {
	if params.OperationID == "" {
		params.OperationID = newOperationID()
	}

	var result hostrpc.CommandResult
	callErr := m.withRetries(ctx, session, params.ModuleName, 0, nil, func(callCtx context.Context, client *jrpc2.Client) error {
		return client.CallResult(callCtx, hostrpc.MethodActionRestart, params, &result)
	})
	if callErr != nil {
		return nil, fmt.Errorf("%s: %w", hostrpc.MethodActionRestart, callErr)
	}

	return &result, nil
}

func (m *ExecutorManager) HostCommand(ctx context.Context, session *transport.Session, params hostrpc.HostCommandParams) (*hostrpc.CommandResult, error) {
	var result hostrpc.CommandResult
	callErr := m.withRetries(ctx, session, "", 0, nil, func(callCtx context.Context, client *jrpc2.Client) error {
		return client.CallResult(callCtx, hostrpc.MethodHostCommand, params, &result)
	})
	if callErr != nil {
		return nil, fmt.Errorf("%s: %w", hostrpc.MethodHostCommand, callErr)
	}

	return &result, nil
}

func (m *ExecutorManager) HostCommandLocked(ctx context.Context, session *transport.Session, params hostrpc.HostCommandParams, lockSet []LockDescriptor) (*hostrpc.CommandResult, error) {
	lease, err := m.acquireOperationLease(ctx, session, OperationMetadata{
		Action: hostrpc.MethodHostCommand,
		Name:   params.Name,
	}, lockSet)
	if err != nil {
		return nil, err
	}

	result, callErr := m.HostCommand(ctx, session, params)
	if releaseErr := lease.Complete(callErr); releaseErr != nil {
		if callErr == nil {
			return result, fmt.Errorf("release operation lease: %w", releaseErr)
		}
		return result, fmt.Errorf("%w; release operation lease: %v", callErr, releaseErr)
	}

	return result, callErr
}

func (m *ExecutorManager) RestartProcessLocked(ctx context.Context, session *transport.Session, params hostrpc.RestartProcessParams, lockSet []LockDescriptor) (*hostrpc.CommandResult, error) {
	lease, err := m.acquireOperationLease(ctx, session, OperationMetadata{
		ModuleName: params.ModuleName,
		Action:     hostrpc.MethodActionRestart,
		Name:       params.Name,
	}, lockSet)
	if err != nil {
		return nil, err
	}

	result, callErr := m.RestartProcess(ctx, session, params)
	if releaseErr := lease.Complete(callErr); releaseErr != nil {
		if callErr == nil {
			return result, fmt.Errorf("release operation lease: %w", releaseErr)
		}
		return result, fmt.Errorf("%w; release operation lease: %v", callErr, releaseErr)
	}

	return result, callErr
}

func (m *ExecutorManager) ensureExecutorForAction(ctx context.Context, session *transport.Session, moduleName string) error {
	session.AcquireUse()
	defer session.ReleaseUse()

	session.BootstrapMu.Lock()
	defer session.BootstrapMu.Unlock()

	if err := m.EnsureExecutor(ctx, session); err != nil {
		_ = m.resetSessionSafely(ctx, session, nil, false, true, "")
		return fmt.Errorf("ensure executor: %w", err)
	}

	if moduleName != "" {
		if err := m.SendPlugin(ctx, session, moduleName); err != nil {
			_ = m.resetSessionSafely(ctx, session, nil, false, true, "")
			return fmt.Errorf("send plugin %s: %w", moduleName, err)
		}
	}

	return nil
}

func (m *ExecutorManager) prepareClientForAction(ctx context.Context, session *transport.Session, moduleName string) (*jrpc2.Client, func(), error) {
	session.AcquireUse()
	runtime := m.sessionRuntime(session)

	session.BootstrapMu.Lock()
	locked := true
	unlock := func() {
		if locked {
			session.BootstrapMu.Unlock()
			locked = false
		}
	}
	cleanupSession := func() {
		unlock()
		session.ReleaseUse()
	}

	if err := m.EnsureExecutor(ctx, session); err != nil {
		_ = m.resetSessionSafely(ctx, session, nil, false, true, "")
		cleanupSession()
		return nil, nil, fmt.Errorf("ensure executor: %w", err)
	}

	if moduleName != "" {
		if err := m.SendPlugin(ctx, session, moduleName); err != nil {
			_ = m.resetSessionSafely(ctx, session, nil, false, true, "")
			cleanupSession()
			return nil, nil, fmt.Errorf("send plugin %s: %w", moduleName, err)
		}
	}

	// Reserve the session before releasing BootstrapMu so a concurrent reset
	// cannot tear down executor stdio between bootstrap and client acquisition.
	runtime.reserveCall()
	client, err := m.clientForSession(session)
	if err != nil {
		runtime.releaseCall()
		_ = m.resetSessionSafely(ctx, session, nil, false, true, "")
		cleanupSession()
		return nil, nil, err
	}

	unlock()
	return client, func() {
		runtime.releaseCall()
		session.ReleaseUse()
	}, nil
}

func (m *ExecutorManager) withRetries(ctx context.Context, session *transport.Session, moduleName string, callTimeout time.Duration, onRetry func(error, time.Time) error, call func(context.Context, *jrpc2.Client) error) error {
	policy := m.getRetryPolicy()
	var lastErr error
	currentSession := session

	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
		attemptCtx, cancel := contextForAttempt(ctx, callTimeout)
		client, release, prepareErr := m.prepareClientForAction(attemptCtx, currentSession, moduleName)
		if prepareErr != nil {
			lastErr = prepareErr
			var violation *supportpolicy.ViolationError
			if errors.As(prepareErr, &violation) {
				cancel()
				return prepareErr
			}
		} else if err := call(attemptCtx, client); err == nil {
			release()
			cancel()
			return nil
		} else {
			release()
			lastErr = err
		}
		cancel()

		if attempt == policy.MaxAttempts-1 {
			break
		}

		backoff := retryBackoff(policy, attempt)
		if onRetry != nil {
			if err := onRetry(lastErr, time.Now().UTC().Add(backoff)); err != nil {
				return err
			}
		}

		recoverErr := m.resetSessionSafely(ctx, currentSession, nil, m.pool != nil, false, reconnectReason(lastErr))
		if recoverErr != nil {
			lastErr = recoverErr
			if m.pool != nil {
				replacement, replacementErr := m.pool.GetOrCreate(ctx, currentSession.Config)
				if replacementErr != nil {
					lastErr = fmt.Errorf("reacquire session: %w", replacementErr)
				} else {
					currentSession = replacement
				}
			}
		}
		if m.pool == nil {
			break
		}

		if err := sleepWithContext(ctx, backoff); err != nil {
			return err
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("executor call failed after %d attempts", policy.MaxAttempts)
	}

	return fmt.Errorf("retry budget exhausted after %d attempts: %w", policy.MaxAttempts, lastErr)
}

func contextForAttempt(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func reconnectReason(err error) string {
	if err == nil {
		return ""
	}

	reason := strings.TrimSpace(err.Error())
	if len(reason) > 240 {
		reason = reason[:240] + "..."
	}
	return reason
}

func retryBackoff(policy RetryPolicy, attempt int) time.Duration {
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

func (m *ExecutorManager) discover(ctx context.Context, session *transport.Session, client *jrpc2.Client) (hostrpc.HostProfile, error) {
	var profile hostrpc.HostProfile
	if err := client.CallResult(ctx, hostrpc.MethodExecutorDiscover, nil, &profile); err != nil {
		return hostrpc.HostProfile{}, err
	}
	if err := m.currentSupportPolicy().Check(profile); err != nil {
		return hostrpc.HostProfile{}, err
	}
	m.setHostProfile(sessionKey(session), profile)

	return profile, nil
}

func (m *ExecutorManager) clientForSession(session *transport.Session) (*jrpc2.Client, error) {
	if session.Stdin == nil || session.Stdout == nil {
		return nil, fmt.Errorf("executor not running on session; call EnsureExecutor first")
	}

	addr := sessionKey(session)
	if client := m.getRPCClient(addr); client != nil {
		return client, nil
	}

	client, err := newRPCClient(session, m.encryptedTunnelEnabled())
	if err != nil {
		return nil, err
	}
	m.setRPCClient(addr, client)
	return client, nil
}

func (m *ExecutorManager) acquireOperationLease(ctx context.Context, session *transport.Session, metadata OperationMetadata, lockSet []LockDescriptor) (*operationLease, error) {
	hostKey := hostKeyForConfig(session.Config)
	requestID := newOperationID()
	params := hostrpc.OperationAcquireParams{
		RequestID:    requestID,
		HostKey:      hostKey,
		SessionKey:   schedulerSessionKey(session.Config),
		ModuleName:   metadata.ModuleName,
		ResourceType: metadata.ResourceType,
		Action:       metadata.Action,
		Name:         metadata.Name,
		LockSet:      toRPCLockSet(lockSet),
		TimeoutMs:    int64(m.hostLockTimeout / time.Millisecond),
	}

	started := time.Now()
	for {
		var result hostrpc.OperationAcquireResult
		if err := m.withRetries(ctx, session, "", 0, nil, func(callCtx context.Context, client *jrpc2.Client) error {
			return client.CallResult(callCtx, hostrpc.MethodJournalOperationAcquire, params, &result)
		}); err != nil {
			return nil, fmt.Errorf("acquire host operation lease: %w", err)
		}

		if result.Granted {
			return &operationLease{complete: func(lastErr error) error {
				return m.releaseOperationLease(session, hostKey, result.OperationID, lastErr)
			}}, nil
		}

		if err := ctx.Err(); err != nil {
			releaseErr := m.releaseOperationLease(session, hostKey, result.OperationID, err)
			if releaseErr != nil {
				return nil, fmt.Errorf("acquire host operation lease: %w; release queued lease: %v", err, releaseErr)
			}
			return nil, fmt.Errorf("acquire host operation lease: %w", err)
		}

		if m.hostLockTimeout > 0 && time.Since(started) >= m.hostLockTimeout {
			err := fmt.Errorf("timed out waiting for host operation lease after %s", m.hostLockTimeout)
			releaseErr := m.releaseOperationLease(session, hostKey, result.OperationID, err)
			if releaseErr != nil {
				return nil, fmt.Errorf("acquire host operation lease: %w; release queued lease: %v", err, releaseErr)
			}
			return nil, fmt.Errorf("acquire host operation lease: %w", err)
		}

		if err := sleepWithContext(ctx, operationAcquirePollDelay); err != nil {
			releaseErr := m.releaseOperationLease(session, hostKey, result.OperationID, err)
			if releaseErr != nil {
				return nil, fmt.Errorf("acquire host operation lease: %w; release queued lease: %v", err, releaseErr)
			}
			return nil, fmt.Errorf("acquire host operation lease: %w", err)
		}
	}
}

func (m *ExecutorManager) releaseOperationLease(session *transport.Session, hostKey, operationID string, lastErr error) error {
	if strings.TrimSpace(operationID) == "" {
		return nil
	}

	releaseParams := hostrpc.OperationReleaseParams{
		HostKey:     hostKey,
		OperationID: operationID,
		Status:      string(operationStatusCompleted),
	}
	if lastErr != nil {
		releaseParams.Status = string(operationStatusFailed)
		releaseParams.LastError = lastErr.Error()
	}

	releaseCtx, cancel := context.WithTimeout(context.Background(), operationReleaseTimeout(lastErr))
	defer cancel()

	return m.withRetries(releaseCtx, session, "", 0, nil, func(callCtx context.Context, client *jrpc2.Client) error {
		_, err := client.Call(callCtx, hostrpc.MethodJournalOperationRelease, releaseParams)
		return err
	})
}

func operationReleaseTimeout(lastErr error) time.Duration {
	if errors.Is(lastErr, context.DeadlineExceeded) || errors.Is(lastErr, context.Canceled) {
		return terminalOperationReleaseTimeout
	}
	return defaultOperationReleaseTimeout
}

func (m *ExecutorManager) AcquireOperationLease(ctx context.Context, session *transport.Session, metadata OperationMetadata, lockSet []LockDescriptor) (func(error) error, error) {
	lease, err := m.acquireOperationLease(ctx, session, metadata, lockSet)
	if err != nil {
		return nil, err
	}
	return lease.Complete, nil
}

func (m *ExecutorManager) acquireClientLease(ctx context.Context, session *transport.Session) (*jrpc2.Client, func(), error) {
	session.AcquireUse()
	runtime := m.sessionRuntime(session)
	if err := runtime.acquireCall(ctx); err != nil {
		session.ReleaseUse()
		return nil, nil, err
	}

	client, err := m.clientForSession(session)
	if err != nil {
		runtime.releaseCall()
		session.ReleaseUse()
		return nil, nil, err
	}

	return client, func() {
		runtime.releaseCall()
		session.ReleaseUse()
	}, nil
}

func (m *ExecutorManager) resetSessionSafely(ctx context.Context, session *transport.Session, client *jrpc2.Client, reconnect, bootstrapHeld bool, reconnectReason string) error {
	runtime := m.sessionRuntime(session)
	leader, waitErr, finish := runtime.beginReset(ctx)
	if !leader {
		return waitErr
	}

	var resetErr error
	defer func() {
		finish(resetErr)
	}()

	if !bootstrapHeld {
		session.BootstrapMu.Lock()
		defer session.BootstrapMu.Unlock()
	}

	if err := runtime.waitForNoActiveCalls(ctx); err != nil {
		resetErr = err
		return err
	}

	m.resetSession(sessionKey(session), session, client)
	if reconnect && m.pool != nil {
		if err := m.pool.ReconnectWithReason(ctx, session, reconnectReason); err != nil {
			resetErr = fmt.Errorf("reconnect session: %w", err)
			return resetErr
		}
	}

	return nil
}

func (m *ExecutorManager) sessionRuntime(session *transport.Session) *sessionRuntime {
	key := sessionKey(session)

	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()

	if runtime := m.sessionRuntimes[key]; runtime != nil {
		return runtime
	}

	runtime := newSessionRuntime()
	m.sessionRuntimes[key] = runtime
	return runtime
}

func (m *ExecutorManager) getRPCClient(addr string) *jrpc2.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rpcClients[addr]
}

func (m *ExecutorManager) setRPCClient(addr string, client *jrpc2.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing := m.rpcClients[addr]; existing != nil && existing != client {
		_ = existing.Close()
	}

	m.rpcClients[addr] = client
}

func (m *ExecutorManager) getHostProfile(addr string) (hostrpc.HostProfile, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	profile, ok := m.hostProfiles[addr]
	return profile, ok
}

func (m *ExecutorManager) setHostProfile(addr string, profile hostrpc.HostProfile) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hostProfiles[addr] = profile
}

func (m *ExecutorManager) currentSupportPolicy() supportpolicy.Policy {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.supportPolicy
}

func (m *ExecutorManager) resetSession(addr string, session *transport.Session, client *jrpc2.Client) {
	m.mu.Lock()
	stored := m.rpcClients[addr]
	delete(m.rpcClients, addr)
	delete(m.sentPlugins, addr)
	delete(m.pluginLoads, addr)
	delete(m.hostProfiles, addr)
	m.mu.Unlock()

	m.sessionRuntime(session).clearReadiness()

	if client != nil {
		_ = client.Close()
	} else if stored != nil {
		_ = stored.Close()
	}

	if session.Stdin != nil {
		_ = session.Stdin.Close()
		session.Stdin = nil
	}
	if session.Stdout != nil {
		_ = session.Stdout.Close()
		session.Stdout = nil
	}
}

// sessionKey returns a stable key for a session, used for tracking sent plugins.
func sessionKey(session *transport.Session) string {
	// Use the address of the Session struct as a unique key.
	return fmt.Sprintf("%p", session)
}

func newRPCClient(session *transport.Session, encryptedTunnel bool) (*jrpc2.Client, error) {
	ch, err := hostrpc.NewClientChannel(session.Stdout, session.Stdin, hostrpc.ChannelOptions{EncryptedTunnel: encryptedTunnel})
	if err != nil {
		return nil, err
	}
	return jrpc2.NewClient(ch, nil), nil
}

func hostKeyForConfig(config transport.TransportConfig) string {
	cacheKey := strings.TrimSpace(config.CacheKey())
	if cacheKey == "" || strings.EqualFold(cacheKey, transport.TransportLocal) {
		return "local"
	}
	return cacheKey
}

func (m *ExecutorManager) configureJournalKey(ctx context.Context, client *jrpc2.Client) error {
	params := hostrpc.JournalConfigureParams{Key: append([]byte(nil), m.journalKey...)}
	_, err := client.Call(ctx, hostrpc.MethodJournalConfigure, params)
	return err
}

func toRPCLockSet(lockSet []LockDescriptor) []hostrpc.LockDescriptor {
	if len(lockSet) == 0 {
		return nil
	}
	result := make([]hostrpc.LockDescriptor, 0, len(lockSet))
	for _, lock := range lockSet {
		result = append(result, hostrpc.LockDescriptor{
			Key:    lock.Key,
			Mode:   hostrpc.LockMode(lock.Mode),
			Source: lock.Source,
		})
	}
	return result
}

func schedulerSessionKey(config transport.TransportConfig) string {
	return strings.Join([]string{
		hostKeyForConfig(config),
	}, "|")
}

func marshalJSON(value map[string]interface{}) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}

	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal RPC payload: %w", err)
	}

	return json.RawMessage(data), nil
}

func mustMarshalJSON(value map[string]interface{}) json.RawMessage {
	data, _ := marshalJSON(value)
	return data
}

func decodeState(raw json.RawMessage) (map[string]interface{}, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var state map[string]interface{}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode executor state: %w", err)
	}

	return state, nil
}

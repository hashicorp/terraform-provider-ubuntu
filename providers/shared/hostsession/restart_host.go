// Copyright IBM Corp. 2026

package hostsession

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

const (
	defaultRebootTimeout             = 15 * time.Minute
	defaultRebootSettle              = 15 * time.Second
	maxRebootReconnectAttempt        = 20 * time.Second
	terminalRebootFailureMarkTimeout = 3 * time.Second
	rebootRecordDir                  = "/var/lib/tf-linux-provider/reboots"
)

type RestartHostParams struct {
	Name          string
	Reason        string
	RebootCommand string
	Timeout       time.Duration
	Settle        time.Duration
	OperationType string
}

type RebootBarrierParams struct {
	ResourceID    string
	Reason        string
	TriggersHash  string
	RebootCommand string
	Timeout       time.Duration
	Settle        time.Duration
}

type RestartHostResult struct {
	OperationID   string
	Phase         string
	StableHostID  string
	PreBootID     string
	PostBootID    string
	CompletedAt   *time.Time
	LastError     string
	HostAddress   string
	Reason        string
	RebootCommand string
}

type HostIdentity struct {
	StableHostID string
	BootID       string
}

func (m *ExecutorManager) RestartHost(ctx context.Context, session *transport.Session, params RestartHostParams) error {
	_, err := m.RestartHostWithResult(ctx, session, params)
	return err
}

func (m *ExecutorManager) RestartHostWithResult(ctx context.Context, session *transport.Session, params RestartHostParams) (*RestartHostResult, error) {
	params = normalizeRestartHostParams(params)
	actionType := params.OperationType
	if actionType == "" {
		actionType = "action.restart_host"
	}
	leaseCtx := ctx
	leaseCancel := func() {}
	if params.Timeout > 0 {
		leaseCtx, leaseCancel = context.WithTimeout(ctx, params.Timeout)
	}
	defer leaseCancel()

	lease, err := m.acquireOperationLease(leaseCtx, session, OperationMetadata{
		Action: actionType,
		Name:   params.Name,
	}, []LockDescriptor{{Key: "reboot:host", Mode: LockModeExclusive, Source: "restart host action"}, {Key: "host", Mode: LockModeExclusive, Source: "restart host action"}})
	if err != nil {
		return nil, err
	}

	result, callErr := m.restartHostUnsafe(ctx, session, params)
	if releaseErr := lease.Complete(callErr); releaseErr != nil {
		if callErr == nil {
			return result, fmt.Errorf("release operation lease: %w", releaseErr)
		}
		return result, fmt.Errorf("%w; release operation lease: %v", callErr, releaseErr)
	}

	return result, callErr
}

func (m *ExecutorManager) RunRebootBarrier(ctx context.Context, session *transport.Session, params RebootBarrierParams) (*RestartHostResult, error) {
	return m.RestartHostWithResult(ctx, session, RestartHostParams{
		Name:          rebootBarrierOperationName(params.ResourceID, params.TriggersHash),
		Reason:        params.Reason,
		RebootCommand: params.RebootCommand,
		Timeout:       params.Timeout,
		Settle:        params.Settle,
		OperationType: "resource.reboot_barrier",
	})
}

func (m *ExecutorManager) ReadHostIdentity(ctx context.Context, session *transport.Session) (*HostIdentity, error) {
	platform := rebootPlatformForSession(session)
	proof, err := platform.ReadHostProof(ctx, m, session)
	if err != nil {
		return nil, err
	}
	return &HostIdentity{StableHostID: proof.StableID(), BootID: proof.BootID}, nil
}

func (m *ExecutorManager) CleanupRebootArtifacts(ctx context.Context, session *transport.Session, operationID string) error {
	if strings.TrimSpace(operationID) == "" {
		return nil
	}
	platform := rebootPlatformForSession(session)
	return platform.Cleanup(ctx, session, operationID)
}

func (m *ExecutorManager) restartHostUnsafe(ctx context.Context, session *transport.Session, params RestartHostParams) (*RestartHostResult, error) {
	platform := rebootPlatformForSession(session)
	entry, err := m.prepareRebootJournal(ctx, session, params)
	if err != nil {
		return nil, fmt.Errorf("prepare reboot journal: %w", err)
	}
	if entry.Phase == rebootPhaseCompleted {
		return rebootResultFromEntry(entry), nil
	}
	if entry.Phase == rebootPhaseFailed {
		if entry.LastError != "" {
			return rebootResultFromEntry(entry), errors.New(entry.LastError)
		}
		return rebootResultFromEntry(entry), fmt.Errorf("previous reboot operation failed")
	}

	timeout := rebootTimeoutForEntry(entry, params)
	settle := rebootSettleForEntry(entry, params)
	rebootCtx := ctx
	cancelReboot := func() {}
	hasRebootDeadline := false
	setRebootDeadline := func(deadline time.Time) {
		cancelReboot()
		rebootCtx, cancelReboot = context.WithDeadline(ctx, deadline)
		hasRebootDeadline = true
	}
	defer cancelReboot()
	if deadline, ok := rebootReconnectDeadline(entry, timeout); ok {
		setRebootDeadline(deadline)
	}

	stableHostID := strings.TrimSpace(entry.StableHostID)
	preBootID := strings.TrimSpace(entry.PreBootID)
	rebootCommand := strings.TrimSpace(entry.RebootCommand)
	if rebootCommand == "" {
		rebootCommand = strings.TrimSpace(params.RebootCommand)
	}

	if rebootPhaseLess(entry.Phase, rebootPhasePrecheckComplete) {
		preProof, proofErr := platform.ReadHostProof(ctx, m, session)
		if proofErr != nil {
			return m.failReboot(ctx, session, entry, proofErr)
		}
		stableHostID = preProof.StableID()
		preBootID = preProof.BootID
		if err := m.markRebootJournalPhase(ctx, session, hostrpc.RebootJournalMarkPhaseParams{
			OperationID:  entry.OperationID,
			Phase:        rebootPhasePrecheckComplete,
			StableHostID: stableHostID,
			PreBootID:    preBootID,
		}); err != nil {
			return rebootResultFromEntry(entry), err
		}
		entry.Phase = rebootPhasePrecheckComplete
		entry.StableHostID = stableHostID
		entry.PreBootID = preBootID
	}
	if stableHostID == "" || preBootID == "" {
		return m.failReboot(ctx, session, entry, fmt.Errorf("reboot journal missing precheck proof for operation %s", entry.OperationID))
	}

	if rebootPhaseLess(entry.Phase, rebootPhaseTargetPrepared) {
		if rebootCommand == "" {
			rebootCommand, err = platform.SelectRebootCommand(ctx, m, session)
			if err != nil {
				return m.failReboot(ctx, session, entry, err)
			}
		}
		if err := m.markRebootJournalPhase(ctx, session, hostrpc.RebootJournalMarkPhaseParams{
			OperationID:   entry.OperationID,
			Phase:         rebootPhaseTargetPrepared,
			RebootCommand: rebootCommand,
		}); err != nil {
			return rebootResultFromEntry(entry), err
		}
		entry.Phase = rebootPhaseTargetPrepared
		entry.RebootCommand = rebootCommand
	}

	if rebootPhaseLess(entry.Phase, rebootPhaseRebootIssued) {
		if err := m.markRebootJournalPhase(ctx, session, hostrpc.RebootJournalMarkPhaseParams{
			OperationID:   entry.OperationID,
			Phase:         rebootPhaseRebootIssued,
			RebootCommand: rebootCommand,
		}); err != nil {
			return rebootResultFromEntry(entry), err
		}
		entry.Phase = rebootPhaseRebootIssued
		entry.RebootCommand = rebootCommand
		if err := issueRebootCommand(ctx, m, session, rebootCommand); err != nil {
			return m.failReboot(ctx, session, entry, err)
		}
		setRebootDeadline(time.Now().UTC().Add(timeout))
	}

	if rebootPhaseLess(entry.Phase, rebootPhaseWaitingForReconnect) {
		if !hasRebootDeadline {
			setRebootDeadline(time.Now().UTC().Add(timeout))
		}
		if err := m.resetSessionSafely(rebootCtx, session, nil, false, false, ""); err != nil {
			return m.failReboot(rebootCtx, session, entry, err)
		}
		if err := m.markRebootJournalPhase(rebootCtx, session, hostrpc.RebootJournalMarkPhaseParams{
			OperationID: entry.OperationID,
			Phase:       rebootPhaseWaitingForReconnect,
		}); err != nil {
			return rebootResultFromEntry(entry), err
		}
		entry.Phase = rebootPhaseWaitingForReconnect
		closeTransportForReboot(session)
	}

	deadline, ok := rebootCtx.Deadline()
	if !ok {
		deadline = time.Now().UTC().Add(timeout)
		setRebootDeadline(deadline)
	}

	for time.Now().Before(deadline) {
		attemptCtx, cancel := rebootReconnectAttemptContext(rebootCtx, deadline)
		err := m.reconnectSessionForReboot(attemptCtx, session)
		if err != nil {
			cancel()
			if sleepErr := sleepWithContext(rebootCtx, 2*time.Second); sleepErr != nil {
				return m.failReboot(rebootCtx, session, entry, sleepErr)
			}
			continue
		}

		postProof, proofErr := platform.ReadHostProof(attemptCtx, m, session)
		if proofErr != nil {
			cancel()
			if sleepErr := sleepWithContext(rebootCtx, 2*time.Second); sleepErr != nil {
				return m.failReboot(rebootCtx, session, entry, sleepErr)
			}
			continue
		}

		if err := validateRebootTransition(stableHostID, preBootID, postProof); err != nil {
			cancel()
			if isBootNotChanged(err) {
				if sleepErr := sleepWithContext(rebootCtx, 2*time.Second); sleepErr != nil {
					return m.failReboot(rebootCtx, session, entry, sleepErr)
				}
				continue
			}
			return m.failReboot(rebootCtx, session, entry, err)
		}

		if err := platform.ProbeReady(attemptCtx, m, session); err != nil {
			cancel()
			if sleepErr := sleepWithContext(rebootCtx, 2*time.Second); sleepErr != nil {
				return m.failReboot(rebootCtx, session, entry, sleepErr)
			}
			continue
		}
		cancel()

		if rebootPhaseLess(entry.Phase, rebootPhasePostReconnectValidated) {
			if err := m.markRebootJournalPhase(rebootCtx, session, hostrpc.RebootJournalMarkPhaseParams{
				OperationID: entry.OperationID,
				Phase:       rebootPhasePostReconnectValidated,
				PostBootID:  postProof.BootID,
			}); err != nil {
				return rebootResultFromEntry(entry), err
			}
			entry.Phase = rebootPhasePostReconnectValidated
			entry.PostBootID = postProof.BootID
		}

		if rebootPhaseLess(entry.Phase, rebootPhaseExecutorRebootstrapped) && settle > 0 {
			if err := sleepWithContext(rebootCtx, settle); err != nil {
				return m.failReboot(rebootCtx, session, entry, err)
			}
		}

		if rebootPhaseLess(entry.Phase, rebootPhaseExecutorRebootstrapped) {
			if err := m.ensureExecutorForAction(rebootCtx, session, ""); err != nil {
				return m.failReboot(rebootCtx, session, entry, err)
			}
			if err := m.markRebootJournalPhase(rebootCtx, session, hostrpc.RebootJournalMarkPhaseParams{
				OperationID: entry.OperationID,
				Phase:       rebootPhaseExecutorRebootstrapped,
			}); err != nil {
				return rebootResultFromEntry(entry), err
			}
			entry.Phase = rebootPhaseExecutorRebootstrapped
		}

		if rebootPhaseLess(entry.Phase, rebootPhaseCompleted) {
			if err := platform.Cleanup(rebootCtx, session, entry.OperationID); err != nil {
				return m.failReboot(rebootCtx, session, entry, fmt.Errorf("cleanup reboot artifacts: %w", err))
			}
			if err := m.markRebootJournalCompleted(rebootCtx, session, entry.OperationID, postProof.BootID); err != nil {
				return rebootResultFromEntry(entry), err
			}
			entry.Phase = rebootPhaseCompleted
			entry.PostBootID = postProof.BootID
			now := time.Now().UTC()
			entry.CompletedAt = &now
			entry.LastError = ""
		}

		return rebootResultFromEntry(entry), nil
	}

	err = fmt.Errorf("reboot timeout waiting for host to return and prove reboot")
	return m.failReboot(rebootCtx, session, entry, err)
}

func normalizeRestartHostParams(params RestartHostParams) RestartHostParams {
	if strings.TrimSpace(params.Name) == "" {
		params.Name = "restart-host"
	}
	if strings.TrimSpace(params.Reason) == "" {
		params.Reason = params.Name
	}
	if params.Timeout <= 0 {
		params.Timeout = defaultRebootTimeout
	}
	if params.Settle < 0 {
		params.Settle = 0
	}
	if params.Settle == 0 {
		params.Settle = defaultRebootSettle
	}
	if strings.TrimSpace(params.OperationType) == "" {
		params.OperationType = "action.restart_host"
	}
	params.RebootCommand = strings.TrimSpace(params.RebootCommand)
	return params
}

func issueRebootCommand(ctx context.Context, manager *ExecutorManager, session *transport.Session, rebootCommand string) error {
	result, err := manager.HostCommand(ctx, session, hostrpc.HostCommandParams{
		Name:      "sh",
		Args:      []string{"-lc", "nohup sh -lc " + shellQuote("sleep 1; "+rebootCommand) + " >/dev/null 2>&1 &"},
		Execution: &hostrpc.ExecutionContext{Become: true},
	})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("issue reboot command failed: %s", commandResultDetail(result))
	}
	return nil
}

func (m *ExecutorManager) reconnectSessionForReboot(ctx context.Context, session *transport.Session) error {
	if session.Transport == nil {
		return fmt.Errorf("nil transport")
	}
	_ = session.Transport.Close()
	return session.Transport.Connect(ctx)
}

func closeTransportForReboot(session *transport.Session) {
	if session == nil || session.Transport == nil {
		return
	}
	_ = session.Transport.Close()
}

func rebootReconnectAttemptContext(ctx context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	if parentDeadline, ok := ctx.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		return context.WithDeadline(ctx, deadline)
	}
	if remaining > maxRebootReconnectAttempt {
		remaining = maxRebootReconnectAttempt
	}
	return context.WithTimeout(ctx, remaining)
}

func rebootReconnectDeadline(entry *rebootJournalEntry, timeout time.Duration) (time.Time, bool) {
	if entry == nil || timeout <= 0 || rebootPhaseLess(entry.Phase, rebootPhaseRebootIssued) || entry.UpdatedAt.IsZero() {
		return time.Time{}, false
	}
	return entry.UpdatedAt.Add(timeout), true
}

func validateRebootProof(pre, post hostProof) error {
	if pre.StableID() != post.StableID() {
		return fmt.Errorf("stable host identity changed across reboot")
	}
	if pre.BootID == post.BootID {
		return errBootNotChanged
	}
	return nil
}

func validateRebootTransition(stableHostID, preBootID string, post hostProof) error {
	if strings.TrimSpace(stableHostID) == "" || strings.TrimSpace(preBootID) == "" {
		return fmt.Errorf("missing reboot proof state")
	}
	if post.StableID() != stableHostID {
		return fmt.Errorf("stable host identity changed across reboot")
	}
	if post.BootID == preBootID {
		return errBootNotChanged
	}
	return nil
}

var errBootNotChanged = fmt.Errorf("boot id unchanged")

func isBootNotChanged(err error) bool {
	return err != nil && strings.Contains(err.Error(), errBootNotChanged.Error())
}

func rebootPhaseRank(phase string) int {
	switch phase {
	case rebootPhasePlanned:
		return 0
	case rebootPhasePrecheckComplete:
		return 1
	case rebootPhaseTargetPrepared:
		return 2
	case rebootPhaseRebootIssued:
		return 3
	case rebootPhaseWaitingForReconnect:
		return 4
	case rebootPhasePostReconnectValidated:
		return 5
	case rebootPhaseExecutorRebootstrapped:
		return 6
	case rebootPhaseCompleted:
		return 7
	case rebootPhaseFailed:
		return 8
	default:
		return -1
	}
}

func rebootPhaseLess(current, target string) bool {
	return rebootPhaseRank(current) < rebootPhaseRank(target)
}

func rebootResultFromEntry(entry *rebootJournalEntry) *RestartHostResult {
	if entry == nil {
		return nil
	}

	return &RestartHostResult{
		OperationID:   entry.OperationID,
		Phase:         entry.Phase,
		StableHostID:  entry.StableHostID,
		PreBootID:     entry.PreBootID,
		PostBootID:    entry.PostBootID,
		CompletedAt:   entry.CompletedAt,
		LastError:     entry.LastError,
		HostAddress:   entry.HostAddress,
		Reason:        entry.Reason,
		RebootCommand: entry.RebootCommand,
	}
}

func (m *ExecutorManager) failReboot(ctx context.Context, session *transport.Session, entry *rebootJournalEntry, failure error) (*RestartHostResult, error) {
	if entry == nil {
		return nil, failure
	}
	markCtx, cancel := rebootFailureMarkContext(ctx)
	defer cancel()
	_ = m.markRebootJournalFailed(markCtx, session, entry.OperationID, failure)
	entry.Phase = rebootPhaseFailed
	entry.LastError = failure.Error()
	return rebootResultFromEntry(entry), failure
}

func rebootFailureMarkContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx != nil && ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.Background(), terminalRebootFailureMarkTimeout)
}

func rebootRequestedAt(entry *rebootJournalEntry) time.Time {
	if entry == nil || entry.RequestedAt.IsZero() {
		return time.Now().UTC()
	}
	return entry.RequestedAt
}

func rebootTimeoutForEntry(entry *rebootJournalEntry, params RestartHostParams) time.Duration {
	if entry != nil && entry.TimeoutSeconds > 0 {
		return time.Duration(entry.TimeoutSeconds) * time.Second
	}
	if params.Timeout > 0 {
		return params.Timeout
	}
	return defaultRebootTimeout
}

func rebootSettleForEntry(entry *rebootJournalEntry, params RestartHostParams) time.Duration {
	if entry != nil && entry.SettleSeconds > 0 {
		return time.Duration(entry.SettleSeconds) * time.Second
	}
	if params.Settle > 0 {
		return params.Settle
	}
	return defaultRebootSettle
}

func rebootBarrierOperationName(resourceID, triggersHash string) string {
	resourceID = strings.TrimSpace(resourceID)
	triggersHash = strings.TrimSpace(triggersHash)
	if resourceID == "" {
		resourceID = "unknown"
	}
	if triggersHash == "" {
		return "reboot-barrier:" + resourceID
	}
	return "reboot-barrier:" + resourceID + ":" + triggersHash
}

func (m *ExecutorManager) prepareRebootJournal(ctx context.Context, session *transport.Session, params RestartHostParams) (*rebootJournalEntry, error) {
	request := hostrpc.RebootJournalPrepareParams{
		HostAddress:    session.Config.Endpoint(),
		Name:           params.Name,
		Reason:         params.Reason,
		RebootCommand:  params.RebootCommand,
		TimeoutSeconds: int(params.Timeout / time.Second),
		SettleSeconds:  int(params.Settle / time.Second),
	}
	var result hostrpc.RebootJournalEntry
	if err := m.withRetries(ctx, session, "", 0, nil, func(callCtx context.Context, client *jrpc2.Client) error {
		return client.CallResult(callCtx, hostrpc.MethodJournalRebootPrepare, request, &result)
	}); err != nil {
		return nil, err
	}
	return rebootJournalEntryFromRPC(&result)
}

func (m *ExecutorManager) markRebootJournalPhase(ctx context.Context, session *transport.Session, params hostrpc.RebootJournalMarkPhaseParams) error {
	return m.withRetries(ctx, session, "", 0, nil, func(callCtx context.Context, client *jrpc2.Client) error {
		_, err := client.Call(callCtx, hostrpc.MethodJournalRebootMarkPhase, params)
		return err
	})
}

func (m *ExecutorManager) markRebootJournalFailed(ctx context.Context, session *transport.Session, operationID string, failure error) error {
	params := hostrpc.RebootJournalMarkFailedParams{OperationID: operationID}
	if failure != nil {
		params.LastError = failure.Error()
	}
	return m.withRetries(ctx, session, "", 0, nil, func(callCtx context.Context, client *jrpc2.Client) error {
		_, err := client.Call(callCtx, hostrpc.MethodJournalRebootMarkFailed, params)
		return err
	})
}

func (m *ExecutorManager) markRebootJournalCompleted(ctx context.Context, session *transport.Session, operationID, postBootID string) error {
	params := hostrpc.RebootJournalMarkCompletedParams{OperationID: operationID, PostBootID: postBootID}
	return m.withRetries(ctx, session, "", 0, nil, func(callCtx context.Context, client *jrpc2.Client) error {
		_, err := client.Call(callCtx, hostrpc.MethodJournalRebootMarkCompleted, params)
		return err
	})
}

func rebootJournalEntryFromRPC(entry *hostrpc.RebootJournalEntry) (*rebootJournalEntry, error) {
	if entry == nil {
		return nil, nil
	}
	requestedAt, err := parseOptionalRFC3339Time(entry.RequestedAt)
	if err != nil {
		return nil, err
	}
	updatedAt, err := parseOptionalRFC3339Time(entry.UpdatedAt)
	if err != nil {
		return nil, err
	}
	completedAt, err := parseOptionalRFC3339TimePtr(entry.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &rebootJournalEntry{
		OperationID:    entry.OperationID,
		Fingerprint:    entry.Fingerprint,
		HostAddress:    entry.HostAddress,
		Name:           entry.Name,
		Reason:         entry.Reason,
		RebootCommand:  entry.RebootCommand,
		Phase:          entry.Phase,
		StableHostID:   entry.StableHostID,
		PreBootID:      entry.PreBootID,
		PostBootID:     entry.PostBootID,
		TimeoutSeconds: entry.TimeoutSeconds,
		SettleSeconds:  entry.SettleSeconds,
		RequestedAt:    requestedAt,
		UpdatedAt:      updatedAt,
		CompletedAt:    completedAt,
		LastError:      entry.LastError,
	}, nil
}

func parseOptionalRFC3339Time(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", value, err)
	}
	return parsed, nil
}

func parseOptionalRFC3339TimePtr(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := parseOptionalRFC3339Time(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

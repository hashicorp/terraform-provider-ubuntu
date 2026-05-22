package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/executor/capabilities"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

func TestOperationJournalAcquireReleaseEncryptsRecord(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_JOURNAL_DIR", t.TempDir())
	mustSetJournalKey(t, "0123456789abcdef0123456789abcdef")

	journal := newOperationJournal()
	operationID, granted, err := journal.Acquire(context.Background(), hostrpc.OperationAcquireParams{
		HostKey:    "ssh:host-a",
		SessionKey: "session-a",
		Action:     "resource.create",
		Name:       "example",
		LockSet: []hostrpc.LockDescriptor{{
			Key:    "pkgmgr:system",
			Mode:   hostrpc.LockModeExclusive,
			Source: "test",
		}},
	})
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if !granted {
		t.Fatal("Acquire returned granted=false, want true")
	}

	data, err := os.ReadFile(journal.operationPath(operationID))
	if err != nil {
		t.Fatalf("read encrypted operation record: %v", err)
	}
	if strings.Contains(string(data), "resource.create") || strings.Contains(string(data), "example") {
		t.Fatalf("operation journal leaked plaintext: %s", string(data))
	}

	if err := journal.Release(hostrpc.OperationReleaseParams{
		HostKey:     "ssh:host-a",
		OperationID: operationID,
		Status:      "completed",
	}); err != nil {
		t.Fatalf("Release returned error: %v", err)
	}

	if _, err := os.Stat(journal.currentHostOperationPath("ssh:host-a", operationID)); !os.IsNotExist(err) {
		t.Fatalf("expected host index to be removed, got err=%v", err)
	}
}

func TestOperationJournalPurgesUnreadableStaleEntry(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_JOURNAL_DIR", t.TempDir())
	journal := newOperationJournal()
	hostKey := "ssh:host-b"
	staleID := "stale-op"

	mustSetJournalKey(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	staleEntry := &operationJournalEntry{
		OperationID: staleID,
		OwnerPID:    999999,
		HostKey:     hostKey,
		SessionKey:  "old-session",
		Action:      "resource.update",
		Status:      operationStatusRunning,
		LockSet: []hostrpc.LockDescriptor{{
			Key:    "identity:system",
			Mode:   hostrpc.LockModeExclusive,
			Source: "stale-test",
		}},
	}
	if err := journal.ensureDirs(hostKey); err != nil {
		t.Fatalf("ensureDirs returned error: %v", err)
	}
	if err := journal.writeOperation(staleEntry); err != nil {
		t.Fatalf("writeOperation returned error: %v", err)
	}
	if err := journal.writeHostIndex(staleEntry); err != nil {
		t.Fatalf("writeHostIndex returned error: %v", err)
	}
	if err := journal.writePlainJSON(journal.lockClaimPath(hostKey, staleID, staleEntry.LockSet[0]), &lockClaim{
		OperationID: staleID,
		OwnerPID:    staleEntry.OwnerPID,
		HostKey:     hostKey,
		SessionKey:  staleEntry.SessionKey,
		Lock:        staleEntry.LockSet[0],
	}); err != nil {
		t.Fatalf("writePlainJSON returned error: %v", err)
	}

	mustSetJournalKey(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	operationID, granted, err := journal.Acquire(context.Background(), hostrpc.OperationAcquireParams{
		HostKey:    hostKey,
		SessionKey: "new-session",
		Action:     "resource.update",
		LockSet: []hostrpc.LockDescriptor{{
			Key:    "identity:system",
			Mode:   hostrpc.LockModeExclusive,
			Source: "new-test",
		}},
	})
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if !granted {
		t.Fatal("Acquire returned granted=false, want true")
	}
	if operationID == staleID {
		t.Fatalf("expected a fresh operation id, got stale id %q", operationID)
	}

	if _, err := os.Stat(journal.currentHostOperationPath(hostKey, staleID)); !os.IsNotExist(err) {
		t.Fatalf("expected stale host index to be removed, got err=%v", err)
	}
	if _, err := os.Stat(journal.operationPath(staleID)); !os.IsNotExist(err) {
		t.Fatalf("expected unreadable stale operation record to be removed, got err=%v", err)
	}
}

func TestOperationJournalAcquireReusesRequestIDForRunningLease(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_JOURNAL_DIR", t.TempDir())
	mustSetJournalKey(t, "cccccccccccccccccccccccccccccccc")

	journal := newOperationJournal()
	params := hostrpc.OperationAcquireParams{
		RequestID:  "req-control-plane-create",
		HostKey:    "ssh:host-c",
		SessionKey: "session-c",
		Action:     "resource.create",
		Name:       "control-plane",
		LockSet: []hostrpc.LockDescriptor{{
			Key:    "kubeadm:cluster",
			Mode:   hostrpc.LockModeExclusive,
			Source: "test",
		}},
	}

	operationID, granted, err := journal.Acquire(context.Background(), params)
	if err != nil {
		t.Fatalf("first Acquire returned error: %v", err)
	}
	if !granted {
		t.Fatal("first Acquire returned granted=false, want true")
	}

	retryID, retryGranted, err := journal.Acquire(context.Background(), params)
	if err != nil {
		t.Fatalf("retry Acquire returned error: %v", err)
	}
	if !retryGranted {
		t.Fatal("retry Acquire returned granted=false, want true")
	}
	if retryID != operationID {
		t.Fatalf("expected retry acquire to reuse operation id %q, got %q", operationID, retryID)
	}

	indexes, err := journal.readHostOperationIndexes(params.HostKey)
	if err != nil {
		t.Fatalf("readHostOperationIndexes returned error: %v", err)
	}
	if len(indexes) != 1 {
		t.Fatalf("expected exactly one current host index, got %d", len(indexes))
	}
	if indexes[0].RequestID != params.RequestID {
		t.Fatalf("expected request id %q, got %q", params.RequestID, indexes[0].RequestID)
	}
}

func TestOperationJournalAcquireAbandonsDifferentBootID(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_JOURNAL_DIR", t.TempDir())
	mustSetJournalKey(t, "dddddddddddddddddddddddddddddddd")

	originalBootID := operationJournalBootID
	operationJournalBootID = func() string { return "boot-new" }
	defer func() {
		operationJournalBootID = originalBootID
	}()

	journal := newOperationJournal()
	hostKey := "ssh:host-d"
	staleID := "stale-op"
	staleLock := hostrpc.LockDescriptor{Key: "host", Mode: hostrpc.LockModeExclusive, Source: "stale-test"}
	now := time.Now().UTC()
	staleEntry := &operationJournalEntry{
		OperationID: staleID,
		OwnerPID:    os.Getpid(),
		OwnerBootID: "boot-old",
		HostKey:     hostKey,
		SessionKey:  "session-old",
		Action:      "resource.update",
		Status:      operationStatusRunning,
		LockSet:     []hostrpc.LockDescriptor{staleLock},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := journal.ensureDirs(hostKey); err != nil {
		t.Fatalf("ensureDirs returned error: %v", err)
	}
	if err := journal.writeOperation(staleEntry); err != nil {
		t.Fatalf("writeOperation returned error: %v", err)
	}
	if err := journal.writeHostIndex(staleEntry); err != nil {
		t.Fatalf("writeHostIndex returned error: %v", err)
	}
	if err := journal.writePlainJSON(journal.lockClaimPath(hostKey, staleID, staleLock), &lockClaim{
		OperationID: staleID,
		OwnerPID:    staleEntry.OwnerPID,
		OwnerBootID: staleEntry.OwnerBootID,
		HostKey:     hostKey,
		SessionKey:  staleEntry.SessionKey,
		Lock:        staleLock,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("writePlainJSON returned error: %v", err)
	}

	operationID, granted, err := journal.Acquire(context.Background(), hostrpc.OperationAcquireParams{
		HostKey:    hostKey,
		SessionKey: "session-new",
		Action:     "resource.update",
		LockSet:    []hostrpc.LockDescriptor{staleLock},
	})
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if !granted {
		t.Fatal("Acquire returned granted=false, want true")
	}
	if operationID == staleID {
		t.Fatalf("expected a fresh operation id, got stale id %q", operationID)
	}
	if _, err := os.Stat(journal.currentHostOperationPath(hostKey, staleID)); !os.IsNotExist(err) {
		t.Fatalf("expected stale host index to be removed, got err=%v", err)
	}
	if _, err := os.Stat(journal.lockClaimPath(hostKey, staleID, staleLock)); !os.IsNotExist(err) {
		t.Fatalf("expected stale lock claim to be removed, got err=%v", err)
	}
}

func TestRebootJournalEncryptsRecords(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_JOURNAL_DIR", t.TempDir())
	mustSetJournalKey(t, "fedcba9876543210fedcba9876543210")

	journal := newRebootJournal()
	entry, err := journal.Prepare(hostrpc.RebootJournalPrepareParams{
		HostAddress:    "capabilities.example",
		Name:           "kernel-upgrade",
		Reason:         "kernel-upgrade",
		RebootCommand:  "systemctl reboot",
		TimeoutSeconds: 600,
		SettleSeconds:  10,
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(defaultExecutorJournalBaseDir(), "reboots", "operations", entry.OperationID+".json"))
	if err != nil {
		t.Fatalf("read reboot journal record: %v", err)
	}
	if strings.Contains(string(data), "kernel-upgrade") || strings.Contains(string(data), "systemctl reboot") {
		t.Fatalf("reboot journal leaked plaintext: %s", string(data))
	}

	updated, err := journal.MarkCompleted(hostrpc.RebootJournalMarkCompletedParams{
		OperationID: entry.OperationID,
		PostBootID:  "boot-2",
	})
	if err != nil {
		t.Fatalf("MarkCompleted returned error: %v", err)
	}
	if updated.Phase != rebootPhaseCompleted {
		t.Fatalf("expected completed phase, got %q", updated.Phase)
	}
}

func TestSharedJournalLockAcceptsReadOnlyFile(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "admission")
	if err := os.WriteFile(lockPath+".lock", []byte(""), 0o444); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	lockFile, err := sharedJournalLock(lockPath)
	if err != nil {
		t.Fatalf("sharedJournalLock returned error: %v", err)
	}
	defer capabilities.FileUnlock(lockFile)

	info, err := os.Stat(lockPath + ".lock")
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	if info.Mode().Perm() != sharedJournalFileMode {
		t.Fatalf("expected lock mode %o, got %o", sharedJournalFileMode, info.Mode().Perm())
	}
}

func TestDefaultExecutorJournalBaseDirUsesXDGStateHome(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_JOURNAL_DIR", "")
	t.Setenv("XDG_STATE_HOME", "/tmp/tf-linux-provider-state")
	t.Setenv("HOME", "/tmp/ignored-home")

	got := defaultExecutorJournalBaseDir()
	want := filepath.Join("/tmp/tf-linux-provider-state", "tf-linux-provider", "journals")
	if got != want {
		t.Fatalf("expected XDG journal dir %q, got %q", want, got)
	}
}

func TestDefaultExecutorJournalBaseDirUsesHomeStateDir(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_JOURNAL_DIR", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/tmp/tf-linux-provider-home")

	got := defaultExecutorJournalBaseDir()
	want := filepath.Join("/tmp/tf-linux-provider-home", ".local", "state", "tf-linux-provider", "journals")
	if got != want {
		t.Fatalf("expected HOME journal dir %q, got %q", want, got)
	}
}

func TestDefaultRestartJournalDirUsesOverride(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_ACTIONS_DIR", "/tmp/tf-linux-provider-actions")

	got := defaultRestartJournalDir()
	if got != "/tmp/tf-linux-provider-actions" {
		t.Fatalf("expected override restart journal dir, got %q", got)
	}
}

func TestNormalizeRestartCommandSpecUsesStructuredCommand(t *testing.T) {
	spec := &restartCommandSpec{Name: "systemctl", Args: []string{"restart", "sshd.service"}}
	if err := normalizeRestartCommandSpec(spec); err != nil {
		t.Fatalf("normalizeRestartCommandSpec returned error: %v", err)
	}
	if spec.Name != "systemctl" {
		t.Fatalf("unexpected command name: %q", spec.Name)
	}
	if len(spec.Args) != 2 || spec.Args[0] != "restart" || spec.Args[1] != "sshd.service" {
		t.Fatalf("unexpected command args: %#v", spec.Args)
	}
}

func TestNormalizeRestartCommandSpecFallsBackToShellForCommandString(t *testing.T) {
	spec := &restartCommandSpec{Command: "echo disconnect"}
	if err := normalizeRestartCommandSpec(spec); err != nil {
		t.Fatalf("normalizeRestartCommandSpec returned error: %v", err)
	}
	if spec.Name != "sh" {
		t.Fatalf("unexpected shell fallback name: %q", spec.Name)
	}
	if len(spec.Args) != 2 || spec.Args[0] != "-lc" || spec.Args[1] != "echo disconnect" {
		t.Fatalf("unexpected shell fallback args: %#v", spec.Args)
	}
}

func TestPrepareRestartOperationHandlesExistingStatuses(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_ACTIONS_DIR", t.TempDir())
	mustSetJournalKey(t, "11111111111111111111111111111111")

	spec := &restartCommandSpec{Name: "systemctl", Args: []string{"restart", "sshd.service"}}

	record, shouldLaunch, err := prepareRestartOperation("op-restart", "restart-sshd", spec, nil)
	if err != nil {
		t.Fatalf("prepareRestartOperation(first) returned error: %v", err)
	}
	if !shouldLaunch {
		t.Fatal("expected first prepareRestartOperation call to request launch")
	}
	if record.Status != restartStatusLaunching || record.CommandName != "systemctl" {
		t.Fatalf("unexpected initial restart record: %#v", record)
	}

	persisted, err := readRestartRecord("op-restart")
	if err != nil {
		t.Fatalf("readRestartRecord(initial) returned error: %v", err)
	}
	if persisted.Status != restartStatusLaunching {
		t.Fatalf("persisted status = %q, want %q", persisted.Status, restartStatusLaunching)
	}

	reused, shouldLaunch, err := prepareRestartOperation("op-restart", "restart-sshd", spec, nil)
	if err != nil {
		t.Fatalf("prepareRestartOperation(reuse launching) returned error: %v", err)
	}
	if shouldLaunch {
		t.Fatal("expected launching restart record to be reused without relaunch")
	}
	if reused.Status != restartStatusLaunching {
		t.Fatalf("reused launching status = %q, want %q", reused.Status, restartStatusLaunching)
	}

	reused.Status = restartStatusLaunchError
	reused.LastError = "boom"
	if err := writeRestartRecord(reused); err != nil {
		t.Fatalf("writeRestartRecord(launch_error) returned error: %v", err)
	}

	retried, shouldLaunch, err := prepareRestartOperation("op-restart", "restart-sshd", spec, nil)
	if err != nil {
		t.Fatalf("prepareRestartOperation(retry launch_error) returned error: %v", err)
	}
	if !shouldLaunch {
		t.Fatal("expected launch_error restart record to request relaunch")
	}
	if retried.Status != restartStatusLaunching || retried.LastError != "" {
		t.Fatalf("retried restart record = %#v, want launching status and cleared error", retried)
	}

	retried.Status = restartStatusCompleted
	if err := writeRestartRecord(retried); err != nil {
		t.Fatalf("writeRestartRecord(completed) returned error: %v", err)
	}

	completed, shouldLaunch, err := prepareRestartOperation("op-restart", "restart-sshd", spec, nil)
	if err != nil {
		t.Fatalf("prepareRestartOperation(completed) returned error: %v", err)
	}
	if shouldLaunch {
		t.Fatal("expected completed restart record to be reused without relaunch")
	}
	if completed.Status != restartStatusCompleted {
		t.Fatalf("completed restart status = %q, want %q", completed.Status, restartStatusCompleted)
	}
}

func TestWaitForRestartResultHandlesTerminalAndCanceledStates(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_ACTIONS_DIR", t.TempDir())
	mustSetJournalKey(t, "22222222222222222222222222222222")

	completed := &restartOperationRecord{
		OperationID: "op-completed",
		Name:        "completed",
		Status:      restartStatusCompleted,
		Result:      hostrpc.CommandResult{Stdout: "done", ExitCode: 0},
	}
	if err := writeRestartRecord(completed); err != nil {
		t.Fatalf("writeRestartRecord(completed) returned error: %v", err)
	}

	result, err := waitForRestartResult(context.Background(), "op-completed")
	if err != nil {
		t.Fatalf("waitForRestartResult(completed) returned error: %v", err)
	}
	if result.Stdout != "done" || result.ExitCode != 0 {
		t.Fatalf("unexpected completed restart result: %#v", result)
	}

	launchError := &restartOperationRecord{
		OperationID: "op-launch-error",
		Name:        "failed",
		Status:      restartStatusLaunchError,
		LastError:   "cannot start helper",
	}
	if err := writeRestartRecord(launchError); err != nil {
		t.Fatalf("writeRestartRecord(launch_error) returned error: %v", err)
	}

	if _, err := waitForRestartResult(context.Background(), "op-launch-error"); err == nil || !strings.Contains(err.Error(), "restart launch failed: cannot start helper") {
		t.Fatalf("waitForRestartResult(launch_error) error = %v, want launch failure", err)
	}

	running := &restartOperationRecord{
		OperationID: "op-running",
		Name:        "running",
		Status:      restartStatusRunning,
	}
	if err := writeRestartRecord(running); err != nil {
		t.Fatalf("writeRestartRecord(running) returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitForRestartResult(ctx, "op-running"); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForRestartResult(canceled) error = %v, want context.Canceled", err)
	}
}

func TestStartRestartHelperRequiresConfiguredJournalKey(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_ACTIONS_DIR", t.TempDir())
	savedKey := snapshotRuntimeJournalKey()
	t.Cleanup(func() {
		restoreRuntimeJournalKey(savedKey)
	})
	restoreRuntimeJournalKey(nil)

	record := &restartOperationRecord{OperationID: "op-missing-key"}
	if err := startRestartHelper(record); err == nil || !strings.Contains(err.Error(), "resolve journal key") {
		t.Fatalf("startRestartHelper() error = %v, want journal key resolution error", err)
	}
}

func TestRunJournaledRestartLegacyAndErrorPaths(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_ACTIONS_DIR", t.TempDir())
	mustSetJournalKey(t, "33333333333333333333333333333333")
	encodedKey, err := runtimeJournalKeyEnv()
	if err != nil {
		t.Fatalf("runtimeJournalKeyEnv() returned error: %v", err)
	}
	t.Setenv(journalKeyEnvVar, encodedKey)

	legacy := &restartOperationRecord{
		OperationID: "op-legacy",
		Name:        "legacy-restart",
		Command:     "printf legacy-restart",
		Status:      restartStatusLaunching,
	}
	if err := writeRestartRecord(legacy); err != nil {
		t.Fatalf("writeRestartRecord(legacy) returned error: %v", err)
	}
	if err := RunJournaledRestart(restartRecordPath("op-legacy")); err != nil {
		t.Fatalf("RunJournaledRestart(legacy) returned error: %v", err)
	}
	legacyRecord, err := readRestartRecord("op-legacy")
	if err != nil {
		t.Fatalf("readRestartRecord(legacy) returned error: %v", err)
	}
	if legacyRecord.Status != restartStatusCompleted || legacyRecord.CompletedAt == nil {
		t.Fatalf("legacy restart record = %#v, want completed status with completion time", legacyRecord)
	}
	if strings.TrimSpace(legacyRecord.Result.Stdout) != "legacy-restart" {
		t.Fatalf("legacy restart stdout = %q, want legacy-restart", legacyRecord.Result.Stdout)
	}
	if legacyRecord.CommandName != "sh" || len(legacyRecord.Args) != 2 || legacyRecord.Args[0] != "-lc" {
		t.Fatalf("legacy restart command normalization = %#v, want shell fallback", legacyRecord)
	}

	empty := &restartOperationRecord{
		OperationID: "op-empty",
		Name:        "empty-command",
		Command:     "   ",
		Status:      restartStatusLaunching,
	}
	if err := writeRestartRecord(empty); err != nil {
		t.Fatalf("writeRestartRecord(empty) returned error: %v", err)
	}
	if err := RunJournaledRestart(restartRecordPath("op-empty")); err == nil || !strings.Contains(err.Error(), "restart action returned empty command") {
		t.Fatalf("RunJournaledRestart(empty) error = %v, want empty command error", err)
	}

	t.Setenv(journalKeyEnvVar, "")
	if err := RunJournaledRestart(restartRecordPath("op-legacy")); err == nil || !strings.Contains(err.Error(), "load journal key") {
		t.Fatalf("RunJournaledRestart(missing env) error = %v, want journal key error", err)
	}
}

func TestRebootJournalMarkPhaseAndFailedUpdateCurrentEntry(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_JOURNAL_DIR", t.TempDir())
	mustSetJournalKey(t, "44444444444444444444444444444444")

	journal := newRebootJournal()
	entry, err := journal.Prepare(hostrpc.RebootJournalPrepareParams{
		HostAddress:   "capabilities.example",
		Name:          "runtime-reboot",
		Reason:        "kernel-upgrade",
		RebootCommand: "systemctl reboot",
	})
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	updated, err := journal.MarkPhase(hostrpc.RebootJournalMarkPhaseParams{
		OperationID:   entry.OperationID,
		Phase:         rebootPhaseWaitingForReconnect,
		StableHostID:  "stable-host",
		PreBootID:     "boot-before",
		PostBootID:    "boot-after",
		RebootCommand: "shutdown -r now",
	})
	if err != nil {
		t.Fatalf("MarkPhase returned error: %v", err)
	}
	if updated.Phase != rebootPhaseWaitingForReconnect || updated.StableHostID != "stable-host" || updated.PreBootID != "boot-before" || updated.PostBootID != "boot-after" || updated.RebootCommand != "shutdown -r now" {
		t.Fatalf("unexpected reboot journal phase update: %#v", updated)
	}
	current, err := journal.readEntry(journal.currentPath(entry.Fingerprint))
	if err != nil {
		t.Fatalf("readEntry(current after MarkPhase) returned error: %v", err)
	}
	if current.Phase != rebootPhaseWaitingForReconnect {
		t.Fatalf("current reboot phase = %q, want %q", current.Phase, rebootPhaseWaitingForReconnect)
	}

	failed, err := journal.MarkFailed(hostrpc.RebootJournalMarkFailedParams{
		OperationID: entry.OperationID,
		LastError:   "reconnect timeout",
	})
	if err != nil {
		t.Fatalf("MarkFailed returned error: %v", err)
	}
	if failed.Phase != rebootPhaseFailed || failed.LastError != "reconnect timeout" {
		t.Fatalf("unexpected reboot journal failure update: %#v", failed)
	}
	current, err = journal.readEntry(journal.currentPath(entry.Fingerprint))
	if err != nil {
		t.Fatalf("readEntry(current after MarkFailed) returned error: %v", err)
	}
	if current.Phase != rebootPhaseFailed || current.LastError != "reconnect timeout" {
		t.Fatalf("current reboot failure state = %#v, want failed with reconnect timeout", current)
	}
}

func TestOperationJournalHelperFunctions(t *testing.T) {
	sharedLock := hostrpc.LockDescriptor{Key: "packages:apt", Mode: hostrpc.LockModeShared}
	exclusiveLock := hostrpc.LockDescriptor{Key: "packages:apt", Mode: hostrpc.LockModeExclusive}
	otherLock := hostrpc.LockDescriptor{Key: "files:/etc/hosts", Mode: hostrpc.LockModeExclusive}
	hostLock := hostrpc.LockDescriptor{Key: "host", Mode: hostrpc.LockModeShared}

	if locksConflict(sharedLock, sharedLock) {
		t.Fatal("locksConflict() should allow shared access to the same key")
	}
	if !locksConflict(sharedLock, exclusiveLock) {
		t.Fatal("locksConflict() should block shared versus exclusive access on the same key")
	}
	if locksConflict(sharedLock, otherLock) {
		t.Fatal("locksConflict() should not block distinct non-dominating keys")
	}
	if !locksConflict(hostLock, otherLock) {
		t.Fatal("locksConflict() should treat host locks as dominating")
	}
	if locksConflict(hostrpc.LockDescriptor{}, otherLock) {
		t.Fatal("locksConflict() should ignore empty keys")
	}

	if !isDominatingLockKey("host") || !isDominatingLockKey("reboot:host") {
		t.Fatal("isDominatingLockKey() should recognize host-wide lock keys")
	}
	if isDominatingLockKey("packages:apt") {
		t.Fatal("isDominatingLockKey() should ignore regular lock keys")
	}

	if got := firstNonEmpty("", "", "value", "fallback"); got != "value" {
		t.Fatalf("firstNonEmpty() = %q, want value", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Fatalf("firstNonEmpty(empty) = %q, want empty", got)
	}

	if err := sleepWithContext(context.Background(), 0); err != nil {
		t.Fatalf("sleepWithContext(zero) returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepWithContext(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepWithContext(canceled) error = %v, want context.Canceled", err)
	}
	if err := sleepWithContext(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("sleepWithContext(wait) returned error: %v", err)
	}
}

func mustSetJournalKey(t *testing.T, value string) {
	t.Helper()
	if err := setRuntimeJournalKey([]byte(value)); err != nil {
		t.Fatalf("setRuntimeJournalKey returned error: %v", err)
	}
}

// Copyright IBM Corp. 2026

package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/executor/capabilities"
	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

type operationStatus string

const (
	operationStatusQueued     operationStatus = "queued"
	operationStatusRunning    operationStatus = "running"
	operationStatusRecovering operationStatus = "recovering"
	operationStatusFailed     operationStatus = "failed"
	operationStatusCompleted  operationStatus = "completed"
	operationStatusAbandoned  operationStatus = "abandoned"
)

type operationJournal struct {
	dir string
}

type operationJournalEntry struct {
	OperationID  string                   `json:"operation_id"`
	RequestID    string                   `json:"request_id,omitempty"`
	OwnerPID     int                      `json:"owner_pid"`
	OwnerBootID  string                   `json:"owner_boot_id,omitempty"`
	HostKey      string                   `json:"host_key"`
	SessionKey   string                   `json:"session_key"`
	ModuleName   string                   `json:"module_name,omitempty"`
	ResourceType string                   `json:"resource_type,omitempty"`
	Action       string                   `json:"action"`
	Name         string                   `json:"name,omitempty"`
	Status       operationStatus          `json:"status"`
	LockSet      []hostrpc.LockDescriptor `json:"lock_set,omitempty"`
	Attempts     int                      `json:"attempts"`
	LastError    string                   `json:"last_error,omitempty"`
	CreatedAt    time.Time                `json:"created_at"`
	UpdatedAt    time.Time                `json:"updated_at"`
}

type lockClaim struct {
	OperationID string                 `json:"operation_id"`
	OwnerPID    int                    `json:"owner_pid"`
	OwnerBootID string                 `json:"owner_boot_id,omitempty"`
	HostKey     string                 `json:"host_key"`
	SessionKey  string                 `json:"session_key"`
	Lock        hostrpc.LockDescriptor `json:"lock"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

type hostOperationIndex struct {
	OperationID string          `json:"operation_id"`
	RequestID   string          `json:"request_id,omitempty"`
	OwnerPID    int             `json:"owner_pid"`
	OwnerBootID string          `json:"owner_boot_id,omitempty"`
	Status      operationStatus `json:"status"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

const (
	sharedJournalDirMode  = 0o777
	sharedJournalFileMode = 0o666
)

var operationJournalBootID = readOperationJournalBootID

func newOperationJournal() *operationJournal {
	return &operationJournal{dir: defaultOperationJournalDir()}
}

func defaultOperationJournalDir() string {
	if dir := os.Getenv("TF_LINUX_PROVIDER_EXECUTOR_OPERATIONS_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(defaultExecutorJournalBaseDir(), "operations")
}

func (j *operationJournal) Acquire(ctx context.Context, params hostrpc.OperationAcquireParams) (string, bool, error) {
	if err := j.ensureDirs(params.HostKey); err != nil {
		return "", false, err
	}

	now := time.Now().UTC()
	entry, err := j.findOrCreateOperation(params, now)
	if err != nil {
		return "", false, err
	}

	if entry.Status == operationStatusRunning {
		return entry.OperationID, true, nil
	}

	if err := ctx.Err(); err != nil {
		_ = j.finalize(entry.OperationID, operationStatusFailed, err)
		return "", false, err
	}

	activated, err := j.tryActivate(entry)
	if err != nil {
		_ = j.finalize(entry.OperationID, operationStatusFailed, err)
		return "", false, err
	}
	if activated {
		return entry.OperationID, true, nil
	}

	return entry.OperationID, false, nil
}

func (j *operationJournal) Release(params hostrpc.OperationReleaseParams) error {
	status := operationStatusCompleted
	var lastErr error
	if params.Status != string(operationStatusCompleted) {
		status = operationStatusFailed
		if params.LastError != "" {
			lastErr = errors.New(params.LastError)
		}
	}
	return j.finalize(params.OperationID, status, lastErr)
}

func (j *operationJournal) tryActivate(entry *operationJournalEntry) (bool, error) {
	var activated bool
	if err := j.withAdmissionLock(entry.HostKey, func() error {
		if err := j.abandonStaleHostEntriesLocked(entry.HostKey, entry.OwnerPID); err != nil {
			return err
		}
		conflicts, err := j.conflictsLocked(entry.HostKey, entry.OperationID, entry.LockSet)
		if err != nil {
			return err
		}
		if conflicts {
			return nil
		}
		entry.Status = operationStatusRunning
		entry.Attempts++
		entry.LastError = ""
		entry.UpdatedAt = time.Now().UTC()
		if err := j.writeOperation(entry); err != nil {
			return err
		}
		if err := j.writeHostIndex(entry); err != nil {
			return err
		}
		for _, lock := range entry.LockSet {
			claim := &lockClaim{
				OperationID: entry.OperationID,
				OwnerPID:    entry.OwnerPID,
				OwnerBootID: entry.OwnerBootID,
				HostKey:     entry.HostKey,
				SessionKey:  entry.SessionKey,
				Lock:        lock,
				CreatedAt:   entry.CreatedAt,
				UpdatedAt:   entry.UpdatedAt,
			}
			if err := j.writePlainJSON(j.lockClaimPath(entry.HostKey, entry.OperationID, lock), claim); err != nil {
				return err
			}
		}
		activated = true
		return nil
	}); err != nil {
		return false, err
	}
	return activated, nil
}

func (j *operationJournal) finalize(operationID string, status operationStatus, lastErr error) error {
	entry, err := j.readOperation(operationID)
	if err != nil {
		return err
	}

	return j.withAdmissionLock(entry.HostKey, func() error {
		entry.Status = status
		entry.UpdatedAt = time.Now().UTC()
		if lastErr != nil {
			entry.LastError = lastErr.Error()
		} else {
			entry.LastError = ""
		}
		if err := j.writeOperation(entry); err != nil {
			return err
		}
		for _, lock := range entry.LockSet {
			if err := os.Remove(j.lockClaimPath(entry.HostKey, entry.OperationID, lock)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove lock claim: %w", err)
			}
		}
		if err := os.Remove(j.currentHostOperationPath(entry.HostKey, entry.OperationID)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove host operation index: %w", err)
		}
		return nil
	})
}

func (j *operationJournal) conflictsLocked(hostKey, operationID string, locks []hostrpc.LockDescriptor) (bool, error) {
	claims, err := j.readLockClaims(hostKey)
	if err != nil {
		return false, err
	}
	for _, claim := range claims {
		if claim.OperationID == operationID {
			continue
		}
		for _, requested := range locks {
			if locksConflict(claim.Lock, requested) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (j *operationJournal) abandonStaleHostEntriesLocked(hostKey string, ownerPID int) error {
	currentBootID := operationJournalBootID()
	entries, err := j.readHostOperationIndexes(hostKey)
	if err != nil {
		return err
	}
	for _, idx := range entries {
		if idx.OwnerPID == ownerPID && sameOperationJournalBoot(idx.OwnerBootID, currentBootID) {
			continue
		}
		if !isActiveOperationStatus(idx.Status) {
			continue
		}
		if operationJournalOwnerActive(idx.OwnerPID, idx.OwnerBootID, currentBootID) {
			continue
		}
		entry, err := j.readOperation(idx.OperationID)
		if err != nil {
			if purgeErr := j.removeClaimsForOperation(hostKey, idx.OperationID); purgeErr != nil {
				return purgeErr
			}
			_ = os.Remove(j.currentHostOperationPath(hostKey, idx.OperationID))
			_ = os.Remove(j.operationPath(idx.OperationID))
			continue
		}
		entry.Status = operationStatusAbandoned
		entry.LastError = fmt.Sprintf("abandoned stale operation from owner pid %d", idx.OwnerPID)
		entry.UpdatedAt = time.Now().UTC()
		if err := j.writeOperation(entry); err != nil {
			return err
		}
		for _, lock := range entry.LockSet {
			if err := os.Remove(j.lockClaimPath(entry.HostKey, entry.OperationID, lock)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := os.Remove(j.currentHostOperationPath(entry.HostKey, entry.OperationID)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (j *operationJournal) removeClaimsForOperation(hostKey, operationID string) error {
	entries, err := os.ReadDir(j.hostLocksPath(hostKey))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(j.hostLocksPath(hostKey), entry.Name())
		var claim lockClaim
		if err := j.readPlainJSON(path, &claim); err != nil {
			_ = os.Remove(path)
			continue
		}
		if claim.OperationID == operationID {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func (j *operationJournal) ensureDirs(hostKey string) error {
	for _, dir := range []string{j.operationsDir(), j.currentHostDir(hostKey), j.hostLocksPath(hostKey), j.admissionDir()} {
		if err := ensureWritableJournalDir(dir); err != nil {
			return err
		}
	}
	return nil
}

func (j *operationJournal) withAdmissionLock(hostKey string, fn func() error) error {
	if err := ensureWritableJournalDir(j.admissionDir()); err != nil {
		return err
	}

	lockFile, err := sharedJournalLock(filepath.Join(j.admissionDir(), digestPathComponent(hostKey)))
	if err != nil {
		return err
	}
	defer capabilities.FileUnlock(lockFile)
	return fn()
}

func (j *operationJournal) operationsDir() string {
	return j.dir
}

func (j *operationJournal) admissionDir() string {
	return filepath.Join(j.dir, "admission")
}

func (j *operationJournal) currentHostDir(hostKey string) string {
	return filepath.Join(j.dir, "current", "hosts", digestPathComponent(hostKey))
}

func (j *operationJournal) hostLocksPath(hostKey string) string {
	return filepath.Join(j.dir, "current", "locks", digestPathComponent(hostKey))
}

func (j *operationJournal) operationPath(operationID string) string {
	return filepath.Join(j.operationsDir(), operationID+".json")
}

func (j *operationJournal) currentHostOperationPath(hostKey, operationID string) string {
	return filepath.Join(j.currentHostDir(hostKey), operationID+".json")
}

func (j *operationJournal) lockClaimPath(hostKey, operationID string, lock hostrpc.LockDescriptor) string {
	name := digestPathComponent(operationID + "|" + lock.Key + "|" + string(lock.Mode))
	return filepath.Join(j.hostLocksPath(hostKey), name+".json")
}

func (j *operationJournal) readOperation(operationID string) (*operationJournalEntry, error) {
	data, err := os.ReadFile(j.operationPath(operationID))
	if err != nil {
		return nil, err
	}
	var entry operationJournalEntry
	if err := unmarshalEncryptedJSON(data, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

func (j *operationJournal) readLockClaims(hostKey string) ([]lockClaim, error) {
	currentBootID := operationJournalBootID()
	entries, err := os.ReadDir(j.hostLocksPath(hostKey))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read lock claims: %w", err)
	}
	claims := make([]lockClaim, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var claim lockClaim
		if err := j.readPlainJSON(filepath.Join(j.hostLocksPath(hostKey), entry.Name()), &claim); err != nil {
			_ = os.Remove(filepath.Join(j.hostLocksPath(hostKey), entry.Name()))
			continue
		}
		if !operationJournalOwnerActive(claim.OwnerPID, claim.OwnerBootID, currentBootID) {
			_ = os.Remove(filepath.Join(j.hostLocksPath(hostKey), entry.Name()))
			continue
		}
		claims = append(claims, claim)
	}
	return claims, nil
}

func (j *operationJournal) readHostOperationIndexes(hostKey string) ([]hostOperationIndex, error) {
	entries, err := os.ReadDir(j.currentHostDir(hostKey))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read host indexes: %w", err)
	}
	result := make([]hostOperationIndex, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var idx hostOperationIndex
		if err := j.readPlainJSON(filepath.Join(j.currentHostDir(hostKey), entry.Name()), &idx); err != nil {
			_ = os.Remove(filepath.Join(j.currentHostDir(hostKey), entry.Name()))
			continue
		}
		result = append(result, idx)
	}
	return result, nil
}

func (j *operationJournal) writeOperation(entry *operationJournalEntry) error {
	data, err := marshalEncryptedJSON(entry)
	if err != nil {
		return err
	}
	return capabilities.FileWrite(j.operationPath(entry.OperationID), string(data), sharedJournalFileMode)
}

func (j *operationJournal) writeHostIndex(entry *operationJournalEntry) error {
	idx := hostOperationIndex{
		OperationID: entry.OperationID,
		RequestID:   entry.RequestID,
		OwnerPID:    entry.OwnerPID,
		OwnerBootID: entry.OwnerBootID,
		Status:      entry.Status,
		CreatedAt:   entry.CreatedAt,
		UpdatedAt:   entry.UpdatedAt,
	}
	return j.writePlainJSON(j.currentHostOperationPath(entry.HostKey, entry.OperationID), idx)
}

func (j *operationJournal) findOrCreateOperation(params hostrpc.OperationAcquireParams, now time.Time) (*operationJournalEntry, error) {
	var entry *operationJournalEntry
	err := j.withAdmissionLock(params.HostKey, func() error {
		if err := j.abandonStaleHostEntriesLocked(params.HostKey, os.Getpid()); err != nil {
			return err
		}

		if strings.TrimSpace(params.RequestID) != "" {
			existing, err := j.findOperationByRequestIDLocked(params.HostKey, params.RequestID)
			if err != nil {
				return err
			}
			if existing != nil {
				entry = existing
				return nil
			}
		}

		entry = &operationJournalEntry{
			OperationID:  newJournalOperationID(),
			RequestID:    params.RequestID,
			OwnerPID:     os.Getpid(),
			OwnerBootID:  operationJournalBootID(),
			HostKey:      params.HostKey,
			SessionKey:   params.SessionKey,
			ModuleName:   params.ModuleName,
			ResourceType: params.ResourceType,
			Action:       params.Action,
			Name:         params.Name,
			Status:       operationStatusQueued,
			LockSet:      normalizeLockSet(params.LockSet),
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if err := j.writeOperation(entry); err != nil {
			return err
		}
		if err := j.writeHostIndex(entry); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entry, nil
}

func (j *operationJournal) findOperationByRequestIDLocked(hostKey, requestID string) (*operationJournalEntry, error) {
	indexes, err := j.readHostOperationIndexes(hostKey)
	if err != nil {
		return nil, err
	}
	for _, idx := range indexes {
		if strings.TrimSpace(idx.RequestID) == "" || idx.RequestID != requestID {
			continue
		}
		entry, err := j.readOperation(idx.OperationID)
		if err != nil {
			_ = os.Remove(j.currentHostOperationPath(hostKey, idx.OperationID))
			continue
		}
		return entry, nil
	}
	return nil, nil
}

func (j *operationJournal) writePlainJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return capabilities.FileWrite(path, string(data), sharedJournalFileMode)
}

func (j *operationJournal) readPlainJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func readOperationJournalBootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func sameOperationJournalBoot(ownerBootID, currentBootID string) bool {
	if ownerBootID == "" || currentBootID == "" {
		return true
	}
	return ownerBootID == currentBootID
}

func operationJournalOwnerActive(ownerPID int, ownerBootID, currentBootID string) bool {
	if ownerPID == 0 {
		return false
	}
	if !sameOperationJournalBoot(ownerBootID, currentBootID) {
		return false
	}
	return processExists(ownerPID)
}

func normalizeLockSet(locks []hostrpc.LockDescriptor) []hostrpc.LockDescriptor {
	if len(locks) == 0 {
		return nil
	}
	normalized := make([]hostrpc.LockDescriptor, 0, len(locks))
	seen := make(map[string]struct{}, len(locks))
	for _, lock := range locks {
		if lock.Key == "" {
			continue
		}
		if lock.Mode == "" {
			lock.Mode = hostrpc.LockModeExclusive
		}
		key := lock.Key + "|" + string(lock.Mode)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, lock)
	}
	return normalized
}

func locksConflict(active, requested hostrpc.LockDescriptor) bool {
	if active.Key == "" || requested.Key == "" {
		return false
	}
	if isDominatingLockKey(active.Key) || isDominatingLockKey(requested.Key) {
		return true
	}
	if active.Key != requested.Key {
		return false
	}
	return !(active.Mode == hostrpc.LockModeShared && requested.Mode == hostrpc.LockModeShared)
}

func isDominatingLockKey(key string) bool {
	switch key {
	case "host", "reboot:host":
		return true
	default:
		return false
	}
}

func isActiveOperationStatus(status operationStatus) bool {
	switch status {
	case operationStatusQueued, operationStatusRunning, operationStatusRecovering:
		return true
	default:
		return false
	}
}

func digestPathComponent(value string) string {
	digest := digestutil.MustDigestBytes(digestutil.AlgorithmXXH3_128, []byte(value))
	return digestutil.Token(digest)
}

func newJournalOperationID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return digestutil.Token(digestutil.MustDigestBytes(digestutil.AlgorithmXXH3_128, []byte(time.Now().UTC().Format(time.RFC3339Nano))))
	}
	return fmt.Sprintf("rand:%x", buf)
}

func defaultExecutorJournalBaseDir() string {
	if dir := os.Getenv("TF_LINUX_PROVIDER_EXECUTOR_JOURNAL_DIR"); dir != "" {
		return dir
	}

	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "tf-linux-provider", "journals")
	}

	if homeDir, err := os.UserHomeDir(); err == nil && homeDir != "" {
		return filepath.Join(homeDir, ".local", "state", "tf-linux-provider", "journals")
	}

	if userName := firstNonEmpty(os.Getenv("USER"), os.Getenv("LOGNAME")); userName != "" {
		return filepath.Join(os.TempDir(), "tf-linux-provider", userName, "journals")
	}

	return filepath.Join(os.TempDir(), "tf-linux-provider", "journals")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}

func defaultRestartJournalDir() string {
	if dir := os.Getenv("TF_LINUX_PROVIDER_EXECUTOR_ACTIONS_DIR"); dir != "" {
		return dir
	}

	return filepath.Join(defaultExecutorJournalBaseDir(), "actions")
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

func ensureWritableJournalDir(path string) error {
	if err := os.MkdirAll(path, sharedJournalDirMode); err != nil {
		return fmt.Errorf("create journal dir %s: %w", path, err)
	}
	if err := os.Chmod(path, sharedJournalDirMode); err != nil {
		if writableErr := verifyWritableJournalDir(path); writableErr != nil {
			return fmt.Errorf("chmod journal dir %s: %w", path, err)
		}
	}
	if err := verifyWritableJournalDir(path); err != nil {
		return err
	}
	return nil
}

func sharedJournalLock(path string) (*os.File, error) {
	lockPath := path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE, sharedJournalFileMode)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
	}
	if err := os.Chmod(lockPath, sharedJournalFileMode); err != nil && !errors.Is(err, os.ErrPermission) {
		f.Close()
		return nil, fmt.Errorf("chmod lock file %s: %w", lockPath, err)
	}
	if err := flockExclusive(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock %s: %w", lockPath, err)
	}
	return f, nil
}

func verifyWritableJournalDir(path string) error {
	tmp, err := os.CreateTemp(path, ".permcheck.*")
	if err != nil {
		return fmt.Errorf("verify journal dir %s writable: %w", path, err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close journal permcheck %s: %w", tmpPath, err)
	}
	if err := os.Remove(tmpPath); err != nil {
		return fmt.Errorf("remove journal permcheck %s: %w", tmpPath, err)
	}
	return nil
}

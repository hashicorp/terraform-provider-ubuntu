package runtime

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/executor/capabilities"
	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

const (
	rebootPhasePlanned                = "planned"
	rebootPhasePrecheckComplete       = "precheck_complete"
	rebootPhaseTargetPrepared         = "target_prepared"
	rebootPhaseRebootIssued           = "reboot_issued"
	rebootPhaseWaitingForReconnect    = "waiting_for_reconnect"
	rebootPhasePostReconnectValidated = "post_reconnect_validation"
	rebootPhaseExecutorRebootstrapped = "executor_rebootstrapped"
	rebootPhaseCompleted              = "completed"
	rebootPhaseFailed                 = "failed"
)

type rebootJournal struct {
	dir string
}

func newRebootJournal() *rebootJournal {
	return &rebootJournal{dir: defaultRebootJournalDir()}
}

func defaultRebootJournalDir() string {
	if dir := os.Getenv("TF_NIX_EXECUTOR_REBOOT_JOURNAL_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(defaultExecutorJournalBaseDir(), "reboots")
}

func (j *rebootJournal) Prepare(params hostrpc.RebootJournalPrepareParams) (*hostrpc.RebootJournalEntry, error) {
	if err := j.ensureDirs(); err != nil {
		return nil, err
	}
	fingerprint, err := rebootFingerprint(params)
	if err != nil {
		return nil, err
	}
	currentPath := j.currentPath(fingerprint)
	if existing, err := j.readEntry(currentPath); err == nil {
		switch existing.Phase {
		case rebootPhasePlanned, rebootPhasePrecheckComplete, rebootPhaseTargetPrepared, rebootPhaseRebootIssued, rebootPhaseWaitingForReconnect, rebootPhasePostReconnectValidated, rebootPhaseExecutorRebootstrapped, rebootPhaseFailed:
			return existing, nil
		case rebootPhaseCompleted:
			_ = os.Remove(currentPath)
		}
	} else if !os.IsNotExist(err) {
		_ = os.Remove(currentPath)
	}

	now := time.Now().UTC()
	entry := &hostrpc.RebootJournalEntry{
		OperationID:    newRebootOperationID(),
		Fingerprint:    fingerprint,
		HostAddress:    params.HostAddress,
		Name:           params.Name,
		Reason:         params.Reason,
		RebootCommand:  params.RebootCommand,
		Phase:          rebootPhasePlanned,
		TimeoutSeconds: params.TimeoutSeconds,
		SettleSeconds:  params.SettleSeconds,
		RequestedAt:    now.Format(time.RFC3339Nano),
		UpdatedAt:      now.Format(time.RFC3339Nano),
	}
	if err := j.writeEntry(j.operationPath(entry.OperationID), entry); err != nil {
		return nil, err
	}
	if err := j.writeEntry(currentPath, entry); err != nil {
		return nil, err
	}
	return entry, nil
}

func (j *rebootJournal) MarkPhase(params hostrpc.RebootJournalMarkPhaseParams) (*hostrpc.RebootJournalEntry, error) {
	return j.update(params.OperationID, func(entry *hostrpc.RebootJournalEntry) {
		entry.Phase = params.Phase
		if params.StableHostID != "" {
			entry.StableHostID = params.StableHostID
		}
		if params.PreBootID != "" {
			entry.PreBootID = params.PreBootID
		}
		if params.PostBootID != "" {
			entry.PostBootID = params.PostBootID
		}
		if params.RebootCommand != "" {
			entry.RebootCommand = params.RebootCommand
		}
	})
}

func (j *rebootJournal) MarkFailed(params hostrpc.RebootJournalMarkFailedParams) (*hostrpc.RebootJournalEntry, error) {
	return j.update(params.OperationID, func(entry *hostrpc.RebootJournalEntry) {
		entry.Phase = rebootPhaseFailed
		entry.LastError = params.LastError
	})
}

func (j *rebootJournal) MarkCompleted(params hostrpc.RebootJournalMarkCompletedParams) (*hostrpc.RebootJournalEntry, error) {
	return j.update(params.OperationID, func(entry *hostrpc.RebootJournalEntry) {
		now := time.Now().UTC()
		entry.Phase = rebootPhaseCompleted
		if params.PostBootID != "" {
			entry.PostBootID = params.PostBootID
		}
		entry.CompletedAt = now.Format(time.RFC3339Nano)
		entry.LastError = ""
	})
}

func (j *rebootJournal) update(operationID string, mutate func(*hostrpc.RebootJournalEntry)) (*hostrpc.RebootJournalEntry, error) {
	entry, err := j.readEntry(j.operationPath(operationID))
	if err != nil {
		return nil, err
	}
	mutate(entry)
	entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := j.writeEntry(j.operationPath(operationID), entry); err != nil {
		return nil, err
	}
	currentPath := j.currentPath(entry.Fingerprint)
	if entry.Phase == rebootPhaseCompleted {
		if err := os.Remove(currentPath); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		return entry, nil
	}
	if err := j.writeEntry(currentPath, entry); err != nil {
		return nil, err
	}
	return entry, nil
}

func (j *rebootJournal) ensureDirs() error {
	for _, dir := range []string{j.operationsDir(), j.currentDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create reboot journal dir %s: %w", dir, err)
		}
	}
	return nil
}

func (j *rebootJournal) operationsDir() string {
	return filepath.Join(j.dir, "operations")
}

func (j *rebootJournal) currentDir() string {
	return filepath.Join(j.dir, "current")
}

func (j *rebootJournal) operationPath(operationID string) string {
	return filepath.Join(j.operationsDir(), operationID+".json")
}

func (j *rebootJournal) currentPath(fingerprint string) string {
	return filepath.Join(j.currentDir(), fingerprint+".json")
}

func (j *rebootJournal) readEntry(path string) (*hostrpc.RebootJournalEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entry hostrpc.RebootJournalEntry
	if err := unmarshalEncryptedJSON(data, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

func (j *rebootJournal) writeEntry(path string, entry *hostrpc.RebootJournalEntry) error {
	data, err := marshalEncryptedJSON(entry)
	if err != nil {
		return err
	}
	return capabilities.FileWrite(path, string(data), 0o600)
}

func rebootFingerprint(params hostrpc.RebootJournalPrepareParams) (string, error) {
	payload := map[string]string{
		"host_address":   params.HostAddress,
		"name":           params.Name,
		"reason":         params.Reason,
		"reboot_command": params.RebootCommand,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal reboot fingerprint: %w", err)
	}
	return digestutil.DigestBytes(digestutil.AlgorithmXXH3_128, data)
}

func newRebootOperationID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return digestutil.Token(digestutil.MustDigestBytes(digestutil.AlgorithmXXH3_128, []byte(time.Now().UTC().Format(time.RFC3339Nano))))
	}
	return fmt.Sprintf("rand:%x", buf)
}

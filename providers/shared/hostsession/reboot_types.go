package hostsession

import "time"

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

type rebootJournalEntry struct {
	OperationID    string
	Fingerprint    string
	HostAddress    string
	Name           string
	Reason         string
	RebootCommand  string
	Phase          string
	StableHostID   string
	PreBootID      string
	PostBootID     string
	TimeoutSeconds int
	SettleSeconds  int
	RequestedAt    time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time
	LastError      string
}

// Copyright IBM Corp. 2026

package hostrpc

import (
	"encoding/json"
	"fmt"
)

const DefaultCompressionThreshold = 4 * 1024

const (
	MethodExecutorDiscover           = "executor.discover"
	MethodExecutorShutdown           = "executor.shutdown"
	MethodJournalConfigure           = "journal.configure"
	MethodJournalOperationAcquire    = "journal.operation_acquire"
	MethodJournalOperationRelease    = "journal.operation_release"
	MethodJournalRebootPrepare       = "journal.reboot_prepare"
	MethodJournalRebootMarkPhase     = "journal.reboot_mark_phase"
	MethodJournalRebootMarkFailed    = "journal.reboot_mark_failed"
	MethodJournalRebootMarkCompleted = "journal.reboot_mark_completed"
	MethodHostCommand                = "host.command"
	MethodActionInvoke               = "action.invoke"
	MethodActionRestart              = "action.restart_process"
	MethodModuleLoad                 = "module.load"
	MethodResourceValidate           = "resource.validate"
	MethodResourceRead               = "resource.read"
	MethodResourceCreate             = "resource.create"
	MethodResourceUpdate             = "resource.update"
	MethodResourceDelete             = "resource.delete"
	MethodResourceImport             = "resource.import"
	MethodDataSourceRead             = "datasource.read"
)

type ExecutionContext struct {
	Become     bool   `json:"become,omitempty"`
	BecomeUser string `json:"become_user,omitempty"`
}

type HostProfile struct {
	Hostname      string            `json:"hostname"`
	DistroID      string            `json:"distro_id"`
	DistroName    string            `json:"distro_name"`
	DistroVersion string            `json:"distro_version"`
	DistroFamily  string            `json:"distro_family"`
	Kernel        string            `json:"kernel"`
	KernelVersion string            `json:"kernel_version"`
	Arch          string            `json:"arch"`
	InitSystem    string            `json:"init_system"`
	PackageMgr    string            `json:"package_manager"`
	SELinux       bool              `json:"selinux"`
	AppArmor      bool              `json:"apparmor"`
	AvailableCmds []string          `json:"available_commands"`
	Extra         map[string]string `json:"extra,omitempty"`
}

type ModuleLoadParams struct {
	Name                   string `json:"name"`
	UsePostQuantumDigests  bool   `json:"use_post_quantum_digests,omitempty"`
	DualPluginVerification bool   `json:"dual_plugin_verification,omitempty"`
	WasmCompression        string `json:"wasm_compression,omitempty"`
	Wasm                   []byte `json:"wasm"`
}

type ModuleLoadResult struct {
	Name   string `json:"name"`
	Loaded bool   `json:"loaded"`
}

type RestartProcessParams struct {
	ModuleName  string            `json:"module_name,omitempty"`
	OperationID string            `json:"operation_id,omitempty"`
	Name        string            `json:"name"`
	Command     string            `json:"command,omitempty"`
	Manager     string            `json:"manager,omitempty"`
	Execution   *ExecutionContext `json:"execution,omitempty"`
}

type ActionInvokeParams struct {
	ModuleName   string            `json:"module_name,omitempty"`
	ResourceType string            `json:"resource_type"`
	Config       json.RawMessage   `json:"config,omitempty"`
	Execution    *ExecutionContext `json:"execution,omitempty"`
}

type CommandResult struct {
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
}

type LockMode string

const (
	LockModeShared    LockMode = "shared"
	LockModeExclusive LockMode = "exclusive"
)

type LockDescriptor struct {
	Key    string   `json:"key"`
	Mode   LockMode `json:"mode"`
	Source string   `json:"source,omitempty"`
}

type JournalConfigureParams struct {
	Key []byte `json:"key"`
}

type OperationAcquireParams struct {
	RequestID    string           `json:"request_id,omitempty"`
	HostKey      string           `json:"host_key"`
	SessionKey   string           `json:"session_key"`
	ModuleName   string           `json:"module_name,omitempty"`
	ResourceType string           `json:"resource_type,omitempty"`
	Action       string           `json:"action"`
	Name         string           `json:"name,omitempty"`
	LockSet      []LockDescriptor `json:"lock_set,omitempty"`
	TimeoutMs    int64            `json:"timeout_ms,omitempty"`
}

type OperationAcquireResult struct {
	OperationID string `json:"operation_id"`
	Granted     bool   `json:"granted,omitempty"`
}

type OperationReleaseParams struct {
	HostKey     string `json:"host_key"`
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
	LastError   string `json:"last_error,omitempty"`
}

type RebootJournalPrepareParams struct {
	HostAddress    string `json:"host_address"`
	Name           string `json:"name"`
	Reason         string `json:"reason"`
	RebootCommand  string `json:"reboot_command,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	SettleSeconds  int    `json:"settle_seconds,omitempty"`
}

type RebootJournalEntry struct {
	OperationID    string `json:"operation_id"`
	Fingerprint    string `json:"fingerprint"`
	HostAddress    string `json:"host_address"`
	Name           string `json:"name"`
	Reason         string `json:"reason"`
	RebootCommand  string `json:"reboot_command,omitempty"`
	Phase          string `json:"phase"`
	StableHostID   string `json:"stable_host_id,omitempty"`
	PreBootID      string `json:"pre_boot_id,omitempty"`
	PostBootID     string `json:"post_boot_id,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	SettleSeconds  int    `json:"settle_seconds,omitempty"`
	RequestedAt    string `json:"requested_at"`
	UpdatedAt      string `json:"updated_at"`
	CompletedAt    string `json:"completed_at,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

type RebootJournalMarkPhaseParams struct {
	OperationID   string `json:"operation_id"`
	Phase         string `json:"phase"`
	StableHostID  string `json:"stable_host_id,omitempty"`
	PreBootID     string `json:"pre_boot_id,omitempty"`
	PostBootID    string `json:"post_boot_id,omitempty"`
	RebootCommand string `json:"reboot_command,omitempty"`
}

type RebootJournalMarkFailedParams struct {
	OperationID string `json:"operation_id"`
	LastError   string `json:"last_error,omitempty"`
}

type RebootJournalMarkCompletedParams struct {
	OperationID string `json:"operation_id"`
	PostBootID  string `json:"post_boot_id,omitempty"`
}

type HostCommandParams struct {
	Name      string            `json:"name"`
	Args      []string          `json:"args,omitempty"`
	Execution *ExecutionContext `json:"execution,omitempty"`
}

type ResourceReadParams struct {
	ModuleName   string            `json:"module_name,omitempty"`
	ResourceType string            `json:"resource_type"`
	State        json.RawMessage   `json:"state,omitempty"`
	Execution    *ExecutionContext `json:"execution,omitempty"`
}

type ResourceValidateParams struct {
	ModuleName   string            `json:"module_name,omitempty"`
	ResourceType string            `json:"resource_type"`
	Config       json.RawMessage   `json:"config,omitempty"`
	Execution    *ExecutionContext `json:"execution,omitempty"`
}

type ResourceCreateParams struct {
	ModuleName   string            `json:"module_name,omitempty"`
	ResourceType string            `json:"resource_type"`
	Plan         json.RawMessage   `json:"plan,omitempty"`
	Execution    *ExecutionContext `json:"execution,omitempty"`
}

type ResourceUpdateParams struct {
	ModuleName   string            `json:"module_name,omitempty"`
	ResourceType string            `json:"resource_type"`
	State        json.RawMessage   `json:"state,omitempty"`
	Plan         json.RawMessage   `json:"plan,omitempty"`
	Execution    *ExecutionContext `json:"execution,omitempty"`
}

type ResourceDeleteParams struct {
	ModuleName   string            `json:"module_name,omitempty"`
	ResourceType string            `json:"resource_type"`
	State        json.RawMessage   `json:"state,omitempty"`
	Execution    *ExecutionContext `json:"execution,omitempty"`
}

type ResourceImportParams struct {
	ModuleName   string            `json:"module_name,omitempty"`
	ResourceType string            `json:"resource_type"`
	ImportID     string            `json:"import_id"`
	Execution    *ExecutionContext `json:"execution,omitempty"`
}

type DataSourceReadParams struct {
	ModuleName   string            `json:"module_name,omitempty"`
	ResourceType string            `json:"resource_type"`
	Config       json.RawMessage   `json:"config,omitempty"`
	Execution    *ExecutionContext `json:"execution,omitempty"`
}

type OperationResult struct {
	State json.RawMessage `json:"state,omitempty"`
}

func MethodForAction(action string) (string, error) {
	switch action {
	case "validate":
		return MethodResourceValidate, nil
	case "read":
		return MethodResourceRead, nil
	case "create":
		return MethodResourceCreate, nil
	case "update":
		return MethodResourceUpdate, nil
	case "delete":
		return MethodResourceDelete, nil
	case "import":
		return MethodResourceImport, nil
	case "data_read":
		return MethodDataSourceRead, nil
	default:
		return "", fmt.Errorf("unsupported action %q", action)
	}
}

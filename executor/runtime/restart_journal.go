//go:build !windows && !js && !wasm

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/executor/capabilities"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

const (
	restartStatusLaunching   = "launching"
	restartStatusRunning     = "running"
	restartStatusCompleted   = "completed"
	restartStatusLaunchError = "launch_error"
)

type restartCommandSpec struct {
	Name    string   `json:"name,omitempty"`
	Args    []string `json:"args,omitempty"`
	Command string   `json:"command,omitempty"`
}

type restartOperationRecord struct {
	OperationID string                    `json:"operation_id"`
	Name        string                    `json:"name"`
	CommandName string                    `json:"command_name,omitempty"`
	Args        []string                  `json:"args,omitempty"`
	Command     string                    `json:"command,omitempty"`
	Execution   *hostrpc.ExecutionContext `json:"execution,omitempty"`
	Status      string                    `json:"status"`
	HelperPID   int                       `json:"helper_pid,omitempty"`
	Result      hostrpc.CommandResult     `json:"result"`
	LastError   string                    `json:"last_error,omitempty"`
	CreatedAt   time.Time                 `json:"created_at"`
	UpdatedAt   time.Time                 `json:"updated_at"`
	CompletedAt *time.Time                `json:"completed_at,omitempty"`
}

var restartJournalMu sync.Mutex

func ensureRestartJournalDir() error {
	return os.MkdirAll(defaultRestartJournalDir(), 0o755)
}

func restartRecordPath(operationID string) string {
	return filepath.Join(defaultRestartJournalDir(), operationID+".json")
}

func restartLockPath(operationID string) string {
	return filepath.Join(defaultRestartJournalDir(), operationID+".lock")
}

func lockRestartOperation(operationID string) (*os.File, error) {
	if err := ensureRestartJournalDir(); err != nil {
		return nil, err
	}

	lockFile, err := os.OpenFile(restartLockPath(operationID), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open restart lock: %w", err)
	}

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("lock restart operation: %w", err)
	}

	return lockFile, nil
}

func unlockRestartOperation(lockFile *os.File) error {
	if lockFile == nil {
		return nil
	}
	defer lockFile.Close()
	return syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
}

func readRestartRecord(operationID string) (*restartOperationRecord, error) {
	data, err := os.ReadFile(restartRecordPath(operationID))
	if err != nil {
		return nil, err
	}

	var record restartOperationRecord
	if err := unmarshalEncryptedJSON(data, &record); err != nil {
		return nil, fmt.Errorf("decode restart record: %w", err)
	}

	return &record, nil
}

func writeRestartRecord(record *restartOperationRecord) error {
	if err := ensureRestartJournalDir(); err != nil {
		return err
	}

	record.UpdatedAt = time.Now().UTC()
	data, err := marshalEncryptedJSON(record)
	if err != nil {
		return fmt.Errorf("marshal restart record: %w", err)
	}
	if err := capabilities.FileWrite(restartRecordPath(record.OperationID), string(data), 0o600); err != nil {
		return fmt.Errorf("write restart record: %w", err)
	}

	return nil
}

func resolveRestartCommand(ctx context.Context, d *Dispatcher, moduleName string, params hostrpc.RestartProcessParams) (*restartCommandSpec, error) {
	config, err := json.Marshal(map[string]string{
		"name":    params.Name,
		"command": params.Command,
		"manager": params.Manager,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal restart action config: %w", err)
	}

	input := json.RawMessage(config)
	if usesModuleDispatch(moduleName, "restart_process") {
		input = marshalModulePayload("restart_process", "invoke", nil, nil, input, "")
	}

	result, err := d.rt.CallInvoke(ctx, moduleName, input)
	if err != nil {
		return nil, err
	}

	result, err = unwrapPluginState(result)
	if err != nil {
		return nil, err
	}

	var spec restartCommandSpec
	if err := json.Unmarshal(result, &spec); err != nil {
		return nil, fmt.Errorf("decode restart command: %w", err)
	}
	if err := normalizeRestartCommandSpec(&spec); err != nil {
		return nil, err
	}

	return &spec, nil
}

func normalizeRestartCommandSpec(spec *restartCommandSpec) error {
	if spec == nil {
		return fmt.Errorf("restart action returned empty command")
	}
	if strings.TrimSpace(spec.Name) == "" {
		spec.Command = strings.TrimSpace(spec.Command)
		if spec.Command == "" {
			return fmt.Errorf("restart action returned empty command")
		}
		spec.Name = "sh"
		spec.Args = []string{"-lc", spec.Command}
	}
	if len(spec.Args) == 0 {
		spec.Args = nil
	} else {
		spec.Args = append([]string(nil), spec.Args...)
	}
	return nil
}

func startRestartHelper(record *restartOperationRecord) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executor path: %w", err)
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devNull.Close()

	cmd := exec.Command(execPath, "--run-restart-journal", restartRecordPath(record.OperationID))
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	journalKey, err := runtimeJournalKeyEnv()
	if err != nil {
		return fmt.Errorf("resolve journal key: %w", err)
	}
	cmd.Env = append(os.Environ(), journalKeyEnvVar+"="+journalKey)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start restart helper: %w", err)
	}

	record.HelperPID = cmd.Process.Pid
	record.Status = restartStatusRunning
	record.LastError = ""

	return writeRestartRecord(record)
}

func waitForRestartResult(ctx context.Context, operationID string) (*hostrpc.CommandResult, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		record, err := readRestartRecord(operationID)
		if err != nil {
			return nil, fmt.Errorf("read restart record: %w", err)
		}

		switch record.Status {
		case restartStatusCompleted:
			result := record.Result
			return &result, nil
		case restartStatusLaunchError:
			return nil, fmt.Errorf("restart launch failed: %s", record.LastError)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func RunJournaledRestart(recordPath string) error {
	restartJournalMu.Lock()
	defer restartJournalMu.Unlock()

	if err := loadRuntimeJournalKeyFromEnv(); err != nil {
		return fmt.Errorf("load journal key: %w", err)
	}

	data, err := os.ReadFile(recordPath)
	if err != nil {
		return fmt.Errorf("read restart journal: %w", err)
	}

	var record restartOperationRecord
	if err := unmarshalEncryptedJSON(data, &record); err != nil {
		return fmt.Errorf("decode restart journal: %w", err)
	}

	if record.Status == restartStatusCompleted {
		return nil
	}

	record.Status = restartStatusRunning
	record.LastError = ""
	if err := writeRestartRecord(&record); err != nil {
		return err
	}

	ctx := capabilities.WithExecutionContext(context.Background(), record.Execution)
	if strings.TrimSpace(record.CommandName) == "" {
		legacy := restartCommandSpec{Command: record.Command}
		if err := normalizeRestartCommandSpec(&legacy); err != nil {
			return err
		}
		record.CommandName = legacy.Name
		record.Args = legacy.Args
	}
	result := capabilities.CmdExec(ctx, record.CommandName, record.Args...)
	record.Result = hostrpc.CommandResult{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
	}
	record.Status = restartStatusCompleted
	now := time.Now().UTC()
	record.CompletedAt = &now

	return writeRestartRecord(&record)
}

func prepareRestartOperation(operationID, name string, spec *restartCommandSpec, execution *hostrpc.ExecutionContext) (*restartOperationRecord, bool, error) {
	lockFile, err := lockRestartOperation(operationID)
	if err != nil {
		return nil, false, err
	}
	defer unlockRestartOperation(lockFile)

	record, err := readRestartRecord(operationID)
	if err == nil {
		switch record.Status {
		case restartStatusCompleted, restartStatusRunning:
			return record, false, nil
		case restartStatusLaunching:
			return record, false, nil
		case restartStatusLaunchError:
			// Safe to try launching the helper again with the same command.
			record.Status = restartStatusLaunching
			record.LastError = ""
			if err := writeRestartRecord(record); err != nil {
				return nil, false, err
			}
			return record, true, nil
		default:
			return record, false, nil
		}
	}

	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}

	now := time.Now().UTC()
	record = &restartOperationRecord{
		OperationID: operationID,
		Name:        name,
		CommandName: spec.Name,
		Args:        append([]string(nil), spec.Args...),
		Command:     spec.Command,
		Execution:   execution,
		Status:      restartStatusLaunching,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := writeRestartRecord(record); err != nil {
		return nil, false, err
	}

	return record, true, nil
}

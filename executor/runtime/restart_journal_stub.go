// Copyright IBM Corp. 2026

//go:build windows || js || wasm

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

const restartStatusLaunchError = "launch_error"

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
	Result      hostrpc.CommandResult     `json:"result"`
	LastError   string                    `json:"last_error,omitempty"`
}

func resolveRestartCommand(ctx context.Context, d *Dispatcher, moduleName string, params hostrpc.RestartProcessParams) (*restartCommandSpec, error) {
	config, err := marshalRestartConfig(params)
	if err != nil {
		return nil, err
	}

	input := config
	if usesModuleDispatch(moduleName, "restart_process") {
		input = d.marshalModulePayload("restart_process", "invoke", nil, nil, config, "")
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

func prepareRestartOperation(operationID, name string, spec *restartCommandSpec, execution *hostrpc.ExecutionContext) (*restartOperationRecord, bool, error) {
	return nil, false, fmt.Errorf("journaled restart is unavailable on wasm")
}

func startRestartHelper(*restartOperationRecord) error {
	return fmt.Errorf("journaled restart is unavailable on wasm")
}

func lockRestartOperation(string) (any, error) {
	return nil, fmt.Errorf("journaled restart is unavailable on wasm")
}

func writeRestartRecord(*restartOperationRecord) error {
	return fmt.Errorf("journaled restart is unavailable on wasm")
}

func unlockRestartOperation(any) error {
	return nil
}

func waitForRestartResult(context.Context, string) (*hostrpc.CommandResult, error) {
	return nil, fmt.Errorf("journaled restart is unavailable on wasm")
}

func RunJournaledRestart(string) error {
	return fmt.Errorf("journaled restart is unavailable on wasm")
}

func marshalRestartConfig(params hostrpc.RestartProcessParams) ([]byte, error) {
	return json.Marshal(map[string]string{
		"name":    params.Name,
		"command": params.Command,
		"manager": params.Manager,
	})
}

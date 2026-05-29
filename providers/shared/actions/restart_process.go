// Copyright IBM Corp. 2026

package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	frameworkaction "github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/hostsession"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

func RestartProcessExecutionPolicy(config map[string]interface{}) (*hostrpc.ExecutionContext, error) {
	user := strings.TrimSpace(stringValue(config, "user"))
	return &hostrpc.ExecutionContext{
		Become:     true,
		BecomeUser: user,
	}, nil
}

func RestartProcessLockPlanner(config map[string]interface{}) ([]hostsession.LockDescriptor, error) {
	name := strings.TrimSpace(stringValue(config, "name"))
	if name == "" {
		return []hostsession.LockDescriptor{{
			Key:    "host",
			Mode:   hostsession.LockModeExclusive,
			Source: "restart action",
		}}, nil
	}

	return []hostsession.LockDescriptor{{
		Key:    "service:" + name,
		Mode:   hostsession.LockModeExclusive,
		Source: "restart action",
	}}, nil
}

func InvokeRestartProcess(ctx context.Context, req engine.ActionInvokeRequest) (map[string]interface{}, error) {
	name := strings.TrimSpace(stringValue(req.Config, "name"))
	if name == "" {
		return nil, fmt.Errorf("name must not be empty")
	}

	command := strings.TrimSpace(stringValue(req.Config, "command"))
	manager := strings.TrimSpace(stringValue(req.Config, "manager"))
	if manager == "" {
		manager = "auto"
	}

	if req.Progress != nil {
		req.Progress(frameworkaction.InvokeProgressEvent{Message: fmt.Sprintf("Restarting %s", name)})
	}

	result, err := req.Manager.RestartProcessLocked(ctx, req.Session, hostrpc.RestartProcessParams{
		ModuleName: req.RuntimeModule,
		Name:       name,
		Command:    command,
		Manager:    manager,
		Execution:  req.Execution,
	}, req.Locks)
	if err != nil {
		return nil, err
	}

	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
		if detail == "" {
			detail = fmt.Sprintf("restart command exited with code %d", result.ExitCode)
		}
		return nil, errors.New(detail)
	}

	if req.Progress != nil {
		req.Progress(frameworkaction.InvokeProgressEvent{Message: fmt.Sprintf("Restarted %s", name)})
	}

	return map[string]interface{}{
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
	}, nil
}

func RestartProcessResultShaper(_ context.Context, result map[string]interface{}) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if result == nil {
		return diagnostics
	}
	if stderr := strings.TrimSpace(stringValue(result, "stderr")); stderr != "" {
		diagnostics.AddWarning("Restart completed with stderr output", stderr)
	}
	return diagnostics
}

func stringValue(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	raw, ok := values[key]
	if !ok {
		return ""
	}
	text, _ := raw.(string)
	return text
}

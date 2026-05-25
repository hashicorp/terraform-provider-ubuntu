// Copyright IBM Corp. 2026

package actions

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/hostsession"
)

func RunCommandLockPlanner(config map[string]interface{}) ([]hostsession.LockDescriptor, error) {
	name := strings.TrimSpace(stringValue(config, "name"))
	if name == "" {
		return []hostsession.LockDescriptor{{
			Key:    "command:host",
			Mode:   hostsession.LockModeExclusive,
			Source: "run_command action",
		}}, nil
	}

	return []hostsession.LockDescriptor{{
		Key:    "command:" + name,
		Mode:   hostsession.LockModeExclusive,
		Source: "run_command action",
	}}, nil
}

func RunCommandResultShaper(_ context.Context, result map[string]interface{}) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if result == nil {
		return diagnostics
	}
	if stderr := strings.TrimSpace(stringValue(result, "stderr")); stderr != "" {
		diagnostics.AddWarning("Command completed with stderr output", stderr)
	}
	return diagnostics
}

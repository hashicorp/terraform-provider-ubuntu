// Copyright IBM Corp. 2026

package linuxcommands

import "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func CommandResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":              {Type: pluginsdk.AttrString, Required: true, Description: "Logical name used as the Terraform resource identifier."},
			"command":           {Type: pluginsdk.AttrString, Required: true, Description: "Shell snippet executed during create and update."},
			"creates":           {Type: pluginsdk.AttrString, Optional: true, Description: "Absolute path that marks the command as already applied when it exists."},
			"unless":            {Type: pluginsdk.AttrString, Optional: true, Description: "Shell snippet that skips create and marks the resource present when it exits successfully."},
			"delete_command":    {Type: pluginsdk.AttrString, Optional: true, Description: "Optional shell snippet executed during delete."},
			"working_directory": {Type: pluginsdk.AttrString, Optional: true, Description: "Absolute working directory used for command execution."},
			"environment":       {Type: pluginsdk.AttrMap, Optional: true, Sensitive: true, Description: "Environment variables exported before the shell snippet runs."},
			"interpreter":       {Type: pluginsdk.AttrList, Optional: true, Computed: true, Description: "Interpreter argv prefix. Defaults to [\"sh\", \"-lc\"]."},
			"triggers":          {Type: pluginsdk.AttrMap, Optional: true, Description: "Opaque trigger values that can force updates when changed."},
			"run_as":            {Type: pluginsdk.AttrString, Optional: true, Description: "Execute as this user when the provider resource uses dynamic privilege."},
			"stdout":            {Type: pluginsdk.AttrString, Computed: true, Sensitive: true, Description: "Captured stdout from the most recent command execution."},
			"stderr":            {Type: pluginsdk.AttrString, Computed: true, Sensitive: true, Description: "Captured stderr from the most recent command execution."},
			"exit_code":         {Type: pluginsdk.AttrInt, Computed: true, Description: "Exit code from the most recent command execution."},
		},
	}
}

func RunCommandActionSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":              {Type: pluginsdk.AttrString, Required: true, Description: "Logical name used for progress and locking."},
			"command":           {Type: pluginsdk.AttrString, Required: true, Description: "Shell snippet executed when the action is invoked."},
			"working_directory": {Type: pluginsdk.AttrString, Optional: true, Description: "Absolute working directory used for command execution."},
			"environment":       {Type: pluginsdk.AttrMap, Optional: true, Description: "Environment variables exported before the shell snippet runs."},
			"interpreter":       {Type: pluginsdk.AttrList, Optional: true, Description: "Interpreter argv prefix. Defaults to [\"sh\", \"-lc\"]."},
			"run_as":            {Type: pluginsdk.AttrString, Optional: true, Description: "Execute as this user when the provider action uses dynamic privilege."},
		},
	}
}

package systemd

import pluginsdk "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func UnitResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":                         {Type: pluginsdk.AttrString, Required: true, Description: "Systemd unit name (e.g. nginx.service)."},
			"enabled":                      {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Whether the unit is enabled at boot."},
			"state":                        {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Desired service state: running or stopped."},
			"content":                      {Type: pluginsdk.AttrString, Optional: true, Description: "Full unit file content to manage under /etc/systemd/system. Mutually exclusive with service_user and service_group."},
			"service_user":                 {Type: pluginsdk.AttrString, Optional: true, Description: "Optional User= value written to a provider-managed service drop-in."},
			"service_group":                {Type: pluginsdk.AttrString, Optional: true, Description: "Optional Group= value written to a provider-managed service drop-in."},
			"service_identity_dropin_path": {Type: pluginsdk.AttrString, Computed: true, Description: "Path to the provider-managed service identity drop-in, when service_user or service_group is configured."},
			"masked":                       {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Whether the unit is masked."},
			"reload_on_change":             {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Whether the unit should be reloaded when reload_triggers change and the unit is already active."},
			"reload_triggers":              {Type: pluginsdk.AttrList, Optional: true, Description: "Opaque trigger values that force a reload when they change even if the rest of the unit configuration is unchanged."},
		},
	}
}

func TimezoneResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"zone": {Type: pluginsdk.AttrString, Required: true, Description: "Timezone name to configure via timedatectl (for example UTC or America/New_York)."},
		},
	}
}

func UnitInfoDataSourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":            {Type: pluginsdk.AttrString, Required: true, Description: "Systemd unit name to inspect (for example kubelet or containerd.service)."},
			"load_state":      {Type: pluginsdk.AttrString, Computed: true, Description: "Raw systemd LoadState value."},
			"active_state":    {Type: pluginsdk.AttrString, Computed: true, Description: "Raw systemd ActiveState value."},
			"sub_state":       {Type: pluginsdk.AttrString, Computed: true, Description: "Raw systemd SubState value."},
			"unit_file_state": {Type: pluginsdk.AttrString, Computed: true, Description: "Raw systemd UnitFileState value."},
			"enabled":         {Type: pluginsdk.AttrBool, Computed: true, Description: "Whether the unit is enabled at boot."},
			"masked":          {Type: pluginsdk.AttrBool, Computed: true, Description: "Whether the unit is masked."},
		},
	}
}

func RestartProcessActionSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":    {Type: pluginsdk.AttrString, Required: true, Description: "Logical process or service name used for progress and default restart resolution."},
			"command": {Type: pluginsdk.AttrString, Optional: true, Description: "Optional explicit shell command to execute for the restart. When unset, the action tries service-manager defaults."},
			"manager": {Type: pluginsdk.AttrString, Optional: true, Description: "Restart strategy: auto, systemd, or service. Defaults to auto."},
			"user":    {Type: pluginsdk.AttrString, Optional: true, Description: "Optional target user for privilege escalation. Defaults to root."},
		},
	}
}

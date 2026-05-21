package timesync

import pluginsdk "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func ResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"enabled":          {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Whether time synchronization should be enabled and the backend service kept running."},
			"servers":          {Type: pluginsdk.AttrList, Optional: true, Computed: true, Description: "Primary NTP servers managed by the backend."},
			"fallback_servers": {Type: pluginsdk.AttrList, Optional: true, Computed: true, Description: "Fallback NTP servers managed by the backend."},
			"backend":          {Type: pluginsdk.AttrString, Computed: true, Description: "Resolved time synchronization backend managing the host."},
			"config_path":      {Type: pluginsdk.AttrString, Computed: true, Description: "Backend-specific configuration file managed by tf-nix."},
			"service_name":     {Type: pluginsdk.AttrString, Computed: true, Description: "Backend-specific service name managed by tf-nix."},
		},
	}
}

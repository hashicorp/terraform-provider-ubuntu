package redhatfirewalld

import "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func ServiceResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":                 {Type: pluginsdk.AttrString, Required: true, Description: "Firewalld service name, for example ssh or https."},
			"zone":                 {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Firewalld zone name. Defaults to public."},
			"ensure":               {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Desired service outcome (present or absent). Defaults to present."},
			"allow_ssh_disconnect": {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Explicitly allow a service change that would otherwise sever the current SSH access path."},
		},
	}
}

func PortResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"port":                 {Type: pluginsdk.AttrString, Required: true, Description: "Single port or port range exposed through the firewalld zone."},
			"protocol":             {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Transport protocol for the port rule. Defaults to tcp."},
			"zone":                 {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Firewalld zone name. Defaults to public."},
			"ensure":               {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Desired port outcome (present or absent). Defaults to present."},
			"allow_ssh_disconnect": {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Explicitly allow a port change that would otherwise sever the current SSH access path."},
		},
	}
}

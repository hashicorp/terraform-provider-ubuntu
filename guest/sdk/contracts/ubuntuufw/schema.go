package ubuntuufw

import "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func RuleResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":                 {Type: pluginsdk.AttrString, Required: true, Description: "Stable tf-nix name used to tag and reconcile the managed UFW rule."},
			"action":               {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "UFW action: allow, deny, reject, or limit. Defaults to allow."},
			"direction":            {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Traffic direction: in or out. Defaults to in."},
			"from":                 {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Source CIDR, address, or the literal any. Defaults to any."},
			"to":                   {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Destination CIDR, address, or the literal any. Defaults to any."},
			"port":                 {Type: pluginsdk.AttrString, Required: true, Description: "Single destination port or port range for the rule."},
			"protocol":             {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Transport protocol: tcp or udp. Defaults to tcp."},
			"ensure":               {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Desired rule outcome (present or absent). Defaults to present."},
			"allow_ssh_disconnect": {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Explicitly allow a rule change that would otherwise sever the current SSH access path."},
			"rule_comment":         {Type: pluginsdk.AttrString, Computed: true, Description: "Computed managed UFW comment marker used to identify the rule on-host."},
		},
	}
}

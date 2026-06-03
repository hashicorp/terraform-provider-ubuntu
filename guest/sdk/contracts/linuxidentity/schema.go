// Copyright IBM Corp. 2026

package linuxidentity

import "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func UserResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":        {Type: pluginsdk.AttrString, Required: true, Description: "Username."},
			"ensure":      {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Desired user outcome (present or absent). Defaults to present."},
			"uid":         {Type: pluginsdk.AttrInt, Optional: true, Computed: true, Description: "User ID."},
			"gid":         {Type: pluginsdk.AttrInt, Optional: true, Computed: true, Description: "Primary group ID."},
			"home":        {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Home directory path."},
			"shell":       {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Login shell."},
			"system":      {Type: pluginsdk.AttrBool, Optional: true, Description: "Whether this is a system user."},
			"groups":      {Type: pluginsdk.AttrList, Optional: true, Description: "Supplementary groups."},
			"comment":     {Type: pluginsdk.AttrString, Optional: true, Description: "GECOS comment field."},
			"remove_home": {Type: pluginsdk.AttrBool, Optional: true, Description: "Remove the home directory when ensuring the user is absent."},
			"run_as":      {Type: pluginsdk.AttrString, Optional: true, Description: "Run commands as this user."},
		},
	}
}

func GroupResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":   {Type: pluginsdk.AttrString, Required: true, Description: "Group name."},
			"ensure": {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Desired group outcome (present or absent). Defaults to present."},
			"gid":    {Type: pluginsdk.AttrInt, Optional: true, Computed: true, Description: "Group ID."},
			"system": {Type: pluginsdk.AttrBool, Optional: true, Description: "Whether this is a system group."},
		},
	}
}

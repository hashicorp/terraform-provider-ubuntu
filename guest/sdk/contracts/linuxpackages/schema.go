package linuxpackages

import "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func PackageResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":              {Type: pluginsdk.AttrString, Required: true, Description: "Package name."},
			"version":           {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Installed package version."},
			"ensure":            {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Desired package outcome (present, latest, or absent). Defaults to present."},
			"update_cache":      {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Refresh package metadata before performing the package operation."},
			"repo_keyring_path": {Type: pluginsdk.AttrString, Optional: true, Description: "Optional apt keyring path to ensure after the package operation. Must be paired with repo_keyring_url."},
			"repo_keyring_url":  {Type: pluginsdk.AttrString, Optional: true, Description: "Optional URL for an ASCII-armored apt repository key that should be downloaded and dearmored into repo_keyring_path."},
		},
	}
}

func PackageLockResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"packages": {Type: pluginsdk.AttrList, Required: true, Description: "Packages to hold or versionlock together."},
			"ensure":   {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Desired package lock outcome (present or absent). Defaults to present."},
		},
	}
}

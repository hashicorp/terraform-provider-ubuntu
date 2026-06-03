// Copyright IBM Corp. 2026

package redhatdnf

import "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func RepositoryResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":        {Type: pluginsdk.AttrString, Required: true, Description: "Repository identifier and managed .repo basename."},
			"description": {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Optional human-readable repository description. Defaults to name."},
			"baseurl":     {Type: pluginsdk.AttrString, Required: true, Description: "Repository base URL."},
			"gpgkey":      {Type: pluginsdk.AttrString, Optional: true, Description: "Optional GPG key URL or local path."},
			"enabled":     {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Whether the repository should be enabled."},
			"gpgcheck":    {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Whether package GPG signature checking is enabled."},
			"ensure":      {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Desired repository outcome (present or absent). Defaults to present."},
			"file_path":   {Type: pluginsdk.AttrString, Computed: true, Description: "Path to the managed yum.repos.d file."},
		},
	}
}

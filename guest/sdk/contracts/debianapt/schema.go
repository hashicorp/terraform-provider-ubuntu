package debianapt

import "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func AptRepositoryResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":              {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Optional repository file basename. Defaults to a value derived from uri."},
			"uri":               {Type: pluginsdk.AttrString, Required: true, Description: "APT repository base URI."},
			"suite":             {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "APT suite or distribution path. Defaults to the host VERSION_CODENAME."},
			"components":        {Type: pluginsdk.AttrList, Optional: true, Description: "Repository components such as stable or main."},
			"architectures":     {Type: pluginsdk.AttrList, Optional: true, Description: "Optional architectures filter for the repository."},
			"signed_by":         {Type: pluginsdk.AttrString, Optional: true, Description: "Optional keyring path rendered as signed-by in the source entry."},
			"signed_by_key_url": {Type: pluginsdk.AttrString, Optional: true, Description: "Optional URL for the repository signing key. When set, the host installs the keyring at signed_by."},
			"ensure":            {Type: pluginsdk.AttrString, Optional: true, Computed: true, Description: "Desired repository outcome (present or absent). Defaults to present."},
			"update_cache":      {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Refresh apt metadata after changing the repository."},
			"file_path":         {Type: pluginsdk.AttrString, Computed: true, Description: "Path to the managed sources.list.d file."},
			"source_line":       {Type: pluginsdk.AttrString, Computed: true, Description: "Rendered deb source line."},
		},
	}
}

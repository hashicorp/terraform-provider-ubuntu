package trustedcert

import "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func ResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":        {Type: pluginsdk.AttrString, Required: true, Description: "Logical certificate name used for the trust-anchor file."},
			"certificate": {Type: pluginsdk.AttrString, Required: true, Description: "PEM-encoded CA certificate to trust on the host."},
			"cert_path":   {Type: pluginsdk.AttrString, Computed: true, Description: "Path of the managed trust-anchor file."},
			"subject":     {Type: pluginsdk.AttrString, Computed: true, Description: "Parsed certificate subject."},
			"issuer":      {Type: pluginsdk.AttrString, Computed: true, Description: "Parsed certificate issuer."},
			"digest":      {Type: pluginsdk.AttrString, Computed: true, Description: "Content digest of the managed certificate PEM, including the algorithm tag."},
		},
	}
}

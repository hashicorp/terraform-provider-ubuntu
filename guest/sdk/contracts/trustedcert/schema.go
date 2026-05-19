package trustedcert

import pluginsdk "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func ResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":        {Type: pluginsdk.AttrString, Required: true, Description: "Logical certificate name used to derive the managed trust-anchor file."},
			"certificate": {Type: pluginsdk.AttrString, Required: true, Description: "CA certificate to trust on the host. Supply exactly one PEM CERTIFICATE block; the provider normalizes it before writing the trust-anchor file."},
			"cert_path":   {Type: pluginsdk.AttrString, Computed: true, Description: "Path of the managed trust-anchor PEM file."},
			"subject":     {Type: pluginsdk.AttrString, Computed: true, Description: "Parsed certificate subject."},
			"issuer":      {Type: pluginsdk.AttrString, Computed: true, Description: "Parsed certificate issuer."},
			"digest":      {Type: pluginsdk.AttrString, Computed: true, Description: "Content digest of the managed normalized certificate PEM, including the algorithm tag."},
		},
	}
}

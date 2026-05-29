// Copyright IBM Corp. 2026

package linuxtls

import pluginsdk "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func TLSIdentityResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"name":                   {Type: pluginsdk.AttrString, Required: true, Description: "Logical TLS identity name used to derive the managed fullchain PEM and private key paths."},
			"fullchain_pem":          {Type: pluginsdk.AttrString, Optional: true, Sensitive: true, Description: "PEM-encoded leaf certificate plus any intermediate CA certificates. The provider normalizes the chain and writes the managed fullchain PEM on disk. Use with private_key_pem."},
			"certificate_pem":        {Type: pluginsdk.AttrString, Optional: true, Sensitive: true, Description: "PEM-encoded leaf certificate. The provider combines it with optional ca_chain_pem, normalizes the result, and writes the managed fullchain PEM on disk. Use with private_key_pem."},
			"ca_chain_pem":           {Type: pluginsdk.AttrString, Optional: true, Sensitive: true, Description: "Optional PEM-encoded intermediate certificate chain used with certificate_pem. The provider appends it after the leaf certificate when writing the managed fullchain PEM on disk."},
			"fullchain_der_base64":   {Type: pluginsdk.AttrString, Optional: true, Sensitive: true, Description: "Base64-encoded DER certificate chain. The provider decodes and normalizes it before writing the managed fullchain PEM on disk. Use with private_key_der_base64."},
			"certificate_der_base64": {Type: pluginsdk.AttrString, Optional: true, Sensitive: true, Description: "Base64-encoded DER leaf certificate. The provider combines it with optional ca_chain_der_base64, normalizes the result, and writes the managed fullchain PEM on disk. Use with private_key_der_base64."},
			"ca_chain_der_base64":    {Type: pluginsdk.AttrString, Optional: true, Sensitive: true, Description: "Optional base64-encoded concatenated DER intermediate certificate chain used with certificate_der_base64. The provider appends it after the leaf certificate when writing the managed fullchain PEM on disk."},
			"private_key_pem":        {Type: pluginsdk.AttrString, Optional: true, Sensitive: true, Description: "PEM-encoded private key matching the leaf certificate in the PEM input families. The provider normalizes it to PKCS#8 PEM on disk."},
			"private_key_der_base64": {Type: pluginsdk.AttrString, Optional: true, Sensitive: true, Description: "Base64-encoded DER private key matching the leaf certificate in the DER input families. The provider normalizes it to PKCS#8 PEM on disk."},
			"input_family":           {Type: pluginsdk.AttrString, Computed: true, Description: "Resolved input family used to build this TLS identity: pem_fullchain, pem_split, der_fullchain, or der_split."},
			"fullchain_path":         {Type: pluginsdk.AttrString, Computed: true, Description: "Path of the managed fullchain PEM on the host."},
			"private_key_path":       {Type: pluginsdk.AttrString, Computed: true, Description: "Path of the managed private key PEM on the host."},
			"subject":                {Type: pluginsdk.AttrString, Computed: true, Description: "Subject of the leaf certificate."},
			"issuer":                 {Type: pluginsdk.AttrString, Computed: true, Description: "Issuer of the leaf certificate."},
			"serial_number":          {Type: pluginsdk.AttrString, Computed: true, Description: "Serial number of the leaf certificate."},
			"not_after":              {Type: pluginsdk.AttrString, Computed: true, Description: "Leaf certificate expiry timestamp in RFC3339 format."},
			"fullchain_digest":       {Type: pluginsdk.AttrString, Computed: true, Description: "Content digest of the managed fullchain PEM, including the algorithm tag."},
			"private_key_digest":     {Type: pluginsdk.AttrString, Computed: true, Description: "Content digest of the managed private key PEM, including the algorithm tag."},
		},
	}
}

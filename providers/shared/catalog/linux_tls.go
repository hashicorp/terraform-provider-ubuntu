package catalog

import (
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"

	linuxtlscontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxtls"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func LinuxTLS() Fragment {
	tlsIdentityContract := linuxtlscontract.TLSIdentityResourceSchema()

	return Fragment{
		ID:             "linux_tls",
		Scope:          "linux",
		RuntimeModules: []string{ModuleLinuxTLS},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:                 "tls_identity",
				Pattern:                  engine.PatternCommand,
				RequiredPrivilege:        "root",
				RuntimeType:              "tls_identity",
				RuntimeModule:            ModuleLinuxTLS,
				LockPlanner:              tlsIdentityLockPlanner,
				ImportRequiredOnExisting: true,
				ImportIdentity: singleStringImportIdentity(
					importIdentityField{Key: "name", Description: "Logical TLS identity name to import."},
				),
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: true,
					PreWriteValidation:   true,
					ReadAfterWrite:       true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedPathGuard("fullchain_path", "private_key_path"),
				},
				Attributes: resourceAttributesFromPluginContract(tlsIdentityContract.Attributes, map[string]resourceschema.Attribute{
					"fullchain_pem":          resourceschema.StringAttribute{Description: tlsIdentityContract.Attributes["fullchain_pem"].Description, Optional: true, Sensitive: true, CustomType: engine.DigestedStringType{}},
					"certificate_pem":        resourceschema.StringAttribute{Description: tlsIdentityContract.Attributes["certificate_pem"].Description, Optional: true, Sensitive: true, CustomType: engine.DigestedStringType{}},
					"ca_chain_pem":           resourceschema.StringAttribute{Description: tlsIdentityContract.Attributes["ca_chain_pem"].Description, Optional: true, Sensitive: true, CustomType: engine.DigestedStringType{}},
					"fullchain_der_base64":   resourceschema.StringAttribute{Description: tlsIdentityContract.Attributes["fullchain_der_base64"].Description, Optional: true, Sensitive: true, CustomType: engine.DigestedBase64StringType{}},
					"certificate_der_base64": resourceschema.StringAttribute{Description: tlsIdentityContract.Attributes["certificate_der_base64"].Description, Optional: true, Sensitive: true, CustomType: engine.DigestedBase64StringType{}},
					"ca_chain_der_base64":    resourceschema.StringAttribute{Description: tlsIdentityContract.Attributes["ca_chain_der_base64"].Description, Optional: true, Sensitive: true, CustomType: engine.DigestedBase64StringType{}},
					"private_key_pem":        resourceschema.StringAttribute{Description: tlsIdentityContract.Attributes["private_key_pem"].Description, Optional: true, Sensitive: true, CustomType: engine.DigestedStringType{}},
					"private_key_der_base64": resourceschema.StringAttribute{Description: tlsIdentityContract.Attributes["private_key_der_base64"].Description, Optional: true, Sensitive: true, CustomType: engine.DigestedBase64StringType{}},
				}),
			},
		},
	}
}

package catalog

import (
	trustedcertcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/trustedcert"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func DebianTrust() Fragment {
	trustedCertContract := trustedcertcontract.ResourceSchema()

	return Fragment{
		ID:             "debian_trust",
		Scope:          "debian",
		RuntimeModules: []string{ModuleDebianTrust},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:                 "trusted_cert",
				Pattern:                  engine.PatternCommand,
				RequiredPrivilege:        "root",
				RuntimeType:              "trusted_cert",
				RuntimeModule:            ModuleDebianTrust,
				LockPlanner:              trustedCertLockPlanner,
				ImportRequiredOnExisting: true,
				ImportIdentity: singleStringImportIdentity(
					importIdentityField{Key: "name", Description: "Logical trusted certificate name to import."},
				),
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: true,
					PreWriteValidation:   true,
					ReadAfterWrite:       true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: trustedCertPathGuard,
				},
				Attributes: resourceAttributesFromPluginContract(trustedCertContract.Attributes, nil),
			},
		},
	}
}

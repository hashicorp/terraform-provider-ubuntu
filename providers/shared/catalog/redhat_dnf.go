package catalog

import (
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"

	redhatdnfcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/redhatdnf"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func RedHatDnf() Fragment {
	dnfRepositoryContract := redhatdnfcontract.RepositoryResourceSchema()

	return Fragment{
		ID:             "redhat_dnf",
		Scope:          "redhat",
		RuntimeModules: []string{ModuleRedHatDnf},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:                 "dnf_repository",
				Pattern:                  engine.PatternCommand,
				RequiredPrivilege:        "root",
				RuntimeType:              "dnf_repository",
				RuntimeModule:            ModuleRedHatDnf,
				LockPlanner:              dnfRepositoryLockPlanner,
				ImportRequiredOnExisting: true,
				ImportIdentity: singleStringImportIdentity(
					importIdentityField{Key: "name", Description: "Repository basename to import."},
				),
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: true,
					PreWriteValidation:   true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedPathGuard("file_path"),
				},
				Attributes: resourceAttributesFromPluginContract(dnfRepositoryContract.Attributes, map[string]resourceschema.Attribute{
					"enabled":  resourceschema.BoolAttribute{Description: dnfRepositoryContract.Attributes["enabled"].Description, Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
					"gpgcheck": resourceschema.BoolAttribute{Description: dnfRepositoryContract.Attributes["gpgcheck"].Description, Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
					"ensure":   resourceschema.StringAttribute{Description: dnfRepositoryContract.Attributes["ensure"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("present")},
				}),
			},
		},
	}
}

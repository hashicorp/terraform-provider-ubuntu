// Copyright IBM Corp. 2026

package catalog

import (
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"

	debianaptcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/debianapt"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func DebianApt() Fragment {
	aptRepositoryContract := debianaptcontract.AptRepositoryResourceSchema()

	return Fragment{
		ID:             "debian_apt",
		Scope:          "debian",
		RuntimeModules: []string{ModuleDebianApt},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:                 "apt_repository",
				Pattern:                  engine.PatternCommand,
				RequiredPrivilege:        "root",
				RuntimeType:              "apt_repository",
				RuntimeModule:            ModuleDebianApt,
				LockPlanner:              aptRepositoryLockPlanner,
				ImportRequiredOnExisting: true,
				ImportIdentity: singleStringImportIdentity(
					importIdentityField{Key: "name", Description: "Repository basename to import."},
				),
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedPathGuard("file_path"),
				},
				Attributes: resourceAttributesFromPluginContract(aptRepositoryContract.Attributes, map[string]resourceschema.Attribute{
					"ensure":       resourceschema.StringAttribute{Description: aptRepositoryContract.Attributes["ensure"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("present")},
					"update_cache": resourceschema.BoolAttribute{Description: aptRepositoryContract.Attributes["update_cache"].Description, Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
				}),
			},
		},
	}
}

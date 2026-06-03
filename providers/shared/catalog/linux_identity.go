// Copyright IBM Corp. 2026

package catalog

import (
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"

	linuxidentitycontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxidentity"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func LinuxIdentity() Fragment {
	userContract := linuxidentitycontract.UserResourceSchema()
	groupContract := linuxidentitycontract.GroupResourceSchema()

	return Fragment{
		ID:             "linux_identity",
		Scope:          "linux",
		RuntimeModules: []string{ModuleLinuxIdentity},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:          "user",
				Pattern:           engine.PatternCommand,
				RequiredPrivilege: "root",
				RuntimeType:       "user",
				RuntimeModule:     ModuleLinuxIdentity,
				LockPlanner:       identityLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: true,
					PreWriteValidation:   true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedUserGuard,
				},
				Attributes: resourceAttributesFromPluginContract(userContract.Attributes, map[string]resourceschema.Attribute{
					"ensure": resourceschema.StringAttribute{Description: userContract.Attributes["ensure"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("present")},
				}),
			},
			{
				TypeName:          "group",
				Pattern:           engine.PatternCommand,
				RequiredPrivilege: "root",
				RuntimeType:       "group",
				RuntimeModule:     ModuleLinuxIdentity,
				LockPlanner:       identityLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: true,
					PreWriteValidation:   true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedGroupGuard,
				},
				Attributes: resourceAttributesFromPluginContract(groupContract.Attributes, map[string]resourceschema.Attribute{
					"ensure": resourceschema.StringAttribute{Description: groupContract.Attributes["ensure"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("present")},
				}),
			},
		},
	}
}

package catalog

import (
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"

	linuxpackagescontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxpackages"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func LinuxPackages() Fragment {
	packageContract := linuxpackagescontract.PackageResourceSchema()
	packageLockContract := linuxpackagescontract.PackageLockResourceSchema()

	return Fragment{
		ID:             "linux_packages",
		Scope:          "linux",
		RuntimeModules: []string{ModuleLinuxPackages},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:          "package",
				Pattern:           engine.PatternCommand,
				RequiredPrivilege: "root",
				RuntimeType:       "package",
				RuntimeModule:     ModuleLinuxPackages,
				LockPlanner:       packageLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedPackageGuard,
				},
				Attributes: resourceAttributesFromPluginContract(packageContract.Attributes, map[string]resourceschema.Attribute{
					"ensure":       resourceschema.StringAttribute{Description: packageContract.Attributes["ensure"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("present")},
					"update_cache": resourceschema.BoolAttribute{Description: packageContract.Attributes["update_cache"].Description, Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
				}),
			},
			{
				TypeName:          "package_lock",
				Pattern:           engine.PatternCommand,
				RequiredPrivilege: "root",
				RuntimeType:       "package_lock",
				RuntimeModule:     ModuleLinuxPackages,
				LockPlanner:       packageLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode: engine.DestroySafetyModeNone,
				},
				Attributes: resourceAttributesFromPluginContract(packageLockContract.Attributes, map[string]resourceschema.Attribute{
					"ensure": resourceschema.StringAttribute{Description: packageLockContract.Attributes["ensure"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("present")},
				}),
			},
		},
	}
}

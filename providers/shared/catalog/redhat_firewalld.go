package catalog

import (
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"

	redhatfirewalldcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/redhatfirewalld"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func RedHatFirewalld() Fragment {
	firewalldServiceContract := redhatfirewalldcontract.ServiceResourceSchema()
	firewalldPortContract := redhatfirewalldcontract.PortResourceSchema()

	return Fragment{
		ID:             "redhat_firewalld",
		Scope:          "redhat",
		RuntimeModules: []string{ModuleRedHatFirewall},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:          "firewalld_service",
				Pattern:           engine.PatternEntry,
				RequiredPrivilege: "root",
				RuntimeType:       "firewalld_service",
				RuntimeModule:     ModuleRedHatFirewall,
				LockPlanner:       firewalldLockPlanner,
				ImportIdentity: joinedStringImportIdentity("/",
					importIdentityField{Key: "zone", Description: "Firewalld zone name. Use public when omitted during import."},
					importIdentityField{Key: "name", Description: "Firewalld service name to import."},
				),
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
					ReadAfterWrite:       true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedFirewalldServiceGuard,
				},
				Attributes: resourceAttributesFromPluginContract(firewalldServiceContract.Attributes, map[string]resourceschema.Attribute{
					"zone":                 resourceschema.StringAttribute{Description: firewalldServiceContract.Attributes["zone"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("public")},
					"ensure":               resourceschema.StringAttribute{Description: firewalldServiceContract.Attributes["ensure"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("present")},
					"allow_ssh_disconnect": resourceschema.BoolAttribute{Description: firewalldServiceContract.Attributes["allow_ssh_disconnect"].Description, Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
				}),
			},
			{
				TypeName:          "firewalld_port",
				Pattern:           engine.PatternEntry,
				RequiredPrivilege: "root",
				RuntimeType:       "firewalld_port",
				RuntimeModule:     ModuleRedHatFirewall,
				LockPlanner:       firewalldLockPlanner,
				ImportIdentity: joinedStringImportIdentity("/",
					importIdentityField{Key: "zone", Description: "Firewalld zone name. Use public when omitted during import."},
					importIdentityField{Key: "port", Description: "Port or port range to import."},
					importIdentityField{Key: "protocol", Description: "Port protocol to import."},
				),
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
					ReadAfterWrite:       true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedFirewalldPortGuard,
				},
				Attributes: resourceAttributesFromPluginContract(firewalldPortContract.Attributes, map[string]resourceschema.Attribute{
					"protocol":             resourceschema.StringAttribute{Description: firewalldPortContract.Attributes["protocol"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("tcp")},
					"zone":                 resourceschema.StringAttribute{Description: firewalldPortContract.Attributes["zone"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("public")},
					"ensure":               resourceschema.StringAttribute{Description: firewalldPortContract.Attributes["ensure"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("present")},
					"allow_ssh_disconnect": resourceschema.BoolAttribute{Description: firewalldPortContract.Attributes["allow_ssh_disconnect"].Description, Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
				}),
			},
		},
	}
}

// Copyright IBM Corp. 2026

package catalog

import (
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"

	ubuntuufwcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/ubuntuufw"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func UbuntuUFW() Fragment {
	ufwRuleContract := ubuntuufwcontract.RuleResourceSchema()

	return Fragment{
		ID:             "ubuntu_ufw",
		Scope:          "ubuntu",
		RuntimeModules: []string{ModuleUbuntuUFW},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:          "ufw_rule",
				Pattern:           engine.PatternEntry,
				RequiredPrivilege: "root",
				RuntimeType:       "ufw_rule",
				RuntimeModule:     ModuleUbuntuUFW,
				LockPlanner:       ufwRuleLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
					ReadAfterWrite:       true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedUFWRuleGuard,
				},
				Attributes: resourceAttributesFromPluginContract(ufwRuleContract.Attributes, map[string]resourceschema.Attribute{
					"action":               resourceschema.StringAttribute{Description: ufwRuleContract.Attributes["action"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("allow")},
					"direction":            resourceschema.StringAttribute{Description: ufwRuleContract.Attributes["direction"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("in")},
					"from":                 resourceschema.StringAttribute{Description: ufwRuleContract.Attributes["from"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("any")},
					"to":                   resourceschema.StringAttribute{Description: ufwRuleContract.Attributes["to"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("any")},
					"protocol":             resourceschema.StringAttribute{Description: ufwRuleContract.Attributes["protocol"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("tcp")},
					"ensure":               resourceschema.StringAttribute{Description: ufwRuleContract.Attributes["ensure"].Description, Optional: true, Computed: true, Default: stringdefault.StaticString("present")},
					"allow_ssh_disconnect": resourceschema.BoolAttribute{Description: ufwRuleContract.Attributes["allow_ssh_disconnect"].Description, Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
				}),
			},
		},
	}
}

package catalog

import (
	timesynccontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/timesync"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func SystemdTimesync() Fragment {
	timesyncResourceContract := timesynccontract.ResourceSchema()

	return Fragment{
		ID:             "systemd_timesync",
		Scope:          "systemd",
		RuntimeModules: []string{ModuleSystemdUnits},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:          "timesync",
				Pattern:           engine.PatternConfig,
				RequiredPrivilege: "root",
				RuntimeType:       "timesync",
				RuntimeModule:     ModuleSystemdUnits,
				LockPlanner:       timesyncdLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
					ReadAfterWrite:       true,
				},
				Attributes: resourceAttributesFromPluginContract(timesyncResourceContract.Attributes, nil),
			},
		},
	}
}

package catalog

import (
	timesynccontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/timesync"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func RedHatTimesync() Fragment {
	timesyncResourceContract := timesynccontract.ResourceSchema()

	return Fragment{
		ID:             "redhat_timesync",
		Scope:          "redhat",
		RuntimeModules: []string{ModuleRedHatTimesync},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:          "timesync",
				Pattern:           engine.PatternConfig,
				RequiredPrivilege: "root",
				RuntimeType:       "timesync",
				RuntimeModule:     ModuleRedHatTimesync,
				LockPlanner:       chronyTimesyncLockPlanner,
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

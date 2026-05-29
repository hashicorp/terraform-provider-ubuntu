// Copyright IBM Corp. 2026

package catalog

import (
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"

	systemdcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/systemd"
	provideractions "github.com/hashicorp/terraform-provider-ubuntu/providers/shared/actions"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func Systemd() Fragment {
	unitContract := systemdcontract.UnitResourceSchema()
	timezoneContract := systemdcontract.TimezoneResourceSchema()
	unitInfoContract := systemdcontract.UnitInfoDataSourceSchema()
	restartProcessContract := systemdcontract.RestartProcessActionSchema()

	return Fragment{
		ID:             "systemd_units",
		Scope:          "systemd",
		RuntimeModules: []string{ModuleSystemdUnits},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:          "systemd_unit",
				Pattern:           engine.PatternCommand,
				RequiredPrivilege: "root",
				RuntimeType:       "systemd_unit",
				RuntimeModule:     ModuleSystemdUnits,
				LockPlanner:       systemdUnitLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
					ReadAfterWrite:       true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeCriticalObject,
					Guard: protectedSystemdServiceGuard,
				},
				Attributes: resourceAttributesFromPluginContract(unitContract.Attributes, map[string]resourceschema.Attribute{
					"reload_on_change": resourceschema.BoolAttribute{Description: unitContract.Attributes["reload_on_change"].Description, Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
				}),
			},
			{
				TypeName:          "timezone",
				Pattern:           engine.PatternConfig,
				RequiredPrivilege: "root",
				RuntimeType:       "timezone",
				RuntimeModule:     ModuleSystemdUnits,
				LockPlanner:       timezoneLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
					ReadAfterWrite:       true,
				},
				Attributes: resourceAttributesFromPluginContract(timezoneContract.Attributes, nil),
			},
		},
		DataSources: []engine.DataSourceDefinition{
			{
				TypeName:      "systemd_unit_info",
				RuntimeType:   "systemd_unit_info",
				RuntimeModule: ModuleSystemdUnits,
				LockPlanner:   systemdUnitLockPlanner,
				Attributes:    dataSourceAttributesFromPluginContract(unitInfoContract.Attributes, nil),
			},
		},
		Actions: []engine.ActionDefinition{
			{
				TypeName:          "restart_process",
				RequiredPrivilege: "dynamic",
				RuntimeType:       "restart_process",
				RuntimeModule:     ModuleSystemdUnits,
				Attributes:        actionAttributesFromPluginContract(restartProcessContract.Attributes, nil),
				LockPlanner:       provideractions.RestartProcessLockPlanner,
				ExecutionPolicy:   provideractions.RestartProcessExecutionPolicy,
				Invoke:            provideractions.InvokeRestartProcess,
				ShapeResult:       provideractions.RestartProcessResultShaper,
			},
		},
	}
}

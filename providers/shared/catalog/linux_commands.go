package catalog

import (
	linuxcommandscontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxcommands"
	provideractions "github.com/hashicorp/terraform-provider-ubuntu/providers/shared/actions"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func LinuxCommands() Fragment {
	commandContract := linuxcommandscontract.CommandResourceSchema()
	runCommandContract := linuxcommandscontract.RunCommandActionSchema()

	return Fragment{
		ID:             "linux_commands",
		Scope:          "linux",
		RuntimeModules: []string{ModuleLinuxCommands},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:          "command",
				Pattern:           engine.PatternCommand,
				RequiredPrivilege: "dynamic",
				RuntimeType:       "command",
				RuntimeModule:     ModuleLinuxCommands,
				LockPlanner:       commandLockPlanner,
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:                  engine.DestroySafetyModeExplicitAllow,
					RequiresExplicitAllow: explicitDeleteCommandGuard,
				},
				Attributes: resourceAttributesFromPluginContract(commandContract.Attributes, nil),
			},
		},
		Actions: []engine.ActionDefinition{
			{
				TypeName:          "run_command",
				RequiredPrivilege: "dynamic",
				RuntimeType:       "run_command",
				RuntimeModule:     ModuleLinuxCommands,
				Attributes:        actionAttributesFromPluginContract(runCommandContract.Attributes, nil),
				LockPlanner:       provideractions.RunCommandLockPlanner,
				ShapeResult:       provideractions.RunCommandResultShaper,
			},
		},
	}
}

package catalog

import (
	frameworkaction "github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	provideractions "github.com/hashicorp/terraform-provider-ubuntu/providers/shared/actions"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
	coreprovider "github.com/hashicorp/terraform-provider-ubuntu/providers/shared/serving"
)

func LinuxLifecycle() Fragment {
	return Fragment{
		ID:                  "linux_lifecycle",
		Scope:               "linux",
		CustomResourceTypes: []string{"reboot_barrier"},
		CustomActionTypes:   []string{"restart_host"},
		CustomResourceBuilders: []ResourceFactoryBuilder{
			func(config coreprovider.ProviderConfig) resource.Resource {
				return engine.NewRebootBarrierResource(config.ResourceType("reboot_barrier"))
			},
		},
		CustomActionBuilders: []ActionFactoryBuilder{
			func(config coreprovider.ProviderConfig) frameworkaction.Action {
				return provideractions.NewRestartHostAction(config.ActionType("restart_host"))
			},
		},
	}
}

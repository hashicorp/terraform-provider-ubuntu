// Copyright IBM Corp. 2026

package catalog

import (
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"

	linuxnetworkcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxnetwork"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func LinuxNetwork() Fragment {
	networkStackContract := linuxnetworkcontract.NetworkStackResourceSchema()
	networkInterfacesContract := linuxnetworkcontract.NetworkInterfacesDataSourceSchema()

	return Fragment{
		ID:             "linux_network",
		Scope:          "linux",
		RuntimeModules: []string{ModuleLinuxFacts, ModuleLinuxFiles},
		Resources: []engine.ResourceDefinition{
			{
				TypeName:          "network_stack",
				Pattern:           engine.PatternConfig,
				RequiredPrivilege: "root",
				RuntimeType:       "network_stack",
				RuntimeModule:     ModuleLinuxFiles,
				LockPlanner:       networkStackLockPlanner,
				ValidationPolicy: engine.ValidationPolicy{
					RemotePlanValidation: false,
					PreWriteValidation:   true,
					ReadAfterWrite:       true,
				},
				DestroySafety: engine.DestroySafetyPolicy{
					Mode:  engine.DestroySafetyModeNoDestroy,
					Guard: protectedNetworkStackGuard,
				},
				Attributes: resourceAttributesFromPluginContract(networkStackContract.Attributes, map[string]resourceschema.Attribute{
					"ipv4_forwarding":       resourceschema.BoolAttribute{Description: networkStackContract.Attributes["ipv4_forwarding"].Description, Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
					"ipv6_forwarding":       resourceschema.BoolAttribute{Description: networkStackContract.Attributes["ipv6_forwarding"].Description, Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
					"bridge_netfilter_ipv4": resourceschema.BoolAttribute{Description: networkStackContract.Attributes["bridge_netfilter_ipv4"].Description, Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
					"bridge_netfilter_ipv6": resourceschema.BoolAttribute{Description: networkStackContract.Attributes["bridge_netfilter_ipv6"].Description, Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
				}),
			},
		},
		DataSources: []engine.DataSourceDefinition{
			{
				TypeName:      "network_interfaces",
				RuntimeType:   "network_interfaces",
				RuntimeModule: ModuleLinuxFacts,
				LockPlanner:   networkInterfacesLockPlanner,
				Attributes:    dataSourceAttributesFromPluginContract(networkInterfacesContract.Attributes, nil),
			},
		},
	}
}

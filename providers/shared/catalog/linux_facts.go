// Copyright IBM Corp. 2026

package catalog

import (
	linuxfactscontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxfacts"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func LinuxFacts() Fragment {
	osReleaseContract := linuxfactscontract.OSReleaseDataSourceSchema()
	systemInfoContract := linuxfactscontract.SystemInfoDataSourceSchema()
	mountsContract := linuxfactscontract.MountsDataSourceSchema()

	return Fragment{
		ID:             "linux_facts",
		Scope:          "linux",
		RuntimeModules: []string{ModuleLinuxFacts},
		DataSources: []engine.DataSourceDefinition{
			{
				TypeName:      "os_release",
				RuntimeType:   "os_release",
				RuntimeModule: ModuleLinuxFacts,
				LockPlanner:   osReleaseLockPlanner,
				Attributes:    dataSourceAttributesFromPluginContract(osReleaseContract.Attributes, nil),
			},
			{
				TypeName:      "system_info",
				RuntimeType:   "system_info",
				RuntimeModule: ModuleLinuxFacts,
				LockPlanner:   systemInfoLockPlanner,
				Attributes:    dataSourceAttributesFromPluginContract(systemInfoContract.Attributes, nil),
			},
			{
				TypeName:      "mounts",
				RuntimeType:   "mounts",
				RuntimeModule: ModuleLinuxFacts,
				LockPlanner:   mountsLockPlanner,
				Attributes:    dataSourceAttributesFromPluginContract(mountsContract.Attributes, nil),
			},
		},
	}
}

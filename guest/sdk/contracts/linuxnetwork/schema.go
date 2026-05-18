package linuxnetwork

import "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func NetworkStackResourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"ipv4_forwarding":       {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Whether IPv4 forwarding should be enabled for the managed network stack."},
			"ipv6_forwarding":       {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Whether IPv6 forwarding should be enabled for the managed network stack."},
			"bridge_netfilter_ipv4": {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Whether bridged IPv4 traffic should traverse iptables hooks."},
			"bridge_netfilter_ipv6": {Type: pluginsdk.AttrBool, Optional: true, Computed: true, Description: "Whether bridged IPv6 traffic should traverse ip6tables hooks."},
			"config_path":           {Type: pluginsdk.AttrString, Computed: true, Description: "Managed sysctl.d file path owned by the network stack resource."},
			"sysctls":               {Type: pluginsdk.AttrMap, Computed: true, Description: "Underlying sysctl keys and values managed by this network stack resource."},
		},
	}
}

func NetworkInterfacesDataSourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"interfaces":     {Type: pluginsdk.AttrList, Computed: true, Description: "Interface names discovered on the host."},
			"ipv4_addresses": {Type: pluginsdk.AttrMap, Computed: true, Description: "Primary IPv4 address by interface."},
			"ipv6_addresses": {Type: pluginsdk.AttrMap, Computed: true, Description: "Primary IPv6 address by interface."},
			"mac_addresses":  {Type: pluginsdk.AttrMap, Computed: true, Description: "MAC address by interface."},
			"states":         {Type: pluginsdk.AttrMap, Computed: true, Description: "Operational state by interface."},
		},
	}
}

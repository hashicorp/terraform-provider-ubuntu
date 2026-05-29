// Copyright IBM Corp. 2026

package linuxfacts

import "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"

func OSReleaseDataSourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"id":               {Type: pluginsdk.AttrString, Computed: true, Description: "OS identifier (e.g. ubuntu, debian, fedora)."},
			"name":             {Type: pluginsdk.AttrString, Computed: true, Description: "OS display name."},
			"version_id":       {Type: pluginsdk.AttrString, Computed: true, Description: "OS version identifier."},
			"pretty_name":      {Type: pluginsdk.AttrString, Computed: true, Description: "Human-readable OS name and version."},
			"id_like":          {Type: pluginsdk.AttrString, Computed: true, Description: "Related OS identifiers (e.g. debian for ubuntu)."},
			"version_codename": {Type: pluginsdk.AttrString, Computed: true, Description: "OS version codename."},
		},
	}
}

func SystemInfoDataSourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"hostname":        {Type: pluginsdk.AttrString, Computed: true, Description: "System hostname."},
			"arch":            {Type: pluginsdk.AttrString, Computed: true, Description: "CPU architecture (e.g. x86_64, aarch64)."},
			"kernel":          {Type: pluginsdk.AttrString, Computed: true, Description: "Kernel name (e.g. Linux)."},
			"kernel_version":  {Type: pluginsdk.AttrString, Computed: true, Description: "Kernel version string."},
			"distro":          {Type: pluginsdk.AttrString, Computed: true, Description: "Distribution name."},
			"distro_family":   {Type: pluginsdk.AttrString, Computed: true, Description: "Distribution family (e.g. debian, rhel)."},
			"init_system":     {Type: pluginsdk.AttrString, Computed: true, Description: "Init system (e.g. systemd)."},
			"package_manager": {Type: pluginsdk.AttrString, Computed: true, Description: "Default package manager (e.g. apt, dnf)."},
			"selinux":         {Type: pluginsdk.AttrBool, Computed: true, Description: "Whether SELinux is present."},
			"apparmor":        {Type: pluginsdk.AttrBool, Computed: true, Description: "Whether AppArmor is present."},
		},
	}
}

func MountsDataSourceSchema() pluginsdk.Schema {
	return pluginsdk.Schema{
		Attributes: map[string]pluginsdk.Attribute{
			"mounts":  {Type: pluginsdk.AttrList, Computed: true, Description: "Mount points discovered on the host."},
			"devices": {Type: pluginsdk.AttrMap, Computed: true, Description: "Mounted device or source by mount point."},
			"fstypes": {Type: pluginsdk.AttrMap, Computed: true, Description: "Filesystem type by mount point."},
			"options": {Type: pluginsdk.AttrMap, Computed: true, Description: "Mount options by mount point."},
		},
	}
}

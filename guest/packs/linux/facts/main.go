// Copyright IBM Corp. 2026

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	linuxfactscontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxfacts"
	linuxnetworkcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxnetwork"
)

type osReleaseDataSource struct{}

func (d *osReleaseDataSource) Name() string { return "os_release" }

func (d *osReleaseDataSource) DataSchema() pluginsdk.Schema {
	return linuxfactscontract.OSReleaseDataSourceSchema()
}

func (d *osReleaseDataSource) DataRead(_ pluginsdk.StateData) (pluginsdk.StateData, error) {
	data, err := pluginsdk.FileRead("/etc/os-release")
	if err != nil {
		return nil, fmt.Errorf("read /etc/os-release: %w", err)
	}
	kv := parseKeyValue(data)
	return pluginsdk.StateData{
		"id":               kv["ID"],
		"name":             kv["NAME"],
		"version_id":       kv["VERSION_ID"],
		"pretty_name":      kv["PRETTY_NAME"],
		"id_like":          kv["ID_LIKE"],
		"version_codename": kv["VERSION_CODENAME"],
	}, nil
}

type systemInfoDataSource struct{}

func (d *systemInfoDataSource) Name() string { return "system_info" }

func (d *systemInfoDataSource) DataSchema() pluginsdk.Schema {
	return linuxfactscontract.SystemInfoDataSourceSchema()
}

func (d *systemInfoDataSource) DataRead(_ pluginsdk.StateData) (pluginsdk.StateData, error) {
	profile, err := pluginsdk.GetHostProfile()
	if err != nil {
		return nil, fmt.Errorf("get host profile: %w", err)
	}
	return pluginsdk.StateData{
		"hostname":        strings.TrimSpace(profile.Hostname),
		"arch":            profile.Arch,
		"kernel":          profile.Kernel,
		"kernel_version":  strings.TrimSpace(profile.KernelVersion),
		"distro":          profile.Distro,
		"distro_family":   profile.DistroFamily,
		"init_system":     profile.InitSystem,
		"package_manager": profile.PackageManager,
		"selinux":         profile.SELinux,
		"apparmor":        profile.AppArmor,
	}, nil
}

type networkInterfacesDataSource struct{}

func (d *networkInterfacesDataSource) Name() string { return "network_interfaces" }

func (d *networkInterfacesDataSource) DataSchema() pluginsdk.Schema {
	return linuxnetworkcontract.NetworkInterfacesDataSourceSchema()
}

func (d *networkInterfacesDataSource) DataRead(_ pluginsdk.StateData) (pluginsdk.StateData, error) {
	devData, err := pluginsdk.FileRead("/proc/net/dev")
	if err != nil {
		return nil, fmt.Errorf("read /proc/net/dev: %w", err)
	}

	interfaces := parseProcNetDev(string(devData))
	ipv4, err := parseInterfaceAddresses([]string{"-o", "-4", "addr", "show"})
	if err != nil {
		return nil, err
	}
	ipv6, err := parseInterfaceAddresses([]string{"-o", "-6", "addr", "show"})
	if err != nil {
		return nil, err
	}

	macs := make(map[string]string, len(interfaces))
	states := make(map[string]string, len(interfaces))
	for _, iface := range interfaces {
		macs[iface] = readTrimmedFile("/sys/class/net/" + iface + "/address")
		states[iface] = readTrimmedFile("/sys/class/net/" + iface + "/operstate")
	}

	return pluginsdk.StateData{
		"id":             "network_interfaces",
		"interfaces":     interfaces,
		"ipv4_addresses": ipv4,
		"ipv6_addresses": ipv6,
		"mac_addresses":  macs,
		"states":         states,
	}, nil
}

type mountsDataSource struct{}

func (d *mountsDataSource) Name() string { return "mounts" }

func (d *mountsDataSource) DataSchema() pluginsdk.Schema {
	return linuxfactscontract.MountsDataSourceSchema()
}

func (d *mountsDataSource) DataRead(_ pluginsdk.StateData) (pluginsdk.StateData, error) {
	content, err := pluginsdk.FileRead("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("read /proc/mounts: %w", err)
	}

	mountList, devices, fstypes, options := parseProcMounts(string(content))
	return pluginsdk.StateData{
		"id":      "mounts",
		"mounts":  mountList,
		"devices": devices,
		"fstypes": fstypes,
		"options": options,
	}, nil
}

func parseKeyValue(data []byte) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		result[line[:idx]] = strings.Trim(line[idx+1:], "\"'")
	}
	return result
}

func parseProcNetDev(content string) []string {
	seen := make(map[string]struct{})
	interfaces := make([]string, 0)
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		iface := strings.TrimSpace(parts[0])
		if iface == "" || iface == "Inter-|" || iface == "face" {
			continue
		}
		if _, ok := seen[iface]; ok {
			continue
		}
		seen[iface] = struct{}{}
		interfaces = append(interfaces, iface)
	}
	sort.Strings(interfaces)
	return interfaces
}

func parseInterfaceAddresses(args []string) (map[string]string, error) {
	result := make(map[string]string)
	res, err := pluginsdk.CmdExec("ip", args)
	if err != nil {
		return nil, fmt.Errorf("ip %s: %w", strings.Join(args, " "), err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("ip %s failed (exit %d): %s", strings.Join(args, " "), res.ExitCode, res.Stderr)
	}

	for _, line := range strings.Split(res.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		iface := fields[1]
		address := fields[3]
		if current, ok := result[iface]; ok && current != "" {
			result[iface] = current + "," + address
			continue
		}
		result[iface] = address
	}
	return result, nil
}

func parseProcMounts(content string) ([]string, map[string]string, map[string]string, map[string]string) {
	mounts := make([]string, 0)
	devices := make(map[string]string)
	fstypes := make(map[string]string)
	options := make(map[string]string)

	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		mount := fields[1]
		mounts = append(mounts, mount)
		devices[mount] = fields[0]
		fstypes[mount] = fields[2]
		options[mount] = fields[3]
	}
	sort.Strings(mounts)
	return mounts, devices, fstypes, options
}

func readTrimmedFile(path string) string {
	data, err := pluginsdk.FileRead(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func init() {
	pluginsdk.RegisterDataSource(&osReleaseDataSource{})
	pluginsdk.RegisterDataSource(&systemInfoDataSource{})
	pluginsdk.RegisterDataSource(&networkInterfacesDataSource{})
	pluginsdk.RegisterDataSource(&mountsDataSource{})
}

func main() {}

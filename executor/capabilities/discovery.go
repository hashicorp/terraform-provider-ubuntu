package capabilities

import (
	"os"
	"path/filepath"
	"strings"
)

// Discover performs host discovery and returns a HostProfile.
// It reads files and uses syscalls -- no external commands.
func Discover() HostProfile {
	p := HostProfile{
		Extra: make(map[string]string),
	}

	p.Hostname = discoverHostname()
	discoverOSRelease(&p)
	p.DistroFamily = resolveDistroFamily(p.DistroID)
	discoverKernel(&p)
	p.InitSystem = discoverInitSystem()
	p.AvailableCmds = discoverCommands()
	p.SELinux = detectSELinux()
	p.AppArmor = detectAppArmor()
	p.PackageMgr = detectPackageManager(p.DistroFamily)

	return p
}

func discoverHostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return name
}

// discoverOSRelease parses /etc/os-release for distro info.
func discoverOSRelease(p *HostProfile) {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return
	}

	fields := parseOSRelease(string(data))
	p.DistroID = strings.ToLower(fields["ID"])
	p.DistroName = fields["NAME"]
	p.DistroVersion = fields["VERSION_ID"]
	if p.DistroVersion == "" {
		p.DistroVersion = fields["VERSION"]
	}
}

// parseOSRelease parses KEY=VALUE (possibly quoted) from os-release format.
func parseOSRelease(content string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := line[:idx]
		value := line[idx+1:]
		value = strings.Trim(value, `"'`)
		result[key] = value
	}
	return result
}

// resolveDistroFamily maps distro IDs to their family.
func resolveDistroFamily(distroID string) string {
	switch distroID {
	case "ubuntu", "debian", "linuxmint", "pop", "elementary", "kali", "raspbian":
		return "debian"
	case "rhel", "centos", "fedora", "rocky", "alma", "oracle", "amzn", "amazon":
		return "rhel"
	case "arch", "manjaro", "endeavouros":
		return "arch"
	case "opensuse-leap", "opensuse-tumbleweed", "sles", "suse":
		return "suse"
	case "alpine":
		return "alpine"
	case "gentoo":
		return "gentoo"
	case "void":
		return "void"
	default:
		// Check ID_LIKE if we can -- but we only have the ID here.
		// Fallback to unknown.
		return "unknown"
	}
}

// discoverKernel is implemented in discovery_linux.go (uname syscall)
// and discovery_other.go (fallback).

// discoverInitSystem detects the init system by reading /proc/1/exe.
func discoverInitSystem() string {
	target, err := os.Readlink("/proc/1/exe")
	if err != nil {
		// Fallback: check for systemd directories.
		if FileExists("/run/systemd/system") {
			return "systemd"
		}
		if FileExists("/etc/init.d") {
			return "sysvinit"
		}
		return "unknown"
	}

	base := filepath.Base(target)
	switch {
	case strings.Contains(base, "systemd"):
		return "systemd"
	case strings.Contains(base, "init"):
		// Could be sysvinit or busybox init.
		if FileExists("/etc/init.d") {
			return "sysvinit"
		}
		return "init"
	case strings.Contains(base, "openrc"):
		return "openrc"
	default:
		return base
	}
}

// discoverCommands walks PATH directories and collects executable names.
func discoverCommands() []string {
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		pathEnv = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}

	seen := make(map[string]struct{})
	var cmds []string

	for _, dir := range strings.Split(pathEnv, ":") {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			cmds = append(cmds, name)
		}
	}

	return cmds
}

// detectSELinux checks if SELinux is active.
func detectSELinux() bool {
	// Check for selinuxfs mount.
	data, err := os.ReadFile("/proc/filesystems")
	if err != nil {
		return false
	}
	if !strings.Contains(string(data), "selinuxfs") {
		return false
	}
	// Check the enforce file.
	return FileExists("/sys/fs/selinux/enforce")
}

// detectAppArmor checks if AppArmor is active.
func detectAppArmor() bool {
	data, err := os.ReadFile("/sys/module/apparmor/parameters/enabled")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == "Y"
}

// detectPackageManager finds the appropriate package manager.
func detectPackageManager(family string) string {
	// Try family-based detection first, then fall back to checking PATH.
	switch family {
	case "debian":
		if FileExists("/usr/bin/apt") || FileExists("/usr/bin/apt-get") {
			return "apt"
		}
	case "rhel":
		if FileExists("/usr/bin/dnf") {
			return "dnf"
		}
		if FileExists("/usr/bin/yum") {
			return "yum"
		}
	case "arch":
		if FileExists("/usr/bin/pacman") {
			return "pacman"
		}
	case "suse":
		if FileExists("/usr/bin/zypper") {
			return "zypper"
		}
	case "alpine":
		if FileExists("/sbin/apk") || FileExists("/usr/sbin/apk") {
			return "apk"
		}
	}

	// Fallback: check common managers in PATH.
	for _, mgr := range []string{"apt", "dnf", "yum", "pacman", "zypper", "apk", "emerge", "xbps-install"} {
		if CmdExists(mgr) {
			return mgr
		}
	}

	return "unknown"
}

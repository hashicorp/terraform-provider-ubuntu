package catalog

import "path"

const (
	ModuleLinuxCommands  = "linux_commands"
	ModuleLinuxFiles     = "linux_files"
	ModuleLinuxIdentity  = "linux_identity"
	ModuleLinuxPackages  = "linux_packages"
	ModuleLinuxFacts     = "linux_facts"
	ModuleLinuxTLS       = "linux_tls"
	ModuleSystemdUnits   = "systemd_units"
	ModuleDebianApt      = "debian_apt"
	ModuleDebianTrust    = "debian_trust"
	ModuleUbuntuUFW      = "ubuntu_ufw"
	ModuleRedHatDnf      = "redhat_dnf"
	ModuleRedHatFirewall = "redhat_firewalld"
	ModuleRedHatTimesync = "redhat_timesync"
	ModuleRedHatTrust    = "redhat_trust"
)

func RuntimeModuleSourceRepoPath(module string) (string, bool) {
	switch module {
	case ModuleLinuxCommands:
		return path.Join("guest", "packs", "linux", "commands"), true
	case ModuleLinuxFiles:
		return path.Join("guest", "packs", "linux", "files_config"), true
	case ModuleLinuxIdentity:
		return path.Join("guest", "packs", "linux", "identity"), true
	case ModuleLinuxPackages:
		return path.Join("guest", "packs", "linux", "packages"), true
	case ModuleLinuxFacts:
		return path.Join("guest", "packs", "linux", "facts"), true
	case ModuleLinuxTLS:
		return path.Join("guest", "packs", "linux", "tls_identity"), true
	case ModuleSystemdUnits:
		return path.Join("guest", "packs", "systemd", "services"), true
	case ModuleDebianApt:
		return path.Join("guest", "packs", "debian", "apt_repository"), true
	case ModuleDebianTrust:
		return path.Join("guest", "packs", "debian", "trusted_cert"), true
	case ModuleUbuntuUFW:
		return path.Join("guest", "packs", "ubuntu", "ufw"), true
	case ModuleRedHatDnf:
		return path.Join("guest", "packs", "redhat", "dnf_repository"), true
	case ModuleRedHatFirewall:
		return path.Join("guest", "packs", "redhat", "firewalld"), true
	case ModuleRedHatTimesync:
		return path.Join("guest", "packs", "redhat", "timesync"), true
	case ModuleRedHatTrust:
		return path.Join("guest", "packs", "redhat", "trusted_cert"), true
	default:
		return "", false
	}
}

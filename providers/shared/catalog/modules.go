// Copyright IBM Corp. 2026

package catalog

const (
	ModuleDebianApt = "debian_apt"
	ModuleDebianTrust = "debian_trust"
	ModuleLinuxCommands = "linux_commands"
	ModuleLinuxFacts = "linux_facts"
	ModuleLinuxFiles = "linux_files"
	ModuleLinuxIdentity = "linux_identity"
	ModuleLinuxPackages = "linux_packages"
	ModuleLinuxTLS = "linux_tls"
	ModuleSystemdUnits = "systemd_units"
	ModuleUbuntuUFW = "ubuntu_ufw"
)

func RuntimeModuleSourceRepoPath(module string) (string, bool) {
	switch module {
	case ModuleDebianApt:
		return "guest/packs/debian/apt_repository", true
	case ModuleDebianTrust:
		return "guest/packs/debian/trusted_cert", true
	case ModuleLinuxCommands:
		return "guest/packs/linux/commands", true
	case ModuleLinuxFacts:
		return "guest/packs/linux/facts", true
	case ModuleLinuxFiles:
		return "guest/packs/linux/files_config", true
	case ModuleLinuxIdentity:
		return "guest/packs/linux/identity", true
	case ModuleLinuxPackages:
		return "guest/packs/linux/packages", true
	case ModuleLinuxTLS:
		return "guest/packs/linux/tls_identity", true
	case ModuleSystemdUnits:
		return "guest/packs/systemd/services", true
	case ModuleUbuntuUFW:
		return "guest/packs/ubuntu/ufw", true
	default:
		return "", false
	}
}

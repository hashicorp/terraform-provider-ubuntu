package catalog

import (
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/hostsession"
)

func hostsEntryLockPlanner(action string, _ *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	return []hostsession.LockDescriptor{{
		Key:    "path:/etc/hosts",
		Mode:   engine.LockModeForAction(action),
		Source: "hosts entry resource",
	}}, nil
}

func packageLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	locks := []hostsession.LockDescriptor{{
		Key:    "pkgmgr:system",
		Mode:   engine.LockModeForAction(action),
		Source: "package manager resource",
	}}
	if path := strings.TrimSpace(engine.StringAttr(op, "repo_keyring_path")); filepath.IsAbs(path) {
		locks = append(locks, hostsession.LockDescriptor{
			Key:    "path:" + path,
			Mode:   engine.LockModeForAction(action),
			Source: "package repository keyring",
		})
	}
	return locks, nil
}

func identityLockPlanner(action string, _ *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	return []hostsession.LockDescriptor{{
		Key:    "identity:system",
		Mode:   engine.LockModeForAction(action),
		Source: "identity resource",
	}}, nil
}

func sshdConfigLockPlanner(action string, _ *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	mode := engine.LockModeForAction(action)
	return []hostsession.LockDescriptor{
		{Key: "sshd:config", Mode: mode, Source: "sshd config resource"},
		{Key: "service:sshd", Mode: mode, Source: "sshd config resource"},
	}, nil
}

func sysctlEntryLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	key := strings.TrimSpace(engine.StringAttr(op, "key"))
	if key == "" {
		key = "system"
	}
	mode := engine.LockModeForAction(action)
	return []hostsession.LockDescriptor{
		{
			Key:    "path:/etc/sysctl.conf",
			Mode:   mode,
			Source: "sysctl resource",
		},
		{
			Key:    "sysctl:" + key,
			Mode:   mode,
			Source: "sysctl resource",
		},
	}, nil
}

func networkStackLockPlanner(action string, _ *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	mode := engine.LockModeForAction(action)
	return []hostsession.LockDescriptor{
		{Key: "path:/etc/sysctl.d/90-tf-nix-network-stack.conf", Mode: mode, Source: "network stack resource"},
		{Key: "network:stack", Mode: mode, Source: "network stack resource"},
		{Key: "sysctl:net.ipv4.ip_forward", Mode: mode, Source: "network stack resource"},
		{Key: "sysctl:net.ipv6.conf.all.forwarding", Mode: mode, Source: "network stack resource"},
		{Key: "sysctl:net.ipv6.conf.default.forwarding", Mode: mode, Source: "network stack resource"},
		{Key: "sysctl:net.bridge.bridge-nf-call-iptables", Mode: mode, Source: "network stack resource"},
		{Key: "sysctl:net.bridge.bridge-nf-call-ip6tables", Mode: mode, Source: "network stack resource"},
	}, nil
}

func ufwRuleLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	mode := engine.LockModeForAction(action)
	name := strings.TrimSpace(engine.StringAttr(op, "name", "id"))
	if name == "" {
		name = "managed"
	}
	return []hostsession.LockDescriptor{
		{Key: "network:system", Mode: mode, Source: "ufw firewall rule"},
		{Key: "firewall:ufw", Mode: mode, Source: "ufw firewall rule"},
		{Key: "firewall:ufw:rule:" + name, Mode: mode, Source: "ufw firewall rule"},
	}, nil
}

func firewalldLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	mode := engine.LockModeForAction(action)
	zone := strings.TrimSpace(engine.StringAttr(op, "zone"))
	if zone == "" {
		zone = "public"
	}
	return []hostsession.LockDescriptor{
		{Key: "network:system", Mode: mode, Source: "firewalld resource"},
		{Key: "firewall:firewalld", Mode: mode, Source: "firewalld resource"},
		{Key: "firewall:firewalld:zone:" + zone, Mode: mode, Source: "firewalld resource"},
	}, nil
}

func fstabEntryLockPlanner(action string, _ *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	return []hostsession.LockDescriptor{{
		Key:    "path:/etc/fstab",
		Mode:   engine.LockModeForAction(action),
		Source: "fstab resource",
	}}, nil
}

func crontabEntryLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	mode := engine.LockModeForAction(action)
	keys := make([]string, 0, 2)
	if op == nil {
		op = &hostsession.OperationMessage{}
	}
	for _, user := range []string{
		strings.TrimSpace(engine.StringAttr(&hostsession.OperationMessage{Plan: op.Plan}, "user")),
		strings.TrimSpace(engine.StringAttr(&hostsession.OperationMessage{State: op.State}, "user")),
	} {
		if user == "" {
			continue
		}
		key := "crontab:user:" + user
		duplicate := false
		for _, existing := range keys {
			if existing == key {
				duplicate = true
				break
			}
		}
		if !duplicate {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		keys = append(keys, "crontab:user:system")
	}

	locks := make([]hostsession.LockDescriptor, 0, len(keys))
	for _, key := range keys {
		locks = append(locks, hostsession.LockDescriptor{
			Key:    key,
			Mode:   mode,
			Source: "crontab resource",
		})
	}
	return locks, nil
}

func fileLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	path := strings.TrimSpace(engine.StringAttr(op, "path", "id"))
	if path == "" || !filepath.IsAbs(path) {
		return nil, nil
	}
	return []hostsession.LockDescriptor{{
		Key:    "path:" + path,
		Mode:   engine.LockModeForAction(action),
		Source: "file resource",
	}}, nil
}

func kernelModulesLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	mode := engine.LockModeForAction(action)
	locks := []hostsession.LockDescriptor{{
		Key:    "kernel-modules:system",
		Mode:   mode,
		Source: "kernel modules resource",
	}}
	if path := strings.TrimSpace(engine.StringAttr(op, "path", "id")); filepath.IsAbs(path) {
		locks = append(locks, hostsession.LockDescriptor{
			Key:    "path:" + path,
			Mode:   mode,
			Source: "kernel modules resource",
		})
	}
	return locks, nil
}

func swapLockPlanner(action string, _ *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	mode := engine.LockModeForAction(action)
	return []hostsession.LockDescriptor{
		{
			Key:    "swap:system",
			Mode:   mode,
			Source: "swap resource",
		},
		{
			Key:    "path:/etc/fstab",
			Mode:   mode,
			Source: "swap resource",
		},
	}, nil
}

func commandLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	name := strings.TrimSpace(engine.StringAttr(op, "name", "id"))
	if name == "" {
		return []hostsession.LockDescriptor{{
			Key:    "command:host",
			Mode:   engine.LockModeForAction(action),
			Source: "command resource",
		}}, nil
	}
	return []hostsession.LockDescriptor{{
		Key:    "command:" + name,
		Mode:   engine.LockModeForAction(action),
		Source: "command resource",
	}}, nil
}

func systemdUnitLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	name := strings.TrimSpace(engine.StringAttr(op, "name", "id"))
	if name == "" {
		return nil, nil
	}
	return []hostsession.LockDescriptor{{
		Key:    "service:" + name,
		Mode:   engine.LockModeForAction(action),
		Source: "systemd unit resource",
	}}, nil
}

func timezoneLockPlanner(action string, _ *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	return []hostsession.LockDescriptor{{
		Key:    "time:timezone",
		Mode:   engine.LockModeForAction(action),
		Source: "timezone resource",
	}}, nil
}

func timesyncdLockPlanner(action string, _ *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	mode := engine.LockModeForAction(action)
	return []hostsession.LockDescriptor{
		{Key: "time:timesync", Mode: mode, Source: "timesync resource"},
		{Key: "path:/etc/systemd/timesyncd.conf.d/90-tf-nix-timesync.conf", Mode: mode, Source: "timesync resource"},
		{Key: "service:systemd-timesyncd", Mode: mode, Source: "timesync resource"},
	}, nil
}

func chronyTimesyncLockPlanner(action string, _ *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	mode := engine.LockModeForAction(action)
	return []hostsession.LockDescriptor{
		{Key: "time:timesync", Mode: mode, Source: "timesync resource"},
		{Key: "path:/etc/chrony.conf", Mode: mode, Source: "timesync resource"},
		{Key: "path:/etc/chrony.d/90-tf-nix-timesync.conf", Mode: mode, Source: "timesync resource"},
		{Key: "service:chronyd", Mode: mode, Source: "timesync resource"},
	}, nil
}

func aptRepositoryLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	locks := []hostsession.LockDescriptor{{
		Key:    "pkgmgr:system",
		Mode:   engine.LockModeForAction(action),
		Source: "apt repository resource",
	}}
	if path := strings.TrimSpace(engine.StringAttr(op, "file_path")); filepath.IsAbs(path) {
		locks = append(locks, hostsession.LockDescriptor{Key: "path:" + path, Mode: engine.LockModeForAction(action), Source: "apt repository file"})
	}
	if signedBy := strings.TrimSpace(engine.StringAttr(op, "signed_by")); filepath.IsAbs(signedBy) && strings.TrimSpace(engine.StringAttr(op, "signed_by_key_url")) != "" {
		locks = append(locks, hostsession.LockDescriptor{
			Key:    "path:" + signedBy,
			Mode:   engine.LockModeForAction(action),
			Source: "apt repository keyring",
		})
	}
	return locks, nil
}

func dnfRepositoryLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	locks := []hostsession.LockDescriptor{{
		Key:    "pkgmgr:system",
		Mode:   engine.LockModeForAction(action),
		Source: "dnf repository resource",
	}}
	if path := strings.TrimSpace(engine.StringAttr(op, "file_path")); filepath.IsAbs(path) {
		locks = append(locks, hostsession.LockDescriptor{
			Key:    "path:" + path,
			Mode:   engine.LockModeForAction(action),
			Source: "dnf repository file",
		})
	}
	return locks, nil
}

func trustedCertLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	path := trustedCertLockPath(op)
	locks := []hostsession.LockDescriptor{{
		Key:    "trust-store:debian",
		Mode:   engine.LockModeForAction(action),
		Source: "debian trust store",
	}}
	if path != "" {
		locks = append(locks, hostsession.LockDescriptor{
			Key:    "path:" + path,
			Mode:   engine.LockModeForAction(action),
			Source: "managed trust anchor path",
		})
	}

	return locks, nil
}

func redHatTrustedCertLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	path := trustedCertLockPath(op)
	locks := []hostsession.LockDescriptor{{
		Key:    "trust-store:redhat",
		Mode:   engine.LockModeForAction(action),
		Source: "redhat trust store",
	}}
	if path != "" {
		locks = append(locks, hostsession.LockDescriptor{
			Key:    "path:" + path,
			Mode:   engine.LockModeForAction(action),
			Source: "managed trust anchor path",
		})
	}
	return locks, nil
}

func tlsIdentityLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	mode := engine.LockModeForAction(action)
	locks := make([]hostsession.LockDescriptor, 0, 2)

	if path := tlsIdentityFullchainLockPath(op); path != "" {
		locks = append(locks, hostsession.LockDescriptor{
			Key:    "path:" + path,
			Mode:   mode,
			Source: "managed tls fullchain path",
		})
	}
	if path := tlsIdentityPrivateKeyLockPath(op); path != "" {
		locks = append(locks, hostsession.LockDescriptor{
			Key:    "path:" + path,
			Mode:   mode,
			Source: "managed tls private key path",
		})
	}

	return locks, nil
}

func nginxSiteLockPlanner(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	mode := engine.LockModeForAction(action)
	locks := []hostsession.LockDescriptor{
		{Key: "pkgmgr:system", Mode: mode, Source: "nginx site resource"},
		{Key: "nginx:config", Mode: mode, Source: "nginx site resource"},
		{Key: "service:nginx", Mode: mode, Source: "nginx site resource"},
	}
	name := strings.TrimSpace(engine.StringAttr(op, "name", "id"))
	if name == "" {
		return locks, nil
	}
	locks = append(locks,
		hostsession.LockDescriptor{Key: "path:" + filepath.Join("/etc/nginx/sites-available", name), Mode: mode, Source: "nginx site file"},
		hostsession.LockDescriptor{Key: "path:" + filepath.Join("/etc/nginx/sites-enabled", name), Mode: mode, Source: "nginx site enablement"},
	)
	return locks, nil
}

func trustedCertLockPath(op *hostsession.OperationMessage) string {
	if path := engine.StringAttr(op, "cert_path"); path != "" {
		return path
	}
	if id := engine.StringAttr(op, "id"); filepath.IsAbs(id) {
		return id
	}
	if name := engine.StringAttr(op, "name"); name != "" {
		return filepath.Join("/usr/local/share/ca-certificates", name+".crt")
	}

	return ""
}

func tlsIdentityFullchainLockPath(op *hostsession.OperationMessage) string {
	if path := engine.StringAttr(op, "fullchain_path"); filepath.IsAbs(path) {
		return path
	}
	name := strings.TrimSpace(engine.StringAttr(op, "name", "id"))
	if name == "" {
		return ""
	}
	return filepath.Join("/etc/ssl/certs", name+"-fullchain.pem")
}

func tlsIdentityPrivateKeyLockPath(op *hostsession.OperationMessage) string {
	if path := engine.StringAttr(op, "private_key_path"); filepath.IsAbs(path) {
		return path
	}
	name := strings.TrimSpace(engine.StringAttr(op, "name", "id"))
	if name == "" {
		return ""
	}
	return filepath.Join("/etc/ssl/private", name+".key")
}

func osReleaseLockPlanner(_ string, _ *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	return []hostsession.LockDescriptor{{Key: "facts:system", Mode: hostsession.LockModeShared, Source: "os release data source"}}, nil
}

func systemInfoLockPlanner(_ string, _ *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	return []hostsession.LockDescriptor{{Key: "facts:system", Mode: hostsession.LockModeShared, Source: "system info data source"}}, nil
}

func networkInterfacesLockPlanner(_ string, _ *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	return []hostsession.LockDescriptor{{Key: "network:system", Mode: hostsession.LockModeShared, Source: "network interfaces data source"}}, nil
}

func mountsLockPlanner(_ string, _ *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	return []hostsession.LockDescriptor{{Key: "mounts:system", Mode: hostsession.LockModeShared, Source: "mounts data source"}}, nil
}

func fileInfoLockPlanner(_ string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error) {
	path := strings.TrimSpace(engine.StringAttr(op, "path", "id"))
	if path == "" || !filepath.IsAbs(path) {
		return []hostsession.LockDescriptor{{Key: "host", Mode: hostsession.LockModeShared, Source: "file info data source"}}, nil
	}
	return []hostsession.LockDescriptor{{Key: "path:" + path, Mode: hostsession.LockModeShared, Source: "file info data source"}}, nil
}

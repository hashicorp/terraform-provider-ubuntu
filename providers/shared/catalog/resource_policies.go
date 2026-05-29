// Copyright IBM Corp. 2026

package catalog

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

func protectedPathGuard(keys ...string) engine.DestroySafetyGuard {
	return func(state map[string]interface{}, cfg engine.DestroySafetyConfig) (bool, string) {
		for _, key := range keys {
			value, _ := state[key].(string)
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if cfg.IsProtectedPath(value) {
				return true, fmt.Sprintf("destroy is blocked because %q targets protected path %s", key, value)
			}
		}
		return false, ""
	}
}

func protectedFixedPathGuard(value string) engine.DestroySafetyGuard {
	return func(_ map[string]interface{}, cfg engine.DestroySafetyConfig) (bool, string) {
		if cfg.IsProtectedPath(value) {
			return true, fmt.Sprintf("destroy is blocked because path %s is protected", value)
		}
		return false, ""
	}
}

func protectedSystemdServiceGuard(state map[string]interface{}, cfg engine.DestroySafetyConfig) (bool, string) {
	name, _ := state["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return false, ""
	}
	if cfg.IsProtectedService(name) {
		return true, fmt.Sprintf("destroy is blocked because systemd unit %q is protected", name)
	}
	return false, ""
}

func protectedPackageGuard(state map[string]interface{}, cfg engine.DestroySafetyConfig) (bool, string) {
	name, _ := state["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return false, ""
	}
	if cfg.IsProtectedPackage(name) {
		return true, fmt.Sprintf("destroy is blocked because package %q is protected", name)
	}
	return false, ""
}

func protectedUserGuard(state map[string]interface{}, cfg engine.DestroySafetyConfig) (bool, string) {
	name, _ := state["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return false, ""
	}
	if cfg.IsProtectedUser(name) {
		return true, fmt.Sprintf("destroy is blocked because user %q is protected", name)
	}
	return false, ""
}

func protectedGroupGuard(state map[string]interface{}, cfg engine.DestroySafetyConfig) (bool, string) {
	name, _ := state["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return false, ""
	}
	if cfg.IsProtectedGroup(name) {
		return true, fmt.Sprintf("destroy is blocked because group %q is protected", name)
	}
	return false, ""
}

func protectedHostsEntryGuard(state map[string]interface{}, _ engine.DestroySafetyConfig) (bool, string) {
	names := []string{}
	if hostname, _ := state["hostname"].(string); strings.TrimSpace(hostname) != "" {
		names = append(names, strings.ToLower(strings.TrimSpace(hostname)))
	}
	if aliases, ok := state["aliases"].([]string); ok {
		for _, alias := range aliases {
			names = append(names, strings.ToLower(strings.TrimSpace(alias)))
		}
	}
	if aliases, ok := state["aliases"].([]interface{}); ok {
		for _, alias := range aliases {
			if text, ok := alias.(string); ok {
				names = append(names, strings.ToLower(strings.TrimSpace(text)))
			}
		}
	}
	for _, name := range names {
		if name == "localhost" || name == "ip6-localhost" {
			return true, fmt.Sprintf("destroy is blocked because /etc/hosts entry %q is protected", name)
		}
	}
	return false, ""
}

func protectedFstabGuard(state map[string]interface{}, _ engine.DestroySafetyConfig) (bool, string) {
	mount, _ := state["mount"].(string)
	mount = strings.TrimSpace(mount)
	switch mount {
	case "/", "/boot", "/boot/efi":
		return true, fmt.Sprintf("destroy is blocked because mountpoint %q is protected", mount)
	default:
		return false, ""
	}
}

func protectedSysctlGuard(state map[string]interface{}, _ engine.DestroySafetyConfig) (bool, string) {
	key, _ := state["key"].(string)
	key = strings.TrimSpace(key)
	if key == "" {
		return false, ""
	}

	switch key {
	case "kernel.modules_disabled",
		"net.ipv4.ip_forward",
		"net.ipv6.conf.all.forwarding",
		"net.ipv6.conf.default.forwarding",
		"net.ipv6.conf.all.disable_ipv6",
		"net.ipv6.conf.default.disable_ipv6":
		return true, fmt.Sprintf("destroy is blocked because sysctl key %q is protected", key)
	default:
		return false, ""
	}
}

func protectedNetworkStackGuard(state map[string]interface{}, _ engine.DestroySafetyConfig) (bool, string) {
	if enabled, _ := state["ipv4_forwarding"].(bool); enabled {
		return true, "destroy is blocked because network_stack currently manages IPv4 forwarding"
	}
	if enabled, _ := state["ipv6_forwarding"].(bool); enabled {
		return true, "destroy is blocked because network_stack currently manages IPv6 forwarding"
	}
	if enabled, _ := state["bridge_netfilter_ipv4"].(bool); enabled {
		return true, "destroy is blocked because network_stack currently manages IPv4 bridge netfilter"
	}
	if enabled, _ := state["bridge_netfilter_ipv6"].(bool); enabled {
		return true, "destroy is blocked because network_stack currently manages IPv6 bridge netfilter"
	}

	if sysctls, ok := state["sysctls"].(map[string]string); ok {
		for key, value := range sysctls {
			if strings.TrimSpace(value) == "1" {
				return true, fmt.Sprintf("destroy is blocked because network_stack currently manages sysctl %q", key)
			}
		}
	}
	if sysctls, ok := state["sysctls"].(map[string]interface{}); ok {
		for key, value := range sysctls {
			if text, ok := value.(string); ok && strings.TrimSpace(text) == "1" {
				return true, fmt.Sprintf("destroy is blocked because network_stack currently manages sysctl %q", key)
			}
		}
	}

	return false, ""
}

func protectedUFWRuleGuard(state map[string]interface{}, _ engine.DestroySafetyConfig) (bool, string) {
	allowDisconnect, _ := state["allow_ssh_disconnect"].(bool)
	if allowDisconnect {
		return false, ""
	}

	action, _ := state["action"].(string)
	direction, _ := state["direction"].(string)
	port, _ := state["port"].(string)
	protocol, _ := state["protocol"].(string)

	if strings.EqualFold(strings.TrimSpace(action), "allow") &&
		strings.EqualFold(strings.TrimSpace(direction), "in") &&
		strings.TrimSpace(port) == "22" &&
		(strings.TrimSpace(protocol) == "" || strings.EqualFold(strings.TrimSpace(protocol), "tcp")) {
		return true, "destroy is blocked because the managed UFW rule currently opens inbound SSH on tcp/22"
	}

	return false, ""
}

func protectedFirewalldServiceGuard(state map[string]interface{}, _ engine.DestroySafetyConfig) (bool, string) {
	allowDisconnect, _ := state["allow_ssh_disconnect"].(bool)
	if allowDisconnect {
		return false, ""
	}

	name, _ := state["name"].(string)
	if strings.EqualFold(strings.TrimSpace(name), "ssh") {
		return true, "destroy is blocked because the managed firewalld service currently opens SSH"
	}
	return false, ""
}

func protectedFirewalldPortGuard(state map[string]interface{}, _ engine.DestroySafetyConfig) (bool, string) {
	allowDisconnect, _ := state["allow_ssh_disconnect"].(bool)
	if allowDisconnect {
		return false, ""
	}

	port, _ := state["port"].(string)
	protocol, _ := state["protocol"].(string)
	if strings.TrimSpace(port) == "22" && (strings.TrimSpace(protocol) == "" || strings.EqualFold(strings.TrimSpace(protocol), "tcp")) {
		return true, "destroy is blocked because the managed firewalld port currently opens tcp/22"
	}
	return false, ""
}

func explicitDeleteCommandGuard(state map[string]interface{}) bool {
	deleteCommand, _ := state["delete_command"].(string)
	return strings.TrimSpace(deleteCommand) != ""
}

func trustedCertPathGuard(state map[string]interface{}, cfg engine.DestroySafetyConfig) (bool, string) {
	if blocked, reason := protectedPathGuard("cert_path")(state, cfg); blocked {
		return blocked, reason
	}
	if name, _ := state["name"].(string); strings.TrimSpace(name) != "" {
		path := filepath.Join("/usr/local/share/ca-certificates", strings.TrimSpace(name)+".crt")
		if cfg.IsProtectedPath(path) {
			return true, fmt.Sprintf("destroy is blocked because trust anchor path %s is protected", path)
		}
	}
	return false, ""
}

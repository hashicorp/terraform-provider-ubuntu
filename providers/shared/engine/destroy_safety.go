package engine

import (
	"path/filepath"
	"strings"
)

type DestroySafetyConfig struct {
	protectedPathExact    map[string]struct{}
	protectedPathPrefixes []string
	protectedServices     map[string]struct{}
	protectedPackages     map[string]struct{}
	protectedUsers        map[string]struct{}
	protectedGroups       map[string]struct{}
}

func NewDestroySafetyConfig(paths, services, packages, users, groups []string) DestroySafetyConfig {
	cfg := DestroySafetyConfig{
		protectedPathExact:    map[string]struct{}{},
		protectedPathPrefixes: []string{},
		protectedServices:     map[string]struct{}{},
		protectedPackages:     map[string]struct{}{},
		protectedUsers:        map[string]struct{}{},
		protectedGroups:       map[string]struct{}{},
	}

	for _, value := range builtinProtectedPathsExact {
		cfg.addProtectedPath(value)
	}
	for _, value := range builtinProtectedPathPrefixes {
		cfg.addProtectedPath(value)
	}
	for _, value := range builtinProtectedServices {
		cfg.addProtectedService(value)
	}
	for _, value := range builtinProtectedPackages {
		cfg.addProtectedPackage(value)
	}
	for _, value := range builtinProtectedUsers {
		cfg.addProtectedUser(value)
	}
	for _, value := range builtinProtectedGroups {
		cfg.addProtectedGroup(value)
	}

	for _, value := range paths {
		cfg.addProtectedPath(value)
	}
	for _, value := range services {
		cfg.addProtectedService(value)
	}
	for _, value := range packages {
		cfg.addProtectedPackage(value)
	}
	for _, value := range users {
		cfg.addProtectedUser(value)
	}
	for _, value := range groups {
		cfg.addProtectedGroup(value)
	}

	return cfg
}

func DefaultDestroySafetyConfig() DestroySafetyConfig {
	return NewDestroySafetyConfig(nil, nil, nil, nil, nil)
}

func (c DestroySafetyConfig) IsProtectedPath(path string) bool {
	path = normalizeProtectedPath(path)
	if path == "" {
		return false
	}
	if _, ok := c.protectedPathExact[path]; ok {
		return true
	}
	for _, prefix := range c.protectedPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func (c DestroySafetyConfig) IsProtectedService(name string) bool {
	name = normalizeProtectedName(name)
	if name == "" {
		return false
	}
	if _, ok := c.protectedServices[name]; ok {
		return true
	}
	trimmed := strings.TrimSuffix(name, ".service")
	_, ok := c.protectedServices[trimmed]
	return ok
}

func (c DestroySafetyConfig) IsProtectedPackage(name string) bool {
	_, ok := c.protectedPackages[normalizeProtectedName(name)]
	return ok
}

func (c DestroySafetyConfig) IsProtectedUser(name string) bool {
	_, ok := c.protectedUsers[normalizeProtectedName(name)]
	return ok
}

func (c DestroySafetyConfig) IsProtectedGroup(name string) bool {
	_, ok := c.protectedGroups[normalizeProtectedName(name)]
	return ok
}

func (c *DestroySafetyConfig) addProtectedPath(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if strings.HasSuffix(value, "/") {
		prefix := filepath.Clean(value)
		if prefix == "." {
			return
		}
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		for _, existing := range c.protectedPathPrefixes {
			if existing == prefix {
				return
			}
		}
		c.protectedPathPrefixes = append(c.protectedPathPrefixes, prefix)
		return
	}

	value = normalizeProtectedPath(value)
	if value == "" {
		return
	}
	c.protectedPathExact[value] = struct{}{}
}

func (c *DestroySafetyConfig) addProtectedService(value string) {
	value = normalizeProtectedName(value)
	if value == "" {
		return
	}
	c.protectedServices[value] = struct{}{}
}

func (c *DestroySafetyConfig) addProtectedPackage(value string) {
	value = normalizeProtectedName(value)
	if value == "" {
		return
	}
	c.protectedPackages[value] = struct{}{}
}

func (c *DestroySafetyConfig) addProtectedUser(value string) {
	value = normalizeProtectedName(value)
	if value == "" {
		return
	}
	c.protectedUsers[value] = struct{}{}
}

func (c *DestroySafetyConfig) addProtectedGroup(value string) {
	value = normalizeProtectedName(value)
	if value == "" {
		return
	}
	c.protectedGroups[value] = struct{}{}
}

func normalizeProtectedPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	if !strings.HasPrefix(cleaned, "/") {
		return ""
	}
	return cleaned
}

func normalizeProtectedName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

var builtinProtectedPathsExact = []string{
	"/etc/passwd",
	"/etc/group",
	"/etc/shadow",
	"/etc/gshadow",
	"/etc/sudoers",
	"/etc/ssh/sshd_config",
	"/etc/hosts",
	"/etc/fstab",
	"/etc/sysctl.conf",
}

var builtinProtectedPathPrefixes = []string{
	"/etc/sudoers.d/",
	"/etc/ssh/sshd_config.d/",
}

var builtinProtectedServices = []string{
	"ssh",
	"sshd",
	"systemd-networkd",
	"networkmanager",
	"network-manager",
	"networking",
	"ufw",
	"firewalld",
	"nftables",
}

var builtinProtectedPackages = []string{
	"openssh-server",
	"openssh-client",
	"sudo",
	"systemd",
	"network-manager",
	"networkmanager",
	"ufw",
	"firewalld",
	"nftables",
	"iptables",
}

var builtinProtectedUsers = []string{
	"root",
}

var builtinProtectedGroups = []string{
	"root",
	"sudo",
	"wheel",
}

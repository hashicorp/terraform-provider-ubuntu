// Copyright IBM Corp. 2026

package pluginsdk

import (
	"fmt"
	"strings"
)

func LoadHostProfile() (*HostProfile, error) {
	profile, err := GetHostProfile()
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, fmt.Errorf("host profile unavailable")
	}
	return profile, nil
}

func HostPackageManager() (string, error) {
	return GetPackageManager()
}

func defaultGetPackageManager() (string, error) {
	profile, err := LoadHostProfile()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(profile.PackageManager) == "" {
		return "", fmt.Errorf("host profile did not include a package manager")
	}
	return profile.PackageManager, nil
}

func HostDistroFamily() (string, error) {
	return GetDistroFamily()
}

func defaultGetDistroFamily() (string, error) {
	profile, err := LoadHostProfile()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(profile.DistroFamily) == "" {
		return "", fmt.Errorf("host profile did not include a distro family")
	}
	return profile.DistroFamily, nil
}

func HostHasCommand(name string) (bool, error) {
	return HasCommand(name)
}

func defaultHasCommand(name string) (bool, error) {
	profile, err := LoadHostProfile()
	if err != nil {
		return false, err
	}
	return profile.HasCommand(name), nil
}

func (p *HostProfile) HasCommand(name string) bool {
	if p == nil {
		return false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, candidate := range p.AvailableCommands {
		if candidate == name {
			return true
		}
	}
	return false
}

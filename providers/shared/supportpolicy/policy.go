package supportpolicy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

type Policy struct {
	ID                     string
	AllowAny               bool
	AllowedDistroIDs       []string
	AllowedVersionByDistro map[string][]string
	AllowedPackageManagers []string
}

type ViolationError struct {
	PolicyID string
	Message  string
}

func (e *ViolationError) Error() string {
	if strings.TrimSpace(e.PolicyID) == "" {
		return e.Message
	}
	return fmt.Sprintf("support policy %q rejected host: %s", e.PolicyID, e.Message)
}

func Resolve(id string) (Policy, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Policy{ID: "unspecified", AllowAny: true}, nil
	}

	policy, ok := policies()[id]
	if !ok {
		return Policy{}, fmt.Errorf("unknown support policy %q", id)
	}
	return policy, nil
}

func IsViolation(err error) bool {
	_, ok := err.(*ViolationError)
	return ok
}

func (p Policy) Check(profile hostrpc.HostProfile) error {
	if p.AllowAny {
		return nil
	}

	id := strings.ToLower(strings.TrimSpace(profile.DistroID))
	if !containsString(p.AllowedDistroIDs, id) {
		return &ViolationError{
			PolicyID: p.ID,
			Message:  fmt.Sprintf("distro %q is unsupported; allowed distros: %s", emptyIfUnknown(id), strings.Join(sortedStrings(p.AllowedDistroIDs), ", ")),
		}
	}

	if prefixes := p.AllowedVersionByDistro[id]; len(prefixes) > 0 {
		version := strings.TrimSpace(profile.DistroVersion)
		if !hasAnyPrefix(version, prefixes) {
			return &ViolationError{
				PolicyID: p.ID,
				Message:  fmt.Sprintf("distro version %q for %s is unsupported; allowed versions: %s", emptyIfUnknown(version), id, strings.Join(prefixes, ", ")),
			}
		}
	}

	if len(p.AllowedPackageManagers) > 0 {
		packageMgr := strings.ToLower(strings.TrimSpace(profile.PackageMgr))
		if !containsString(p.AllowedPackageManagers, packageMgr) {
			return &ViolationError{
				PolicyID: p.ID,
				Message:  fmt.Sprintf("package manager %q is unsupported; allowed package managers: %s", emptyIfUnknown(packageMgr), strings.Join(sortedStrings(p.AllowedPackageManagers), ", ")),
			}
		}
	}

	return nil
}

func policies() map[string]Policy {
	return map[string]Policy{
		"internal-only": {
			ID:       "internal-only",
			AllowAny: true,
		},
		"debian-direct": {
			ID:               "debian-direct",
			AllowedDistroIDs: []string{"debian"},
			AllowedVersionByDistro: map[string][]string{
				"debian": {"12"},
			},
			AllowedPackageManagers: []string{"apt"},
		},
		"ubuntu-beta": {
			ID:               "ubuntu-beta",
			AllowedDistroIDs: []string{"ubuntu"},
			AllowedVersionByDistro: map[string][]string{
				"ubuntu": {"22.04", "24.04"},
			},
			AllowedPackageManagers: []string{"apt"},
		},
		"rocky-beta": {
			ID:               "rocky-beta",
			AllowedDistroIDs: []string{"rocky"},
			AllowedVersionByDistro: map[string][]string{
				"rocky": {"8", "9"},
			},
			AllowedPackageManagers: []string{"dnf", "yum"},
		},
		"rhel-beta": {
			ID:               "rhel-beta",
			AllowedDistroIDs: []string{"rhel"},
			AllowedVersionByDistro: map[string][]string{
				"rhel": {"8", "9"},
			},
			AllowedPackageManagers: []string{"dnf", "yum"},
		},
	}
}

func containsString(values []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == target {
			return true
		}
	}
	return false
}

func hasAnyPrefix(value string, prefixes []string) bool {
	value = strings.TrimSpace(value)
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func emptyIfUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

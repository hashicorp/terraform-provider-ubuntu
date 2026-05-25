// Copyright IBM Corp. 2026

package serving

import "strings"

func FormatProviderVersion(version, commit string) string {
	version = strings.TrimSpace(version)
	commit = strings.TrimSpace(commit)
	if version == "" {
		version = "dev"
	}
	if commit == "" || commit == "unknown" {
		return version
	}
	if version != "dev" {
		return version
	}
	return version + "+" + commit
}

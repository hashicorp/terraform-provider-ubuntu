// Copyright IBM Corp. 2026

package hostsfile

import (
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
)

func Parse(content []byte) ([]pluginsdk.HostEntry, error) {
	var entries []pluginsdk.HostEntry
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			entries = append(entries, pluginsdk.HostEntry{IsBlank: true, Raw: line})
			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			entries = append(entries, pluginsdk.HostEntry{Comment: trimmed, Raw: line})
			continue
		}

		var comment string
		if idx := strings.IndexByte(trimmed, '#'); idx >= 0 {
			comment = strings.TrimSpace(trimmed[idx:])
			trimmed = strings.TrimSpace(trimmed[:idx])
		}

		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			entries = append(entries, pluginsdk.HostEntry{Raw: line})
			continue
		}

		entry := pluginsdk.HostEntry{
			IP:       fields[0],
			Hostname: fields[1],
			Comment:  comment,
		}
		if len(fields) > 2 {
			entry.Aliases = fields[2:]
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

func Serialize(entries []pluginsdk.HostEntry) ([]byte, error) {
	var builder strings.Builder
	for i, entry := range entries {
		if i > 0 {
			builder.WriteByte('\n')
		}
		if entry.IsBlank {
			if entry.Raw != "" {
				builder.WriteString(entry.Raw)
			}
			continue
		}
		if entry.IP == "" && entry.Raw != "" {
			builder.WriteString(entry.Raw)
			continue
		}
		if entry.IP == "" && entry.Comment != "" {
			builder.WriteString(entry.Comment)
			continue
		}

		builder.WriteString(entry.IP)
		builder.WriteByte('\t')
		builder.WriteString(entry.Hostname)
		for _, alias := range entry.Aliases {
			builder.WriteByte(' ')
			builder.WriteString(alias)
		}
		if entry.Comment != "" {
			builder.WriteByte(' ')
			builder.WriteString(entry.Comment)
		}
	}

	return []byte(builder.String()), nil
}

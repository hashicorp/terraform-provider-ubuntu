// Copyright IBM Corp. 2026

package crontab

import (
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
)

func Parse(content []byte) ([]pluginsdk.CrontabLine, error) {
	var lines []pluginsdk.CrontabLine
	for _, line := range splitConfigLines(string(content)) {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			lines = append(lines, pluginsdk.CrontabLine{IsBlank: true, Raw: line})
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			lines = append(lines, pluginsdk.CrontabLine{IsComment: true, Comment: trimmed, Raw: line})
			continue
		}
		if isEnvAssignment(trimmed) {
			lines = append(lines, pluginsdk.CrontabLine{IsEnv: true, Raw: line})
			continue
		}
		if strings.HasPrefix(trimmed, "@") {
			fields := strings.Fields(trimmed)
			if len(fields) < 2 {
				lines = append(lines, pluginsdk.CrontabLine{Raw: line})
				continue
			}
			lines = append(lines, pluginsdk.CrontabLine{
				Special: strings.ToLower(fields[0]),
				Command: strings.TrimSpace(trimmed[len(fields[0]):]),
			})
			continue
		}

		fields, command, ok := splitSchedule(trimmed)
		if !ok {
			lines = append(lines, pluginsdk.CrontabLine{Raw: line})
			continue
		}
		lines = append(lines, pluginsdk.CrontabLine{
			Minute:     fields[0],
			Hour:       fields[1],
			DayOfMonth: fields[2],
			Month:      fields[3],
			DayOfWeek:  fields[4],
			Command:    command,
		})
	}

	return lines, nil
}

func Serialize(lines []pluginsdk.CrontabLine) ([]byte, error) {
	if len(lines) == 0 {
		return nil, nil
	}

	var builder strings.Builder
	for i, line := range lines {
		if i > 0 {
			builder.WriteByte('\n')
		}

		switch {
		case line.IsBlank:
			builder.WriteString(line.Raw)
		case line.IsComment:
			if line.Raw != "" {
				builder.WriteString(line.Raw)
				continue
			}
			comment := strings.TrimSpace(line.Comment)
			if comment == "" {
				comment = "#"
			}
			if !strings.HasPrefix(comment, "#") {
				comment = "# " + comment
			}
			builder.WriteString(comment)
		case line.IsEnv || line.Raw != "":
			builder.WriteString(line.Raw)
		case line.Special != "":
			builder.WriteString(strings.ToLower(strings.TrimSpace(line.Special)))
			builder.WriteByte(' ')
			builder.WriteString(strings.TrimSpace(line.Command))
		default:
			builder.WriteString(strings.TrimSpace(line.Minute))
			builder.WriteByte(' ')
			builder.WriteString(strings.TrimSpace(line.Hour))
			builder.WriteByte(' ')
			builder.WriteString(strings.TrimSpace(line.DayOfMonth))
			builder.WriteByte(' ')
			builder.WriteString(strings.TrimSpace(line.Month))
			builder.WriteByte(' ')
			builder.WriteString(strings.TrimSpace(line.DayOfWeek))
			builder.WriteByte(' ')
			builder.WriteString(strings.TrimSpace(line.Command))
		}
	}
	builder.WriteByte('\n')

	return []byte(builder.String()), nil
}

func splitSchedule(line string) ([5]string, string, bool) {
	var fields [5]string
	pos := 0
	for i := range fields {
		for pos < len(line) && isSpace(line[pos]) {
			pos++
		}
		if pos >= len(line) {
			return fields, "", false
		}
		start := pos
		for pos < len(line) && !isSpace(line[pos]) {
			pos++
		}
		fields[i] = line[start:pos]
	}
	for pos < len(line) && isSpace(line[pos]) {
		pos++
	}
	if pos >= len(line) {
		return fields, "", false
	}
	command := strings.TrimSpace(line[pos:])
	if command == "" {
		return fields, "", false
	}
	return fields, command, true
}

func isEnvAssignment(line string) bool {
	idx := strings.IndexByte(line, '=')
	if idx <= 0 {
		return false
	}
	name := strings.TrimSpace(line[:idx])
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
			continue
		}
		if i > 0 && ch >= '0' && ch <= '9' {
			continue
		}
		return false
	}
	return true
}

func isSpace(ch byte) bool {
	return ch == ' ' || ch == '\t'
}

func splitConfigLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

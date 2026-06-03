// Copyright IBM Corp. 2026

package pluginsdk

import "strings"

type SSHBinding struct {
	LocalAddress string `json:"local_address,omitempty"`
	Port         string `json:"port"`
	Protocol     string `json:"protocol"`
}

func DiscoverActiveSSHBindings() []SSHBinding {
	bindings := sshBindingsFromEstablishedSessions()
	if len(bindings) == 0 {
		bindings = sshBindingsFromSSHDConfig()
	}
	if len(bindings) == 0 {
		bindings = []SSHBinding{{Port: "22", Protocol: "tcp"}}
	}
	return dedupeSSHBindings(bindings)
}

func sshBindingsFromEstablishedSessions() []SSHBinding {
	exists, err := CmdExists("ss")
	if err != nil || !exists {
		return nil
	}

	result, err := CmdExec("ss", []string{"-H", "-tnp"})
	if err != nil || result.ExitCode != 0 {
		return nil
	}

	bindings := make([]SSHBinding, 0)
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "sshd") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "ESTAB" {
			continue
		}
		localAddress, port, ok := splitNetworkEndpoint(fields[3])
		if !ok {
			continue
		}
		bindings = append(bindings, SSHBinding{LocalAddress: localAddress, Port: port, Protocol: "tcp"})
	}

	return bindings
}

func sshBindingsFromSSHDConfig() []SSHBinding {
	exists, err := CmdExists("sshd")
	if err != nil || !exists {
		return nil
	}

	result, err := CmdExec("sshd", []string{"-T"})
	if err != nil || result.ExitCode != 0 {
		return nil
	}

	bindings := make([]SSHBinding, 0)
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 || fields[0] != "port" {
			continue
		}
		bindings = append(bindings, SSHBinding{Port: fields[1], Protocol: "tcp"})
	}

	return bindings
}

func splitNetworkEndpoint(raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	if strings.HasPrefix(raw, "[") {
		end := strings.LastIndex(raw, "]")
		if end < 0 || end+2 > len(raw) || raw[end+1] != ':' {
			return "", "", false
		}
		return raw[1:end], raw[end+2:], true
	}
	index := strings.LastIndex(raw, ":")
	if index <= 0 || index == len(raw)-1 {
		return "", "", false
	}
	return strings.Trim(raw[:index], "[]"), raw[index+1:], true
}

func dedupeSSHBindings(bindings []SSHBinding) []SSHBinding {
	seen := make(map[string]struct{}, len(bindings))
	result := make([]SSHBinding, 0, len(bindings))
	for _, binding := range bindings {
		key := binding.LocalAddress + "|" + binding.Port + "|" + binding.Protocol
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, binding)
	}
	return result
}

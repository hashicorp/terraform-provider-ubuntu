package hostsession

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

type rebootPlatform interface {
	SelectRebootCommand(ctx context.Context, manager *ExecutorManager, session *transport.Session) (string, error)
	ReadHostProof(ctx context.Context, manager *ExecutorManager, session *transport.Session) (hostProof, error)
	ProbeReady(ctx context.Context, manager *ExecutorManager, session *transport.Session) error
	Cleanup(ctx context.Context, session *transport.Session, operationID string) error
}

type genericLinuxRebootPlatform struct{}

type hostProof struct {
	MachineID string
	BootID    string
	Hostname  string
	Distro    string
	Arch      string
}

func rebootPlatformForSession(_ *transport.Session) rebootPlatform {
	return genericLinuxRebootPlatform{}
}

func (genericLinuxRebootPlatform) SelectRebootCommand(ctx context.Context, manager *ExecutorManager, session *transport.Session) (string, error) {
	hasSystemctl, err := executorHasCommand(ctx, manager, session, "systemctl")
	if err != nil {
		return "", err
	}
	if hasSystemctl {
		return "systemctl reboot", nil
	}

	hasShutdown, err := executorHasCommand(ctx, manager, session, "shutdown")
	if err != nil {
		return "", err
	}
	if hasShutdown {
		return "shutdown -r now", nil
	}

	hasReboot, err := executorHasCommand(ctx, manager, session, "reboot")
	if err != nil {
		return "", err
	}
	if hasReboot {
		return "reboot", nil
	}

	return "", fmt.Errorf("no supported reboot command found on host")
}

func (genericLinuxRebootPlatform) ReadHostProof(ctx context.Context, manager *ExecutorManager, session *transport.Session) (hostProof, error) {
	proof := hostProof{}
	if profile, ok := manager.getHostProfile(sessionKey(session)); ok {
		proof.Hostname = strings.TrimSpace(profile.Hostname)
		proof.Distro = strings.TrimSpace(profile.DistroID)
		proof.Arch = strings.TrimSpace(profile.Arch)
	}

	proof.MachineID = bestEffortExecutorCommand(ctx, manager, session, "cat", "/etc/machine-id")
	proof.BootID = bestEffortExecutorCommand(ctx, manager, session, "cat", "/proc/sys/kernel/random/boot_id")
	if proof.Hostname == "" {
		proof.Hostname = bestEffortExecutorCommand(ctx, manager, session, "hostname")
	}
	if proof.Distro == "" {
		proof.Distro = parseOSReleaseID(bestEffortExecutorCommand(ctx, manager, session, "cat", "/etc/os-release"))
	}
	if proof.Arch == "" {
		proof.Arch = bestEffortExecutorCommand(ctx, manager, session, "uname", "-m")
	}

	if strings.TrimSpace(proof.MachineID) == "" || strings.TrimSpace(proof.BootID) == "" {
		return hostProof{}, fmt.Errorf("incomplete host proof: machine-id or boot_id missing")
	}

	return proof, nil
}

func (genericLinuxRebootPlatform) ProbeReady(ctx context.Context, manager *ExecutorManager, session *transport.Session) error {
	if _, err := runExecutorCommand(ctx, manager, session, "test", "-r", "/proc/sys/kernel/random/boot_id"); err != nil {
		return fmt.Errorf("post-reboot readiness probe failed: %w", err)
	}
	if _, err := runExecutorCommand(ctx, manager, session, "test", "-r", "/etc/machine-id"); err != nil {
		return fmt.Errorf("post-reboot readiness probe failed: %w", err)
	}
	return nil
}

func (genericLinuxRebootPlatform) Cleanup(_ context.Context, _ *transport.Session, _ string) error {
	return nil
}

func (p hostProof) StableID() string {
	return fmt.Sprintf("machine-id:%s|hostname:%s|distro:%s|arch:%s", p.MachineID, p.Hostname, p.Distro, p.Arch)
}

func executorHasCommand(ctx context.Context, manager *ExecutorManager, session *transport.Session, name string) (bool, error) {
	if profile, ok := manager.getHostProfile(sessionKey(session)); ok {
		for _, candidate := range profile.AvailableCmds {
			if candidate == name {
				return true, nil
			}
		}
		return false, nil
	}

	probeArgs := []string{"--help"}
	if name == "systemctl" {
		probeArgs = []string{"--version"}
	}
	result, err := runExecutorCommandResult(ctx, manager, session, false, name, probeArgs...)
	if err != nil {
		return false, err
	}
	return result.ExitCode != -1, nil
}

func parseOSReleaseID(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ID=") {
			continue
		}
		return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "ID=")), `"'`)
	}
	return ""
}

func bestEffortExecutorCommand(ctx context.Context, manager *ExecutorManager, session *transport.Session, name string, args ...string) string {
	output, err := runExecutorCommand(ctx, manager, session, name, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

func runExecutorCommand(ctx context.Context, manager *ExecutorManager, session *transport.Session, name string, args ...string) (string, error) {
	result, err := runExecutorCommandResult(ctx, manager, session, true, name, args...)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
		if detail == "" {
			detail = fmt.Sprintf("exit status %d", result.ExitCode)
		} else {
			detail = fmt.Sprintf("exit status %d: %s", result.ExitCode, detail)
		}
		return result.Stdout, errors.New(detail)
	}
	return result.Stdout, nil
}

func runExecutorCommandResult(ctx context.Context, manager *ExecutorManager, session *transport.Session, become bool, name string, args ...string) (*hostrpc.CommandResult, error) {
	params := hostrpc.HostCommandParams{
		Name: name,
		Args: args,
	}
	if become {
		params.Execution = &hostrpc.ExecutionContext{Become: true}
	}
	return manager.HostCommand(ctx, session, params)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `"'"'`) + "'"
}

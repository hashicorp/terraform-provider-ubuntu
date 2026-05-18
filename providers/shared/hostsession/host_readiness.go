package hostsession

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

const (
	hostReadinessTimeout      = 12 * time.Minute
	hostReadinessPollDelay    = 5 * time.Second
	hostReadinessPollAttempts = 120
)

func (m *ExecutorManager) EnsureHostReady(ctx context.Context, session *transport.Session, needRoot bool) error {
	if session == nil {
		return fmt.Errorf("nil session")
	}

	runtime := m.sessionRuntime(session)
	leader, waitErr, finish := runtime.beginReadinessCheck(ctx, needRoot)
	if !leader {
		return waitErr
	}

	err := m.ensureHostReady(ctx, session, needRoot)
	finish(err, needRoot)
	return err
}

func (m *ExecutorManager) ensureHostReady(ctx context.Context, session *transport.Session, needRoot bool) error {
	ctx, cancel := context.WithTimeout(ctx, hostReadinessTimeout)
	defer cancel()

	scope := "mutation"
	if needRoot {
		scope = "privileged mutation"
		result, err := m.HostCommand(ctx, session, hostrpc.HostCommandParams{
			Name:      "true",
			Execution: &hostrpc.ExecutionContext{Become: true},
		})
		if err != nil {
			return fmt.Errorf("host %s readiness check failed before %s: %w", session.Config.Endpoint(), scope, err)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("host %s is not ready for %s: sudo -n true failed: %s", session.Config.Endpoint(), scope, commandResultDetail(result))
		}
	}

	cloudInitAvailable, err := executorHasCommand(ctx, m, session, "cloud-init")
	if err != nil {
		return fmt.Errorf("host %s readiness check failed before %s: %w", session.Config.Endpoint(), scope, err)
	}
	if cloudInitAvailable {
		result, err := runReadinessCommand(ctx, m, session, needRoot, "cloud-init", "status", "--wait")
		if err != nil {
			return fmt.Errorf("host %s readiness check failed before %s: %w", session.Config.Endpoint(), scope, err)
		}
		if result.ExitCode != 0 && !cloudInitReportedDone(result) {
			return fmt.Errorf("host %s is not ready for %s: %s", session.Config.Endpoint(), scope, commandResultDetail(result))
		}
		ok, testErr := readinessTest(ctx, m, session, needRoot, "-e", "/var/lib/cloud/instance/boot-finished")
		if testErr != nil {
			return fmt.Errorf("host %s readiness check failed before %s: %w", session.Config.Endpoint(), scope, testErr)
		}
		if !ok {
			return fmt.Errorf("host %s is not ready for %s: cloud-init boot-finished marker missing at /var/lib/cloud/instance/boot-finished", session.Config.Endpoint(), scope)
		}
	}

	systemdAvailable, err := readinessSystemdAvailable(ctx, m, session, needRoot)
	if err != nil {
		return fmt.Errorf("host %s readiness check failed before %s: %w", session.Config.Endpoint(), scope, err)
	}
	if systemdAvailable {
		result, err := runReadinessCommand(ctx, m, session, needRoot, "systemctl", "is-system-running", "--wait")
		if err != nil {
			return fmt.Errorf("host %s readiness check failed before %s: %w", session.Config.Endpoint(), scope, err)
		}
		state := readinessCommandState(result)
		if state != "running" && state != "degraded" {
			if state == "" {
				state = "unknown"
			}
			return fmt.Errorf("host %s is not ready for %s: systemd is not ready: %s", session.Config.Endpoint(), scope, state)
		}
	}

	aptAvailable, err := executorHasCommand(ctx, m, session, "apt-get")
	if err != nil {
		return fmt.Errorf("host %s readiness check failed before %s: %w", session.Config.Endpoint(), scope, err)
	}
	if aptAvailable {
		if err := waitForAptReadiness(ctx, m, session, needRoot, systemdAvailable); err != nil {
			return fmt.Errorf("host %s is not ready for %s: %w", session.Config.Endpoint(), scope, err)
		}
	}

	return nil
}

func runReadinessCommand(ctx context.Context, m *ExecutorManager, session *transport.Session, needRoot bool, name string, args ...string) (*hostrpc.CommandResult, error) {
	params := hostrpc.HostCommandParams{Name: name, Args: args}
	if needRoot {
		params.Execution = &hostrpc.ExecutionContext{Become: true}
	}
	return m.HostCommand(ctx, session, params)
}

func readinessTest(ctx context.Context, m *ExecutorManager, session *transport.Session, needRoot bool, testFlag string, path string) (bool, error) {
	result, err := runReadinessCommand(ctx, m, session, needRoot, "test", testFlag, path)
	if err != nil {
		return false, err
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("test %s %s failed: %s", testFlag, path, commandResultDetail(result))
	}
}

func readinessSystemdAvailable(ctx context.Context, m *ExecutorManager, session *transport.Session, needRoot bool) (bool, error) {
	hasSystemctl, err := executorHasCommand(ctx, m, session, "systemctl")
	if err != nil || !hasSystemctl {
		return false, err
	}
	return readinessTest(ctx, m, session, needRoot, "-d", "/run/systemd/system")
}

func waitForAptReadiness(ctx context.Context, m *ExecutorManager, session *transport.Session, needRoot bool, systemdAvailable bool) error {
	for attempt := 0; attempt < hostReadinessPollAttempts; attempt++ {
		blocker, err := aptReadinessBlocker(ctx, m, session, needRoot, systemdAvailable)
		if err != nil {
			return err
		}
		if blocker == "" {
			return nil
		}
		if attempt == hostReadinessPollAttempts-1 {
			return fmt.Errorf("apt is not ready after 10m: %s", blocker)
		}
		if err := sleepWithContext(ctx, hostReadinessPollDelay); err != nil {
			return err
		}
	}
	return nil
}

func aptReadinessBlocker(ctx context.Context, m *ExecutorManager, session *transport.Session, needRoot bool, systemdAvailable bool) (string, error) {
	if systemdAvailable {
		for _, service := range []string{"apt-daily.service", "apt-daily-upgrade.service"} {
			result, err := runReadinessCommand(ctx, m, session, needRoot, "systemctl", "is-active", service)
			if err != nil {
				return "", err
			}
			state := readinessCommandState(result)
			switch state {
			case "active", "activating", "reloading":
				return fmt.Sprintf("service %s is %s", service, state), nil
			}
		}
	}

	result, err := runReadinessCommand(ctx, m, session, needRoot, "ps", "-eo", "comm=")
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("ps -eo comm= failed: %s", commandResultDetail(result))
	}
	if aptProcessRunning(result.Stdout) {
		return "package manager process is active", nil
	}
	return "", nil
}

func readinessCommandState(result *hostrpc.CommandResult) string {
	if result == nil {
		return ""
	}
	text := strings.TrimSpace(result.Stdout)
	if text == "" {
		text = strings.TrimSpace(result.Stderr)
	}
	text = strings.ReplaceAll(text, "\r", "")
	state := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			state = line
		}
	}
	return strings.ToLower(state)
}

func aptProcessRunning(stdout string) bool {
	for _, line := range strings.Split(stdout, "\n") {
		switch strings.TrimSpace(line) {
		case "apt", "apt-get", "dpkg", "unattended-upgrade":
			return true
		}
	}
	return false
}

func cloudInitReportedDone(result *hostrpc.CommandResult) bool {
	if result == nil {
		return false
	}

	status := strings.ToLower(strings.TrimSpace(result.Stderr))
	if status == "" {
		status = strings.ToLower(strings.TrimSpace(result.Stdout))
	}

	return strings.Contains(status, "status: done")
}

func commandResultDetail(result *hostrpc.CommandResult) string {
	if result == nil {
		return "unknown command failure"
	}

	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	if detail == "" {
		return fmt.Sprintf("exit status %d", result.ExitCode)
	}
	return fmt.Sprintf("exit status %d: %s", result.ExitCode, detail)
}

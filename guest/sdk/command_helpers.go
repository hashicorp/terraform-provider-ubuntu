// Copyright IBM Corp. 2026

package pluginsdk

import (
	"fmt"
	"strings"
	"time"
)

type CommandRetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	Sleep       func(time.Duration)
	IsTransient func(detail string) bool
	OnRetry     func(delay time.Duration, detail string)
}

func RetryCommand(description, cmd string, args []string, policy CommandRetryPolicy) error {
	attempts := policy.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	sleep := policy.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		result, err := CmdExec(cmd, args)
		if err != nil {
			return fmt.Errorf("%s: %w", description, err)
		}
		if result.ExitCode == 0 {
			return nil
		}

		detail := CommandFailureDetail(result)
		lastErr = fmt.Errorf("%s failed (%s)", description, detail)
		if policy.IsTransient == nil || !policy.IsTransient(detail) || attempt == attempts-1 {
			return lastErr
		}

		delay := ExponentialBackoff(policy.BaseDelay, attempt)
		if policy.OnRetry != nil {
			policy.OnRetry(delay, detail)
		}
		sleep(delay)
	}

	return lastErr
}

func ExponentialBackoff(baseDelay time.Duration, attempt int) time.Duration {
	if baseDelay <= 0 {
		return 0
	}
	delay := baseDelay
	for i := 0; i < attempt; i++ {
		delay *= 2
	}
	return delay
}

func IsTransientAptBusy(detail string) bool {
	text := strings.ToLower(strings.TrimSpace(detail))
	return strings.Contains(text, "could not get lock /var/lib/dpkg/lock") ||
		strings.Contains(text, "could not get lock /var/lib/apt/lists/lock") ||
		strings.Contains(text, "unable to acquire the dpkg frontend lock") ||
		strings.Contains(text, "unable to lock directory /var/lib/apt/lists/") ||
		strings.Contains(text, "waiting for cache lock") ||
		strings.Contains(text, "is another process using it")
}

func CommandFailureDetail(result *CmdResult) string {
	if result == nil {
		return "unknown command failure"
	}
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	if detail == "" {
		return fmt.Sprintf("exit %d", result.ExitCode)
	}
	return fmt.Sprintf("exit %d: %s", result.ExitCode, detail)
}

func CommandOutputDetail(result *CmdResult) string {
	if result == nil {
		return "no result"
	}
	parts := make([]string, 0, 2)
	if stdout := strings.TrimSpace(result.Stdout); stdout != "" {
		parts = append(parts, "stdout: "+stdout)
	}
	if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
		parts = append(parts, "stderr: "+stderr)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("exit %d", result.ExitCode)
	}
	return strings.Join(parts, "; ")
}

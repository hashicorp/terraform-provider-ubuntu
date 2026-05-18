package engine

import (
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/hostsession"
)

type LockPlanner func(action string, op *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error)

func DefaultLockSet(action string) []hostsession.LockDescriptor {
	mode := LockModeForAction(action)
	source := "default host lock"
	if mode == hostsession.LockModeShared {
		source = "default host read lock"
	}

	return []hostsession.LockDescriptor{{
		Key:    "host",
		Mode:   mode,
		Source: source,
	}}
}

func LockModeForAction(action string) hostsession.LockMode {
	switch action {
	case "validate", "read", "import", "data_read":
		return hostsession.LockModeShared
	default:
		return hostsession.LockModeExclusive
	}
}

func NormalizeLockSet(action string, locks []hostsession.LockDescriptor) []hostsession.LockDescriptor {
	if len(locks) == 0 {
		return DefaultLockSet(action)
	}

	normalized := make([]hostsession.LockDescriptor, 0, len(locks))
	seen := make(map[string]struct{}, len(locks))
	for _, lock := range locks {
		if strings.TrimSpace(lock.Key) == "" {
			continue
		}
		if lock.Mode == "" {
			lock.Mode = LockModeForAction(action)
		}
		key := lock.Key + "|" + string(lock.Mode)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, lock)
	}

	if len(normalized) == 0 {
		return DefaultLockSet(action)
	}

	return normalized
}

func StringAttr(op *hostsession.OperationMessage, keys ...string) string {
	if op == nil {
		return ""
	}

	for _, attrs := range []map[string]interface{}{op.Plan, op.State, op.Config} {
		for _, key := range keys {
			if attrs == nil {
				continue
			}
			value, ok := attrs[key]
			if !ok {
				continue
			}
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}

	return ""
}

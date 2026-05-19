package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

const (
	envLogPath          = "TF_LINUX_EXECUTOR_LOG_PATH"
	defaultLogFileStem  = "tf-linux-executor"
	defaultPreviewLimit = 160
	maxSummaryDepth     = 3
	maxSummaryItems     = 8
)

func DefaultPath() string {
	if path := strings.TrimSpace(os.Getenv(envLogPath)); path != "" {
		return path
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d.log", defaultLogFileStem, os.Geteuid()))
}

func Configure() (string, io.Closer, error) {
	path := DefaultPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", nil, fmt.Errorf("create executor log directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", nil, fmt.Errorf("open executor log file: %w", err)
	}

	log.SetOutput(io.MultiWriter(os.Stderr, file))
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.LUTC)
	log.SetPrefix(fmt.Sprintf("executor[%d] ", os.Getpid()))
	return path, file, nil
}

func Preview(text string, max int) string {
	if max <= 0 {
		max = defaultPreviewLimit
	}
	if text == "" {
		return `""`
	}

	clean := strings.ReplaceAll(text, "\r", `\r`)
	clean = strings.ReplaceAll(clean, "\n", `\n`)
	if len(clean) > max {
		clean = clean[:max] + "..."
	}
	return strconv.Quote(clean)
}

func ShortDigest(text string) string {
	digest := digestutil.MustDigestBytes(digestutil.AlgorithmXXH3_128, []byte(text))
	algorithm, encoded, _ := strings.Cut(digest, ":")
	if len(encoded) > 12 {
		encoded = encoded[:12]
	}
	return algorithm + ":" + encoded
}

func SummarizeExecution(execution *hostrpc.ExecutionContext) string {
	if execution == nil || !execution.Become {
		return "none"
	}
	if execution.BecomeUser == "" || execution.BecomeUser == "root" {
		return "sudo(root)"
	}
	return fmt.Sprintf("sudo(%s)", execution.BecomeUser)
}

func SummarizeArgs(args []string) string {
	if len(args) == 0 {
		return "[]"
	}

	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, summarizeString("", arg, false))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func SummarizeJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "<empty>"
	}

	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Sprintf("<invalid-json len=%d digest=%s preview=%s>", len(raw), ShortDigest(string(raw)), Preview(string(raw), defaultPreviewLimit))
	}
	return summarizeValue("", value, 0)
}

func summarizeValue(key string, value interface{}, depth int) string {
	if depth >= maxSummaryDepth {
		return "<depth-limit>"
	}

	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return summarizeString(key, typed, true)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64, int, int64, uint64:
		return fmt.Sprintf("%v", typed)
	case json.Number:
		return typed.String()
	case []interface{}:
		if len(typed) == 0 {
			return "[]"
		}
		parts := make([]string, 0, min(len(typed), maxSummaryItems))
		for idx, item := range typed {
			if idx >= maxSummaryItems {
				parts = append(parts, fmt.Sprintf("...+%d", len(typed)-idx))
				break
			}
			parts = append(parts, summarizeValue(key, item, depth+1))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]interface{}:
		return summarizeMap(key, typed, depth+1)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func summarizeMap(parentKey string, values map[string]interface{}, depth int) string {
	if len(values) == 0 {
		return "{}"
	}

	if isEnvironmentKey(parentKey) {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > maxSummaryItems {
			keys = append(keys[:maxSummaryItems], fmt.Sprintf("...+%d", len(values)-maxSummaryItems))
		}
		return fmt.Sprintf("{keys=%v}", keys)
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, min(len(keys), maxSummaryItems))
	for idx, key := range keys {
		if idx >= maxSummaryItems {
			parts = append(parts, fmt.Sprintf("...+%d", len(keys)-idx))
			break
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, summarizeValue(key, values[key], depth)))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func summarizeString(key, value string, allowPreview bool) string {
	switch {
	case isEnvironmentKey(key):
		return fmt.Sprintf("<env len=%d digest=%s>", len(value), ShortDigest(value))
	case isShellLikeKey(key):
		return fmt.Sprintf("<shell len=%d digest=%s>", len(value), ShortDigest(value))
	case isSensitiveKey(key):
		return fmt.Sprintf("<redacted len=%d digest=%s>", len(value), ShortDigest(value))
	case !allowPreview:
		return fmt.Sprintf("<text len=%d digest=%s>", len(value), ShortDigest(value))
	case strings.Contains(value, "\n") || len(value) > defaultPreviewLimit:
		return fmt.Sprintf("<text len=%d digest=%s preview=%s>", len(value), ShortDigest(value), Preview(value, defaultPreviewLimit))
	default:
		return Preview(value, defaultPreviewLimit)
	}
}

func isShellLikeKey(key string) bool {
	key = normalizeKey(key)
	switch key {
	case "command", "delete_command", "stdout", "stderr":
		return true
	default:
		return false
	}
}

func isEnvironmentKey(key string) bool {
	return normalizeKey(key) == "environment"
}

func isSensitiveKey(key string) bool {
	key = normalizeKey(key)
	if key == "" {
		return false
	}
	if strings.Contains(key, "token") || strings.Contains(key, "password") || strings.Contains(key, "secret") {
		return true
	}
	if strings.Contains(key, "private_key") || strings.Contains(key, "client_key") {
		return true
	}
	if strings.Contains(key, "certificate") || strings.Contains(key, "kubeconfig") {
		return true
	}
	switch key {
	case "content", "content_base64", "config_yaml":
		return true
	default:
		return false
	}
}

func normalizeKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Copyright IBM Corp. 2026

package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPathUsesUIDScopedFileName(t *testing.T) {
	t.Setenv(envLogPath, "")

	want := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d.log", defaultLogFileStem, os.Geteuid()))
	if got := DefaultPath(); got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestDefaultPathHonorsOverride(t *testing.T) {
	t.Setenv(envLogPath, "/var/tmp/custom-executor.log")

	if got := DefaultPath(); got != "/var/tmp/custom-executor.log" {
		t.Fatalf("DefaultPath() = %q", got)
	}
}

func TestSummarizeJSONRedactsSensitiveFields(t *testing.T) {
	raw := json.RawMessage(`{
		"name": "disable_swap",
		"creates": "/var/lib/tf-linux/swap-disabled",
		"command": "swapoff -a && touch /var/lib/tf-linux/swap-disabled",
		"content": "very-secret-content",
		"environment": {"AWS_SECRET_ACCESS_KEY": "shh"},
		"token": "abc123"
	}`)

	got := SummarizeJSON(raw)

	for _, forbidden := range []string{
		"very-secret-content",
		"AWS_SECRET_ACCESS_KEY\":\"shh",
		"abc123",
		"swapoff -a && touch",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("summary leaked sensitive content %q: %s", forbidden, got)
		}
	}

	for _, expected := range []string{
		`name="disable_swap"`,
		`creates="/var/lib/tf-linux/swap-disabled"`,
		`command=<shell len=`,
		`content=<redacted len=`,
		`environment={keys=[AWS_SECRET_ACCESS_KEY]}`,
		`token=<redacted len=`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("summary missing %q: %s", expected, got)
		}
	}
}

func TestSummarizeArgsHashesLongShellArg(t *testing.T) {
	got := SummarizeArgs([]string{"sh", "-lc", "line1\nline2\nline3"})

	if strings.Contains(got, "line1") {
		t.Fatalf("args summary leaked shell body: %s", got)
	}
	if !strings.Contains(got, "<text len=") {
		t.Fatalf("args summary did not summarize long arg: %s", got)
	}
}

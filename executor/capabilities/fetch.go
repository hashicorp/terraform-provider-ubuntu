package capabilities

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultFetchURLMaxBytes = 8 << 20
	defaultFetchURLTimeout  = 30 * time.Second
)

func FetchURL(ctx context.Context, rawURL string) ([]byte, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("fetch url must not be empty")
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse fetch url %s: %w", rawURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("fetch url %s must use http or https", rawURL)
	}

	if ctx == nil {
		ctx = context.Background()
	}

	client := &http.Client{Timeout: defaultFetchURLTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build fetch request %s: %w", rawURL, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, defaultFetchURLMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read fetch response %s: %w", rawURL, err)
	}
	if len(body) > defaultFetchURLMaxBytes {
		return nil, fmt.Errorf("fetch %s exceeds %d bytes", rawURL, defaultFetchURLMaxBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := strings.TrimSpace(string(bytes.TrimSpace(body)))
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		if preview == "" {
			return nil, fmt.Errorf("fetch %s returned HTTP %d", rawURL, resp.StatusCode)
		}
		return nil, fmt.Errorf("fetch %s returned HTTP %d: %s", rawURL, resp.StatusCode, preview)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, fmt.Errorf("fetch %s returned empty content", rawURL)
	}
	return body, nil
}

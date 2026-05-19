package capabilities

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchURLReturnsResponseBody(t *testing.T) {
	t.Parallel()

	expected := []byte("signed-by-data\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(expected)
	}))
	defer server.Close()

	body, err := FetchURL(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("FetchURL returned error: %v", err)
	}
	if !bytes.Equal(body, expected) {
		t.Fatalf("unexpected body: %q", string(body))
	}
}

func TestFetchURLRejectsUnsupportedScheme(t *testing.T) {
	t.Parallel()

	if _, err := FetchURL(context.Background(), "file:///tmp/keyring.gpg"); err == nil {
		t.Fatal("expected unsupported scheme error")
	}
}

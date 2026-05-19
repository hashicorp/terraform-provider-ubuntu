package runtime

import (
	"encoding/json"
	"testing"
)

func TestUnwrapPluginStateEnvelope(t *testing.T) {
	result, err := unwrapPluginState(json.RawMessage(`{"state":{"id":"/tmp/test","path":"/tmp/test"}}`))
	if err != nil {
		t.Fatalf("unwrapPluginState returned error: %v", err)
	}

	if string(result) != `{"id":"/tmp/test","path":"/tmp/test"}` {
		t.Fatalf("unexpected unwrapped state: %s", string(result))
	}
}

func TestUnwrapPluginStateBareObject(t *testing.T) {
	result, err := unwrapPluginState(json.RawMessage(`{"id":"/tmp/test","path":"/tmp/test"}`))
	if err != nil {
		t.Fatalf("unwrapPluginState returned error: %v", err)
	}

	if string(result) != `{"id":"/tmp/test","path":"/tmp/test"}` {
		t.Fatalf("unexpected bare state: %s", string(result))
	}
}

func TestUnwrapPluginStateEmptyEnvelopeMeansNilState(t *testing.T) {
	result, err := unwrapPluginState(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unwrapPluginState returned error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil state for empty envelope, got %s", string(result))
	}
}

func TestUnwrapPluginStateErrorEnvelope(t *testing.T) {
	_, err := unwrapPluginState(json.RawMessage(`{"error":"boom"}`))
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected boom error, got %v", err)
	}
}

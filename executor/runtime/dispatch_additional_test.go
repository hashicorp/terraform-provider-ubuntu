// Copyright IBM Corp. 2026

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/executor/capabilities"
	"github.com/hashicorp/terraform-provider-ubuntu/executor/runtime/plugincodec"
	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assetmanifest"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

type recordedDispatchCall struct {
	moduleName string
	action     string
	input      json.RawMessage
	execution  *hostrpc.ExecutionContext
}

func TestDispatcherOperationEntryPointsUseRuntimeCalls(t *testing.T) {
	oldDispatchRuntimeCall := dispatchRuntimeCall
	t.Cleanup(func() {
		dispatchRuntimeCall = oldDispatchRuntimeCall
	})

	var calls []recordedDispatchCall
	dispatchRuntimeCall = func(_ *WASMRuntime, ctx context.Context, moduleName, action string, input json.RawMessage) (json.RawMessage, error) {
		call := recordedDispatchCall{moduleName: moduleName, action: action, input: append(json.RawMessage(nil), input...)}
		if execution, ok := capabilities.ExecutionContextFromContext(ctx); ok {
			executionCopy := execution
			call.execution = &executionCopy
		}
		calls = append(calls, call)
		return json.RawMessage(fmt.Sprintf(`{"state":{"action":%q}}`, action)), nil
	}

	dispatcher := NewDispatcherWithManifest(&WASMRuntime{hostAPI: capabilities.NewHostAPI(capabilities.HostProfile{})}, assetmanifest.Manifest{}, nil)
	dispatcher.plugins["module"] = true
	dispatcher.plugins["resource"] = true

	execution := &hostrpc.ExecutionContext{Become: true, BecomeUser: "deploy"}
	state := json.RawMessage(`{"id":"demo"}`)
	plan := json.RawMessage(`{"name":"demo"}`)
	config := json.RawMessage(`{"enabled":true}`)

	readResult, err := dispatcher.ReadResource(context.Background(), hostrpc.ResourceReadParams{ModuleName: "module", ResourceType: "resource", State: state, Execution: execution})
	if err != nil {
		t.Fatalf("ReadResource() error = %v, want nil", err)
	}
	assertOperationState(t, readResult, `{"action":"read"}`)
	assertModuleDispatchCall(t, calls[len(calls)-1], "module", "read", "resource", "state", state)

	validateResult, err := dispatcher.ValidateResource(context.Background(), hostrpc.ResourceValidateParams{ResourceType: "resource", Config: config, Execution: execution})
	if err != nil {
		t.Fatalf("ValidateResource() error = %v, want nil", err)
	}
	assertOperationState(t, validateResult, `{"action":"validate"}`)
	assertDirectDispatchCall(t, calls[len(calls)-1], "resource", "validate", config)

	createResult, err := dispatcher.CreateResource(context.Background(), hostrpc.ResourceCreateParams{ModuleName: "module", ResourceType: "resource", Plan: plan, Execution: execution})
	if err != nil {
		t.Fatalf("CreateResource() error = %v, want nil", err)
	}
	assertOperationState(t, createResult, `{"action":"create"}`)
	assertModuleDispatchCall(t, calls[len(calls)-1], "module", "create", "resource", "plan", plan)

	updateResult, err := dispatcher.UpdateResource(context.Background(), hostrpc.ResourceUpdateParams{ModuleName: "module", ResourceType: "resource", State: state, Plan: plan, Execution: execution})
	if err != nil {
		t.Fatalf("UpdateResource() error = %v, want nil", err)
	}
	assertOperationState(t, updateResult, `{"action":"update"}`)
	assertModuleDispatchCall(t, calls[len(calls)-1], "module", "update", "resource", "state", state)
	assertModuleDispatchCall(t, calls[len(calls)-1], "module", "update", "resource", "plan", plan)

	deleteResult, err := dispatcher.DeleteResource(context.Background(), hostrpc.ResourceDeleteParams{ModuleName: "module", ResourceType: "resource", State: state, Execution: execution})
	if err != nil {
		t.Fatalf("DeleteResource() error = %v, want nil", err)
	}
	assertOperationState(t, deleteResult, `{"action":"delete"}`)
	assertModuleDispatchCall(t, calls[len(calls)-1], "module", "delete", "resource", "state", state)

	importResult, err := dispatcher.ImportResource(context.Background(), hostrpc.ResourceImportParams{ModuleName: "module", ResourceType: "resource", ImportID: "import-1", Execution: execution})
	if err != nil {
		t.Fatalf("ImportResource() error = %v, want nil", err)
	}
	assertOperationState(t, importResult, `{"action":"import"}`)
	assertModuleDispatchCall(t, calls[len(calls)-1], "module", "import", "resource", "import_id", json.RawMessage(`"import-1"`))

	dataResult, err := dispatcher.ReadDataSource(context.Background(), hostrpc.DataSourceReadParams{ModuleName: "module", ResourceType: "resource", Config: config, Execution: execution})
	if err != nil {
		t.Fatalf("ReadDataSource() error = %v, want nil", err)
	}
	assertOperationState(t, dataResult, `{"action":"data_read"}`)
	assertModuleDispatchCall(t, calls[len(calls)-1], "module", "data_read", "resource", "config", config)

	actionResult, err := dispatcher.InvokeAction(context.Background(), hostrpc.ActionInvokeParams{ModuleName: "module", ResourceType: "resource", Config: config, Execution: execution})
	if err != nil {
		t.Fatalf("InvokeAction() error = %v, want nil", err)
	}
	assertOperationState(t, actionResult, `{"action":"invoke"}`)
	assertModuleDispatchCall(t, calls[len(calls)-1], "module", "invoke", "resource", "config", config)

	if len(calls) != 8 {
		t.Fatalf("runtime call count = %d, want 8", len(calls))
	}
	for _, call := range calls {
		if call.execution == nil || !call.execution.Become || call.execution.BecomeUser != "deploy" {
			t.Fatalf("execution context = %#v, want become deploy", call.execution)
		}
	}
}

func TestDispatcherHelpersAndHostCommand(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join(root, "echo-script")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'host:%s' \"$1\"\n"), 0o755); err != nil {
		t.Fatalf("write host command script: %v", err)
	}

	dispatcher := NewDispatcherWithManifest(&WASMRuntime{hostAPI: capabilities.NewHostAPI(capabilities.HostProfile{})}, assetmanifest.Manifest{}, nil)

	commandResult, err := dispatcher.HostCommand(context.Background(), hostrpc.HostCommandParams{Name: scriptPath, Args: []string{"hello"}})
	if err != nil {
		t.Fatalf("HostCommand() error = %v, want nil", err)
	}
	if commandResult.ExitCode != 0 || strings.TrimSpace(commandResult.Stdout) != "host:hello" {
		t.Fatalf("HostCommand() = %#v, want exit 0 and host:hello", commandResult)
	}

	savedKey := snapshotRuntimeJournalKey()
	t.Cleanup(func() {
		restoreRuntimeJournalKey(savedKey)
	})
	key := bytes.Repeat([]byte{0x42}, 32)
	if err := dispatcher.ConfigureJournal(hostrpc.JournalConfigureParams{Key: key}); err != nil {
		t.Fatalf("ConfigureJournal() error = %v, want nil", err)
	}
	configuredKey, err := runtimeJournalKeyBytes()
	if err != nil {
		t.Fatalf("runtimeJournalKeyBytes() error = %v, want nil", err)
	}
	if !bytes.Equal(configuredKey, key) {
		t.Fatalf("runtime journal key = %v, want %v", configuredKey, key)
	}

	dispatcher.plugins["resource"] = true
	moduleName, err := dispatcher.loadedModule("", "resource")
	if err != nil || moduleName != "resource" {
		t.Fatalf("loadedModule() = (%q, %v), want (resource, nil)", moduleName, err)
	}
	if _, err := dispatcher.loadedModule("missing", "resource"); err == nil || !strings.Contains(err.Error(), `plugin "missing" not loaded`) {
		t.Fatalf("loadedModule(missing) error = %v, want not loaded", err)
	}

	if _, err := dispatcher.stateResult(nil, errors.New("boom")); err == nil || err.Error() != "boom" {
		t.Fatalf("stateResult(error) = %v, want boom", err)
	}
	stateResult, err := dispatcher.stateResult(json.RawMessage(`{"state":{"id":"demo"}}`), nil)
	if err != nil {
		t.Fatalf("stateResult(envelope) error = %v, want nil", err)
	}
	assertOperationState(t, stateResult, `{"id":"demo"}`)

	if got := string(marshalUpdatePayload(json.RawMessage(`{"state":1}`), json.RawMessage(`{"plan":2}`))); !strings.Contains(got, `"state":{"state":1}`) || !strings.Contains(got, `"plan":{"plan":2}`) {
		t.Fatalf("marshalUpdatePayload() = %s, want state and plan payload", got)
	}
	if got := string(marshalUpdatePayload(json.RawMessage(`{"state":1}`), json.RawMessage(`{invalid}`))); got != `{invalid}` {
		t.Fatalf("marshalUpdatePayload(invalid) = %q, want invalid plan fallback", got)
	}
	if !usesModuleDispatch("module", "resource") {
		t.Fatal("usesModuleDispatch() should be true when module and resource differ")
	}
	if usesModuleDispatch("resource", "resource") || usesModuleDispatch("", "resource") {
		t.Fatal("usesModuleDispatch() should be false when module is empty or matches resource")
	}
	if got := terraformProviderName("ubuntu"); got != "terraform_provider_ubuntu" {
		t.Fatalf("terraformProviderName(ubuntu) = %q", got)
	}
	if got := terraformProviderName("test-provider"); got != "terraform_provider_test_provider" {
		t.Fatalf("terraformProviderName(test-provider) = %q", got)
	}
	modulePayload := marshalModulePayload("terraform_provider_ubuntu", "resource", "invoke", json.RawMessage(`{"plan":1}`), json.RawMessage(`{"state":2}`), json.RawMessage(`{"config":3}`), "import-1")
	assertModuleDispatchJSON(t, modulePayload, "resource", "invoke", "provider_name", json.RawMessage(`"terraform_provider_ubuntu"`))
	assertModuleDispatchJSON(t, modulePayload, "resource", "invoke", "plan", json.RawMessage(`{"plan":1}`))
	assertModuleDispatchJSON(t, modulePayload, "resource", "invoke", "state", json.RawMessage(`{"state":2}`))
	assertModuleDispatchJSON(t, modulePayload, "resource", "invoke", "config", json.RawMessage(`{"config":3}`))
	assertModuleDispatchJSON(t, modulePayload, "resource", "invoke", "import_id", json.RawMessage(`"import-1"`))
	if got := string(marshalModulePayload("", "resource", "read", nil, nil, json.RawMessage(`{invalid}`), "")); got != `{invalid}` {
		t.Fatalf("marshalModulePayload(invalid config) = %q, want config fallback", got)
	}
	if got := string(mustMarshalString("demo")); got != `"demo"` {
		t.Fatalf("mustMarshalString() = %q, want \"demo\"", got)
	}

	dispatcher.traceFinishOperation(0, "resource.read", time.Time{}, "", nil)
	if traceID := dispatcher.nextTraceID(); traceID == 0 {
		t.Fatal("nextTraceID() should increment from zero")
	}
	if _, err := dispatcher.ReadResource(context.Background(), hostrpc.ResourceReadParams{ResourceType: "missing"}); err == nil || !strings.Contains(err.Error(), `plugin "missing" not loaded`) {
		t.Fatalf("ReadResource(missing) error = %v, want not loaded", err)
	}
}

func TestDispatcherLoadModuleReportsDigestAndDecompressionFailures(t *testing.T) {
	compressed, err := plugincodec.CompressPluginModule([]byte("plugin"))
	if err != nil {
		t.Fatalf("CompressPluginModule() returned error: %v", err)
	}
	badDigestRecord, err := assetmanifest.NewPluginRecord([]byte("plugin"), compressed)
	if err != nil {
		t.Fatalf("NewPluginManifestRecord(bad-digest) returned error: %v", err)
	}
	badCompressionRecord, err := assetmanifest.NewPluginRecord([]byte("plugin"), []byte("not-zstd"))
	if err != nil {
		t.Fatalf("NewPluginManifestRecord(bad-compression) returned error: %v", err)
	}
	unsupportedCompressionRecord, err := assetmanifest.NewPluginRecord([]byte("plugin"), []byte("plugin"))
	if err != nil {
		t.Fatalf("NewPluginManifestRecord(unsupported-compression) returned error: %v", err)
	}
	dispatcher := NewDispatcherWithManifest(nil, assetmanifest.Manifest{
		Version:        assetmanifest.ManifestVersion,
		Provider:       "test-provider",
		ExecutorArches: []string{"amd64"},
		Plugins: map[string]assetmanifest.PluginManifestRecord{
			"bad-digest":              badDigestRecord,
			"bad-compression":         badCompressionRecord,
			"unsupported-compression": unsupportedCompressionRecord,
		},
	}, nil)

	if _, err := dispatcher.LoadModule(hostrpc.ModuleLoadParams{
		Name:            "bad-digest",
		Wasm:            []byte("not-a-real-plugin"),
		WasmCompression: assetmanifest.CompressionZstd,
	}); err == nil {
		t.Fatal("LoadModule() should reject a mismatched digest")
	}

	if _, err := dispatcher.LoadModule(hostrpc.ModuleLoadParams{
		Name:            "bad-compression",
		Wasm:            []byte("not-zstd"),
		WasmCompression: assetmanifest.CompressionZstd,
	}); err == nil || !strings.Contains(err.Error(), "decode zstd plugin module") {
		t.Fatalf("LoadModule(zstd decode failure) error = %v, want zstd decode failure", err)
	}

	if _, err := dispatcher.LoadModule(hostrpc.ModuleLoadParams{
		Name:            "unsupported-compression",
		Wasm:            []byte("plugin"),
		WasmCompression: "gzip",
	}); err == nil || !strings.Contains(err.Error(), `compression mismatch`) {
		t.Fatalf("LoadModule(unsupported compression) error = %v, want compression mismatch error", err)
	}
}

func TestDispatcherLoadModuleManifestAndRecordErrors(t *testing.T) {
	compressed, err := plugincodec.CompressPluginModule([]byte("plugin"))
	if err != nil {
		t.Fatalf("CompressPluginModule() returned error: %v", err)
	}
	record, err := assetmanifest.NewPluginRecord([]byte("plugin"), compressed)
	if err != nil {
		t.Fatalf("NewPluginManifestRecord() returned error: %v", err)
	}
	delete(record.CompressedDigests, assetmanifest.ConventionalDigestAlgorithm)

	recordMissingUncompressed, err := assetmanifest.NewPluginRecord([]byte("plugin"), compressed)
	if err != nil {
		t.Fatalf("NewPluginManifestRecord() returned error: %v", err)
	}
	delete(recordMissingUncompressed.UncompressedDigests, assetmanifest.ConventionalDigestAlgorithm)

	manifestErr := errors.New("manifest failed")
	manifest := assetmanifest.Manifest{
		Version:        assetmanifest.ManifestVersion,
		Provider:       "test-provider",
		ExecutorArches: []string{"amd64"},
		Plugins: map[string]assetmanifest.PluginManifestRecord{
			"missing-compressed":   record,
			"missing-uncompressed": recordMissingUncompressed,
		},
	}

	tests := []struct {
		name       string
		dispatcher *Dispatcher
		params     hostrpc.ModuleLoadParams
		wantErr    string
	}{
		{
			name:       "manifest error",
			dispatcher: NewDispatcherWithManifest(nil, assetmanifest.Manifest{}, manifestErr),
			params: hostrpc.ModuleLoadParams{
				Name:            "linux_commands",
				Wasm:            compressed,
				WasmCompression: assetmanifest.CompressionZstd,
			},
			wantErr: "manifest failed",
		},
		{
			name:       "missing plugin",
			dispatcher: NewDispatcherWithManifest(nil, manifest, nil),
			params: hostrpc.ModuleLoadParams{
				Name:            "linux_commands",
				Wasm:            compressed,
				WasmCompression: assetmanifest.CompressionZstd,
			},
			wantErr: `plugin digest manifest missing entry for "linux_commands"`,
		},
		{
			name:       "missing compressed digest",
			dispatcher: NewDispatcherWithManifest(nil, manifest, nil),
			params: hostrpc.ModuleLoadParams{
				Name:            "missing-compressed",
				Wasm:            compressed,
				WasmCompression: assetmanifest.CompressionZstd,
			},
			wantErr: "plugin digest manifest missing compressed blake3 digest",
		},
		{
			name:       "missing uncompressed digest when dual verification enabled",
			dispatcher: NewDispatcherWithManifest(nil, manifest, nil),
			params: hostrpc.ModuleLoadParams{
				Name:                   "missing-uncompressed",
				Wasm:                   compressed,
				WasmCompression:        assetmanifest.CompressionZstd,
				DualPluginVerification: true,
			},
			wantErr: "plugin digest manifest missing uncompressed blake3 digest",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.dispatcher.LoadModule(tc.params); err == nil || err.Error() != tc.wantErr {
				t.Fatalf("LoadModule() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestDispatcherLoadModuleDigestSelectionAndDualVerification(t *testing.T) {
	compressed, err := plugincodec.CompressPluginModule([]byte("plugin"))
	if err != nil {
		t.Fatalf("CompressPluginModule() returned error: %v", err)
	}

	conventionalRecord, err := assetmanifest.NewPluginRecord([]byte("plugin"), compressed)
	if err != nil {
		t.Fatalf("NewPluginManifestRecord(conventional) returned error: %v", err)
	}
	conventionalRecord.CompressedDigests[assetmanifest.PostQuantumDigestAlgorithm] = conventionalRecord.CompressedDigests[assetmanifest.PostQuantumDigestAlgorithm] + "broken"
	conventionalRecord.UncompressedDigests[assetmanifest.PostQuantumDigestAlgorithm] = conventionalRecord.UncompressedDigests[assetmanifest.PostQuantumDigestAlgorithm] + "broken"

	skipDualRecord, err := assetmanifest.NewPluginRecord([]byte("plugin"), compressed)
	if err != nil {
		t.Fatalf("NewPluginManifestRecord(skip-dual) returned error: %v", err)
	}
	skipDualRecord.UncompressedDigests[assetmanifest.ConventionalDigestAlgorithm], err = digestutil.DigestBytes(assetmanifest.ConventionalDigestAlgorithm, []byte("different-plugin"))
	if err != nil {
		t.Fatalf("DigestBytes(different-plugin) returned error: %v", err)
	}

	rt, err := NewWASMRuntime(context.Background(), capabilities.NewHostAPI(capabilities.HostProfile{}))
	if err != nil {
		t.Fatalf("NewWASMRuntime() returned error: %v", err)
	}
	defer func() {
		if closeErr := rt.Close(); closeErr != nil {
			t.Fatalf("Close() returned error: %v", closeErr)
		}
	}()

	if _, err := NewDispatcherWithManifest(rt, testManifestWithRecord("linux_commands", conventionalRecord), nil).LoadModule(hostrpc.ModuleLoadParams{
		Name:                   "linux_commands",
		Wasm:                   compressed,
		WasmCompression:        assetmanifest.CompressionZstd,
		DualPluginVerification: true,
	}); err == nil || !strings.Contains(err.Error(), `compile plugin "linux_commands"`) {
		t.Fatalf("LoadModule(conventional digest selection) error = %v, want compile failure after verification", err)
	}

	if _, err := NewDispatcherWithManifest(nil, testManifestWithRecord("linux_commands", conventionalRecord), nil).LoadModule(hostrpc.ModuleLoadParams{
		Name:                   "linux_commands",
		Wasm:                   compressed,
		WasmCompression:        assetmanifest.CompressionZstd,
		UsePostQuantumDigests:  true,
		DualPluginVerification: true,
	}); err == nil {
		t.Fatal("LoadModule(post-quantum digest selection) should reject the broken shake256 digest")
	}

	if _, err := NewDispatcherWithManifest(rt, testManifestWithRecord("linux_commands", skipDualRecord), nil).LoadModule(hostrpc.ModuleLoadParams{
		Name:            "linux_commands",
		Wasm:            compressed,
		WasmCompression: assetmanifest.CompressionZstd,
	}); err == nil || !strings.Contains(err.Error(), `compile plugin "linux_commands"`) {
		t.Fatalf("LoadModule(skip dual verification) error = %v, want compile failure after compressed verification", err)
	}

	if _, err := NewDispatcherWithManifest(nil, testManifestWithRecord("linux_commands", skipDualRecord), nil).LoadModule(hostrpc.ModuleLoadParams{
		Name:                   "linux_commands",
		Wasm:                   compressed,
		WasmCompression:        assetmanifest.CompressionZstd,
		DualPluginVerification: true,
	}); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("LoadModule(dual verification enabled) error = %v, want uncompressed verification failure", err)
	}
}

func TestDispatcherLoadModuleUsesSelectedDigestFamilyAndDualVerification(t *testing.T) {
	compressed, err := plugincodec.CompressPluginModule([]byte("plugin"))
	if err != nil {
		t.Fatalf("CompressPluginModule() returned error: %v", err)
	}

	rt, err := NewWASMRuntime(context.Background(), capabilities.NewHostAPI(capabilities.HostProfile{}))
	if err != nil {
		t.Fatalf("NewWASMRuntime() returned error: %v", err)
	}
	defer func() {
		if closeErr := rt.Close(); closeErr != nil {
			t.Fatalf("Close() returned error: %v", closeErr)
		}
	}()
	dispatcher := NewDispatcherWithManifest(rt, testManifest(t, "linux_commands", []byte("plugin"), compressed), nil)

	if _, err := dispatcher.LoadModule(hostrpc.ModuleLoadParams{
		Name:                   "linux_commands",
		UsePostQuantumDigests:  true,
		DualPluginVerification: true,
		WasmCompression:        assetmanifest.CompressionZstd,
		Wasm:                   compressed,
	}); err == nil || !strings.Contains(err.Error(), `compile plugin "linux_commands"`) {
		t.Fatalf("LoadModule() error = %v, want compile failure after verification passes", err)
	}
}

func TestDispatcherRestartProcessRequiresOperationID(t *testing.T) {
	dispatcher := NewDispatcherWithManifest(&WASMRuntime{hostAPI: capabilities.NewHostAPI(capabilities.HostProfile{})}, assetmanifest.Manifest{}, nil)

	if _, err := dispatcher.RestartProcess(context.Background(), hostrpc.RestartProcessParams{Name: "sshd"}); err == nil || !strings.Contains(err.Error(), "missing operation_id") {
		t.Fatalf("RestartProcess() error = %v, want missing operation_id", err)
	}
}

func TestDispatcherJournalAndRebootWrappers(t *testing.T) {
	t.Setenv("TF_LINUX_PROVIDER_EXECUTOR_JOURNAL_DIR", t.TempDir())
	mustSetJournalKey(t, "55555555555555555555555555555555")

	dispatcher := NewDispatcherWithManifest(&WASMRuntime{hostAPI: capabilities.NewHostAPI(capabilities.HostProfile{})}, assetmanifest.Manifest{}, nil)

	acquired, err := dispatcher.AcquireOperation(context.Background(), hostrpc.OperationAcquireParams{
		HostKey:    "ssh:host-wrapper",
		SessionKey: "session-wrapper",
		Action:     "resource.read",
		LockSet: []hostrpc.LockDescriptor{{
			Key:  "host:wrapper",
			Mode: hostrpc.LockModeShared,
		}},
	})
	if err != nil {
		t.Fatalf("AcquireOperation() error = %v, want nil", err)
	}
	if acquired.OperationID == "" || !acquired.Granted {
		t.Fatalf("AcquireOperation() = %#v, want granted operation id", acquired)
	}

	if err := dispatcher.ReleaseOperation(hostrpc.OperationReleaseParams{
		HostKey:     "ssh:host-wrapper",
		OperationID: acquired.OperationID,
		Status:      "completed",
	}); err != nil {
		t.Fatalf("ReleaseOperation() error = %v, want nil", err)
	}

	rebootEntry, err := dispatcher.PrepareRebootJournal(hostrpc.RebootJournalPrepareParams{
		HostAddress:   "host-wrapper.example",
		Name:          "wrapper-reboot",
		Reason:        "kernel-upgrade",
		RebootCommand: "systemctl reboot",
	})
	if err != nil {
		t.Fatalf("PrepareRebootJournal() error = %v, want nil", err)
	}
	if rebootEntry.OperationID == "" || rebootEntry.Phase != rebootPhasePlanned {
		t.Fatalf("PrepareRebootJournal() = %#v, want planned reboot entry", rebootEntry)
	}

	phaseEntry, err := dispatcher.MarkRebootJournalPhase(hostrpc.RebootJournalMarkPhaseParams{
		OperationID: rebootEntry.OperationID,
		Phase:       rebootPhaseTargetPrepared,
	})
	if err != nil {
		t.Fatalf("MarkRebootJournalPhase() error = %v, want nil", err)
	}
	if phaseEntry.Phase != rebootPhaseTargetPrepared {
		t.Fatalf("MarkRebootJournalPhase() phase = %q, want %q", phaseEntry.Phase, rebootPhaseTargetPrepared)
	}

	failedEntry, err := dispatcher.MarkRebootJournalFailed(hostrpc.RebootJournalMarkFailedParams{
		OperationID: rebootEntry.OperationID,
		LastError:   "reconnect timeout",
	})
	if err != nil {
		t.Fatalf("MarkRebootJournalFailed() error = %v, want nil", err)
	}
	if failedEntry.Phase != rebootPhaseFailed || failedEntry.LastError != "reconnect timeout" {
		t.Fatalf("MarkRebootJournalFailed() = %#v, want failed entry with last error", failedEntry)
	}

	completedEntry, err := dispatcher.MarkRebootJournalCompleted(hostrpc.RebootJournalMarkCompletedParams{
		OperationID: rebootEntry.OperationID,
		PostBootID:  "boot-2",
	})
	if err != nil {
		t.Fatalf("MarkRebootJournalCompleted() error = %v, want nil", err)
	}
	if completedEntry.Phase != rebootPhaseCompleted || completedEntry.PostBootID != "boot-2" {
		t.Fatalf("MarkRebootJournalCompleted() = %#v, want completed entry with post boot id", completedEntry)
	}
}

func assertOperationState(t *testing.T, result *hostrpc.OperationResult, want string) {
	t.Helper()
	if result == nil || string(result.State) != want {
		t.Fatalf("operation result = %#v, want state %s", result, want)
	}
}

func testManifest(t *testing.T, module string, uncompressed, compressed []byte) assetmanifest.Manifest {
	t.Helper()

	record, err := assetmanifest.NewPluginRecord(uncompressed, compressed)
	if err != nil {
		t.Fatalf("NewPluginManifestRecord() returned error: %v", err)
	}
	return assetmanifest.Manifest{
		Version:        assetmanifest.ManifestVersion,
		Provider:       "test-provider",
		ExecutorArches: []string{"amd64"},
		Plugins: map[string]assetmanifest.PluginManifestRecord{
			module: record,
		},
	}
}

func testManifestWithRecord(module string, record assetmanifest.PluginManifestRecord) assetmanifest.Manifest {
	return assetmanifest.Manifest{
		Version:        assetmanifest.ManifestVersion,
		Provider:       "test-provider",
		ExecutorArches: []string{"amd64"},
		Plugins: map[string]assetmanifest.PluginManifestRecord{
			module: record,
		},
	}
}

func assertDirectDispatchCall(t *testing.T, call recordedDispatchCall, wantModule string, wantAction string, wantInput json.RawMessage) {
	t.Helper()
	if call.moduleName != wantModule || call.action != wantAction {
		t.Fatalf("dispatch call = %#v, want module=%q action=%q", call, wantModule, wantAction)
	}
	if string(call.input) != string(wantInput) {
		t.Fatalf("dispatch input = %s, want %s", string(call.input), string(wantInput))
	}
	if call.execution == nil {
		t.Fatal("dispatch call should carry execution context")
	}
}

func assertModuleDispatchCall(t *testing.T, call recordedDispatchCall, wantModule string, wantAction string, wantResourceType string, wantField string, wantValue json.RawMessage) {
	t.Helper()
	if call.moduleName != wantModule || call.action != wantAction {
		t.Fatalf("dispatch call = %#v, want module=%q action=%q", call, wantModule, wantAction)
	}
	assertModuleDispatchJSON(t, call.input, wantResourceType, wantAction, wantField, wantValue)
	if call.execution == nil {
		t.Fatal("dispatch call should carry execution context")
	}
}

func assertModuleDispatchJSON(t *testing.T, input json.RawMessage, wantResourceType string, wantAction string, wantField string, wantValue json.RawMessage) {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(input, &payload); err != nil {
		t.Fatalf("unmarshal module payload: %v", err)
	}
	if got := string(payload["resource_type"]); got != fmt.Sprintf(`"%s"`, wantResourceType) {
		t.Fatalf("resource_type = %s, want %q", got, wantResourceType)
	}
	if got := string(payload["action"]); got != fmt.Sprintf(`"%s"`, wantAction) {
		t.Fatalf("action = %s, want %q", got, wantAction)
	}
	if got := string(payload[wantField]); got != string(wantValue) {
		t.Fatalf("payload[%q] = %s, want %s", wantField, got, string(wantValue))
	}
}

func snapshotRuntimeJournalKey() []byte {
	runtimeJournalKey.mu.RLock()
	defer runtimeJournalKey.mu.RUnlock()
	return append([]byte(nil), runtimeJournalKey.key...)
}

func restoreRuntimeJournalKey(key []byte) {
	runtimeJournalKey.mu.Lock()
	defer runtimeJournalKey.mu.Unlock()
	runtimeJournalKey.key = append([]byte(nil), key...)
}

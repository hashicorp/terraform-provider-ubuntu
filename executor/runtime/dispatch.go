package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/terraform-provider-ubuntu/executor/capabilities"
	"github.com/hashicorp/terraform-provider-ubuntu/executor/logging"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assets"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

// Dispatcher routes operations to loaded WASM plugins.
type Dispatcher struct {
	rt               *WASMRuntime
	manifest         assets.Manifest
	manifestErr      error
	mu               sync.RWMutex
	plugins          map[string]bool // tracks which plugins are loaded
	operationJournal *operationJournal
	rebootJournal    *rebootJournal
	traceSeq         uint64
}

var dispatchRuntimeCall = func(rt *WASMRuntime, ctx context.Context, moduleName, action string, input json.RawMessage) (json.RawMessage, error) {
	switch action {
	case "read":
		return rt.CallRead(ctx, moduleName, input)
	case "validate":
		return rt.CallValidate(ctx, moduleName, input)
	case "create":
		return rt.CallCreate(ctx, moduleName, input)
	case "update":
		return rt.CallUpdate(ctx, moduleName, input)
	case "delete":
		return rt.CallDelete(ctx, moduleName, input)
	case "import":
		return rt.CallImport(ctx, moduleName, input)
	case "data_read":
		return rt.CallDataRead(ctx, moduleName, input)
	case "invoke":
		return rt.CallInvoke(ctx, moduleName, input)
	default:
		return nil, fmt.Errorf("unsupported dispatch action %q", action)
	}
}

// NewDispatcher creates a new Dispatcher backed by a WASMRuntime.
func NewDispatcher(rt *WASMRuntime) *Dispatcher {
	manifest, err := loadEmbeddedManifest()
	return NewDispatcherWithManifest(rt, manifest, err)
}

func NewDispatcherWithManifest(rt *WASMRuntime, manifest assets.Manifest, manifestErr error) *Dispatcher {
	return &Dispatcher{
		rt:               rt,
		manifest:         manifest,
		manifestErr:      manifestErr,
		plugins:          make(map[string]bool),
		operationJournal: newOperationJournal(),
		rebootJournal:    newRebootJournal(),
	}
}

type pluginResponseEnvelope struct {
	State json.RawMessage `json:"state,omitempty"`
	Error string          `json:"error,omitempty"`
}

func (d *Dispatcher) LoadModule(params hostrpc.ModuleLoadParams) (*hostrpc.ModuleLoadResult, error) {
	traceID := d.nextTraceID()
	started := time.Now()
	algorithm := assets.DigestAlgorithmForSelection(params.UsePostQuantumDigests)
	log.Printf("[rpc#%d] module.load start name=%q bytes=%d algorithm=%q dual=%t compression=%q", traceID, params.Name, len(params.Wasm), algorithm, params.DualPluginVerification, params.WasmCompression)
	if d.manifestErr != nil {
		log.Printf("[rpc#%d] module.load error duration=%s err=%v", traceID, time.Since(started), d.manifestErr)
		return nil, d.manifestErr
	}

	record, err := d.manifest.Plugin(params.Name)
	if err != nil {
		log.Printf("[rpc#%d] module.load error duration=%s err=%v", traceID, time.Since(started), err)
		return nil, err
	}
	if params.WasmCompression != record.Compression {
		err := fmt.Errorf("plugin %q compression mismatch: expected %q, got %q", params.Name, record.Compression, params.WasmCompression)
		log.Printf("[rpc#%d] module.load error duration=%s err=%v", traceID, time.Since(started), err)
		return nil, err
	}
	compressedDigest, err := record.CompressedDigest(algorithm)
	if err != nil {
		log.Printf("[rpc#%d] module.load error duration=%s err=%v", traceID, time.Since(started), err)
		return nil, err
	}
	if err := assets.VerifyDigest(params.Wasm, compressedDigest); err != nil {
		log.Printf("[rpc#%d] module.load error duration=%s err=%v", traceID, time.Since(started), err)
		return nil, err
	}

	wasmBytes, err := assets.DecompressPluginModule(params.Wasm, params.WasmCompression)
	if err != nil {
		log.Printf("[rpc#%d] module.load error duration=%s err=%v", traceID, time.Since(started), err)
		return nil, err
	}
	if params.DualPluginVerification {
		uncompressedDigest, err := record.UncompressedDigest(algorithm)
		if err != nil {
			log.Printf("[rpc#%d] module.load error duration=%s err=%v", traceID, time.Since(started), err)
			return nil, err
		}
		if err := assets.VerifyDigest(wasmBytes, uncompressedDigest); err != nil {
			log.Printf("[rpc#%d] module.load error duration=%s err=%v", traceID, time.Since(started), err)
			return nil, err
		}
	}

	if err := d.rt.LoadPlugin(params.Name, wasmBytes); err != nil {
		log.Printf("[rpc#%d] module.load error duration=%s err=%v", traceID, time.Since(started), err)
		return nil, err
	}

	d.mu.Lock()
	d.plugins[params.Name] = true
	d.mu.Unlock()

	log.Printf("[rpc#%d] module.load complete duration=%s loaded=true", traceID, time.Since(started))
	return &hostrpc.ModuleLoadResult{Name: params.Name, Loaded: true}, nil
}

func (d *Dispatcher) ReadResource(ctx context.Context, params hostrpc.ResourceReadParams) (opResult *hostrpc.OperationResult, err error) {
	traceID, started := d.traceStart("resource.read", params.ModuleName, params.ResourceType, params.Execution, "state", params.State)
	var resultSummary string
	defer func() {
		d.traceFinishOperation(traceID, "resource.read", started, resultSummary, err)
	}()

	moduleName, err := d.loadedModule(params.ModuleName, params.ResourceType)
	if err != nil {
		return nil, err
	}

	ctx = capabilities.WithExecutionContext(ctx, params.Execution)

	input := params.State
	if usesModuleDispatch(moduleName, params.ResourceType) {
		input = d.marshalModulePayload(params.ResourceType, "read", nil, params.State, nil, "")
	}

	result, err := dispatchRuntimeCall(d.rt, ctx, moduleName, "read", input)
	opResult, err = d.stateResult(result, err)
	if opResult != nil {
		resultSummary = logging.SummarizeJSON(opResult.State)
	}
	return opResult, err
}

func (d *Dispatcher) ValidateResource(ctx context.Context, params hostrpc.ResourceValidateParams) (opResult *hostrpc.OperationResult, err error) {
	traceID, started := d.traceStart("resource.validate", params.ModuleName, params.ResourceType, params.Execution, "config", params.Config)
	var resultSummary string
	defer func() {
		d.traceFinishOperation(traceID, "resource.validate", started, resultSummary, err)
	}()

	moduleName, err := d.loadedModule(params.ModuleName, params.ResourceType)
	if err != nil {
		return nil, err
	}

	ctx = capabilities.WithExecutionContext(ctx, params.Execution)

	input := params.Config
	if usesModuleDispatch(moduleName, params.ResourceType) {
		input = d.marshalModulePayload(params.ResourceType, "validate", nil, nil, params.Config, "")
	}

	result, err := dispatchRuntimeCall(d.rt, ctx, moduleName, "validate", input)
	opResult, err = d.stateResult(result, err)
	if opResult != nil {
		resultSummary = logging.SummarizeJSON(opResult.State)
	}
	return opResult, err
}

func (d *Dispatcher) CreateResource(ctx context.Context, params hostrpc.ResourceCreateParams) (opResult *hostrpc.OperationResult, err error) {
	traceID, started := d.traceStart("resource.create", params.ModuleName, params.ResourceType, params.Execution, "plan", params.Plan)
	var resultSummary string
	defer func() {
		d.traceFinishOperation(traceID, "resource.create", started, resultSummary, err)
	}()

	moduleName, err := d.loadedModule(params.ModuleName, params.ResourceType)
	if err != nil {
		return nil, err
	}

	ctx = capabilities.WithExecutionContext(ctx, params.Execution)

	input := params.Plan
	if usesModuleDispatch(moduleName, params.ResourceType) {
		input = d.marshalModulePayload(params.ResourceType, "create", params.Plan, nil, nil, "")
	}

	result, err := dispatchRuntimeCall(d.rt, ctx, moduleName, "create", input)
	opResult, err = d.stateResult(result, err)
	if opResult != nil {
		resultSummary = logging.SummarizeJSON(opResult.State)
	}
	return opResult, err
}

func (d *Dispatcher) UpdateResource(ctx context.Context, params hostrpc.ResourceUpdateParams) (opResult *hostrpc.OperationResult, err error) {
	traceID, started := d.traceStart("resource.update", params.ModuleName, params.ResourceType, params.Execution, "payload", marshalUpdatePayload(params.State, params.Plan))
	var resultSummary string
	defer func() {
		d.traceFinishOperation(traceID, "resource.update", started, resultSummary, err)
	}()

	moduleName, err := d.loadedModule(params.ModuleName, params.ResourceType)
	if err != nil {
		return nil, err
	}

	ctx = capabilities.WithExecutionContext(ctx, params.Execution)

	input := marshalUpdatePayload(params.State, params.Plan)
	if usesModuleDispatch(moduleName, params.ResourceType) {
		input = d.marshalModulePayload(params.ResourceType, "update", params.Plan, params.State, nil, "")
	}

	result, err := dispatchRuntimeCall(d.rt, ctx, moduleName, "update", input)
	opResult, err = d.stateResult(result, err)
	if opResult != nil {
		resultSummary = logging.SummarizeJSON(opResult.State)
	}
	return opResult, err
}

func (d *Dispatcher) DeleteResource(ctx context.Context, params hostrpc.ResourceDeleteParams) (opResult *hostrpc.OperationResult, err error) {
	traceID, started := d.traceStart("resource.delete", params.ModuleName, params.ResourceType, params.Execution, "state", params.State)
	var resultSummary string
	defer func() {
		d.traceFinishOperation(traceID, "resource.delete", started, resultSummary, err)
	}()

	moduleName, err := d.loadedModule(params.ModuleName, params.ResourceType)
	if err != nil {
		return nil, err
	}

	ctx = capabilities.WithExecutionContext(ctx, params.Execution)

	input := params.State
	if usesModuleDispatch(moduleName, params.ResourceType) {
		input = d.marshalModulePayload(params.ResourceType, "delete", nil, params.State, nil, "")
	}

	result, err := dispatchRuntimeCall(d.rt, ctx, moduleName, "delete", input)
	opResult, err = d.stateResult(result, err)
	if opResult != nil {
		resultSummary = logging.SummarizeJSON(opResult.State)
	}
	return opResult, err
}

func (d *Dispatcher) ImportResource(ctx context.Context, params hostrpc.ResourceImportParams) (opResult *hostrpc.OperationResult, err error) {
	traceID, started := d.traceStart("resource.import", params.ModuleName, params.ResourceType, params.Execution, "import_id", mustMarshalString(params.ImportID))
	var resultSummary string
	defer func() {
		d.traceFinishOperation(traceID, "resource.import", started, resultSummary, err)
	}()

	moduleName, err := d.loadedModule(params.ModuleName, params.ResourceType)
	if err != nil {
		return nil, err
	}

	ctx = capabilities.WithExecutionContext(ctx, params.Execution)

	input, err := json.Marshal(params.ImportID)
	if err != nil {
		return nil, err
	}
	if usesModuleDispatch(moduleName, params.ResourceType) {
		input = d.marshalModulePayload(params.ResourceType, "import", nil, nil, nil, params.ImportID)
	}

	result, err := dispatchRuntimeCall(d.rt, ctx, moduleName, "import", input)
	opResult, err = d.stateResult(result, err)
	if opResult != nil {
		resultSummary = logging.SummarizeJSON(opResult.State)
	}
	return opResult, err
}

func (d *Dispatcher) ReadDataSource(ctx context.Context, params hostrpc.DataSourceReadParams) (opResult *hostrpc.OperationResult, err error) {
	traceID, started := d.traceStart("datasource.read", params.ModuleName, params.ResourceType, params.Execution, "config", params.Config)
	var resultSummary string
	defer func() {
		d.traceFinishOperation(traceID, "datasource.read", started, resultSummary, err)
	}()

	moduleName, err := d.loadedModule(params.ModuleName, params.ResourceType)
	if err != nil {
		return nil, err
	}

	ctx = capabilities.WithExecutionContext(ctx, params.Execution)

	input := params.Config
	if usesModuleDispatch(moduleName, params.ResourceType) {
		input = d.marshalModulePayload(params.ResourceType, "data_read", nil, nil, params.Config, "")
	}

	result, err := dispatchRuntimeCall(d.rt, ctx, moduleName, "data_read", input)
	opResult, err = d.stateResult(result, err)
	if opResult != nil {
		resultSummary = logging.SummarizeJSON(opResult.State)
	}
	return opResult, err
}

func (d *Dispatcher) RestartProcess(ctx context.Context, params hostrpc.RestartProcessParams) (*hostrpc.CommandResult, error) {
	if params.OperationID == "" {
		return nil, fmt.Errorf("missing operation_id")
	}

	moduleName, err := d.loadedModule(params.ModuleName, "restart_process")
	if err != nil {
		return nil, err
	}

	ctx = capabilities.WithExecutionContext(ctx, params.Execution)

	spec, err := resolveRestartCommand(ctx, d, moduleName, params)
	if err != nil {
		return nil, err
	}

	record, shouldLaunch, err := prepareRestartOperation(params.OperationID, params.Name, spec, params.Execution)
	if err != nil {
		return nil, err
	}

	if shouldLaunch {
		if err := startRestartHelper(record); err != nil {
			lockFile, lockErr := lockRestartOperation(params.OperationID)
			if lockErr == nil {
				record.Status = restartStatusLaunchError
				record.LastError = err.Error()
				_ = writeRestartRecord(record)
				_ = unlockRestartOperation(lockFile)
			}
			return nil, err
		}
	}

	return waitForRestartResult(ctx, params.OperationID)
}

func (d *Dispatcher) InvokeAction(ctx context.Context, params hostrpc.ActionInvokeParams) (opResult *hostrpc.OperationResult, err error) {
	traceID, started := d.traceStart("action.invoke", params.ModuleName, params.ResourceType, params.Execution, "config", params.Config)
	var resultSummary string
	defer func() {
		d.traceFinishOperation(traceID, "action.invoke", started, resultSummary, err)
	}()

	moduleName, err := d.loadedModule(params.ModuleName, params.ResourceType)
	if err != nil {
		return nil, err
	}

	ctx = capabilities.WithExecutionContext(ctx, params.Execution)

	input := params.Config
	if usesModuleDispatch(moduleName, params.ResourceType) {
		input = d.marshalModulePayload(params.ResourceType, "invoke", nil, nil, params.Config, "")
	}

	result, err := dispatchRuntimeCall(d.rt, ctx, moduleName, "invoke", input)
	opResult, err = d.stateResult(result, err)
	if opResult != nil {
		resultSummary = logging.SummarizeJSON(opResult.State)
	}
	return opResult, err
}

func (d *Dispatcher) HostCommand(ctx context.Context, params hostrpc.HostCommandParams) (*hostrpc.CommandResult, error) {
	traceID := d.nextTraceID()
	started := time.Now()
	log.Printf("[rpc#%d] capabilities.command start name=%q args=%s execution=%s", traceID, params.Name, logging.SummarizeArgs(params.Args), logging.SummarizeExecution(params.Execution))
	ctx = capabilities.WithExecutionContext(ctx, params.Execution)
	result := d.rt.hostAPI.CmdExec(ctx, params.Name, params.Args...)
	commandResult := &hostrpc.CommandResult{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
	}
	log.Printf("[rpc#%d] capabilities.command complete duration=%s exit=%d stdout=%s stderr=%s", traceID, time.Since(started), commandResult.ExitCode, logging.Preview(commandResult.Stdout, 240), logging.Preview(commandResult.Stderr, 240))
	return commandResult, nil
}

func (d *Dispatcher) ConfigureJournal(params hostrpc.JournalConfigureParams) error {
	return setRuntimeJournalKey(params.Key)
}

func (d *Dispatcher) AcquireOperation(ctx context.Context, params hostrpc.OperationAcquireParams) (*hostrpc.OperationAcquireResult, error) {
	operationID, granted, err := d.operationJournal.Acquire(ctx, params)
	if err != nil {
		return nil, err
	}
	return &hostrpc.OperationAcquireResult{OperationID: operationID, Granted: granted}, nil
}

func (d *Dispatcher) ReleaseOperation(params hostrpc.OperationReleaseParams) error {
	return d.operationJournal.Release(params)
}

func (d *Dispatcher) PrepareRebootJournal(params hostrpc.RebootJournalPrepareParams) (*hostrpc.RebootJournalEntry, error) {
	return d.rebootJournal.Prepare(params)
}

func (d *Dispatcher) MarkRebootJournalPhase(params hostrpc.RebootJournalMarkPhaseParams) (*hostrpc.RebootJournalEntry, error) {
	return d.rebootJournal.MarkPhase(params)
}

func (d *Dispatcher) MarkRebootJournalFailed(params hostrpc.RebootJournalMarkFailedParams) (*hostrpc.RebootJournalEntry, error) {
	return d.rebootJournal.MarkFailed(params)
}

func (d *Dispatcher) MarkRebootJournalCompleted(params hostrpc.RebootJournalMarkCompletedParams) (*hostrpc.RebootJournalEntry, error) {
	return d.rebootJournal.MarkCompleted(params)
}

func (d *Dispatcher) loadedModule(moduleName, resourceType string) (string, error) {
	if moduleName == "" {
		moduleName = resourceType
	}

	d.mu.RLock()
	loaded := d.plugins[moduleName]
	d.mu.RUnlock()

	if !loaded {
		return "", fmt.Errorf("plugin %q not loaded", moduleName)
	}

	return moduleName, nil
}

func (d *Dispatcher) nextTraceID() uint64 {
	return atomic.AddUint64(&d.traceSeq, 1)
}

func (d *Dispatcher) traceStart(operation string, moduleName string, resourceType string, execution *hostrpc.ExecutionContext, payloadLabel string, payload json.RawMessage) (uint64, time.Time) {
	traceID := d.nextTraceID()
	started := time.Now()
	log.Printf("[rpc#%d] %s start module=%q type=%q execution=%s %s=%s", traceID, operation, moduleName, resourceType, logging.SummarizeExecution(execution), payloadLabel, logging.SummarizeJSON(payload))
	return traceID, started
}

func (d *Dispatcher) traceFinishOperation(traceID uint64, operation string, started time.Time, resultSummary string, err error) {
	if traceID == 0 {
		return
	}
	if err != nil {
		log.Printf("[rpc#%d] %s error duration=%s err=%v", traceID, operation, time.Since(started), err)
		return
	}
	if resultSummary == "" {
		resultSummary = "<nil>"
	}
	log.Printf("[rpc#%d] %s complete duration=%s result=%s", traceID, operation, time.Since(started), resultSummary)
}

func (d *Dispatcher) stateResult(result json.RawMessage, err error) (*hostrpc.OperationResult, error) {
	if err != nil {
		return nil, err
	}

	result, err = unwrapPluginState(result)
	if err != nil {
		return nil, err
	}

	return &hostrpc.OperationResult{State: result}, nil
}

func marshalUpdatePayload(state, plan json.RawMessage) json.RawMessage {
	payload := map[string]json.RawMessage{
		"state": state,
		"plan":  plan,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return plan
	}

	return data
}

func usesModuleDispatch(moduleName, resourceType string) bool {
	return moduleName != "" && resourceType != "" && moduleName != resourceType
}

func (d *Dispatcher) marshalModulePayload(resourceType, action string, plan, state, config json.RawMessage, importID string) json.RawMessage {
	return marshalModulePayload(terraformProviderName(d.manifest.Provider), resourceType, action, plan, state, config, importID)
}

func terraformProviderName(providerName string) string {
	name := strings.TrimSpace(providerName)
	if name == "" {
		return ""
	}
	name = strings.NewReplacer("-", "_", ".", "_", "/", "_").Replace(name)
	if strings.HasPrefix(name, "terraform_provider_") {
		return name
	}
	return "terraform_provider_" + name
}

func marshalModulePayload(providerName, resourceType, action string, plan, state, config json.RawMessage, importID string) json.RawMessage {
	payload := map[string]interface{}{
		"resource_type": resourceType,
		"action":        action,
	}
	if providerName != "" {
		payload["provider_name"] = providerName
	}

	if plan != nil {
		payload["plan"] = plan
	}
	if state != nil {
		payload["state"] = state
	}
	if config != nil {
		payload["config"] = config
	}
	if importID != "" {
		payload["import_id"] = importID
	}

	data, err := json.Marshal(payload)
	if err != nil {
		if config != nil {
			return config
		}
		if state != nil {
			return state
		}
		return plan
	}

	return data
}

func unwrapPluginState(result json.RawMessage) (json.RawMessage, error) {
	if len(result) == 0 || string(result) == "null" {
		return nil, nil
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(result, &payload); err != nil {
		return result, nil
	}

	stateValue, hasState := payload["state"]
	_, hasError := payload["error"]
	if len(payload) == 0 {
		return nil, nil
	}
	if !hasState && !hasError {
		return result, nil
	}

	var envelope pluginResponseEnvelope
	if err := json.Unmarshal(result, &envelope); err != nil {
		return nil, fmt.Errorf("decode plugin response envelope: %w", err)
	}

	if envelope.Error != "" {
		return nil, errors.New(envelope.Error)
	}

	if len(envelope.State) == 0 || string(stateValue) == "null" {
		return nil, nil
	}

	return envelope.State, nil
}

func mustMarshalString(value string) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`"<marshal-error>"`)
	}
	return data
}

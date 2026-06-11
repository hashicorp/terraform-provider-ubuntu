// Copyright IBM Corp. 2026

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/hostsession"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
)

var (
	_                        resource.Resource                   = (*GenericResource)(nil)
	_                        resource.ResourceWithImportState    = (*GenericResource)(nil)
	_                        resource.ResourceWithConfigure      = (*GenericResource)(nil)
	_                        resource.ResourceWithValidateConfig = (*GenericResource)(nil)
	_                        resource.ResourceWithModifyPlan     = (*GenericResource)(nil)
	_                        resource.Resource                   = (*GenericIdentityResource)(nil)
	_                        resource.ResourceWithImportState    = (*GenericIdentityResource)(nil)
	_                        resource.ResourceWithConfigure      = (*GenericIdentityResource)(nil)
	_                        resource.ResourceWithValidateConfig = (*GenericIdentityResource)(nil)
	_                        resource.ResourceWithModifyPlan     = (*GenericIdentityResource)(nil)
	_                        resource.ResourceWithIdentity       = (*GenericIdentityResource)(nil)
	sendResourceOperation                                        = sendOperation
	lookupHostKeyFingerprint                                     = func(pool *transport.ConnectionPool, config transport.TransportConfig) string {
		if pool == nil {
			return ""
		}
		return pool.HostKeyFingerprint(config)
	}
)

type ProviderData struct {
	ExecutorMgr   *hostsession.ExecutorManager
	Pool          *transport.ConnectionPool
	DefaultHost   *transport.TransportConfig
	DestroySafety DestroySafetyConfig
}

type privateStateReader interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
}

type privateStateWriter interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}

type GenericResource struct {
	typeName          string
	runtimeType       string
	runtimeModule     string
	requiredPrivilege string
	attributes        map[string]resourceschema.Attribute
	blocks            map[string]resourceschema.Block
	schemaCustomizer  ResourceSchemaCustomizer
	lockPlanner       LockPlanner
	executionPolicy   ResourceExecutionPolicy
	shapeResult       ResourceResultShaper
	shapeImport       ResourceImportShaper
	validationPolicy  ValidationPolicy
	destroySafety     DestroySafetyPolicy
	importRequired    bool
	importIdentity    *ResourceImportIdentity
	hostAttrs         transportAttributeNames

	executorMgr    *hostsession.ExecutorManager
	pool           *transport.ConnectionPool
	defaultHost    *transport.TransportConfig
	destroySafetyC DestroySafetyConfig
}

type GenericIdentityResource struct {
	*GenericResource
}

const genericAllowDestroyKey = "generic_allow_destructive_destroy"

func NewGenericResource(typeName string, def ResourceDefinition) *GenericResource {
	return &GenericResource{
		typeName:          typeName,
		runtimeType:       def.RuntimeType,
		runtimeModule:     def.RuntimeModule,
		requiredPrivilege: def.RequiredPrivilege,
		attributes:        def.Attributes,
		blocks:            def.Blocks,
		schemaCustomizer:  def.CustomizeSchema,
		lockPlanner:       def.LockPlanner,
		executionPolicy:   def.ExecutionPolicy,
		shapeResult:       def.ShapeResult,
		shapeImport:       def.ShapeImport,
		validationPolicy:  def.ValidationPolicy,
		destroySafety:     def.DestroySafety,
		importRequired:    def.ImportRequiredOnExisting,
		importIdentity:    def.ImportIdentity,
		hostAttrs:         transportAttributeNamesForResourceSchema(def.Attributes, def.CustomizeSchema),
	}
}

func NewGenericIdentityResource(typeName string, def ResourceDefinition) *GenericIdentityResource {
	return &GenericIdentityResource{GenericResource: NewGenericResource(typeName, def)}
}

func applyPinnedHostKeyFingerprint(config transport.TransportConfig, fingerprint types.String) transport.TransportConfig {
	fingerprint = normalizeHostKeyFingerprintValue(fingerprint)
	if !fingerprint.IsNull() {
		config.SSHHostKeyFingerprint = fingerprint.ValueString()
	}
	return config
}

func observedHostKeyFingerprintValue(pool *transport.ConnectionPool, config transport.TransportConfig, fallback types.String) types.String {
	if observed := strings.TrimSpace(lookupHostKeyFingerprint(pool, config)); observed != "" {
		return types.StringValue(observed)
	}
	return normalizeHostKeyFingerprintValue(fallback)
}

func (r *GenericResource) buildIdentity(ctx context.Context, data map[string]interface{}, hostConfig transport.TransportConfig) (*tfsdk.ResourceIdentity, diag.Diagnostics) {
	if r.importIdentity == nil {
		return nil, nil
	}
	return buildResourceIdentityWithNames(ctx, r.importIdentity, data, hostConfig, r.hostAttrs)
}

func (r *GenericResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = r.typeName
}

func (r *GenericIdentityResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = r.typeName
	resp.ResourceBehavior.MutableIdentity = true
}

func (r *GenericIdentityResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	if !supportsResourceIdentity(r.importIdentity) {
		resp.IdentitySchema = identityschema.Schema{}
		return
	}
	resp.IdentitySchema = buildIdentitySchemaWithNames(r.importIdentity, r.hostAttrs)
}

func (r *GenericResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resourceschema.Schema{
		Description: fmt.Sprintf("Manages a %s resource via the hostsession.", r.runtimeType),
		Attributes:  resourceSchemaAttributes(r.attributes, r.schemaCustomizer, r.destroySafety),
		Blocks:      resourceSchemaBlocks(r.blocks),
	}
}

func (r *GenericResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Provider Data",
			fmt.Sprintf("Expected *engine.ProviderData, got %T", req.ProviderData),
		)
		return
	}

	r.executorMgr = pd.ExecutorMgr
	r.pool = pd.Pool
	r.defaultHost = pd.DefaultHost
	r.destroySafetyC = pd.DestroySafety
}

func (r *GenericResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	if !r.validationPolicy.RemotePlanValidation || r.executorMgr == nil {
		return
	}

	hostConfig, hostKnown, diagnostics := resolveHostFromConfigIfKnownWithNames(ctx, &req.Config, r.defaultHost, r.hostAttrs)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() || !hostKnown {
		return
	}

	configAttrs, diagnostics := extractResourceValuesFromConfig(ctx, &req.Config, r.attributes, r.blocks)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(r.validateRemote(ctx, hostConfig, configAttrs)...)
}

func (r *GenericResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if destroySafetyUsesAllowFlag(r.destroySafety.Mode) {
		resp.Diagnostics.Append(storeGenericAllowDestroyFromConfig(ctx, &req.Config, resp.Private)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	if req.Plan.Raw.IsNull() {
		if r.destroySafety.Mode == DestroySafetyModeNone {
			return
		}

		stateAttrs, diagnostics := stateToJSON(ctx, &req.State, r.attributes, r.blocks)
		resp.Diagnostics.Append(diagnostics...)
		if resp.Diagnostics.HasError() {
			return
		}

		if isAbsentResourceState(stateAttrs) {
			return
		}

		allow, diagnostics := allowDestructiveDestroyFromStateOrPrivate(ctx, &req.State, req.Private)
		if destroySafetyUsesAllowFlag(r.destroySafety.Mode) {
			resp.Diagnostics.Append(diagnostics...)
			if resp.Diagnostics.HasError() {
				return
			}
			if !allow {
				allow, diagnostics = allowDestructiveDestroyFromConfig(ctx, &req.Config)
				resp.Diagnostics.Append(diagnostics...)
				if resp.Diagnostics.HasError() {
					return
				}
			}
		}

		if err := r.checkDestroySafety(stateAttrs, allow); err != nil {
			resp.Diagnostics.AddError("Destructive destroy blocked", err.Error())
		}
		return
	}

	planAttrs, diagnostics := planToJSON(ctx, &req.Plan, r.attributes, r.blocks)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	hostConfig, hostKnown, diagnostics := resolveHostFromPlanIfKnownWithNames(ctx, &req.Plan, r.defaultHost, r.hostAttrs)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	if r.importIdentity != nil {
		targetConfig := transport.TransportConfig{}
		if hostKnown {
			targetConfig = hostConfig
		}
		targetConfig, diagnostics = preferKnownIdentityTargetConfigWithNames(ctx, req.Identity, r.importIdentity, targetConfig, r.hostAttrs)
		resp.Diagnostics.Append(diagnostics...)
		if resp.Diagnostics.HasError() {
			return
		}
		resp.Identity, diagnostics = buildResourceIdentityWithNames(ctx, r.importIdentity, planAttrs, targetConfig, r.hostAttrs)
		resp.Diagnostics.Append(diagnostics...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	if !req.State.Raw.IsNull() {
		return
	}

	if r.importRequired && shouldProbeExistingForImport(req.Identity) {
		if hostKnown {
			exists, importID, err := r.probeExisting(ctx, hostConfig, planAttrs)
			if err == nil && exists {
				resp.Diagnostics.AddError("Import required for existing object", r.importRequiredMessage(importID))
				return
			}
		}
	}
}

func (r *GenericResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	hostConfig, hostKnown, diagnostics := resolveHostFromPlanIfKnownWithNames(ctx, &req.Plan, r.defaultHost, r.hostAttrs)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !hostKnown {
		resp.Diagnostics.AddError("Host is unknown", "Resource host address is still unknown during apply.")
		return
	}

	planAttrs, diagnostics := planToJSON(ctx, &req.Plan, r.attributes, r.blocks)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	if r.validationPolicy.PreWriteValidation {
		resp.Diagnostics.Append(r.validateRemote(ctx, hostConfig, planAttrs)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	result, err := r.send(ctx, "create", hostConfig, &hostsession.OperationMessage{
		ModuleName:   r.runtimeModule,
		ResourceType: r.runtimeType,
		Action:       "create",
		Plan:         planAttrs,
	})
	if err != nil {
		resp.Diagnostics.AddError("Create failed", err.Error())
		return
	}
	if result == nil || result.State == nil {
		resp.Diagnostics.AddError("Create failed", "executor returned no state")
		return
	}

	state := result.State
	if r.shapeResult != nil {
		state, diagnostics = r.shapeResult(ctx, "create", state)
		resp.Diagnostics.Append(diagnostics...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	if r.validationPolicy.ReadAfterWrite {
		state, err = r.readBackState(ctx, hostConfig, state)
		if err != nil {
			resp.Diagnostics.AddError("Post-write verification failed", err.Error())
			return
		}
	}

	resp.Diagnostics.Append(setResourceStateFromJSON(ctx, state, r.attributes, r.blocks, &resp.State)...)
	resp.Diagnostics.Append(preserveHostFromPlanWithNames(ctx, &req.Plan, &resp.State, r.hostAttrs)...)
	resp.Diagnostics.Append(setHostKeyFingerprintStateAttributeWithNames(ctx, &resp.State, observedHostKeyFingerprintValue(r.pool, hostConfig, nullHostKeyFingerprint()), r.hostAttrs)...)
	resp.Diagnostics.Append(preserveAllowDestructiveDestroyFromPlan(ctx, &req.Plan, &resp.State)...)
	resp.Identity, diagnostics = r.buildIdentity(ctx, state, hostConfig)
	resp.Diagnostics.Append(diagnostics...)
}

func (r *GenericResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	priorFingerprint, diagnostics := hostKeyFingerprintFromStateWithNames(ctx, &req.State, r.hostAttrs)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	stateAttrs, diagnostics := stateToJSON(ctx, &req.State, r.attributes, r.blocks)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	hostConfig, diagnostics := resolveHostFromReadWithNames(ctx, &req.State, req.Identity, r.defaultHost, r.importIdentity, r.hostAttrs)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	hostConfig = applyPinnedHostKeyFingerprint(hostConfig, priorFingerprint)

	stateAttrs, diagnostics = mergeIdentityIntoStateWithNames(ctx, stateAttrs, req.Identity, r.importIdentity, hostConfig, r.hostAttrs)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	readCtx, cancel := withReadOperationTimeout(ctx)
	defer cancel()

	result, err := r.send(readCtx, "read", hostConfig, &hostsession.OperationMessage{
		ModuleName:   r.runtimeModule,
		ResourceType: r.runtimeType,
		Action:       "read",
		State:        stateAttrs,
	})
	if err != nil {
		if readCtx.Err() != nil {
			resp.Diagnostics.AddError(
				"Read canceled or timed out",
				fmt.Sprintf("Read did not complete before the read timeout (%s) or context cancellation: %s", defaultReadOperationTimeout, err.Error()),
			)
			return
		}
		if shouldDropStateOnReadError(err) {
			resp.Diagnostics.AddWarning(
				"Prior host is unreachable during refresh",
				fmt.Sprintf("Resource state still points at %s, but that host is unreachable. Removing the stale resource state so Terraform can recreate it on the current host if needed.", hostConfig.Endpoint()),
			)
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read failed", err.Error())
		return
	}

	if result == nil || result.State == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	state := result.State
	if r.shapeResult != nil {
		state, diagnostics = r.shapeResult(ctx, "read", state)
		resp.Diagnostics.Append(diagnostics...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	resp.Diagnostics.Append(setResourceStateFromJSON(ctx, state, r.attributes, r.blocks, &resp.State)...)
	resp.Diagnostics.Append(preserveHostFromStateWithNames(ctx, &req.State, &resp.State, r.hostAttrs)...)
	resp.Diagnostics.Append(setHostKeyFingerprintStateAttributeWithNames(ctx, &resp.State, observedHostKeyFingerprintValue(r.pool, hostConfig, priorFingerprint), r.hostAttrs)...)
	resp.Diagnostics.Append(preserveAllowDestructiveDestroyFromState(ctx, &req.State, &resp.State)...)
	resp.Identity, diagnostics = r.buildIdentity(ctx, state, hostConfig)
	resp.Diagnostics.Append(diagnostics...)
}

func (r *GenericResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	priorHostConfig, diagnostics := resolveHostFromStateWithNames(ctx, &req.State, r.defaultHost, r.hostAttrs)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	priorFingerprint, diagnostics := hostKeyFingerprintFromStateWithNames(ctx, &req.State, r.hostAttrs)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	hostConfig, hostKnown, diagnostics := resolveHostFromPlanIfKnownWithNames(ctx, &req.Plan, r.defaultHost, r.hostAttrs)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !hostKnown {
		resp.Diagnostics.AddError("Host is unknown", "Resource host address is still unknown during apply.")
		return
	}
	fingerprintFallback := nullHostKeyFingerprint()
	if sameTransportEndpoint(priorHostConfig, hostConfig) {
		hostConfig = applyPinnedHostKeyFingerprint(hostConfig, priorFingerprint)
		fingerprintFallback = priorFingerprint
	}

	priorAttrs, diagnostics := stateToJSON(ctx, &req.State, r.attributes, r.blocks)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	planAttrs, diagnostics := planToJSON(ctx, &req.Plan, r.attributes, r.blocks)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	if r.validationPolicy.PreWriteValidation {
		resp.Diagnostics.Append(r.validateRemote(ctx, hostConfig, planAttrs)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	result, err := r.send(ctx, "update", hostConfig, &hostsession.OperationMessage{
		ModuleName:   r.runtimeModule,
		ResourceType: r.runtimeType,
		Action:       "update",
		State:        priorAttrs,
		Plan:         planAttrs,
	})
	if err != nil {
		resp.Diagnostics.AddError("Update failed", err.Error())
		return
	}

	state := result.State
	if r.shapeResult != nil {
		state, diagnostics = r.shapeResult(ctx, "update", state)
		resp.Diagnostics.Append(diagnostics...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	if r.validationPolicy.ReadAfterWrite {
		state, err = r.readBackState(ctx, hostConfig, state)
		if err != nil {
			resp.Diagnostics.AddError("Post-write verification failed", err.Error())
			return
		}
	}

	resp.Diagnostics.Append(setResourceStateFromJSON(ctx, state, r.attributes, r.blocks, &resp.State)...)
	resp.Diagnostics.Append(preserveHostFromPlanWithNames(ctx, &req.Plan, &resp.State, r.hostAttrs)...)
	resp.Diagnostics.Append(setHostKeyFingerprintStateAttributeWithNames(ctx, &resp.State, observedHostKeyFingerprintValue(r.pool, hostConfig, fingerprintFallback), r.hostAttrs)...)
	resp.Diagnostics.Append(preserveAllowDestructiveDestroyFromPlan(ctx, &req.Plan, &resp.State)...)
	resp.Identity, diagnostics = r.buildIdentity(ctx, state, hostConfig)
	resp.Diagnostics.Append(diagnostics...)
}

func (r *GenericResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	hostConfig, diagnostics := resolveHostFromStateWithNames(ctx, &req.State, r.defaultHost, r.hostAttrs)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	priorFingerprint, diagnostics := hostKeyFingerprintFromStateWithNames(ctx, &req.State, r.hostAttrs)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	hostConfig = applyPinnedHostKeyFingerprint(hostConfig, priorFingerprint)

	stateAttrs, diagnostics := stateToJSON(ctx, &req.State, r.attributes, r.blocks)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	if isAbsentResourceState(stateAttrs) {
		return
	}

	allow, diagnostics := allowDestructiveDestroyFromStateOrPrivate(ctx, &req.State, req.Private)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.checkDestroySafety(stateAttrs, allow); err != nil {
		resp.Diagnostics.AddError("Destructive destroy blocked", err.Error())
		return
	}

	if _, err := r.send(ctx, "delete", hostConfig, &hostsession.OperationMessage{
		ModuleName:   r.runtimeModule,
		ResourceType: r.runtimeType,
		Action:       "delete",
		State:        stateAttrs,
	}); err != nil {
		resp.Diagnostics.AddError("Delete failed", err.Error())
		return
	}
}

func (r *GenericResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	hostConfig, diags := resolveHostFromImportWithNames(ctx, req, r.defaultHost, r.importIdentity, r.hostAttrs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	importID, diags := resolveImportIDWithNames(ctx, req, r.importIdentity, r.hostAttrs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	result, err := r.send(ctx, "import", hostConfig, &hostsession.OperationMessage{
		ModuleName:   r.runtimeModule,
		ResourceType: r.runtimeType,
		Action:       "import",
		ImportID:     importID,
	})
	if err != nil {
		resp.Diagnostics.AddError("Import failed", err.Error())
		return
	}
	if result == nil || result.State == nil {
		resp.Diagnostics.AddError("Import failed", "executor returned no state")
		return
	}

	state := result.State
	var diagnostics diag.Diagnostics
	if r.shapeImport != nil {
		state, diagnostics = r.shapeImport(ctx, state)
		resp.Diagnostics.Append(diagnostics...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	resp.Diagnostics.Append(setResourceStateFromJSON(ctx, state, r.attributes, r.blocks, &resp.State)...)
	if req.Identity != nil {
		_, targetConfig, diags := extractImportIdentityValuesWithNames(ctx, req.Identity, r.importIdentity, r.hostAttrs)
		resp.Diagnostics.Append(diags...)
		if strings.TrimSpace(targetConfig.Target) != "" {
			resp.Diagnostics.Append(preserveHostFromAddressWithNames(ctx, targetConfig, &resp.State, r.hostAttrs)...)
		}
	}
	resp.Diagnostics.Append(setHostKeyFingerprintStateAttributeWithNames(ctx, &resp.State, observedHostKeyFingerprintValue(r.pool, hostConfig, nullHostKeyFingerprint()), r.hostAttrs)...)
	resp.Identity, diagnostics = r.buildIdentity(ctx, state, hostConfig)
	resp.Diagnostics.Append(diagnostics...)
}

func (r *GenericResource) send(
	ctx context.Context,
	action string,
	hostConfig transport.TransportConfig,
	op *hostsession.OperationMessage,
) (*hostsession.ResultMessage, error) {
	execution, err := resolveResourceExecutionContext(ResourceDefinition{
		RequiredPrivilege: r.requiredPrivilege,
		ExecutionPolicy:   r.executionPolicy,
	}, action, op)
	if err != nil {
		return nil, fmt.Errorf("resolve execution context: %w", err)
	}
	op.Execution = execution

	locks, err := planResourceLocks(action, op, r.lockPlanner)
	if err != nil {
		return nil, err
	}

	return sendResourceOperation(ctx, r.executorMgr, r.pool, hostConfig, *op, locks)
}

func (r *GenericResource) validateRemote(ctx context.Context, hostConfig transport.TransportConfig, config map[string]interface{}) diag.Diagnostics {
	var diagnostics diag.Diagnostics

	_, err := r.send(ctx, "validate", hostConfig, &hostsession.OperationMessage{
		ModuleName:   r.runtimeModule,
		ResourceType: r.runtimeType,
		Action:       "validate",
		Config:       config,
	})
	if err != nil {
		diagnostics.AddError("Validation failed", err.Error())
	}

	return diagnostics
}

func shouldProbeExistingForImport(identity *tfsdk.ResourceIdentity) bool {
	return identity == nil
}

func (r *GenericResource) probeExisting(ctx context.Context, hostConfig transport.TransportConfig, attrs map[string]interface{}) (bool, string, error) {
	if !r.importRequired || !supportsResourceIdentity(r.importIdentity) {
		return false, "", nil
	}

	importID, err := r.importIdentity.FormatID(attrs)
	if err != nil {
		return false, "", err
	}

	probeState := make(map[string]interface{}, len(r.importIdentity.Attributes))
	for key := range r.importIdentity.Attributes {
		if value, ok := attrs[key]; ok {
			probeState[key] = value
		}
	}

	result, err := r.send(ctx, "read", hostConfig, &hostsession.OperationMessage{
		ModuleName:   r.runtimeModule,
		ResourceType: r.runtimeType,
		Action:       "read",
		State:        probeState,
	})
	if err != nil {
		return false, importID, err
	}

	return result != nil && result.State != nil, importID, nil
}

func (r *GenericResource) importRequiredMessage(importID string) string {
	return fmt.Sprintf("%s target %q already exists on the host; import it before Terraform can manage it", r.typeName, importID)
}

func (r *GenericResource) readBackState(ctx context.Context, hostConfig transport.TransportConfig, state map[string]interface{}) (map[string]interface{}, error) {
	result, err := r.send(ctx, "read", hostConfig, &hostsession.OperationMessage{
		ModuleName:   r.runtimeModule,
		ResourceType: r.runtimeType,
		Action:       "read",
		State:        state,
	})
	if err != nil {
		return nil, err
	}
	if result == nil || result.State == nil {
		return nil, fmt.Errorf("resource disappeared during post-write verification")
	}

	if r.shapeResult != nil {
		shaped, diagnostics := r.shapeResult(ctx, "read", result.State)
		if diagnostics.HasError() {
			return nil, fmt.Errorf("shape verification state: %s", diagnostics.Errors()[0].Detail())
		}
		return shaped, nil
	}

	return result.State, nil
}

func (r *GenericResource) checkDestroySafety(state map[string]interface{}, allow bool) error {
	switch r.destroySafety.Mode {
	case DestroySafetyModeNone:
		return nil
	case DestroySafetyModeNoDestroy:
		if r.destroySafety.Guard != nil {
			blocked, reason := r.destroySafety.Guard(state, r.destroySafetyC)
			if blocked {
				return fmt.Errorf("%s; destroy is not supported for %s", reason, r.typeName)
			}
		}
		return fmt.Errorf("destroy is not supported for %s", r.typeName)
	case DestroySafetyModeExplicitAllow:
		if allow {
			return nil
		}
		if r.destroySafety.RequiresExplicitAllow != nil && !r.destroySafety.RequiresExplicitAllow(state) {
			return nil
		}
		return fmt.Errorf("destroy is blocked for %s because it has side-effecting delete behavior; set allow_destructive_destroy = true and apply before destroying if you intend to permit it", r.typeName)
	case DestroySafetyModeCriticalObject:
		if allow {
			return nil
		}
		if r.destroySafety.Guard == nil {
			return nil
		}
		blocked, reason := r.destroySafety.Guard(state, r.destroySafetyC)
		if !blocked {
			return nil
		}
		return fmt.Errorf("%s; set allow_destructive_destroy = true and apply before destroying if you intend to permit it", reason)
	default:
		return nil
	}
}

func allowDestructiveDestroyFromState(ctx context.Context, state *tfsdk.State) (bool, diag.Diagnostics) {
	if state == nil || !resourceSchemaHasAttribute(state.Schema, "allow_destructive_destroy") {
		return false, nil
	}

	var allow types.Bool
	diagnostics := state.GetAttribute(ctx, path.Root("allow_destructive_destroy"), &allow)
	if diagnostics.HasError() || allow.IsNull() || allow.IsUnknown() {
		return false, diagnostics
	}
	return allow.ValueBool(), diagnostics
}

func allowDestructiveDestroyFromConfig(ctx context.Context, config *tfsdk.Config) (bool, diag.Diagnostics) {
	if config == nil || config.Schema == nil || !resourceSchemaHasAttribute(config.Schema, "allow_destructive_destroy") {
		return false, nil
	}

	var allow types.Bool
	diagnostics := config.GetAttribute(ctx, path.Root("allow_destructive_destroy"), &allow)
	if diagnostics.HasError() || allow.IsNull() || allow.IsUnknown() {
		return false, diagnostics
	}
	return allow.ValueBool(), diagnostics
}

func allowDestructiveDestroyFromStateOrPrivate(ctx context.Context, state *tfsdk.State, privateState privateStateReader) (bool, diag.Diagnostics) {
	allow, diagnostics := allowDestructiveDestroyFromState(ctx, state)
	if diagnostics.HasError() || allow {
		return allow, diagnostics
	}

	allowFromPrivate, privateDiagnostics := readGenericAllowDestroyFromPrivate(ctx, privateState)
	diagnostics.Append(privateDiagnostics...)
	if diagnostics.HasError() {
		return false, diagnostics
	}

	return allowFromPrivate, diagnostics
}

func storeGenericAllowDestroyFromConfig(ctx context.Context, config *tfsdk.Config, privateState privateStateWriter) diag.Diagnostics {
	if config == nil || config.Schema == nil || privateState == nil {
		return nil
	}

	var allow types.Bool
	diagnostics := config.GetAttribute(ctx, path.Root("allow_destructive_destroy"), &allow)
	if diagnostics.HasError() || allow.IsNull() || allow.IsUnknown() {
		return diagnostics
	}

	raw, err := json.Marshal(allow.ValueBool())
	if err != nil {
		diagnostics.AddError("Encode planned destructive destroy flag", fmt.Sprintf("Failed to encode planned allow_destructive_destroy value: %s", err))
		return diagnostics
	}

	diagnostics.Append(privateState.SetKey(ctx, genericAllowDestroyKey, raw)...)
	return diagnostics
}

func readGenericAllowDestroyFromPrivate(ctx context.Context, privateState privateStateReader) (bool, diag.Diagnostics) {
	if privateState == nil {
		return false, nil
	}

	raw, diagnostics := privateState.GetKey(ctx, genericAllowDestroyKey)
	if diagnostics.HasError() || len(raw) == 0 {
		return false, diagnostics
	}

	var allow bool
	if err := json.Unmarshal(raw, &allow); err != nil {
		diagnostics.AddError("Decode planned destructive destroy flag", fmt.Sprintf("Failed to decode planned allow_destructive_destroy value: %s", err))
		return false, diagnostics
	}

	return allow, diagnostics
}

func isAbsentResourceState(state map[string]interface{}) bool {
	if state == nil {
		return false
	}
	if ensure, ok := state["ensure"].(string); ok && ensure == "absent" {
		return true
	}
	if exists, ok := state["exists"].(bool); ok && !exists {
		return true
	}
	return false
}

func planToJSON(ctx context.Context, plan *tfsdk.Plan, attrs map[string]resourceschema.Attribute, blocks map[string]resourceschema.Block) (map[string]interface{}, diag.Diagnostics) {
	return extractResourceValuesFromPlan(ctx, plan, attrs, blocks)
}

func stateToJSON(ctx context.Context, state *tfsdk.State, attrs map[string]resourceschema.Attribute, blocks map[string]resourceschema.Block) (map[string]interface{}, diag.Diagnostics) {
	return extractResourceValuesFromState(ctx, state, attrs, blocks)
}

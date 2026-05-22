package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/hostsession"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
)

var (
	_ resource.Resource              = (*RebootBarrierResource)(nil)
	_ resource.ResourceWithConfigure = (*RebootBarrierResource)(nil)
)

type RebootBarrierResource struct {
	typeName    string
	executorMgr *hostsession.ExecutorManager
	pool        *transport.ConnectionPool
	defaultHost *transport.TransportConfig
}

type rebootBarrierModel struct {
	ID                 types.String `tfsdk:"id"`
	Reason             types.String `tfsdk:"reason"`
	Triggers           types.Map    `tfsdk:"triggers"`
	TimeoutSeconds     types.Int64  `tfsdk:"timeout_seconds"`
	SettleSeconds      types.Int64  `tfsdk:"settle_seconds"`
	RebootCommand      types.String `tfsdk:"reboot_command"`
	Phase              types.String `tfsdk:"phase"`
	OperationID        types.String `tfsdk:"operation_id"`
	StableHostID       types.String `tfsdk:"stable_host_id"`
	PreBootID          types.String `tfsdk:"pre_boot_id"`
	PostBootID         types.String `tfsdk:"post_boot_id"`
	TriggersHash       types.String `tfsdk:"triggers_hash"`
	CompletedAt        types.String `tfsdk:"completed_at"`
	LastError          types.String `tfsdk:"last_error"`
	Target             types.String `tfsdk:"target"`
	Port               types.Int64  `tfsdk:"port"`
	Transport          types.String `tfsdk:"transport"`
	HostKeyFingerprint types.String `tfsdk:"host_key_fingerprint"`
}

func NewRebootBarrierResource(typeName string) *RebootBarrierResource {
	return &RebootBarrierResource{typeName: typeName}
}

func (r *RebootBarrierResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = r.typeName
}

func (r *RebootBarrierResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resourceschema.Schema{
		Description: "Acts as a Terraform DAG barrier that safely reboots a host and resumes after it returns on a new boot instance.",
		Attributes: map[string]resourceschema.Attribute{
			"id": resourceschema.StringAttribute{
				Description: "Stable reboot barrier identifier derived from host and reason.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"reason": resourceschema.StringAttribute{
				Description: "Logical reason for the reboot barrier.",
				Required:    true,
			},
			"triggers": resourceschema.MapAttribute{
				Description: "Trigger values that cause the reboot barrier to run again when they change.",
				Required:    true,
				ElementType: types.StringType,
			},
			"timeout_seconds": resourceschema.Int64Attribute{
				Description: "Maximum time to wait for reboot and reconnect.",
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(900),
			},
			"settle_seconds": resourceschema.Int64Attribute{
				Description: "Additional settle time after boot proof succeeds.",
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(15),
			},
			"reboot_command": resourceschema.StringAttribute{
				Description: "Optional explicit reboot command override.",
				Optional:    true,
			},
			"phase": resourceschema.StringAttribute{
				Description: "Current persisted reboot phase.",
				Computed:    true,
			},
			"operation_id": resourceschema.StringAttribute{
				Description: "Provider-side reboot operation identifier.",
				Computed:    true,
			},
			"stable_host_id": resourceschema.StringAttribute{
				Description: "Stable host identity proven across the reboot.",
				Computed:    true,
			},
			"pre_boot_id": resourceschema.StringAttribute{
				Description: "Boot identifier observed before reboot.",
				Computed:    true,
			},
			"post_boot_id": resourceschema.StringAttribute{
				Description: "Boot identifier observed after reboot.",
				Computed:    true,
			},
			"triggers_hash": resourceschema.StringAttribute{
				Description: "Deterministic hash of the trigger map used for state reconciliation.",
				Computed:    true,
			},
			"completed_at": resourceschema.StringAttribute{
				Description: "Completion timestamp for the last successful reboot barrier execution.",
				Computed:    true,
			},
			"last_error": resourceschema.StringAttribute{
				Description: "Last persisted reboot failure detail, if any.",
				Computed:    true,
			},
			targetAttributeName:             commonResourceTargetAttribute(),
			portAttributeName:               commonResourcePortAttribute(),
			transportAttributeName:          commonResourceTransportAttribute(),
			hostKeyFingerprintAttributeName: commonResourceHostKeyFingerprintAttribute(),
		},
	}
}

func (r *RebootBarrierResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Provider Data", fmt.Sprintf("Expected *engine.ProviderData, got %T", req.ProviderData))
		return
	}

	r.executorMgr = pd.ExecutorMgr
	r.pool = pd.Pool
	r.defaultHost = pd.DefaultHost
}

func (r *RebootBarrierResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan rebootBarrierModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.applyPlan(ctx, nil, plan, &resp.Diagnostics, &resp.State)
}

func (r *RebootBarrierResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var prior rebootBarrierModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var plan rebootBarrierModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.applyPlan(ctx, &prior, plan, &resp.Diagnostics, &resp.State)
}

func (r *RebootBarrierResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state rebootBarrierModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	phase := strings.TrimSpace(state.Phase.ValueString())
	if phase == "" {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}

	if phase != executorRebootPhaseCompleted() {
		detail := fmt.Sprintf("reboot barrier %s is in non-terminal phase %q", state.ID.ValueString(), phase)
		if lastError := strings.TrimSpace(state.LastError.ValueString()); lastError != "" {
			detail = detail + ": " + lastError
		}
		resp.Diagnostics.AddError("Reboot barrier requires reconciliation", detail)
		return
	}

	if r.executorMgr != nil && r.pool != nil {
		hostConfig, hostErr := r.buildTransportConfig(state.Target, state.Port, state.Transport)
		if hostErr != nil {
			resp.Diagnostics.AddError("Invalid host configuration", hostErr.Error())
			return
		}
		hostConfig = applyPinnedHostKeyFingerprint(hostConfig, state.HostKeyFingerprint)

		session, err := r.pool.GetOrCreate(ctx, hostConfig)
		if err != nil {
			resp.Diagnostics.AddError("Get session failed", err.Error())
			return
		}

		identity, err := r.executorMgr.ReadHostIdentity(ctx, session)
		if err != nil {
			resp.Diagnostics.AddError("Read host identity failed", err.Error())
			return
		}

		if stableHostID := strings.TrimSpace(state.StableHostID.ValueString()); stableHostID != "" && identity.StableHostID != stableHostID {
			resp.Diagnostics.AddError(
				"Reboot barrier host identity changed",
				fmt.Sprintf("expected stable host identity %q, got %q", stableHostID, identity.StableHostID),
			)
			return
		}

		state.HostKeyFingerprint = observedHostKeyFingerprintValue(r.pool, hostConfig, state.HostKeyFingerprint)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *RebootBarrierResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state rebootBarrierModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if r.executorMgr != nil && r.pool != nil {
		hostConfig, hostErr := r.buildTransportConfig(state.Target, state.Port, state.Transport)
		if hostErr == nil {
			hostConfig = applyPinnedHostKeyFingerprint(hostConfig, state.HostKeyFingerprint)
			session, err := r.pool.GetOrCreate(ctx, hostConfig)
			if err == nil {
				if cleanupErr := r.executorMgr.CleanupRebootArtifacts(ctx, session, strings.TrimSpace(state.OperationID.ValueString())); cleanupErr != nil {
					resp.Diagnostics.AddWarning("Reboot barrier cleanup incomplete", cleanupErr.Error())
				}
			}
		}
	}

	resp.State.RemoveResource(ctx)
}

func (r *RebootBarrierResource) applyPlan(
	ctx context.Context,
	prior *rebootBarrierModel,
	plan rebootBarrierModel,
	diags *diag.Diagnostics,
	state *tfsdk.State,
) {
	if r.executorMgr == nil || r.pool == nil {
		diags.AddError("Resource not configured", "Provider configuration was not available to the reboot barrier resource.")
		return
	}

	hostConfig, hostErr := r.buildTransportConfig(plan.Target, plan.Port, plan.Transport)
	if hostErr != nil {
		diags.AddError("Invalid host configuration", hostErr.Error())
		return
	}
	fingerprintFallback := nullHostKeyFingerprint()
	if prior != nil {
		priorHostConfig, err := r.buildTransportConfig(prior.Target, prior.Port, prior.Transport)
		if err == nil && sameTransportEndpoint(priorHostConfig, hostConfig) {
			hostConfig = applyPinnedHostKeyFingerprint(hostConfig, prior.HostKeyFingerprint)
			fingerprintFallback = normalizeHostKeyFingerprintValue(prior.HostKeyFingerprint)
		}
	}

	triggerValues, triggerErr := rebootBarrierTriggerValues(ctx, plan.Triggers)
	if triggerErr != nil {
		diags.AddError("Invalid triggers", triggerErr.Error())
		return
	}

	triggersDigest, err := digestRebootBarrierTriggers(triggerValues)
	if err != nil {
		diags.AddError("Digest triggers failed", err.Error())
		return
	}

	resourceID := buildRebootBarrierID(hostConfig.Endpoint(), plan.Reason.ValueString())
	updatedState := plan
	updatedState.ID = types.StringValue(resourceID)
	updatedState.TriggersHash = types.StringValue(triggersDigest)
	updatedState.HostKeyFingerprint = fingerprintFallback

	if shouldSkipRebootBarrier(prior, resourceID, triggersDigest) {
		updatedState = copyRebootBarrierExecutionState(updatedState, *prior)
		diags.Append(state.Set(ctx, &updatedState)...)
		return
	}

	session, err := r.pool.GetOrCreate(ctx, hostConfig)
	if err != nil {
		diags.AddError("Get session failed", err.Error())
		return
	}

	result, err := r.executorMgr.RunRebootBarrier(ctx, session, hostsession.RebootBarrierParams{
		ResourceID:    resourceID,
		Reason:        strings.TrimSpace(plan.Reason.ValueString()),
		TriggersHash:  triggersDigest,
		RebootCommand: strings.TrimSpace(plan.RebootCommand.ValueString()),
		Timeout:       int64ToDuration(plan.TimeoutSeconds, 900),
		Settle:        int64ToDuration(plan.SettleSeconds, 15),
	})
	if result != nil {
		updatedState = applyRebootBarrierResult(updatedState, result)
	}
	updatedState.HostKeyFingerprint = observedHostKeyFingerprintValue(r.pool, hostConfig, fingerprintFallback)

	if err != nil {
		diags.AddError("Reboot barrier failed", err.Error())
		return
	}

	diags.Append(state.Set(ctx, &updatedState)...)
}

func (r *RebootBarrierResource) buildTransportConfig(target types.String, port types.Int64, transportName types.String) (transport.TransportConfig, error) {
	config, diagnostics := buildTransportConfig(target, port, transportName, r.defaultHost, nil)
	if diagnostics.HasError() {
		return transport.TransportConfig{}, fmt.Errorf("%s", diagnostics.Errors()[0].Detail())
	}
	return config, nil
}

func rebootBarrierTriggerValues(ctx context.Context, triggers types.Map) (map[string]string, error) {
	if triggers.IsNull() || triggers.IsUnknown() {
		return nil, fmt.Errorf("triggers must be fully known")
	}

	values := make(map[string]string)
	if diags := triggers.ElementsAs(ctx, &values, false); diags.HasError() {
		return nil, fmt.Errorf("decode triggers: %s", diags.Errors()[0].Detail())
	}
	return values, nil
}

func digestRebootBarrierTriggers(triggers map[string]string) (string, error) {
	data, err := json.Marshal(triggers)
	if err != nil {
		return "", fmt.Errorf("marshal reboot barrier triggers: %w", err)
	}
	return digestutil.DigestBytes(digestutil.AlgorithmXXH3_128, data)
}

func buildRebootBarrierID(hostAddress, reason string) string {
	return strings.TrimSpace(hostAddress) + "|" + strings.TrimSpace(reason)
}

func int64ToDuration(value types.Int64, fallbackSeconds int64) time.Duration {
	seconds := fallbackSeconds
	if !value.IsNull() && !value.IsUnknown() {
		seconds = value.ValueInt64()
	}
	return time.Duration(seconds) * time.Second
}

func applyRebootBarrierResult(state rebootBarrierModel, result *hostsession.RestartHostResult) rebootBarrierModel {
	state.Phase = stringValueOrNull(result.Phase)
	state.OperationID = stringValueOrNull(result.OperationID)
	state.StableHostID = stringValueOrNull(result.StableHostID)
	state.PreBootID = stringValueOrNull(result.PreBootID)
	state.PostBootID = stringValueOrNull(result.PostBootID)
	state.LastError = stringValueOrNull(result.LastError)
	if result.CompletedAt != nil {
		state.CompletedAt = types.StringValue(result.CompletedAt.UTC().Format(time.RFC3339))
	} else {
		state.CompletedAt = types.StringNull()
	}
	return state
}

func copyRebootBarrierExecutionState(dst, src rebootBarrierModel) rebootBarrierModel {
	dst.Phase = src.Phase
	dst.OperationID = src.OperationID
	dst.StableHostID = src.StableHostID
	dst.PreBootID = src.PreBootID
	dst.PostBootID = src.PostBootID
	dst.CompletedAt = src.CompletedAt
	dst.LastError = src.LastError
	dst.HostKeyFingerprint = normalizeHostKeyFingerprintValue(src.HostKeyFingerprint)
	return dst
}

func shouldSkipRebootBarrier(prior *rebootBarrierModel, resourceID, triggersHash string) bool {
	if prior == nil {
		return false
	}
	if strings.TrimSpace(prior.ID.ValueString()) != strings.TrimSpace(resourceID) {
		return false
	}
	if strings.TrimSpace(prior.TriggersHash.ValueString()) != strings.TrimSpace(triggersHash) {
		return false
	}
	return strings.TrimSpace(prior.Phase.ValueString()) == executorRebootPhaseCompleted()
}

func executorRebootPhaseCompleted() string {
	return "completed"
}

func stringValueOrNull(value string) types.String {
	if strings.TrimSpace(value) == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

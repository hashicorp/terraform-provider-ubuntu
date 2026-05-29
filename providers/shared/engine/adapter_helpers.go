// Copyright IBM Corp. 2026

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	actionschema "github.com/hashicorp/terraform-plugin-framework/action/schema"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/hostsession"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
	"github.com/hashicorp/terraform-provider-ubuntu/shared/hostrpc"
)

const (
	targetAttributeName                   = "target"
	portAttributeName                     = "port"
	transportAttributeName                = "transport"
	hostKeyFingerprintAttributeName       = "host_key_fingerprint"
	targetHostAttributeName               = "target_host"
	targetPortAttributeName               = "target_port"
	targetTransportAttributeName          = "target_transport"
	targetHostKeyFingerprintAttributeName = "target_host_key_fingerprint"
)

type transportAttributeNames struct {
	target             string
	port               string
	transport          string
	hostKeyFingerprint string
}

func defaultTransportAttributeNames() transportAttributeNames {
	return transportAttributeNames{
		target:             targetAttributeName,
		port:               portAttributeName,
		transport:          transportAttributeName,
		hostKeyFingerprint: hostKeyFingerprintAttributeName,
	}
}

func transportAttributeNamesForResourceSchema(attrs map[string]resourceschema.Attribute, customize ResourceSchemaCustomizer) transportAttributeNames {
	result := make(map[string]resourceschema.Attribute, len(attrs))
	for key, value := range attrs {
		result[key] = value
	}
	if customize != nil {
		result = customize(result)
	}

	names := defaultTransportAttributeNames()
	if _, ok := result[targetAttributeName]; ok {
		names.target = targetHostAttributeName
	}
	if _, ok := result[portAttributeName]; ok {
		names.port = targetPortAttributeName
	}
	if _, ok := result[transportAttributeName]; ok {
		names.transport = targetTransportAttributeName
	}
	if _, ok := result[hostKeyFingerprintAttributeName]; ok {
		names.hostKeyFingerprint = targetHostKeyFingerprintAttributeName
	}
	return names
}

func resourceSchemaAttributes(
	attrs map[string]resourceschema.Attribute,
	customize ResourceSchemaCustomizer,
	destroySafety DestroySafetyPolicy,
) map[string]resourceschema.Attribute {
	extraAttrs := 5
	if destroySafetyUsesAllowFlag(destroySafety.Mode) {
		extraAttrs++
	}
	result := make(map[string]resourceschema.Attribute, len(attrs)+extraAttrs)
	for key, value := range attrs {
		result[key] = value
	}
	if customize != nil {
		result = customize(result)
	}
	hostAttrs := defaultTransportAttributeNames()
	if _, ok := result[targetAttributeName]; ok {
		hostAttrs.target = targetHostAttributeName
	}
	if _, ok := result[portAttributeName]; ok {
		hostAttrs.port = targetPortAttributeName
	}
	if _, ok := result[transportAttributeName]; ok {
		hostAttrs.transport = targetTransportAttributeName
	}
	if _, ok := result[hostKeyFingerprintAttributeName]; ok {
		hostAttrs.hostKeyFingerprint = targetHostKeyFingerprintAttributeName
	}

	result["id"] = resourceschema.StringAttribute{
		Description: "Unique identifier for this resource.",
		Computed:    true,
		PlanModifiers: []planmodifier.String{
			stringplanmodifier.UseStateForUnknown(),
		},
	}
	result[hostAttrs.target] = commonResourceTargetAttribute()
	result[hostAttrs.port] = commonResourcePortAttribute()
	result[hostAttrs.transport] = commonResourceTransportAttribute()
	result[hostAttrs.hostKeyFingerprint] = commonResourceHostKeyFingerprintAttribute()
	if destroySafetyUsesAllowFlag(destroySafety.Mode) {
		result["allow_destructive_destroy"] = resourceschema.BoolAttribute{
			Description: "Explicitly allow destructive destroy for protected or side-effecting host objects managed by this resource.",
			Optional:    true,
		}
	}
	return result
}

func resourceSchemaBlocks(blocks map[string]resourceschema.Block) map[string]resourceschema.Block {
	if len(blocks) == 0 {
		return nil
	}

	result := make(map[string]resourceschema.Block, len(blocks))
	for key, value := range blocks {
		result[key] = value
	}
	return result
}

func destroySafetyUsesAllowFlag(mode DestroySafetyMode) bool {
	switch mode {
	case DestroySafetyModeCriticalObject, DestroySafetyModeExplicitAllow:
		return true
	default:
		return false
	}
}

func dataSourceSchemaAttributes(
	attrs map[string]datasourceschema.Attribute,
	customize DataSourceSchemaCustomizer,
) map[string]datasourceschema.Attribute {
	result := make(map[string]datasourceschema.Attribute, len(attrs)+4)
	for key, value := range attrs {
		result[key] = value
	}
	if customize != nil {
		result = customize(result)
	}

	result["id"] = datasourceschema.StringAttribute{
		Description: "Unique identifier for this data source.",
		Computed:    true,
	}
	result[targetAttributeName] = commonDataSourceTargetAttribute()
	result[portAttributeName] = commonDataSourcePortAttribute()
	result[transportAttributeName] = commonDataSourceTransportAttribute()
	return result
}

func actionSchemaAttributes(
	attrs map[string]actionschema.Attribute,
	customize ActionSchemaCustomizer,
) map[string]actionschema.Attribute {
	result := make(map[string]actionschema.Attribute, len(attrs)+3)
	for key, value := range attrs {
		result[key] = value
	}
	if customize != nil {
		result = customize(result)
	}

	result[targetAttributeName] = commonActionTargetAttribute()
	result[portAttributeName] = commonActionPortAttribute()
	result[transportAttributeName] = commonActionTransportAttribute()
	return result
}

func commonResourceTargetAttribute() resourceschema.StringAttribute {
	return resourceschema.StringAttribute{
		Description: "Target host or address for this resource. Overrides provider default_target.target.",
		Optional:    true,
	}
}

func commonResourcePortAttribute() resourceschema.Int64Attribute {
	return resourceschema.Int64Attribute{
		Description: "Target port for this resource. Overrides provider default_target.port.",
		Optional:    true,
	}
}

func commonResourceTransportAttribute() resourceschema.StringAttribute {
	return resourceschema.StringAttribute{
		Description: "Transport for this resource. The current provider surface supports ssh.",
		Optional:    true,
	}
}

func commonResourceHostKeyFingerprintAttribute() resourceschema.StringAttribute {
	return resourceschema.StringAttribute{
		Description: "Observed SSH host key fingerprint for this resource target. The provider records the first accepted fingerprint and rejects unexpected changes when reconnecting to the same target.",
		Computed:    true,
		Sensitive:   true,
		PlanModifiers: []planmodifier.String{
			stringplanmodifier.UseStateForUnknown(),
		},
	}
}

func commonDataSourceTargetAttribute() datasourceschema.StringAttribute {
	return datasourceschema.StringAttribute{
		Description: "Target host or address for this data source. Overrides provider default_target.target.",
		Optional:    true,
		Computed:    true,
	}
}

func commonDataSourcePortAttribute() datasourceschema.Int64Attribute {
	return datasourceschema.Int64Attribute{
		Description: "Target port for this data source. Overrides provider default_target.port.",
		Optional:    true,
		Computed:    true,
	}
}

func commonDataSourceTransportAttribute() datasourceschema.StringAttribute {
	return datasourceschema.StringAttribute{
		Description: "Transport for this data source. The current provider surface supports ssh.",
		Optional:    true,
		Computed:    true,
	}
}

func commonActionTargetAttribute() actionschema.StringAttribute {
	return actionschema.StringAttribute{
		Description: "Target host or address for this action. Overrides provider default_target.target.",
		Optional:    true,
	}
}

func commonActionPortAttribute() actionschema.Int64Attribute {
	return actionschema.Int64Attribute{
		Description: "Target port for this action. Overrides provider default_target.port.",
		Optional:    true,
	}

}

func commonActionTransportAttribute() actionschema.StringAttribute {
	return actionschema.StringAttribute{
		Description: "Transport for this action. The current provider surface supports ssh.",
		Optional:    true,
	}
}

func resolveHostFromPlan(ctx context.Context, plan *tfsdk.Plan, defaultHost *transport.TransportConfig) (transport.TransportConfig, diag.Diagnostics) {
	return resolveHostFromPlanWithNames(ctx, plan, defaultHost, defaultTransportAttributeNames())
}

func resolveHostFromPlanWithNames(ctx context.Context, plan *tfsdk.Plan, defaultHost *transport.TransportConfig, names transportAttributeNames) (transport.TransportConfig, diag.Diagnostics) {
	target := types.StringNull()
	port := types.Int64Null()
	transportName := types.StringNull()
	diagnostics := plan.GetAttribute(ctx, path.Root(names.target), &target)
	diagnostics.Append(plan.GetAttribute(ctx, path.Root(names.port), &port)...)
	diagnostics.Append(plan.GetAttribute(ctx, path.Root(names.transport), &transportName)...)
	return buildTransportConfig(target, port, transportName, defaultHost, diagnostics)
}

func resolveHostFromPlanIfKnown(ctx context.Context, plan *tfsdk.Plan, defaultHost *transport.TransportConfig) (transport.TransportConfig, bool, diag.Diagnostics) {
	return resolveHostFromPlanIfKnownWithNames(ctx, plan, defaultHost, defaultTransportAttributeNames())
}

func resolveHostFromPlanIfKnownWithNames(ctx context.Context, plan *tfsdk.Plan, defaultHost *transport.TransportConfig, names transportAttributeNames) (transport.TransportConfig, bool, diag.Diagnostics) {
	target := types.StringNull()
	port := types.Int64Null()
	transportName := types.StringNull()
	diagnostics := plan.GetAttribute(ctx, path.Root(names.target), &target)
	diagnostics.Append(plan.GetAttribute(ctx, path.Root(names.port), &port)...)
	diagnostics.Append(plan.GetAttribute(ctx, path.Root(names.transport), &transportName)...)
	return buildTransportConfigIfKnown(target, port, transportName, defaultHost, diagnostics)
}

func resolveHostFromState(ctx context.Context, state *tfsdk.State, defaultHost *transport.TransportConfig) (transport.TransportConfig, diag.Diagnostics) {
	return resolveHostFromStateWithNames(ctx, state, defaultHost, defaultTransportAttributeNames())
}

func resolveHostFromStateWithNames(ctx context.Context, state *tfsdk.State, defaultHost *transport.TransportConfig, names transportAttributeNames) (transport.TransportConfig, diag.Diagnostics) {
	target := types.StringNull()
	port := types.Int64Null()
	transportName := types.StringNull()
	diagnostics := state.GetAttribute(ctx, path.Root(names.target), &target)
	diagnostics.Append(state.GetAttribute(ctx, path.Root(names.port), &port)...)
	diagnostics.Append(state.GetAttribute(ctx, path.Root(names.transport), &transportName)...)
	return buildTransportConfig(target, port, transportName, defaultHost, diagnostics)
}

func resolveHostFromConfig(ctx context.Context, config *tfsdk.Config, defaultHost *transport.TransportConfig) (transport.TransportConfig, diag.Diagnostics) {
	return resolveHostFromConfigWithNames(ctx, config, defaultHost, defaultTransportAttributeNames())
}

func resolveHostFromConfigWithNames(ctx context.Context, config *tfsdk.Config, defaultHost *transport.TransportConfig, names transportAttributeNames) (transport.TransportConfig, diag.Diagnostics) {
	target := types.StringNull()
	port := types.Int64Null()
	transportName := types.StringNull()
	diagnostics := config.GetAttribute(ctx, path.Root(names.target), &target)
	diagnostics.Append(config.GetAttribute(ctx, path.Root(names.port), &port)...)
	diagnostics.Append(config.GetAttribute(ctx, path.Root(names.transport), &transportName)...)
	return buildTransportConfig(target, port, transportName, defaultHost, diagnostics)
}

func resolveHostFromConfigIfKnown(ctx context.Context, config *tfsdk.Config, defaultHost *transport.TransportConfig) (transport.TransportConfig, bool, diag.Diagnostics) {
	return resolveHostFromConfigIfKnownWithNames(ctx, config, defaultHost, defaultTransportAttributeNames())
}

func resolveHostFromConfigIfKnownWithNames(ctx context.Context, config *tfsdk.Config, defaultHost *transport.TransportConfig, names transportAttributeNames) (transport.TransportConfig, bool, diag.Diagnostics) {
	target := types.StringNull()
	port := types.Int64Null()
	transportName := types.StringNull()
	diagnostics := config.GetAttribute(ctx, path.Root(names.target), &target)
	diagnostics.Append(config.GetAttribute(ctx, path.Root(names.port), &port)...)
	diagnostics.Append(config.GetAttribute(ctx, path.Root(names.transport), &transportName)...)
	return buildTransportConfigIfKnown(target, port, transportName, defaultHost, diagnostics)
}

func buildTransportConfig(target types.String, port types.Int64, transportName types.String, defaultHost *transport.TransportConfig, diagnostics diag.Diagnostics) (transport.TransportConfig, diag.Diagnostics) {
	config, _, diagnostics := buildTransportConfigIfKnown(target, port, transportName, defaultHost, diagnostics)
	return config, diagnostics
}

func buildTransportConfigIfKnown(target types.String, port types.Int64, transportName types.String, defaultHost *transport.TransportConfig, diagnostics diag.Diagnostics) (transport.TransportConfig, bool, diag.Diagnostics) {
	if diagnostics.HasError() {
		return transport.TransportConfig{}, false, diagnostics
	}
	if target.IsUnknown() || port.IsUnknown() || transportName.IsUnknown() {
		return transport.TransportConfig{}, false, diagnostics
	}

	config := transport.TransportConfig{}
	if defaultHost != nil {
		config = *defaultHost
	}
	if !target.IsNull() {
		config.Target = strings.TrimSpace(target.ValueString())
	}
	if !port.IsNull() {
		config.Port = int(port.ValueInt64())
	}
	if !transportName.IsNull() {
		config.Transport = strings.TrimSpace(transportName.ValueString())
	}

	return finalizeTransportConfig(config, diagnostics)
}

func finalizeTransportConfig(config transport.TransportConfig, diagnostics diag.Diagnostics) (transport.TransportConfig, bool, diag.Diagnostics) {
	if diagnostics.HasError() {
		return transport.TransportConfig{}, false, diagnostics
	}

	config.Target = strings.TrimSpace(config.Target)
	config.Transport = strings.TrimSpace(config.Transport)
	if config.Transport == "" {
		config.Transport = transport.TransportSSH
	}
	config.Transport = strings.ToLower(config.Transport)

	if config.Port < 0 {
		diagnostics.AddError(
			"Invalid port",
			fmt.Sprintf("port resolves to %d. Set a positive port value.", config.Port),
		)
		return transport.TransportConfig{}, false, diagnostics
	}
	if config.Transport != transport.TransportSSH && config.Transport != transport.TransportLocal {
		diagnostics.AddError(
			"Unsupported transport",
			fmt.Sprintf("transport resolves to %q. The current provider surface supports ssh.", config.Transport),
		)
		return transport.TransportConfig{}, false, diagnostics
	}
	if config.Transport == transport.TransportLocal {
		if config.Target == "" {
			config.Target = transport.TransportLocal
		}
		return config, true, diagnostics
	}
	if config.Target == "" {
		diagnostics.AddError(
			"No target configured",
			"Either set the target attribute or configure default_target on the provider.",
		)
		return transport.TransportConfig{}, false, diagnostics
	}
	if config.Port == 0 {
		config.Port = transport.DefaultSSHPort
	}
	return config, true, diagnostics
}

func sameTransportEndpoint(left, right transport.TransportConfig) bool {
	if left.NormalizedTransport() != right.NormalizedTransport() {
		return false
	}
	if left.NormalizedTarget() != right.NormalizedTarget() {
		return false
	}
	if left.IsLocal() || right.IsLocal() {
		return left.IsLocal() && right.IsLocal()
	}
	return left.ResolvedPort() == right.ResolvedPort()
}

func defaultExecutionContext(requiredPrivilege string, sources ...map[string]interface{}) *hostrpc.ExecutionContext {
	switch requiredPrivilege {
	case "root":
		return &hostrpc.ExecutionContext{Become: true}
	case "dynamic":
		return &hostrpc.ExecutionContext{
			Become:     true,
			BecomeUser: effectiveRunAs(sources...),
		}
	default:
		return nil
	}
}

func resolveResourceExecutionContext(def ResourceDefinition, action string, op *hostsession.OperationMessage) (*hostrpc.ExecutionContext, error) {
	if def.ExecutionPolicy != nil {
		return def.ExecutionPolicy(action, op)
	}
	return defaultExecutionContext(def.RequiredPrivilege, op.Plan, op.State, op.Config), nil
}

func resolveDataSourceExecutionContext(def DataSourceDefinition, action string, op *hostsession.OperationMessage) (*hostrpc.ExecutionContext, error) {
	if def.ExecutionPolicy != nil {
		return def.ExecutionPolicy(action, op)
	}
	return nil, nil
}

func resolveActionExecutionContext(def ActionDefinition, config map[string]interface{}) (*hostrpc.ExecutionContext, error) {
	if def.ExecutionPolicy != nil {
		return def.ExecutionPolicy(config)
	}
	return defaultExecutionContext(def.RequiredPrivilege, config), nil
}

func effectiveRunAs(sources ...map[string]interface{}) string {
	for _, attrs := range sources {
		if attrs == nil {
			continue
		}
		if runAs, ok := attrs["run_as"].(string); ok {
			return runAs
		}
	}
	return ""
}

func planResourceLocks(action string, op *hostsession.OperationMessage, planner LockPlanner) ([]hostsession.LockDescriptor, error) {
	locks := DefaultLockSet(action)
	if planner == nil {
		return locks, nil
	}
	plannedLocks, err := planner(action, op)
	if err != nil {
		return nil, fmt.Errorf("plan locks for %s: %w", action, err)
	}
	return NormalizeLockSet(action, plannedLocks), nil
}

func planDataSourceLocks(action string, op *hostsession.OperationMessage, planner func(string, *hostsession.OperationMessage) ([]hostsession.LockDescriptor, error)) ([]hostsession.LockDescriptor, error) {
	locks := DefaultLockSet(action)
	if planner == nil {
		return locks, nil
	}
	plannedLocks, err := planner(action, op)
	if err != nil {
		return nil, fmt.Errorf("plan locks for %s: %w", action, err)
	}
	return NormalizeLockSet(action, plannedLocks), nil
}

func planActionLocks(action string, config map[string]interface{}, planner ActionLockPlanner) ([]hostsession.LockDescriptor, error) {
	locks := DefaultLockSet(action)
	if planner == nil {
		return locks, nil
	}
	plannedLocks, err := planner(config)
	if err != nil {
		return nil, fmt.Errorf("plan locks for %s: %w", action, err)
	}
	return NormalizeLockSet(action, plannedLocks), nil
}

func sendOperation(
	ctx context.Context,
	manager *hostsession.ExecutorManager,
	pool *transport.ConnectionPool,
	hostConfig transport.TransportConfig,
	op hostsession.OperationMessage,
	locks []hostsession.LockDescriptor,
) (*hostsession.ResultMessage, error) {
	session, err := pool.GetOrCreate(ctx, hostConfig)
	if err != nil {
		return nil, fmt.Errorf("get session for %s: %w", hostConfig.Endpoint(), err)
	}

	if err := manager.EnsureExecutor(ctx, session); err != nil {
		return nil, annotateOperationError(hostConfig, op, fmt.Errorf("ensure executor: %w", err))
	}

	if operationMutatesHost(op.Action) {
		if err := manager.EnsureHostReady(ctx, session, operationNeedsPrivilege(op.Execution)); err != nil {
			return nil, annotateOperationError(hostConfig, op, fmt.Errorf("host readiness preflight failed: %w", err))
		}
	}

	result, err := manager.SendOperationLocked(ctx, session, op, locks)
	if err != nil {
		return nil, annotateOperationError(hostConfig, op, err)
	}
	return result, nil
}

func sendActionInvoke(
	ctx context.Context,
	manager *hostsession.ExecutorManager,
	pool *transport.ConnectionPool,
	hostConfig transport.TransportConfig,
	runtimeModule string,
	runtimeType string,
	config map[string]interface{},
	execution *hostrpc.ExecutionContext,
	locks []hostsession.LockDescriptor,
) (map[string]interface{}, error) {
	session, err := pool.GetOrCreate(ctx, hostConfig)
	if err != nil {
		return nil, fmt.Errorf("get session for %s: %w", hostConfig.Endpoint(), err)
	}

	result, err := manager.InvokeActionLocked(ctx, session, hostrpc.ActionInvokeParams{
		ModuleName:   runtimeModule,
		ResourceType: runtimeType,
		Config:       mustMarshalJSON(config),
		Execution:    execution,
	}, locks)
	if err != nil {
		return nil, annotateActionError(hostConfig, runtimeType, config, err)
	}
	return result, nil
}

func operationMutatesHost(action string) bool {
	switch action {
	case "create", "update", "delete":
		return true
	default:
		return false
	}
}

func operationNeedsPrivilege(execution *hostrpc.ExecutionContext) bool {
	return execution != nil && execution.Become
}

func shouldDropStateOnReadError(err error) bool {
	if err == nil {
		return false
	}

	text := strings.ToLower(err.Error())
	if !strings.Contains(text, "get session for ") && !strings.Contains(text, "ssh dial ") {
		return false
	}

	for _, marker := range []string{
		"connect: connection timed out",
		"dial tcp",
		"i/o timeout",
		"connection refused",
		"no route to host",
		"network is unreachable",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}

	return false
}

func annotateOperationError(hostConfig transport.TransportConfig, op hostsession.OperationMessage, err error) error {
	if err == nil {
		return nil
	}

	subject := strings.TrimSpace(op.ResourceType)
	if subject == "" {
		subject = "resource"
	}
	name := strings.TrimSpace(stringAttr(op.Plan, "name", "id"))
	if name == "" {
		name = strings.TrimSpace(stringAttr(op.State, "name", "id"))
	}
	if name == "" {
		name = strings.TrimSpace(stringAttr(op.Config, "name", "id"))
	}

	if name != "" {
		return fmt.Errorf("%s %q %s on host %s failed: %w", subject, name, op.Action, hostConfig.Endpoint(), err)
	}
	return fmt.Errorf("%s %s on host %s failed: %w", subject, op.Action, hostConfig.Endpoint(), err)
}

func annotateActionError(hostConfig transport.TransportConfig, runtimeType string, config map[string]interface{}, err error) error {
	if err == nil {
		return nil
	}

	name := strings.TrimSpace(stringAttr(config, "name"))
	if name != "" {
		return fmt.Errorf("action %s %q on host %s failed: %w", runtimeType, name, hostConfig.Endpoint(), err)
	}
	return fmt.Errorf("action %s on host %s failed: %w", runtimeType, hostConfig.Endpoint(), err)
}

func stringAttr(values map[string]interface{}, keys ...string) string {
	if values == nil {
		return ""
	}
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok {
			return text
		}
	}
	return ""
}

func mustMarshalJSON(data map[string]interface{}) []byte {
	raw, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return raw
}

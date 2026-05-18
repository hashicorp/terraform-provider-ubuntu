package engine

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
)

const (
	importIdentityTargetKey    = "target"
	importIdentityPortKey      = "port"
	importIdentityTransportKey = "transport"
)

func supportsResourceIdentity(spec *ResourceImportIdentity) bool {
	return spec != nil && len(spec.Attributes) > 0 && spec.FormatID != nil
}

func buildIdentitySchema(spec *ResourceImportIdentity) identityschema.Schema {
	return buildIdentitySchemaWithNames(spec, defaultTransportAttributeNames())
}

func buildIdentitySchemaWithNames(spec *ResourceImportIdentity, names transportAttributeNames) identityschema.Schema {
	attrs := make(map[string]identityschema.Attribute, len(spec.Attributes)+3)
	for key, attribute := range spec.Attributes {
		attrs[key] = attribute
	}
	attrs[names.target] = identityschema.StringAttribute{
		OptionalForImport: true,
		Description:       "Optional import target used when provider default_target is not set.",
	}
	attrs[names.port] = identityschema.Int64Attribute{
		OptionalForImport: true,
		Description:       "Optional import port used when provider default_target.port is not set.",
	}
	attrs[names.transport] = identityschema.StringAttribute{
		OptionalForImport: true,
		Description:       "Optional import transport used when provider default_target.transport is not set.",
	}

	return identityschema.Schema{Attributes: attrs}
}

func extractImportIdentityValues(ctx context.Context, identity *tfsdk.ResourceIdentity, spec *ResourceImportIdentity) (map[string]interface{}, transport.TransportConfig, diag.Diagnostics) {
	return extractImportIdentityValuesWithNames(ctx, identity, spec, defaultTransportAttributeNames())
}

func extractImportIdentityValuesWithNames(ctx context.Context, identity *tfsdk.ResourceIdentity, spec *ResourceImportIdentity, names transportAttributeNames) (map[string]interface{}, transport.TransportConfig, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	if identity == nil || !supportsResourceIdentity(spec) {
		return nil, transport.TransportConfig{}, diagnostics
	}

	values := make(map[string]interface{}, len(spec.Attributes))
	for key, attribute := range spec.Attributes {
		switch attribute.(type) {
		case identityschema.StringAttribute:
			var value types.String
			diagnostics.Append(identity.GetAttribute(ctx, path.Root(key), &value)...)
			if diagnostics.HasError() {
				return nil, transport.TransportConfig{}, diagnostics
			}
			if !value.IsNull() && !value.IsUnknown() {
				values[key] = value.ValueString()
			}
		case identityschema.ListAttribute:
			var value types.List
			diagnostics.Append(identity.GetAttribute(ctx, path.Root(key), &value)...)
			if diagnostics.HasError() {
				return nil, transport.TransportConfig{}, diagnostics
			}
			if value.IsNull() || value.IsUnknown() {
				continue
			}

			items := make([]string, 0, len(value.Elements()))
			diagnostics.Append(value.ElementsAs(ctx, &items, false)...)
			if diagnostics.HasError() {
				return nil, transport.TransportConfig{}, diagnostics
			}
			values[key] = items
		}
	}

	var target types.String
	diagnostics.Append(identity.GetAttribute(ctx, path.Root(names.target), &target)...)
	if diagnostics.HasError() {
		return nil, transport.TransportConfig{}, diagnostics
	}
	var port types.Int64
	diagnostics.Append(identity.GetAttribute(ctx, path.Root(names.port), &port)...)
	if diagnostics.HasError() {
		return nil, transport.TransportConfig{}, diagnostics
	}
	var transportName types.String
	diagnostics.Append(identity.GetAttribute(ctx, path.Root(names.transport), &transportName)...)
	if diagnostics.HasError() {
		return nil, transport.TransportConfig{}, diagnostics
	}

	config := transport.TransportConfig{}
	if !target.IsNull() && !target.IsUnknown() {
		config.Target = target.ValueString()
	}
	if !port.IsNull() && !port.IsUnknown() {
		config.Port = int(port.ValueInt64())
	}
	if !transportName.IsNull() && !transportName.IsUnknown() {
		config.Transport = transportName.ValueString()
	}

	return values, config, diagnostics
}

func buildResourceIdentity(ctx context.Context, spec *ResourceImportIdentity, data map[string]interface{}, targetConfig transport.TransportConfig) (*tfsdk.ResourceIdentity, diag.Diagnostics) {
	return buildResourceIdentityWithNames(ctx, spec, data, targetConfig, defaultTransportAttributeNames())
}

func buildResourceIdentityWithNames(ctx context.Context, spec *ResourceImportIdentity, data map[string]interface{}, targetConfig transport.TransportConfig, names transportAttributeNames) (*tfsdk.ResourceIdentity, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	if !supportsResourceIdentity(spec) {
		return nil, diagnostics
	}

	schema := buildIdentitySchemaWithNames(spec, names)
	identity := &tfsdk.ResourceIdentity{Schema: schema}
	objectType, ok := schema.Type().(types.ObjectType)
	if !ok {
		diagnostics.AddError("Resource identity build failed", "identity schema did not resolve to an object type")
		return nil, diagnostics
	}
	raw, err := types.ObjectNull(objectType.AttrTypes).ToTerraformValue(ctx)
	if err != nil {
		diagnostics.AddError("Resource identity build failed", err.Error())
		return nil, diagnostics
	}
	identity.Raw = raw

	if strings.TrimSpace(targetConfig.Target) != "" {
		diagnostics.Append(identity.SetAttribute(ctx, path.Root(names.target), targetConfig.Target)...)
		if diagnostics.HasError() {
			return nil, diagnostics
		}
	}
	if targetConfig.Port > 0 {
		diagnostics.Append(identity.SetAttribute(ctx, path.Root(names.port), int64(targetConfig.Port))...)
		if diagnostics.HasError() {
			return nil, diagnostics
		}
	}
	if strings.TrimSpace(targetConfig.Transport) != "" {
		diagnostics.Append(identity.SetAttribute(ctx, path.Root(names.transport), targetConfig.Transport)...)
		if diagnostics.HasError() {
			return nil, diagnostics
		}
	}

	for key := range spec.Attributes {
		value, ok := data[key]
		if !ok {
			continue
		}
		diagnostics.Append(identity.SetAttribute(ctx, path.Root(key), identityValue(value))...)
		if diagnostics.HasError() {
			return nil, diagnostics
		}
	}

	return identity, diagnostics
}

func preferKnownIdentityTargetConfig(
	ctx context.Context,
	identity *tfsdk.ResourceIdentity,
	spec *ResourceImportIdentity,
	targetConfig transport.TransportConfig,
) (transport.TransportConfig, diag.Diagnostics) {
	return preferKnownIdentityTargetConfigWithNames(ctx, identity, spec, targetConfig, defaultTransportAttributeNames())
}

func preferKnownIdentityTargetConfigWithNames(
	ctx context.Context,
	identity *tfsdk.ResourceIdentity,
	spec *ResourceImportIdentity,
	targetConfig transport.TransportConfig,
	names transportAttributeNames,
) (transport.TransportConfig, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	if strings.TrimSpace(targetConfig.Target) != "" || identity == nil || !supportsResourceIdentity(spec) {
		return targetConfig, diagnostics
	}

	_, priorConfig, diags := extractImportIdentityValuesWithNames(ctx, identity, spec, names)
	diagnostics.Append(diags...)
	if diagnostics.HasError() {
		return transport.TransportConfig{}, diagnostics
	}

	return priorConfig, diagnostics
}

func identityValue(value interface{}) interface{} {
	switch value := value.(type) {
	case string:
		return value
	case bool:
		return value
	case int64:
		return value
	case int:
		return int64(value)
	case []string:
		return value
	default:
		return value
	}
}

func preserveHostFromAddress(ctx context.Context, config transport.TransportConfig, state *tfsdk.State) diag.Diagnostics {
	return preserveHostFromAddressWithNames(ctx, config, state, defaultTransportAttributeNames())
}

func preserveHostFromAddressWithNames(ctx context.Context, config transport.TransportConfig, state *tfsdk.State, names transportAttributeNames) diag.Diagnostics {
	if strings.TrimSpace(config.Target) == "" {
		return setTransportStateAttributesWithNames(ctx, state, nullTransportTarget(), nullTransportPort(), nullTransportName(), names)
	}
	target, port, transportName := transportStateValuesFromConfig(config)
	return setTransportStateAttributesWithNames(ctx, state, target, port, transportName, names)
}

func resolveHostFromRead(
	ctx context.Context,
	state *tfsdk.State,
	identity *tfsdk.ResourceIdentity,
	defaultHost *transport.TransportConfig,
	spec *ResourceImportIdentity,
) (transport.TransportConfig, diag.Diagnostics) {
	return resolveHostFromReadWithNames(ctx, state, identity, defaultHost, spec, defaultTransportAttributeNames())
}

func resolveHostFromReadWithNames(
	ctx context.Context,
	state *tfsdk.State,
	identity *tfsdk.ResourceIdentity,
	defaultHost *transport.TransportConfig,
	spec *ResourceImportIdentity,
	names transportAttributeNames,
) (transport.TransportConfig, diag.Diagnostics) {
	target := nullTransportTarget()
	port := nullTransportPort()
	transportName := nullTransportName()
	diagnostics := state.GetAttribute(ctx, path.Root(names.target), &target)
	diagnostics.Append(state.GetAttribute(ctx, path.Root(names.port), &port)...)
	diagnostics.Append(state.GetAttribute(ctx, path.Root(names.transport), &transportName)...)
	if diagnostics.HasError() {
		return transport.TransportConfig{}, diagnostics
	}
	config, resolved, diagnostics := buildTransportConfigIfKnown(target, port, transportName, defaultHost, diagnostics)
	if diagnostics.HasError() {
		return transport.TransportConfig{}, diagnostics
	}
	if resolved {
		return config, diagnostics
	}

	if identity != nil && supportsResourceIdentity(spec) {
		_, identityConfig, diags := extractImportIdentityValuesWithNames(ctx, identity, spec, names)
		diagnostics.Append(diags...)
		if diagnostics.HasError() {
			return transport.TransportConfig{}, diagnostics
		}
		if strings.TrimSpace(identityConfig.Target) != "" {
			config, _, diagnostics = finalizeTransportConfig(identityConfig, diagnostics)
			return config, diagnostics
		}
	}

	if defaultHost != nil {
		return *defaultHost, diagnostics
	}

	diagnostics.AddError(
		"No target configured",
		"Either set the target attribute or configure default_target on the provider.",
	)
	return transport.TransportConfig{}, diagnostics
}

func mergeIdentityIntoState(
	ctx context.Context,
	state map[string]interface{},
	identity *tfsdk.ResourceIdentity,
	spec *ResourceImportIdentity,
	targetConfig transport.TransportConfig,
) (map[string]interface{}, diag.Diagnostics) {
	return mergeIdentityIntoStateWithNames(ctx, state, identity, spec, targetConfig, defaultTransportAttributeNames())
}

func mergeIdentityIntoStateWithNames(
	ctx context.Context,
	state map[string]interface{},
	identity *tfsdk.ResourceIdentity,
	spec *ResourceImportIdentity,
	targetConfig transport.TransportConfig,
	names transportAttributeNames,
) (map[string]interface{}, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	if !supportsResourceIdentity(spec) || identity == nil {
		return state, diagnostics
	}

	identityValues, identityConfig, diags := extractImportIdentityValuesWithNames(ctx, identity, spec, names)
	diagnostics.Append(diags...)
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	if state == nil {
		state = make(map[string]interface{}, len(identityValues)+1)
	}

	for key, value := range identityValues {
		if _, ok := state[key]; !ok {
			state[key] = value
		}
	}
	resolved := identityConfig
	if strings.TrimSpace(resolved.Target) == "" {
		resolved = targetConfig
	}

	if _, ok := state[names.target]; !ok {
		if strings.TrimSpace(resolved.Target) != "" {
			state[names.target] = resolved.Target
		}
	}

	if _, ok := state[names.port]; !ok && resolved.Port > 0 {
		state[names.port] = int64(resolved.Port)
	}
	if _, ok := state[names.transport]; !ok && strings.TrimSpace(resolved.Transport) != "" {
		state[names.transport] = resolved.Transport
	}

	return state, diagnostics
}

func resolveHostFromImport(ctx context.Context, req resource.ImportStateRequest, defaultHost *transport.TransportConfig, spec *ResourceImportIdentity) (transport.TransportConfig, diag.Diagnostics) {
	return resolveHostFromImportWithNames(ctx, req, defaultHost, spec, defaultTransportAttributeNames())
}

func resolveHostFromImportWithNames(ctx context.Context, req resource.ImportStateRequest, defaultHost *transport.TransportConfig, spec *ResourceImportIdentity, names transportAttributeNames) (transport.TransportConfig, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	if req.Identity != nil && supportsResourceIdentity(spec) {
		_, identityConfig, diags := extractImportIdentityValuesWithNames(ctx, req.Identity, spec, names)
		diagnostics.Append(diags...)
		if diagnostics.HasError() {
			return transport.TransportConfig{}, diagnostics
		}
		if strings.TrimSpace(identityConfig.Target) != "" {
			config, _, diagnostics := finalizeTransportConfig(identityConfig, diagnostics)
			return config, diagnostics
		}
	}

	if defaultHost != nil {
		return *defaultHost, diagnostics
	}

	diagnostics.AddError(
		"Import failed",
		"Resource import requires either provider default_target or identity.target.",
	)
	return transport.TransportConfig{}, diagnostics
}

func resolveImportID(ctx context.Context, req resource.ImportStateRequest, spec *ResourceImportIdentity) (string, diag.Diagnostics) {
	return resolveImportIDWithNames(ctx, req, spec, defaultTransportAttributeNames())
}

func resolveImportIDWithNames(ctx context.Context, req resource.ImportStateRequest, spec *ResourceImportIdentity, names transportAttributeNames) (string, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	if req.ID != "" {
		return req.ID, diagnostics
	}

	values, _, diags := extractImportIdentityValuesWithNames(ctx, req.Identity, spec, names)
	diagnostics.Append(diags...)
	if diagnostics.HasError() {
		return "", diagnostics
	}
	if !supportsResourceIdentity(spec) {
		diagnostics.AddError("Import failed", "Structured import identity is not supported for this resource.")
		return "", diagnostics
	}

	importID, err := spec.FormatID(values)
	if err != nil {
		diagnostics.AddError("Import failed", err.Error())
		return "", diagnostics
	}

	return importID, diagnostics
}

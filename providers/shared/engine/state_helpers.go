package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	actionschema "github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/transport"
)

func nullTransportTarget() types.String { return types.StringNull() }

func nullTransportPort() types.Int64 { return types.Int64Null() }

func nullTransportName() types.String { return types.StringNull() }

func nullHostKeyFingerprint() types.String { return types.StringNull() }

func hostKeyFingerprintStateValue(fingerprint string) types.String {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return types.StringNull()
	}
	return types.StringValue(fingerprint)
}

func normalizeHostKeyFingerprintValue(value types.String) types.String {
	if value.IsNull() || value.IsUnknown() {
		return types.StringNull()
	}
	return hostKeyFingerprintStateValue(value.ValueString())
}

func transportStateValuesFromConfig(config transport.TransportConfig) (types.String, types.Int64, types.String) {
	target := types.StringNull()
	if config.NormalizedTarget() != "" && !config.IsLocal() {
		target = types.StringValue(config.NormalizedTarget())
	}
	port := types.Int64Null()
	if !config.IsLocal() {
		port = types.Int64Value(int64(config.ResolvedPort()))
	}
	transportName := types.StringNull()
	if config.NormalizedTransport() != "" {
		transportName = types.StringValue(config.NormalizedTransport())
	}
	return target, port, transportName
}

func extractResourceValuesFromPlan(
	ctx context.Context,
	plan *tfsdk.Plan,
	attrs map[string]resourceschema.Attribute,
	blocks map[string]resourceschema.Block,
) (map[string]interface{}, diag.Diagnostics) {
	return extractResourceValues(ctx, attrs, blocks, func(key string, target interface{}) diag.Diagnostics {
		return plan.GetAttribute(ctx, path.Root(key), target)
	})
}

func extractResourceValuesFromState(
	ctx context.Context,
	state *tfsdk.State,
	attrs map[string]resourceschema.Attribute,
	blocks map[string]resourceschema.Block,
) (map[string]interface{}, diag.Diagnostics) {
	return extractResourceValues(ctx, attrs, blocks, func(key string, target interface{}) diag.Diagnostics {
		return state.GetAttribute(ctx, path.Root(key), target)
	})
}

func extractResourceValuesFromConfig(
	ctx context.Context,
	config *tfsdk.Config,
	attrs map[string]resourceschema.Attribute,
	blocks map[string]resourceschema.Block,
) (map[string]interface{}, diag.Diagnostics) {
	return extractResourceValues(ctx, attrs, blocks, func(key string, target interface{}) diag.Diagnostics {
		return config.GetAttribute(ctx, path.Root(key), target)
	})
}

func extractDataSourceValuesFromConfig(
	ctx context.Context,
	config *tfsdk.Config,
	attrs map[string]datasourceschema.Attribute,
) (map[string]interface{}, diag.Diagnostics) {
	return extractDataSourceValues(ctx, attrs, func(key string, target interface{}) diag.Diagnostics {
		return config.GetAttribute(ctx, path.Root(key), target)
	})
}

func extractActionValuesFromConfig(
	ctx context.Context,
	config *tfsdk.Config,
	attrs map[string]actionschema.Attribute,
) (map[string]interface{}, diag.Diagnostics) {
	result := make(map[string]interface{})
	var diagnostics diag.Diagnostics

	for key, attribute := range attrs {
		value, ok, d := extractValue(ctx, key, attribute, func(key string, target interface{}) diag.Diagnostics {
			return config.GetAttribute(ctx, path.Root(key), target)
		})
		diagnostics.Append(d...)
		if diagnostics.HasError() {
			return nil, diagnostics
		}
		if ok {
			result[key] = value
		}
	}

	return result, diagnostics
}

func extractResourceValues(
	ctx context.Context,
	attrs map[string]resourceschema.Attribute,
	blocks map[string]resourceschema.Block,
	getter func(key string, target interface{}) diag.Diagnostics,
) (map[string]interface{}, diag.Diagnostics) {
	result := make(map[string]interface{})
	var diagnostics diag.Diagnostics

	for key, attribute := range attrs {
		value, ok, d := extractValue(ctx, key, attribute, getter)
		diagnostics.Append(d...)
		if diagnostics.HasError() {
			return nil, diagnostics
		}
		if ok {
			result[key] = value
		}
	}

	for key, block := range blocks {
		value, ok, d := extractBlockValue(ctx, key, block, getter)
		diagnostics.Append(d...)
		if diagnostics.HasError() {
			return nil, diagnostics
		}
		if ok {
			result[key] = value
		}
	}

	return result, diagnostics
}

func extractBlockValue(
	ctx context.Context,
	key string,
	block resourceschema.Block,
	getter func(key string, target interface{}) diag.Diagnostics,
) (interface{}, bool, diag.Diagnostics) {
	switch block.(type) {
	case resourceschema.SingleNestedBlock:
		var value types.Object
		diagnostics := getter(key, &value)
		if diagnostics.HasError() || value.IsNull() || value.IsUnknown() {
			return nil, false, diagnostics
		}
		result, d := objectValueToJSON(ctx, key, value)
		diagnostics.Append(d...)
		if diagnostics.HasError() {
			return nil, false, diagnostics
		}
		return result, true, diagnostics
	default:
		var diagnostics diag.Diagnostics
		diagnostics.AddWarning(
			"Unsupported block conversion",
			fmt.Sprintf("Skipping block %s because it does not have a supported JSON conversion path.", key),
		)
		return nil, false, diagnostics
	}
}

func objectValueToJSON(ctx context.Context, key string, value types.Object) (map[string]interface{}, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	result := make(map[string]interface{}, len(value.Attributes()))

	for name, attributeValue := range value.Attributes() {
		converted, ok, d := attrValueToJSON(ctx, key+"."+name, attributeValue)
		diagnostics.Append(d...)
		if diagnostics.HasError() {
			return nil, diagnostics
		}
		if ok {
			result[name] = converted
		}
	}

	return result, diagnostics
}

func attrValueToJSON(ctx context.Context, key string, value attr.Value) (interface{}, bool, diag.Diagnostics) {
	switch value := value.(type) {
	case types.String:
		if value.IsNull() || value.IsUnknown() {
			return nil, false, nil
		}
		return value.ValueString(), true, nil
	case types.Int64:
		if value.IsNull() || value.IsUnknown() {
			return nil, false, nil
		}
		return value.ValueInt64(), true, nil
	case types.Bool:
		if value.IsNull() || value.IsUnknown() {
			return nil, false, nil
		}
		return value.ValueBool(), true, nil
	case types.List:
		if value.IsNull() || value.IsUnknown() {
			return nil, false, nil
		}
		if value.ElementType(ctx) == types.StringType {
			var items []string
			diagnostics := value.ElementsAs(ctx, &items, false)
			if diagnostics.HasError() {
				return nil, false, diagnostics
			}
			return items, true, diagnostics
		}
	case types.Map:
		if value.IsNull() || value.IsUnknown() {
			return nil, false, nil
		}
		if value.ElementType(ctx) == types.StringType {
			items := map[string]string{}
			diagnostics := value.ElementsAs(ctx, &items, false)
			if diagnostics.HasError() {
				return nil, false, diagnostics
			}
			return items, true, diagnostics
		}
	case types.Object:
		if value.IsNull() || value.IsUnknown() {
			return nil, false, nil
		}
		result, diagnostics := objectValueToJSON(ctx, key, value)
		if diagnostics.HasError() {
			return nil, false, diagnostics
		}
		return result, true, diagnostics
	}

	var diagnostics diag.Diagnostics
	diagnostics.AddWarning(
		"Unsupported nested value conversion",
		fmt.Sprintf("Skipping nested value %s because it does not have a supported JSON conversion path.", key),
	)
	return nil, false, diagnostics
}

func extractDataSourceValues(
	ctx context.Context,
	attrs map[string]datasourceschema.Attribute,
	getter func(key string, target interface{}) diag.Diagnostics,
) (map[string]interface{}, diag.Diagnostics) {
	result := make(map[string]interface{})
	var diagnostics diag.Diagnostics

	for key, attribute := range attrs {
		value, ok, d := extractValue(ctx, key, attribute, getter)
		diagnostics.Append(d...)
		if diagnostics.HasError() {
			return nil, diagnostics
		}
		if ok {
			result[key] = value
		}
	}

	return result, diagnostics
}

func extractValue(
	ctx context.Context,
	key string,
	attribute interface{},
	getter func(key string, target interface{}) diag.Diagnostics,
) (interface{}, bool, diag.Diagnostics) {
	switch attribute := attribute.(type) {
	case resourceschema.StringAttribute:
		return extractResourceStringValue(key, attribute, getter)
	case datasourceschema.StringAttribute:
		return extractStringValue(key, getter)
	case actionschema.StringAttribute:
		return extractStringValue(key, getter)
	case resourceschema.Int64Attribute:
		return extractInt64Value(key, getter)
	case datasourceschema.Int64Attribute:
		return extractInt64Value(key, getter)
	case actionschema.Int64Attribute:
		return extractInt64Value(key, getter)
	case resourceschema.BoolAttribute:
		return extractBoolValue(key, getter)
	case datasourceschema.BoolAttribute:
		return extractBoolValue(key, getter)
	case actionschema.BoolAttribute:
		return extractBoolValue(key, getter)
	case resourceschema.ListAttribute:
		if attribute.ElementType == types.StringType {
			return extractStringListValue(key, getter)
		}
	case datasourceschema.ListAttribute:
		if attribute.ElementType == types.StringType {
			return extractStringListValue(key, getter)
		}
	case actionschema.ListAttribute:
		if attribute.ElementType == types.StringType {
			return extractStringListValue(key, getter)
		}
	case resourceschema.MapAttribute:
		if attribute.ElementType == types.StringType {
			return extractStringMapValue(key, getter)
		}
	case datasourceschema.MapAttribute:
		if attribute.ElementType == types.StringType {
			return extractStringMapValue(key, getter)
		}
	case actionschema.MapAttribute:
		if attribute.ElementType == types.StringType {
			return extractStringMapValue(key, getter)
		}
	}

	var diagnostics diag.Diagnostics
	diagnostics.AddWarning(
		"Unsupported attribute conversion",
		fmt.Sprintf("Skipping attribute %s because it does not have a supported JSON conversion path.", key),
	)
	_ = ctx
	return nil, false, diagnostics
}

func extractResourceStringValue(key string, attribute resourceschema.StringAttribute, getter func(string, interface{}) diag.Diagnostics) (interface{}, bool, diag.Diagnostics) {
	switch attribute.CustomType.(type) {
	case DigestedStringType:
		var value DigestedStringValue
		diagnostics := getter(key, &value)
		if diagnostics.HasError() || value.IsNull() || value.IsUnknown() {
			return nil, false, diagnostics
		}
		return value.ValueString(), true, diagnostics
	case DigestedBase64StringType:
		var value DigestedBase64StringValue
		diagnostics := getter(key, &value)
		if diagnostics.HasError() || value.IsNull() || value.IsUnknown() {
			return nil, false, diagnostics
		}
		return value.ValueString(), true, diagnostics
	default:
		_ = attribute
		return extractStringValue(key, getter)
	}
}

func extractStringValue(key string, getter func(string, interface{}) diag.Diagnostics) (interface{}, bool, diag.Diagnostics) {
	var value types.String
	diagnostics := getter(key, &value)
	if diagnostics.HasError() || value.IsNull() || value.IsUnknown() {
		return nil, false, diagnostics
	}
	return value.ValueString(), true, diagnostics
}

func extractInt64Value(key string, getter func(string, interface{}) diag.Diagnostics) (interface{}, bool, diag.Diagnostics) {
	var value types.Int64
	diagnostics := getter(key, &value)
	if diagnostics.HasError() || value.IsNull() || value.IsUnknown() {
		return nil, false, diagnostics
	}
	return value.ValueInt64(), true, diagnostics
}

func extractBoolValue(key string, getter func(string, interface{}) diag.Diagnostics) (interface{}, bool, diag.Diagnostics) {
	var value types.Bool
	diagnostics := getter(key, &value)
	if diagnostics.HasError() || value.IsNull() || value.IsUnknown() {
		return nil, false, diagnostics
	}
	return value.ValueBool(), true, diagnostics
}

func extractStringListValue(key string, getter func(string, interface{}) diag.Diagnostics) (interface{}, bool, diag.Diagnostics) {
	var value types.List
	diagnostics := getter(key, &value)
	if diagnostics.HasError() || value.IsNull() || value.IsUnknown() {
		return nil, false, diagnostics
	}

	items := make([]string, 0, len(value.Elements()))
	diagnostics.Append(value.ElementsAs(context.Background(), &items, false)...)
	if diagnostics.HasError() {
		return nil, false, diagnostics
	}

	return items, true, diagnostics
}

func extractStringMapValue(key string, getter func(string, interface{}) diag.Diagnostics) (interface{}, bool, diag.Diagnostics) {
	var value types.Map
	diagnostics := getter(key, &value)
	if diagnostics.HasError() || value.IsNull() || value.IsUnknown() {
		return nil, false, diagnostics
	}

	items := map[string]string{}
	diagnostics.Append(value.ElementsAs(context.Background(), &items, false)...)
	if diagnostics.HasError() {
		return nil, false, diagnostics
	}

	return items, true, diagnostics
}

func setResourceStateFromJSON(
	ctx context.Context,
	data map[string]interface{},
	attrs map[string]resourceschema.Attribute,
	blocks map[string]resourceschema.Block,
	state *tfsdk.State,
) diag.Diagnostics {
	var diagnostics diag.Diagnostics

	for key, value := range data {
		if key == "id" {
			diagnostics.Append(setStringValue(ctx, state, path.Root("id"), key, value)...)
			continue
		}

		attrPath := path.Root(key)
		attribute, ok := attrs[key]
		if !ok {
			continue
		}

		diagnostics.Append(setResourceAttribute(ctx, state, attrPath, key, attribute, value)...)
		if diagnostics.HasError() {
			return diagnostics
		}
	}

	for key, value := range data {
		block, ok := blocks[key]
		if !ok {
			continue
		}

		diagnostics.Append(setResourceBlock(ctx, state, path.Root(key), key, block, value)...)
		if diagnostics.HasError() {
			return diagnostics
		}
	}

	return diagnostics
}

func setResourceBlock(
	ctx context.Context,
	state *tfsdk.State,
	attrPath path.Path,
	key string,
	block resourceschema.Block,
	value interface{},
) diag.Diagnostics {
	switch block := block.(type) {
	case resourceschema.SingleNestedBlock:
		return setSingleNestedResourceBlock(ctx, state, attrPath, key, block, value)
	default:
		var diagnostics diag.Diagnostics
		diagnostics.AddWarning(
			"Unsupported block state conversion",
			fmt.Sprintf("Skipping block %s because it does not have a supported state conversion path.", key),
		)
		return diagnostics
	}
}

func setSingleNestedResourceBlock(
	ctx context.Context,
	state *tfsdk.State,
	attrPath path.Path,
	key string,
	block resourceschema.SingleNestedBlock,
	value interface{},
) diag.Diagnostics {
	attrTypes := make(map[string]attr.Type, len(block.Attributes))
	attrValues := make(map[string]attr.Value, len(block.Attributes))

	for name, attribute := range block.Attributes {
		attrTypes[name] = attribute.GetType()
	}

	if value == nil {
		return state.SetAttribute(ctx, attrPath, types.ObjectNull(attrTypes))
	}

	items, ok := asNestedMap(value)
	if !ok {
		return newTypeDiagnostic(key, "map[string]interface{}", value)
	}

	var diagnostics diag.Diagnostics
	for name, attribute := range block.Attributes {
		raw, exists := items[name]
		if !exists {
			raw = nil
		}
		converted, d := resourceAttributeValue(name, attribute, raw)
		diagnostics.Append(d...)
		if diagnostics.HasError() {
			return diagnostics
		}
		attrValues[name] = converted
	}

	objectValue, d := types.ObjectValue(attrTypes, attrValues)
	diagnostics.Append(d...)
	if diagnostics.HasError() {
		return diagnostics
	}

	return state.SetAttribute(ctx, attrPath, objectValue)
}

func resourceAttributeValue(key string, attribute resourceschema.Attribute, value interface{}) (attr.Value, diag.Diagnostics) {
	switch attribute := attribute.(type) {
	case resourceschema.StringAttribute:
		if value == nil {
			return types.StringNull(), nil
		}
		text, ok := asString(value)
		if !ok {
			return nil, newTypeDiagnostic(key, "string", value)
		}
		return types.StringValue(text), nil
	case resourceschema.Int64Attribute:
		if value == nil {
			return types.Int64Null(), nil
		}
		number, ok := asInt64(value)
		if !ok {
			return nil, newTypeDiagnostic(key, "int64", value)
		}
		return types.Int64Value(number), nil
	case resourceschema.BoolAttribute:
		if value == nil {
			return types.BoolNull(), nil
		}
		boolean, ok := value.(bool)
		if !ok {
			return nil, newTypeDiagnostic(key, "bool", value)
		}
		return types.BoolValue(boolean), nil
	case resourceschema.ListAttribute:
		if attribute.ElementType == types.StringType {
			if value == nil {
				return types.ListNull(types.StringType), nil
			}
			list, ok := asStringSlice(value)
			if !ok {
				return nil, newTypeDiagnostic(key, "[]string", value)
			}
			result, diagnostics := types.ListValueFrom(context.Background(), types.StringType, list)
			if diagnostics.HasError() {
				return nil, diagnostics
			}
			return result, nil
		}
	case resourceschema.MapAttribute:
		if attribute.ElementType == types.StringType {
			if value == nil {
				return types.MapNull(types.StringType), nil
			}
			mapping, ok := asStringMap(value)
			if !ok {
				return nil, newTypeDiagnostic(key, "map[string]string", value)
			}
			result, diagnostics := types.MapValueFrom(context.Background(), types.StringType, mapping)
			if diagnostics.HasError() {
				return nil, diagnostics
			}
			return result, nil
		}
	}

	var diagnostics diag.Diagnostics
	diagnostics.AddWarning(
		"Unsupported nested attribute state conversion",
		fmt.Sprintf("Skipping nested attribute %s because it does not have a supported state conversion path.", key),
	)
	return types.StringNull(), diagnostics
}

func asNestedMap(value interface{}) (map[string]interface{}, bool) {
	switch value := value.(type) {
	case map[string]interface{}:
		return value, true
	default:
		return nil, false
	}
}

func setDataSourceStateFromJSON(
	ctx context.Context,
	data map[string]interface{},
	attrs map[string]datasourceschema.Attribute,
	state *tfsdk.State,
) diag.Diagnostics {
	var diagnostics diag.Diagnostics

	for key, value := range data {
		if key == "id" {
			diagnostics.Append(setStringValue(ctx, state, path.Root("id"), key, value)...)
			continue
		}

		attrPath := path.Root(key)
		attribute, ok := attrs[key]
		if !ok {
			continue
		}

		diagnostics.Append(setDataSourceAttribute(ctx, state, attrPath, key, attribute, value)...)
		if diagnostics.HasError() {
			return diagnostics
		}
	}

	return diagnostics
}

func convertResourceStateValue(key string, attribute resourceschema.Attribute, value interface{}) (interface{}, diag.Diagnostics) {
	switch attribute := attribute.(type) {
	case resourceschema.StringAttribute:
		text, ok := asString(value)
		if !ok {
			return nil, newTypeDiagnostic(key, "string", value)
		}
		return text, nil
	case resourceschema.Int64Attribute:
		number, ok := asInt64(value)
		if !ok {
			return nil, newTypeDiagnostic(key, "int64", value)
		}
		return number, nil
	case resourceschema.BoolAttribute:
		boolean, ok := value.(bool)
		if !ok {
			return nil, newTypeDiagnostic(key, "bool", value)
		}
		return boolean, nil
	case resourceschema.ListAttribute:
		if attribute.ElementType == types.StringType {
			list, ok := asStringSlice(value)
			if !ok {
				return nil, newTypeDiagnostic(key, "[]string", value)
			}
			return list, nil
		}
	case resourceschema.MapAttribute:
		if attribute.ElementType == types.StringType {
			mapping, ok := asStringMap(value)
			if !ok {
				return nil, newTypeDiagnostic(key, "map[string]string", value)
			}
			return mapping, nil
		}
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		var diagnostics diag.Diagnostics
		diagnostics.AddError("Marshal attribute", fmt.Sprintf("Failed to marshal %s: %s", key, err))
		return nil, diagnostics
	}
	return string(encoded), nil
}

func convertDataSourceStateValue(key string, attribute datasourceschema.Attribute, value interface{}) (interface{}, diag.Diagnostics) {
	switch attribute := attribute.(type) {
	case datasourceschema.StringAttribute:
		text, ok := asString(value)
		if !ok {
			return nil, newTypeDiagnostic(key, "string", value)
		}
		return text, nil
	case datasourceschema.Int64Attribute:
		number, ok := asInt64(value)
		if !ok {
			return nil, newTypeDiagnostic(key, "int64", value)
		}
		return number, nil
	case datasourceschema.BoolAttribute:
		boolean, ok := value.(bool)
		if !ok {
			return nil, newTypeDiagnostic(key, "bool", value)
		}
		return boolean, nil
	case datasourceschema.ListAttribute:
		if attribute.ElementType == types.StringType {
			list, ok := asStringSlice(value)
			if !ok {
				return nil, newTypeDiagnostic(key, "[]string", value)
			}
			return list, nil
		}
	case datasourceschema.MapAttribute:
		if attribute.ElementType == types.StringType {
			mapping, ok := asStringMap(value)
			if !ok {
				return nil, newTypeDiagnostic(key, "map[string]string", value)
			}
			return mapping, nil
		}
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		var diagnostics diag.Diagnostics
		diagnostics.AddError("Marshal attribute", fmt.Sprintf("Failed to marshal %s: %s", key, err))
		return nil, diagnostics
	}
	return string(encoded), nil
}

func setResourceAttribute(
	ctx context.Context,
	state *tfsdk.State,
	attrPath path.Path,
	key string,
	attribute resourceschema.Attribute,
	value interface{},
) diag.Diagnostics {
	switch attribute := attribute.(type) {
	case resourceschema.StringAttribute:
		return setStringValue(ctx, state, attrPath, key, value)
	case resourceschema.Int64Attribute:
		return setInt64Value(ctx, state, attrPath, key, value)
	case resourceschema.BoolAttribute:
		return setBoolValue(ctx, state, attrPath, key, value)
	case resourceschema.ListAttribute:
		return setListValue(ctx, state, attrPath, key, attribute.ElementType, value)
	case resourceschema.MapAttribute:
		return setMapValue(ctx, state, attrPath, key, attribute.ElementType, value)
	default:
		return setFallbackValue(ctx, state, attrPath, key, value)
	}
}

func setDataSourceAttribute(
	ctx context.Context,
	state *tfsdk.State,
	attrPath path.Path,
	key string,
	attribute datasourceschema.Attribute,
	value interface{},
) diag.Diagnostics {
	switch attribute := attribute.(type) {
	case datasourceschema.StringAttribute:
		return setStringValue(ctx, state, attrPath, key, value)
	case datasourceschema.Int64Attribute:
		return setInt64Value(ctx, state, attrPath, key, value)
	case datasourceschema.BoolAttribute:
		return setBoolValue(ctx, state, attrPath, key, value)
	case datasourceschema.ListAttribute:
		return setListValue(ctx, state, attrPath, key, attribute.ElementType, value)
	case datasourceschema.MapAttribute:
		return setMapValue(ctx, state, attrPath, key, attribute.ElementType, value)
	default:
		return setFallbackValue(ctx, state, attrPath, key, value)
	}
}

func setStringValue(ctx context.Context, state *tfsdk.State, attrPath path.Path, key string, value interface{}) diag.Diagnostics {
	if value == nil {
		return state.SetAttribute(ctx, attrPath, types.StringNull())
	}

	text, ok := asString(value)
	if !ok {
		return newTypeDiagnostic(key, "string", value)
	}

	return state.SetAttribute(ctx, attrPath, text)
}

func setInt64Value(ctx context.Context, state *tfsdk.State, attrPath path.Path, key string, value interface{}) diag.Diagnostics {
	number, ok := asInt64(value)
	if !ok {
		return newTypeDiagnostic(key, "int64", value)
	}

	return state.SetAttribute(ctx, attrPath, number)
}

func setBoolValue(ctx context.Context, state *tfsdk.State, attrPath path.Path, key string, value interface{}) diag.Diagnostics {
	boolean, ok := value.(bool)
	if !ok {
		return newTypeDiagnostic(key, "bool", value)
	}

	return state.SetAttribute(ctx, attrPath, boolean)
}

func setListValue(
	ctx context.Context,
	state *tfsdk.State,
	attrPath path.Path,
	key string,
	elementType attr.Type,
	value interface{},
) diag.Diagnostics {
	if elementType == types.StringType {
		if value == nil {
			return state.SetAttribute(ctx, attrPath, types.ListNull(types.StringType))
		}
		list, ok := asStringSlice(value)
		if !ok {
			return newTypeDiagnostic(key, "[]string", value)
		}
		if list == nil {
			return state.SetAttribute(ctx, attrPath, types.ListNull(types.StringType))
		}
		typed, diagnostics := types.ListValueFrom(ctx, types.StringType, list)
		if diagnostics.HasError() {
			return diagnostics
		}

		return state.SetAttribute(ctx, attrPath, typed)
	}

	return setFallbackValue(ctx, state, attrPath, key, value)
}

func setMapValue(
	ctx context.Context,
	state *tfsdk.State,
	attrPath path.Path,
	key string,
	elementType attr.Type,
	value interface{},
) diag.Diagnostics {
	if elementType == types.StringType {
		mapping, ok := asStringMap(value)
		if !ok {
			return newTypeDiagnostic(key, "map[string]string", value)
		}

		return state.SetAttribute(ctx, attrPath, mapping)
	}

	return setFallbackValue(ctx, state, attrPath, key, value)
}

func setFallbackValue(ctx context.Context, state *tfsdk.State, attrPath path.Path, key string, value interface{}) diag.Diagnostics {
	var diagnostics diag.Diagnostics

	if value == nil {
		diagnostics.Append(state.SetAttribute(ctx, attrPath, types.StringNull())...)
		return diagnostics
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		diagnostics.AddError("Marshal attribute", fmt.Sprintf("Failed to marshal %s: %s", key, err))
		return diagnostics
	}

	diagnostics.Append(state.SetAttribute(ctx, attrPath, string(encoded))...)
	return diagnostics
}

func preserveHostFromPlan(ctx context.Context, plan *tfsdk.Plan, state *tfsdk.State) diag.Diagnostics {
	return preserveHostFromPlanWithNames(ctx, plan, state, defaultTransportAttributeNames())
}

func preserveHostFromPlanWithNames(ctx context.Context, plan *tfsdk.Plan, state *tfsdk.State, names transportAttributeNames) diag.Diagnostics {
	target := nullTransportTarget()
	port := nullTransportPort()
	transportName := nullTransportName()
	diagnostics := plan.GetAttribute(ctx, path.Root(names.target), &target)
	diagnostics.Append(plan.GetAttribute(ctx, path.Root(names.port), &port)...)
	diagnostics.Append(plan.GetAttribute(ctx, path.Root(names.transport), &transportName)...)
	if diagnostics.HasError() {
		return diagnostics
	}

	return setTransportStateAttributesWithNames(ctx, state, target, port, transportName, names)
}

func preserveHostFromState(ctx context.Context, source *tfsdk.State, state *tfsdk.State) diag.Diagnostics {
	return preserveHostFromStateWithNames(ctx, source, state, defaultTransportAttributeNames())
}

func preserveHostFromStateWithNames(ctx context.Context, source *tfsdk.State, state *tfsdk.State, names transportAttributeNames) diag.Diagnostics {
	target := nullTransportTarget()
	port := nullTransportPort()
	transportName := nullTransportName()
	diagnostics := source.GetAttribute(ctx, path.Root(names.target), &target)
	diagnostics.Append(source.GetAttribute(ctx, path.Root(names.port), &port)...)
	diagnostics.Append(source.GetAttribute(ctx, path.Root(names.transport), &transportName)...)
	if diagnostics.HasError() {
		return diagnostics
	}

	return setTransportStateAttributesWithNames(ctx, state, target, port, transportName, names)
}

func hostKeyFingerprintFromStateWithNames(ctx context.Context, state *tfsdk.State, names transportAttributeNames) (types.String, diag.Diagnostics) {
	if state == nil || !resourceSchemaHasAttribute(state.Schema, names.hostKeyFingerprint) {
		return nullHostKeyFingerprint(), nil
	}

	value := nullHostKeyFingerprint()
	diagnostics := state.GetAttribute(ctx, path.Root(names.hostKeyFingerprint), &value)
	if diagnostics.HasError() {
		return nullHostKeyFingerprint(), diagnostics
	}
	return normalizeHostKeyFingerprintValue(value), diagnostics
}

func setHostKeyFingerprintStateAttributeWithNames(ctx context.Context, state *tfsdk.State, fingerprint types.String, names transportAttributeNames) diag.Diagnostics {
	if state == nil || !resourceSchemaHasAttribute(state.Schema, names.hostKeyFingerprint) {
		return nil
	}
	return state.SetAttribute(ctx, path.Root(names.hostKeyFingerprint), normalizeHostKeyFingerprintValue(fingerprint))
}

func preserveHostKeyFingerprintFromStateWithNames(ctx context.Context, source *tfsdk.State, state *tfsdk.State, names transportAttributeNames) diag.Diagnostics {
	fingerprint, diagnostics := hostKeyFingerprintFromStateWithNames(ctx, source, names)
	if diagnostics.HasError() {
		return diagnostics
	}
	return setHostKeyFingerprintStateAttributeWithNames(ctx, state, fingerprint, names)
}

func preserveAllowDestructiveDestroyFromPlan(ctx context.Context, plan *tfsdk.Plan, state *tfsdk.State) diag.Diagnostics {
	if !resourceSchemaHasAttribute(plan.Schema, "allow_destructive_destroy") || !resourceSchemaHasAttribute(state.Schema, "allow_destructive_destroy") {
		return nil
	}

	var allow types.Bool
	diagnostics := plan.GetAttribute(ctx, path.Root("allow_destructive_destroy"), &allow)
	if diagnostics.HasError() || allow.IsUnknown() {
		return diagnostics
	}
	if allow.IsNull() {
		diagnostics.Append(state.SetAttribute(ctx, path.Root("allow_destructive_destroy"), types.BoolNull())...)
		return diagnostics
	}

	diagnostics.Append(state.SetAttribute(ctx, path.Root("allow_destructive_destroy"), allow)...)
	return diagnostics
}

func preserveAllowDestructiveDestroyFromState(ctx context.Context, source *tfsdk.State, state *tfsdk.State) diag.Diagnostics {
	if !resourceSchemaHasAttribute(source.Schema, "allow_destructive_destroy") || !resourceSchemaHasAttribute(state.Schema, "allow_destructive_destroy") {
		return nil
	}

	var allow types.Bool
	diagnostics := source.GetAttribute(ctx, path.Root("allow_destructive_destroy"), &allow)
	if diagnostics.HasError() || allow.IsUnknown() {
		return diagnostics
	}
	if allow.IsNull() {
		diagnostics.Append(state.SetAttribute(ctx, path.Root("allow_destructive_destroy"), types.BoolNull())...)
		return diagnostics
	}

	diagnostics.Append(state.SetAttribute(ctx, path.Root("allow_destructive_destroy"), allow)...)
	return diagnostics
}

func preserveDataSourceHostFromConfig(ctx context.Context, config *tfsdk.Config, state *tfsdk.State) diag.Diagnostics {
	target := nullTransportTarget()
	port := nullTransportPort()
	transportName := nullTransportName()
	diagnostics := config.GetAttribute(ctx, path.Root(targetAttributeName), &target)
	diagnostics.Append(config.GetAttribute(ctx, path.Root(portAttributeName), &port)...)
	diagnostics.Append(config.GetAttribute(ctx, path.Root(transportAttributeName), &transportName)...)
	if diagnostics.HasError() {
		return diagnostics
	}

	return setTransportStateAttributes(ctx, state, target, port, transportName)
}

func setTransportStateAttributes(ctx context.Context, state *tfsdk.State, target types.String, port types.Int64, transportName types.String) diag.Diagnostics {
	return setTransportStateAttributesWithNames(ctx, state, target, port, transportName, defaultTransportAttributeNames())
}

func setTransportStateAttributesWithNames(ctx context.Context, state *tfsdk.State, target types.String, port types.Int64, transportName types.String, names transportAttributeNames) diag.Diagnostics {
	var diagnostics diag.Diagnostics

	diagnostics.Append(state.SetAttribute(ctx, path.Root(names.target), target)...)
	diagnostics.Append(state.SetAttribute(ctx, path.Root(names.port), port)...)
	diagnostics.Append(state.SetAttribute(ctx, path.Root(names.transport), transportName)...)
	return diagnostics
}

func asString(value interface{}) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

func resourceSchemaHasAttribute(schema any, key string) bool {
	switch typed := schema.(type) {
	case resourceschema.Schema:
		_, ok := typed.Attributes[key]
		return ok
	case *resourceschema.Schema:
		if typed == nil {
			return false
		}
		_, ok := typed.Attributes[key]
		return ok
	default:
		return false
	}
}

func asInt64(value interface{}) (int64, bool) {
	switch value := value.(type) {
	case int:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		return int64(value), true
	case json.Number:
		number, err := value.Int64()
		return number, err == nil
	default:
		return 0, false
	}
}

func asStringSlice(value interface{}) ([]string, bool) {
	switch value := value.(type) {
	case []string:
		return value, true
	case []interface{}:
		result := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			result = append(result, text)
		}
		return result, true
	default:
		return nil, false
	}
}

func asStringMap(value interface{}) (map[string]string, bool) {
	switch value := value.(type) {
	case map[string]string:
		return value, true
	case map[string]interface{}:
		result := make(map[string]string, len(value))
		for key, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			result[key] = text
		}
		return result, true
	default:
		return nil, false
	}
}

func newTypeDiagnostic(key, expected string, value interface{}) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	diagnostics.AddError(
		"Unsupported attribute type",
		fmt.Sprintf("Attribute %s expected %s, got %T", key, expected, value),
	)
	return diagnostics
}

package engine

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
)

type DigestedStringType struct{}

func (t DigestedStringType) ApplyTerraform5AttributePathStep(step tftypes.AttributePathStep) (interface{}, error) {
	return nil, fmt.Errorf("cannot apply AttributePathStep %T to %s", step, t.String())
}

func (t DigestedStringType) Equal(other attr.Type) bool {
	_, ok := other.(DigestedStringType)
	return ok
}

func (t DigestedStringType) String() string {
	return "engine.DigestedStringType"
}

func (t DigestedStringType) TerraformType(_ context.Context) tftypes.Type {
	return tftypes.String
}

func (t DigestedStringType) ValueFromString(_ context.Context, value basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return DigestedStringValue{StringValue: value}, nil
}

func (t DigestedStringType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	value, err := basetypes.StringType{}.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}

	stringValue, ok := value.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected string value type %T", value)
	}

	return DigestedStringValue{StringValue: stringValue}, nil
}

func (t DigestedStringType) ValueType(_ context.Context) attr.Value {
	return DigestedStringValue{}
}

type DigestedBase64StringType struct{}

func (t DigestedBase64StringType) ApplyTerraform5AttributePathStep(step tftypes.AttributePathStep) (interface{}, error) {
	return nil, fmt.Errorf("cannot apply AttributePathStep %T to %s", step, t.String())
}

func (t DigestedBase64StringType) Equal(other attr.Type) bool {
	_, ok := other.(DigestedBase64StringType)
	return ok
}

func (t DigestedBase64StringType) String() string {
	return "engine.DigestedBase64StringType"
}

func (t DigestedBase64StringType) TerraformType(_ context.Context) tftypes.Type {
	return tftypes.String
}

func (t DigestedBase64StringType) ValueFromString(_ context.Context, value basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return DigestedBase64StringValue{StringValue: value}, nil
}

func (t DigestedBase64StringType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	value, err := basetypes.StringType{}.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}

	stringValue, ok := value.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected string value type %T", value)
	}

	return DigestedBase64StringValue{StringValue: stringValue}, nil
}

func (t DigestedBase64StringType) ValueType(_ context.Context) attr.Value {
	return DigestedBase64StringValue{}
}

type DigestedStringValue struct {
	basetypes.StringValue
}

func (v DigestedStringValue) Type(_ context.Context) attr.Type {
	return DigestedStringType{}
}

func (v DigestedStringValue) Equal(other attr.Value) bool {
	o, ok := other.(DigestedStringValue)
	if !ok {
		return false
	}

	return v.StringValue.Equal(o.StringValue)
}

func (v DigestedStringValue) ToStringValue(_ context.Context) (basetypes.StringValue, diag.Diagnostics) {
	return v.StringValue, nil
}

func (v DigestedStringValue) StringSemanticEquals(_ context.Context, other basetypes.StringValuable) (bool, diag.Diagnostics) {
	otherValue, diags := other.ToStringValue(context.Background())
	if diags.HasError() || v.IsNull() || v.IsUnknown() || otherValue.IsNull() || otherValue.IsUnknown() {
		return false, diags
	}

	return digestedValuesEqual(v.ValueString(), otherValue.ValueString(), false), diags
}

type DigestedBase64StringValue struct {
	basetypes.StringValue
}

func (v DigestedBase64StringValue) Type(_ context.Context) attr.Type {
	return DigestedBase64StringType{}
}

func (v DigestedBase64StringValue) Equal(other attr.Value) bool {
	o, ok := other.(DigestedBase64StringValue)
	if !ok {
		return false
	}

	return v.StringValue.Equal(o.StringValue)
}

func (v DigestedBase64StringValue) ToStringValue(_ context.Context) (basetypes.StringValue, diag.Diagnostics) {
	return v.StringValue, nil
}

func (v DigestedBase64StringValue) StringSemanticEquals(_ context.Context, other basetypes.StringValuable) (bool, diag.Diagnostics) {
	otherValue, diags := other.ToStringValue(context.Background())
	if diags.HasError() || v.IsNull() || v.IsUnknown() || otherValue.IsNull() || otherValue.IsUnknown() {
		return false, diags
	}

	return digestedValuesEqual(v.ValueString(), otherValue.ValueString(), true), diags
}

func digestedValuesEqual(left, right string, decodeBase64 bool) bool {
	leftDigest, ok := digestedStateValue(left)
	if ok {
		return leftDigest == digestForStateValue(right, decodeBase64)
	}

	rightDigest, ok := digestedStateValue(right)
	if ok {
		return rightDigest == digestForStateValue(left, decodeBase64)
	}

	return digestForStateValue(left, decodeBase64) == digestForStateValue(right, decodeBase64)
}

func digestForStateValue(value string, decodeBase64 bool) string {
	if digest, ok := digestedStateValue(value); ok {
		return digest
	}

	data := []byte(value)
	if decodeBase64 {
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return ""
		}
		data = decoded
	}

	return digestutil.MustDigestBytes(digestutil.AlgorithmBlake3, data)
}

func digestedStateValue(value string) (string, bool) {
	if _, err := digestutil.Algorithm(value); err != nil {
		return "", false
	}
	return value, true
}

func DigestStateString(digest string) string {
	return digest
}

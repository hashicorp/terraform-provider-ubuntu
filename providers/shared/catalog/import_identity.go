// Copyright IBM Corp. 2026

package catalog

import (
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
)

type importIdentityField struct {
	Key         string
	Description string
}

func singleStringImportIdentity(field importIdentityField) *engine.ResourceImportIdentity {
	return &engine.ResourceImportIdentity{
		Attributes: map[string]identityschema.Attribute{
			field.Key: identityschema.StringAttribute{
				RequiredForImport: true,
				Description:       field.Description,
			},
		},
		FormatID: func(values map[string]interface{}) (string, error) {
			return requireImportIdentityString(values, field.Key)
		},
	}
}

func joinedStringImportIdentity(sep string, fields ...importIdentityField) *engine.ResourceImportIdentity {
	attrs := make(map[string]identityschema.Attribute, len(fields))
	for _, field := range fields {
		attrs[field.Key] = identityschema.StringAttribute{
			RequiredForImport: true,
			Description:       field.Description,
		}
	}

	return &engine.ResourceImportIdentity{
		Attributes: attrs,
		FormatID: func(values map[string]interface{}) (string, error) {
			parts := make([]string, 0, len(fields))
			missing := make([]string, 0)
			for _, field := range fields {
				value, ok := values[field.Key].(string)
				if !ok || strings.TrimSpace(value) == "" {
					missing = append(missing, field.Key)
					continue
				}
				parts = append(parts, value)
			}
			if len(missing) > 0 {
				return "", &engine.MissingImportIdentityError{Missing: missing}
			}
			return strings.Join(parts, sep), nil
		},
	}
}

func requireImportIdentityString(values map[string]interface{}, key string) (string, error) {
	value, ok := values[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", &engine.MissingImportIdentityError{Missing: []string{key}}
	}
	return value, nil
}

// Copyright IBM Corp. 2026

package catalog

import (
	"fmt"

	actionschema "github.com/hashicorp/terraform-plugin-framework/action/schema"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
)

func actionAttributesFromPluginContract(attributes map[string]pluginsdk.Attribute, overrides map[string]actionschema.Attribute) map[string]actionschema.Attribute {
	if len(attributes) == 0 && len(overrides) == 0 {
		return nil
	}

	result := make(map[string]actionschema.Attribute, len(attributes)+len(overrides))
	for name, attribute := range attributes {
		result[name] = actionAttributeFromPluginContract(attribute)
	}
	for name, attribute := range overrides {
		result[name] = attribute
	}
	return result
}

func dataSourceAttributesFromPluginContract(attributes map[string]pluginsdk.Attribute, overrides map[string]datasourceschema.Attribute) map[string]datasourceschema.Attribute {
	if len(attributes) == 0 && len(overrides) == 0 {
		return nil
	}

	result := make(map[string]datasourceschema.Attribute, len(attributes)+len(overrides))
	for name, attribute := range attributes {
		result[name] = dataSourceAttributeFromPluginContract(attribute)
	}
	for name, attribute := range overrides {
		result[name] = attribute
	}
	return result
}

func resourceAttributesFromPluginContract(attributes map[string]pluginsdk.Attribute, overrides map[string]resourceschema.Attribute) map[string]resourceschema.Attribute {
	if len(attributes) == 0 && len(overrides) == 0 {
		return nil
	}

	result := make(map[string]resourceschema.Attribute, len(attributes)+len(overrides))
	for name, attribute := range attributes {
		result[name] = resourceAttributeFromPluginContract(attribute)
	}
	for name, attribute := range overrides {
		result[name] = attribute
	}
	return result
}

func resourceBlocksFromPluginContract(blocks map[string]pluginsdk.Block, overrides map[string]resourceschema.Block) map[string]resourceschema.Block {
	if len(blocks) == 0 && len(overrides) == 0 {
		return nil
	}

	result := make(map[string]resourceschema.Block, len(blocks)+len(overrides))
	for name, block := range blocks {
		result[name] = resourceBlockFromPluginContract(block)
	}
	for name, block := range overrides {
		result[name] = block
	}
	return result
}

func resourceAttributeFromPluginContract(attribute pluginsdk.Attribute) resourceschema.Attribute {
	switch attribute.Type {
	case pluginsdk.AttrString:
		return resourceschema.StringAttribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
			Computed:    attribute.Computed,
			Sensitive:   attribute.Sensitive,
		}
	case pluginsdk.AttrInt:
		return resourceschema.Int64Attribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
			Computed:    attribute.Computed,
		}
	case pluginsdk.AttrBool:
		return resourceschema.BoolAttribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
			Computed:    attribute.Computed,
		}
	case pluginsdk.AttrList:
		return resourceschema.ListAttribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
			Computed:    attribute.Computed,
			Sensitive:   attribute.Sensitive,
			ElementType: types.StringType,
		}
	case pluginsdk.AttrMap:
		return resourceschema.MapAttribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
			Computed:    attribute.Computed,
			Sensitive:   attribute.Sensitive,
			ElementType: types.StringType,
		}
	default:
		panic(fmt.Sprintf("unsupported pluginsdk attribute type %q", attribute.Type))
	}
}

func actionAttributeFromPluginContract(attribute pluginsdk.Attribute) actionschema.Attribute {
	switch attribute.Type {
	case pluginsdk.AttrString:
		return actionschema.StringAttribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
		}
	case pluginsdk.AttrInt:
		return actionschema.Int64Attribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
		}
	case pluginsdk.AttrBool:
		return actionschema.BoolAttribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
		}
	case pluginsdk.AttrList:
		return actionschema.ListAttribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
			ElementType: types.StringType,
		}
	case pluginsdk.AttrMap:
		return actionschema.MapAttribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
			ElementType: types.StringType,
		}
	default:
		panic(fmt.Sprintf("unsupported pluginsdk attribute type %q", attribute.Type))
	}
}

func dataSourceAttributeFromPluginContract(attribute pluginsdk.Attribute) datasourceschema.Attribute {
	switch attribute.Type {
	case pluginsdk.AttrString:
		return datasourceschema.StringAttribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
			Computed:    attribute.Computed,
			Sensitive:   attribute.Sensitive,
		}
	case pluginsdk.AttrInt:
		return datasourceschema.Int64Attribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
			Computed:    attribute.Computed,
		}
	case pluginsdk.AttrBool:
		return datasourceschema.BoolAttribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
			Computed:    attribute.Computed,
		}
	case pluginsdk.AttrList:
		return datasourceschema.ListAttribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
			Computed:    attribute.Computed,
			Sensitive:   attribute.Sensitive,
			ElementType: types.StringType,
		}
	case pluginsdk.AttrMap:
		return datasourceschema.MapAttribute{
			Description: attribute.Description,
			Required:    attribute.Required,
			Optional:    attribute.Optional,
			Computed:    attribute.Computed,
			Sensitive:   attribute.Sensitive,
			ElementType: types.StringType,
		}
	default:
		panic(fmt.Sprintf("unsupported pluginsdk attribute type %q", attribute.Type))
	}
}

func resourceBlockFromPluginContract(block pluginsdk.Block) resourceschema.Block {
	switch block.Kind {
	case pluginsdk.BlockSingleNested:
		return resourceschema.SingleNestedBlock{
			Description: block.Description,
			Attributes:  resourceAttributesFromPluginContract(block.Attributes, nil),
		}
	default:
		panic(fmt.Sprintf("unsupported pluginsdk block kind %q", block.Kind))
	}
}

package catalog

import (
	frameworkaction "github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assets"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/engine"
	coreprovider "github.com/hashicorp/terraform-provider-ubuntu/providers/shared/serving"
)

type ResourceFactoryBuilder func(config coreprovider.ProviderConfig) resource.Resource

type DataSourceFactoryBuilder func(config coreprovider.ProviderConfig) datasource.DataSource

type ActionFactoryBuilder func(config coreprovider.ProviderConfig) frameworkaction.Action

type Fragment struct {
	ID                       string
	Scope                    string
	RuntimeModules           []string
	Resources                []engine.ResourceDefinition
	DataSources              []engine.DataSourceDefinition
	Actions                  []engine.ActionDefinition
	CustomResourceTypes      []string
	CustomDataSourceTypes    []string
	CustomActionTypes        []string
	CustomResourceBuilders   []ResourceFactoryBuilder
	CustomDataSourceBuilders []DataSourceFactoryBuilder
	CustomActionBuilders     []ActionFactoryBuilder
}

type Catalog struct {
	Fragments                []Fragment
	RuntimeModules           []string
	Resources                []engine.ResourceDefinition
	DataSources              []engine.DataSourceDefinition
	Actions                  []engine.ActionDefinition
	CustomResourceTypes      []string
	CustomDataSourceTypes    []string
	CustomActionTypes        []string
	CustomResourceBuilders   []ResourceFactoryBuilder
	CustomDataSourceBuilders []DataSourceFactoryBuilder
	CustomActionBuilders     []ActionFactoryBuilder
}

func Compose(fragments ...Fragment) Catalog {
	catalog := Catalog{Fragments: append([]Fragment(nil), fragments...)}
	for _, fragment := range fragments {
		catalog.RuntimeModules = append(catalog.RuntimeModules, fragment.RuntimeModules...)
		catalog.Resources = append(catalog.Resources, fragment.Resources...)
		catalog.DataSources = append(catalog.DataSources, fragment.DataSources...)
		catalog.Actions = append(catalog.Actions, fragment.Actions...)
		catalog.CustomResourceTypes = append(catalog.CustomResourceTypes, fragment.CustomResourceTypes...)
		catalog.CustomDataSourceTypes = append(catalog.CustomDataSourceTypes, fragment.CustomDataSourceTypes...)
		catalog.CustomActionTypes = append(catalog.CustomActionTypes, fragment.CustomActionTypes...)
		catalog.CustomResourceBuilders = append(catalog.CustomResourceBuilders, fragment.CustomResourceBuilders...)
		catalog.CustomDataSourceBuilders = append(catalog.CustomDataSourceBuilders, fragment.CustomDataSourceBuilders...)
		catalog.CustomActionBuilders = append(catalog.CustomActionBuilders, fragment.CustomActionBuilders...)
	}

	return catalog
}

func (c Catalog) AssetSpec() assets.Spec {
	modules := append([]string(nil), c.RuntimeModules...)
	for _, def := range c.Resources {
		modules = append(modules, def.RuntimeModule)
	}
	for _, def := range c.DataSources {
		modules = append(modules, def.RuntimeModule)
	}
	for _, def := range c.Actions {
		modules = append(modules, def.RuntimeModule)
	}

	return assets.Spec{
		ExecutorArches: []string{"amd64", "arm64"},
		PluginModules:  uniqueStrings(modules),
	}
}

func (c Catalog) Register(p *coreprovider.BaseProvider, config coreprovider.ProviderConfig) {
	for _, def := range c.Resources {
		def := def
		p.RegisterResource(func() resource.Resource {
			if def.ImportIdentity != nil {
				return engine.NewGenericIdentityResource(config.ResourceType(def.TypeName), def)
			}
			return engine.NewGenericResource(config.ResourceType(def.TypeName), def)
		})
	}
	for _, build := range c.CustomResourceBuilders {
		build := build
		p.RegisterResource(func() resource.Resource {
			return build(config)
		})
	}
	for _, def := range c.DataSources {
		def := def
		p.RegisterDataSource(func() datasource.DataSource {
			return engine.NewGenericDataSource(config.DataSourceType(def.TypeName), def)
		})
	}
	for _, build := range c.CustomDataSourceBuilders {
		build := build
		p.RegisterDataSource(func() datasource.DataSource {
			return build(config)
		})
	}
	for _, def := range c.Actions {
		def := def
		p.RegisterAction(func() frameworkaction.Action {
			return engine.NewGenericAction(config.ActionType(def.TypeName), def)
		})
	}
	for _, build := range c.CustomActionBuilders {
		build := build
		p.RegisterAction(func() frameworkaction.Action {
			return build(config)
		})
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

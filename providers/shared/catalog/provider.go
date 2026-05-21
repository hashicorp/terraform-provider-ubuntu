package catalog

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	coreprovider "github.com/hashicorp/terraform-provider-ubuntu/providers/shared/serving"
)

var serveCatalogProvider = providerserver.Serve

func (s ProviderSpec) ReleaseBinaryName(version string) string {
	return fmt.Sprintf("terraform-provider-%s_v%s", s.Name, version)
}

func (s ProviderSpec) ReleaseArchiveName(version string, platform ProviderPlatform) string {
	return fmt.Sprintf("terraform-provider-%s_%s_%s_%s.zip", s.Name, version, platform.OS, platform.Arch)
}

func ServeProvider(ctx context.Context, config coreprovider.ProviderConfig, address string, providerCatalog Catalog) error {
	p := coreprovider.NewBaseProvider(config)
	providerCatalog.Register(p, config)

	return serveCatalogProvider(ctx, func() provider.Provider {
		return p
	}, providerserver.ServeOpts{
		Address: address,
	})
}

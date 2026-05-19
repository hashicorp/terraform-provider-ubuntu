package catalog

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type ProviderLifecycle string

type ReleaseBump string

type ReleaseLayer struct {
	Name            string
	FragmentAliases []string
	PathHints       []string
	DefaultBump     ReleaseBump
}

type DownstreamRepo struct {
	Owner            string
	Name             string
	DefaultBranch    string
	ReleaseTagFormat string
	SyncEnabled      bool
	ReadmeTitle      string
}

const (
	ProviderLifecycleInternal ProviderLifecycle = "internal"
	ProviderLifecycleDirect   ProviderLifecycle = "direct"
	ProviderLifecycleBeta     ProviderLifecycle = "beta"

	ReleaseBumpPatch ReleaseBump = "patch"
	ReleaseBumpMinor ReleaseBump = "minor"
	ReleaseBumpMajor ReleaseBump = "major"
)

type ProviderSpec struct {
	Name                          string
	Address                       string
	Catalog                       Catalog
	ReleasePlatforms              []ProviderPlatform
	ReleaseLayers                 []string
	Lifecycle                     ProviderLifecycle
	Publishable                   bool
	SupportPolicy                 string
	DefaultAcceptanceLane         string
	SupportMatrixFixture          string
	VersionTagPrefix              string
	InitialVersion                string
	AllowDocsOnlySync             bool
	RequiresSupportMatrixEvidence bool
	DownstreamRepo                DownstreamRepo
}

type ProviderPlatform struct {
	OS   string
	Arch string
}

type FragmentFactory func() Fragment

type ProviderBlueprint struct {
	Name        string
	Address     string
	Fragments   []string
	SpecOptions providerSpecOptions
}

type providerSpecOptions struct {
	Lifecycle                     ProviderLifecycle
	Publishable                   bool
	SupportPolicy                 string
	DefaultAcceptanceLane         string
	SupportMatrixFixture          string
	VersionTagPrefix              string
	ReleasePlatforms              []ProviderPlatform
	ReleaseLayers                 []string
	InitialVersion                string
	AllowDocsOnlySync             bool
	RequiresSupportMatrixEvidence bool
	DownstreamRepo                DownstreamRepo
}

func defaultProviderReleasePlatforms() []ProviderPlatform {
	return []ProviderPlatform{
		{OS: "linux", Arch: "amd64"},
		{OS: "linux", Arch: "arm64"},
		{OS: "darwin", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
		{OS: "windows", Arch: "amd64"},
		{OS: "windows", Arch: "arm64"},
	}
}

func defaultProviderInitialVersion() string {
	return "0.1.0"
}

func defaultDownstreamRepo(name, address string, publishable bool) DownstreamRepo {
	if !publishable {
		return DownstreamRepo{}
	}

	owner := ""
	parts := strings.Split(strings.TrimSpace(address), "/")
	if len(parts) >= 2 {
		owner = parts[len(parts)-2]
	}

	return DownstreamRepo{
		Owner:            owner,
		Name:             "terraform-provider-" + name,
		DefaultBranch:    "main",
		ReleaseTagFormat: "v%s",
		SyncEnabled:      true,
		ReadmeTitle:      fmt.Sprintf("Terraform Provider: %s", cases.Title(language.English).String(name)),
	}
}

func newProviderSpec(name, address string, providerCatalog Catalog, opts providerSpecOptions) ProviderSpec {
	releasePlatforms := append([]ProviderPlatform(nil), opts.ReleasePlatforms...)
	if len(releasePlatforms) == 0 {
		releasePlatforms = append([]ProviderPlatform(nil), defaultProviderReleasePlatforms()...)
	}
	releaseLayers := append([]string(nil), opts.ReleaseLayers...)
	lifecycle := opts.Lifecycle
	if lifecycle == "" {
		lifecycle = ProviderLifecycleDirect
	}
	versionTagPrefix := opts.VersionTagPrefix
	if versionTagPrefix == "" {
		versionTagPrefix = name
	}
	initialVersion := strings.TrimSpace(opts.InitialVersion)
	if initialVersion == "" {
		initialVersion = defaultProviderInitialVersion()
	}
	downstreamRepo := opts.DownstreamRepo
	if downstreamRepo == (DownstreamRepo{}) {
		downstreamRepo = defaultDownstreamRepo(name, address, opts.Publishable)
	}
	allowDocsOnlySync := opts.AllowDocsOnlySync
	if opts.Publishable && downstreamRepo.SyncEnabled && !allowDocsOnlySync {
		allowDocsOnlySync = true
	}
	requiresSupportMatrixEvidence := opts.RequiresSupportMatrixEvidence
	if strings.TrimSpace(opts.SupportMatrixFixture) != "" && !requiresSupportMatrixEvidence {
		requiresSupportMatrixEvidence = true
	}

	return ProviderSpec{
		Name:                          name,
		Address:                       address,
		Catalog:                       providerCatalog,
		ReleasePlatforms:              releasePlatforms,
		ReleaseLayers:                 releaseLayers,
		Lifecycle:                     lifecycle,
		Publishable:                   opts.Publishable,
		SupportPolicy:                 opts.SupportPolicy,
		DefaultAcceptanceLane:         opts.DefaultAcceptanceLane,
		SupportMatrixFixture:          opts.SupportMatrixFixture,
		VersionTagPrefix:              versionTagPrefix,
		InitialVersion:                initialVersion,
		AllowDocsOnlySync:             allowDocsOnlySync,
		RequiresSupportMatrixEvidence: requiresSupportMatrixEvidence,
		DownstreamRepo:                downstreamRepo,
	}
}

func releaseLayerRegistry() []ReleaseLayer {
	return []ReleaseLayer{
		{
			Name:            "linux-base",
			FragmentAliases: []string{"linux_lifecycle", "linux_commands", "linux_files", "linux_identity", "linux_packages", "linux_facts", "linux_network", "linux_tls"},
			PathHints: []string{
				"providers/shared/distro/",
				"providers/shared/engine/",
				"providers/shared/hostsession/",
				"providers/shared/serving/",
				"tools/providers/",
				"providers/shared/transport/",
				"executor/",
				"guest/packs/base/",
				"guest/packs/linux/",
				"guest/sdk/",
				"providers/shared/catalog/linux_",
				"providers/shared/catalog/import_identity",
				"providers/shared/catalog/locks",
				"providers/shared/catalog/modules.go",
				"providers/shared/catalog/provider.go",
				"providers/shared/catalog/resource_policies",
			},
			DefaultBump: ReleaseBumpPatch,
		},
		{
			Name:            "provider-manifest",
			FragmentAliases: []string{},
			PathHints:       []string{"providers/shared/catalog/provider_manifest.go"},
			DefaultBump:     ReleaseBumpPatch,
		},
		{
			Name:            "systemd-shared",
			FragmentAliases: []string{"systemd_units"},
			PathHints:       []string{"guest/packs/systemd/", "providers/shared/catalog/systemd.go"},
			DefaultBump:     ReleaseBumpMinor,
		},
		{
			Name:            "debian-family",
			FragmentAliases: []string{"debian_apt", "debian_trust"},
			PathHints:       []string{"guest/packs/debian/", "providers/shared/catalog/debian_", "providers/debian/"},
			DefaultBump:     ReleaseBumpMinor,
		},
		{
			Name:            "redhat-family",
			FragmentAliases: []string{"redhat_dnf", "redhat_firewalld", "redhat_trust"},
			PathHints:       []string{"guest/packs/redhat/", "providers/shared/catalog/redhat_"},
			DefaultBump:     ReleaseBumpMinor,
		},
		{
			Name:            "ubuntu-shim",
			FragmentAliases: []string{"ubuntu_delta", "ubuntu_ufw"},
			PathHints:       []string{"guest/packs/ubuntu/", "providers/shared/catalog/ubuntu", "providers/ubuntu/"},
			DefaultBump:     ReleaseBumpMinor,
		},
		{
			Name:            "rocky-shim",
			FragmentAliases: []string{"rocky_delta"},
			PathHints:       []string{"providers/shared/catalog/rocky.go", "providers/rocky/"},
			DefaultBump:     ReleaseBumpMinor,
		},
		{
			Name:            "rhel-shim",
			FragmentAliases: []string{"rhel_delta"},
			PathHints:       []string{"providers/shared/catalog/rhel.go", "providers/rhel/"},
			DefaultBump:     ReleaseBumpMinor,
		},
	}
}

func ReleaseLayers() []ReleaseLayer {
	layers := releaseLayerRegistry()
	cloned := make([]ReleaseLayer, 0, len(layers))
	for _, layer := range layers {
		cloned = append(cloned, ReleaseLayer{
			Name:            layer.Name,
			FragmentAliases: append([]string(nil), layer.FragmentAliases...),
			PathHints:       append([]string(nil), layer.PathHints...),
			DefaultBump:     layer.DefaultBump,
		})
	}
	return cloned
}

func LookupReleaseLayer(name string) (ReleaseLayer, bool) {
	for _, layer := range releaseLayerRegistry() {
		if layer.Name == name {
			return ReleaseLayer{
				Name:            layer.Name,
				FragmentAliases: append([]string(nil), layer.FragmentAliases...),
				PathHints:       append([]string(nil), layer.PathHints...),
				DefaultBump:     layer.DefaultBump,
			}, true
		}
	}
	return ReleaseLayer{}, false
}

func ImpactedProvidersForReleaseLayer(name string) []string {
	providers := make([]string, 0)
	for _, spec := range ProviderSpecs() {
		if !spec.Publishable {
			continue
		}
		for _, layer := range spec.ReleaseLayers {
			if layer == name {
				providers = append(providers, spec.Name)
				break
			}
		}
	}
	sort.Strings(providers)
	return providers
}

func fragmentRegistry() map[string]FragmentFactory {
	return map[string]FragmentFactory{
		"linux_lifecycle":  LinuxLifecycle,
		"linux_commands":   LinuxCommands,
		"linux_files":      LinuxFiles,
		"linux_identity":   LinuxIdentity,
		"linux_packages":   LinuxPackages,
		"linux_facts":      LinuxFacts,
		"linux_network":    LinuxNetwork,
		"linux_tls":        LinuxTLS,
		"systemd_units":    Systemd,
		"debian_apt":       DebianApt,
		"debian_trust":     DebianTrust,
		"ubuntu_ufw":       UbuntuUFW,
		"ubuntu_delta":     UbuntuDelta,
		"redhat_dnf":       RedHatDnf,
		"redhat_firewalld": RedHatFirewalld,
		"redhat_trust":     RedHatTrust,
		"rocky_delta":      RockyDelta,
		"rhel_delta":       RHELDelta,
	}
}

func buildCatalogFromFragments(fragmentIDs ...string) Catalog {
	registry := fragmentRegistry()
	fragments := make([]Fragment, 0, len(fragmentIDs))
	for _, fragmentID := range fragmentIDs {
		factory, ok := registry[fragmentID]
		if !ok {
			panic(fmt.Sprintf("unknown fragment %q", fragmentID))
		}
		fragments = append(fragments, factory())
	}
	return Compose(fragments...)
}

func providerBlueprints() []ProviderBlueprint {
	return []ProviderBlueprint{
		{
			Name:      "linux",
			Address:   "registry.terraform.io/hashicorp/linux",
			Fragments: []string{"linux_lifecycle", "linux_commands", "linux_files", "linux_identity", "linux_packages", "linux_facts", "linux_network", "linux_tls"},
			SpecOptions: providerSpecOptions{
				Lifecycle:             ProviderLifecycleInternal,
				Publishable:           false,
				SupportPolicy:         "internal-only",
				DefaultAcceptanceLane: "acceptance:smoke",
				ReleaseLayers:         []string{"linux-base"},
			},
		},
		{
			Name:      "debian",
			Address:   "registry.terraform.io/hashicorp/debian",
			Fragments: []string{"linux_lifecycle", "linux_commands", "linux_files", "linux_identity", "linux_packages", "linux_facts", "linux_network", "linux_tls", "systemd_units", "debian_apt", "debian_trust"},
			SpecOptions: providerSpecOptions{
				Lifecycle:     ProviderLifecycleDirect,
				Publishable:   true,
				SupportPolicy: "debian-direct",
				ReleaseLayers: []string{"linux-base", "provider-manifest", "systemd-shared", "debian-family"},
			},
		},
		{
			Name:      "rocky",
			Address:   "registry.terraform.io/hashicorp/rocky",
			Fragments: []string{"linux_lifecycle", "linux_commands", "linux_files", "linux_identity", "linux_packages", "linux_facts", "linux_network", "linux_tls", "systemd_units", "redhat_dnf", "redhat_firewalld", "redhat_trust", "rocky_delta"},
			SpecOptions: providerSpecOptions{
				Lifecycle:             ProviderLifecycleBeta,
				Publishable:           true,
				SupportPolicy:         "rocky-beta",
				DefaultAcceptanceLane: "acceptance:rocky:smoke",
				SupportMatrixFixture:  "smoke_rocky",
				ReleaseLayers:         []string{"linux-base", "provider-manifest", "systemd-shared", "redhat-family", "rocky-shim"},
			},
		},
		{
			Name:      "rhel",
			Address:   "registry.terraform.io/hashicorp/rhel",
			Fragments: []string{"linux_lifecycle", "linux_commands", "linux_files", "linux_identity", "linux_packages", "linux_facts", "linux_network", "linux_tls", "systemd_units", "redhat_dnf", "redhat_firewalld", "redhat_trust", "rhel_delta"},
			SpecOptions: providerSpecOptions{
				Lifecycle:            ProviderLifecycleBeta,
				Publishable:          true,
				SupportPolicy:        "rhel-beta",
				SupportMatrixFixture: "smoke_rhel",
				ReleaseLayers:        []string{"linux-base", "provider-manifest", "systemd-shared", "redhat-family", "rhel-shim"},
			},
		},
		{
			Name:      "ubuntu",
			Address:   "registry.terraform.io/hashicorp/ubuntu",
			Fragments: []string{"linux_lifecycle", "linux_commands", "linux_files", "linux_identity", "linux_packages", "linux_facts", "linux_network", "linux_tls", "systemd_units", "debian_apt", "debian_trust", "ubuntu_ufw", "ubuntu_delta"},
			SpecOptions: providerSpecOptions{
				Lifecycle:             ProviderLifecycleBeta,
				Publishable:           true,
				SupportPolicy:         "ubuntu-beta",
				DefaultAcceptanceLane: "acceptance:smoke",
				SupportMatrixFixture:  "smoke",
				ReleaseLayers:         []string{"linux-base", "provider-manifest", "systemd-shared", "debian-family", "ubuntu-shim"},
			},
		},
	}
}

func blueprintCatalog(name string) Catalog {
	for _, blueprint := range providerBlueprints() {
		if blueprint.Name == name {
			return buildCatalogFromFragments(blueprint.Fragments...)
		}
	}
	panic(fmt.Sprintf("unknown provider blueprint %q", name))
}

func ProviderSpecs() []ProviderSpec {
	blueprints := providerBlueprints()
	specs := make([]ProviderSpec, 0, len(blueprints))
	for _, blueprint := range blueprints {
		specs = append(specs, newProviderSpec(
			blueprint.Name,
			blueprint.Address,
			buildCatalogFromFragments(blueprint.Fragments...),
			blueprint.SpecOptions,
		))
	}
	return specs
}

func LookupProviderSpec(name string) (ProviderSpec, bool) {
	for _, spec := range ProviderSpecs() {
		if spec.Name == name {
			return spec, true
		}
	}
	return ProviderSpec{}, false
}

func MustProviderSpec(name string) ProviderSpec {
	if spec, ok := LookupProviderSpec(name); ok {
		return spec
	}
	panic(fmt.Sprintf("unknown provider spec %q", name))
}

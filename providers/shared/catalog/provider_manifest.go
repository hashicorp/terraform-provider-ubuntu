package catalog

import "fmt"

type ProviderLifecycle string

type DownstreamRepo struct {
	Owner string
	Name string
	DefaultBranch string
	ReleaseTagFormat string
	SyncEnabled bool
	ReadmeTitle string
}

const (
	ProviderLifecycleInternal ProviderLifecycle = "internal"
	ProviderLifecycleDirect ProviderLifecycle = "direct"
	ProviderLifecycleBeta ProviderLifecycle = "beta"
)

type ProviderSpec struct {
	Name string
	Address string
	Catalog Catalog
	ReleasePlatforms []ProviderPlatform
	ReleaseLayers []string
	Lifecycle ProviderLifecycle
	Publishable bool
	SupportPolicy string
	DefaultAcceptanceLane string
	SupportMatrixFixture string
	VersionTagPrefix string
	InitialVersion string
	AllowDocsOnlySync bool
	RequiresSupportMatrixEvidence bool
	DownstreamRepo DownstreamRepo
}

type ProviderPlatform struct {
	OS string
	Arch string
}

func ProviderSpecs() []ProviderSpec {
	return []ProviderSpec{
		{
			Name: "ubuntu",
			Address: "registry.terraform.io/hashicorp/ubuntu",
			Catalog: Compose(
				LinuxLifecycle(),
				LinuxCommands(),
				LinuxFiles(),
				LinuxIdentity(),
				LinuxPackages(),
				LinuxFacts(),
				LinuxNetwork(),
				LinuxTLS(),
				Systemd(),
				SystemdTimesync(),
				DebianApt(),
				DebianTrust(),
				UbuntuUFW(),
				UbuntuDelta(),
			),
			ReleasePlatforms: []ProviderPlatform{{OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64"}, {OS: "darwin", Arch: "amd64"}, {OS: "darwin", Arch: "arm64"}, {OS: "windows", Arch: "amd64"}, {OS: "windows", Arch: "arm64"}},
			ReleaseLayers: []string{"linux-base", "provider-manifest", "systemd-shared", "debian-family", "ubuntu-shim"},
			Lifecycle: ProviderLifecycle("beta"),
			Publishable: true,
			SupportPolicy: "ubuntu-beta",
			DefaultAcceptanceLane: "acceptance:smoke",
			SupportMatrixFixture: "smoke",
			VersionTagPrefix: "ubuntu",
			InitialVersion: "0.1.0",
			AllowDocsOnlySync: true,
			RequiresSupportMatrixEvidence: true,
			DownstreamRepo: DownstreamRepo{Owner: "hashicorp", Name: "terraform-provider-ubuntu", DefaultBranch: "main", ReleaseTagFormat: "v%s", SyncEnabled: true, ReadmeTitle: "Terraform Provider: Ubuntu"},
		},
	}
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

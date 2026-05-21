package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assets"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/catalog"
)

var buildScopedExecutorBinaryFunc = buildScopedExecutorBinary

func main() {
	var (
		distDir   = flag.String("dist", "dist", "directory containing built wasm runtime modules and scoped executor outputs")
		repoRoot  = flag.String("root", ".", "repository root")
		providers = flag.String("providers", "", "comma-separated provider names to build; defaults to all")
	)
	flag.Parse()

	selected := make(map[string]struct{})
	if strings.TrimSpace(*providers) != "" {
		for _, name := range strings.Split(*providers, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				selected[name] = struct{}{}
			}
		}
	}

	for _, spec := range catalog.ProviderSpecs() {
		if len(selected) > 0 {
			if _, ok := selected[spec.Name]; !ok {
				continue
			}
		}

		if err := buildProviderScopedExecutors(*repoRoot, *distDir, spec); err != nil {
			fmt.Fprintf(os.Stderr, "build scoped executors for %s: %v\n", spec.Name, err)
			os.Exit(1)
		}
	}
}

func buildProviderScopedExecutors(repoRoot, distDir string, spec catalog.ProviderSpec) error {
	distRoot := filepath.Join(repoRoot, distDir)
	artifactDir := assets.ScopedProviderArtifactsDir(distRoot, spec.Name)
	if err := os.RemoveAll(artifactDir); err != nil {
		return fmt.Errorf("reset scoped artifact dir: %w", err)
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return fmt.Errorf("create scoped artifact dir: %w", err)
	}
	for _, dir := range []string{
		assets.ScopedExecutorsDir(distRoot, spec.Name),
		assets.ScopedPluginsDir(distRoot, spec.Name),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create scoped asset dir %s: %w", dir, err)
		}
	}

	pluginContent := make(map[string]assets.PluginManifestContent, len(spec.Catalog.AssetSpec().PluginModules))
	for _, module := range spec.Catalog.AssetSpec().PluginModules {
		data, err := os.ReadFile(filepath.Join(distRoot, module+".wasm"))
		if err != nil {
			return fmt.Errorf("read plugin %s: %w", module, err)
		}
		compressed, err := assets.CompressPluginModule(data)
		if err != nil {
			return fmt.Errorf("compress plugin %s: %w", module, err)
		}
		pluginContent[module] = assets.PluginManifestContent{Uncompressed: data, Compressed: compressed}
		if err := os.WriteFile(assets.ScopedCompressedPluginPath(distRoot, spec.Name, module), compressed, 0o644); err != nil {
			return fmt.Errorf("write compressed plugin %s: %w", module, err)
		}
	}

	manifest, err := assets.BuildManifestWithPluginContent(spec.Name, spec.Catalog.AssetSpec(), func(name string) (assets.PluginManifestContent, error) {
		content, ok := pluginContent[name]
		if !ok {
			return assets.PluginManifestContent{}, fmt.Errorf("plugin %s not staged", name)
		}
		return content, nil
	})
	if err != nil {
		return err
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(assets.ScopedManifestPath(distRoot, spec.Name), append(manifestBytes, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	manifestBase64 := base64.StdEncoding.EncodeToString(manifestBytes)
	for _, arch := range manifest.ExecutorArches {
		if err := buildScopedExecutorBinaryFunc(repoRoot, assets.ScopedExecutorBinaryPath(distRoot, spec.Name, arch), arch, manifestBase64, spec.Name); err != nil {
			return err
		}
	}

	return nil
}

func buildScopedExecutorBinary(repoRoot, outputPath, arch, manifestBase64, provider string) error {
	ldflags := strings.Join([]string{
		"-s",
		"-w",
		"-buildid=",
		"-X",
		"github.com/hashicorp/terraform-provider-ubuntu/executor/runtime.embeddedManifestBase64=" + manifestBase64,
		"-X",
		"github.com/hashicorp/terraform-provider-ubuntu/executor/runtime.embeddedManifestProvider=" + provider,
	}, " ")

	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags="+ldflags, "-o", outputPath, "./executor/daemon")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build executor linux/%s: %w\n%s", arch, err, strings.TrimSpace(string(output)))
	}

	return nil
}

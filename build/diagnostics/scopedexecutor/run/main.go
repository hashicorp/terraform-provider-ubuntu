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

	"github.com/hashicorp/terraform-provider-ubuntu/executor/runtime/plugincodec"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assetmanifest"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assets"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/catalog"
)

var buildScopedExecutorBinaryFunc = buildScopedExecutorBinary

const scopedExecutorGCFlags = "all=-l"

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

	assetSpec := spec.Catalog.AssetSpec()
	pluginContent := make(map[string]assetmanifest.PluginContent, len(assetSpec.PluginModules))
	for _, module := range assetSpec.PluginModules {
		data, err := os.ReadFile(filepath.Join(distRoot, module+".wasm"))
		if err != nil {
			return fmt.Errorf("read plugin %s: %w", module, err)
		}
		compressed, err := plugincodec.CompressPluginModule(data)
		if err != nil {
			return fmt.Errorf("compress plugin %s: %w", module, err)
		}
		pluginContent[module] = assetmanifest.PluginContent{Uncompressed: data, Compressed: compressed}
		if err := os.WriteFile(assets.ScopedCompressedPluginPath(distRoot, spec.Name, module), compressed, 0o644); err != nil {
			return fmt.Errorf("write compressed plugin %s: %w", module, err)
		}
	}

	manifest, err := assetmanifest.BuildWithPluginContent(spec.Name, assetSpec.ExecutorArches, assetSpec.PluginModules, func(name string) (assetmanifest.PluginContent, error) {
		content, ok := pluginContent[name]
		if !ok {
			return assetmanifest.PluginContent{}, fmt.Errorf("plugin %s not staged", name)
		}
		return content, nil
	})
	if err != nil {
		return err
	}

	embeddedManifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	manifestBase64 := base64.StdEncoding.EncodeToString(embeddedManifestBytes)
	for _, arch := range manifest.ExecutorArches {
		executorPath := assets.ScopedExecutorBinaryPath(distRoot, spec.Name, arch)
		if err := buildScopedExecutorBinaryFunc(repoRoot, executorPath, arch, manifestBase64, spec.Name); err != nil {
			return err
		}
		executorBytes, err := os.ReadFile(executorPath)
		if err != nil {
			return fmt.Errorf("read scoped executor %s: %w", arch, err)
		}
		if err := manifest.SetExecutor(arch, executorBytes); err != nil {
			return fmt.Errorf("build executor manifest record %s: %w", arch, err)
		}
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(assets.ScopedManifestPath(distRoot, spec.Name), append(manifestBytes, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	return nil
}

func buildScopedExecutorBinary(repoRoot, outputPath, arch, manifestBase64, provider string) error {
	ldflags := scopedExecutorLDFlags(manifestBase64, provider)

	cmd := exec.Command("go", scopedExecutorBuildArgs(outputPath, ldflags)...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build executor linux/%s: %w\n%s", arch, err, strings.TrimSpace(string(output)))
	}

	return nil
}

func scopedExecutorBuildArgs(outputPath, ldflags string) []string {
	return []string{"build", "-trimpath", "-buildvcs=false", "-gcflags=" + scopedExecutorGCFlags, "-ldflags=" + ldflags, "-o", outputPath, "./executor/daemon"}
}

func scopedExecutorLDFlags(manifestBase64, provider string) string {
	return strings.Join([]string{
		"-s",
		"-w",
		"-buildid=",
		"-X",
		"github.com/hashicorp/terraform-provider-ubuntu/executor/runtime.embeddedManifestBase64=" + manifestBase64,
		"-X",
		"github.com/hashicorp/terraform-provider-ubuntu/executor/runtime.embeddedManifestProvider=" + provider,
	}, " ")
}

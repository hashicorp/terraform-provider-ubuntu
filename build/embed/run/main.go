package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	providerlayout "github.com/hashicorp/terraform-provider-ubuntu/providers/layout"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assets"
	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/catalog"
)

func main() {
	var (
		distDir   = flag.String("dist", "dist", "directory containing built executor binaries and wasm runtime modules")
		repoRoot  = flag.String("root", ".", "repository root")
		providers = flag.String("providers", "", "comma-separated provider names to stage; defaults to all")
	)
	flag.Parse()

	selected := map[string]struct{}{}
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

		if err := stageProvider(*repoRoot, *distDir, spec); err != nil {
			fmt.Fprintf(os.Stderr, "stage %s: %v\n", spec.Name, err)
			os.Exit(1)
		}
	}
}

func stageProvider(repoRoot, distDir string, spec catalog.ProviderSpec) error {
	assetSpec := spec.Catalog.AssetSpec()
	embedRoot := providerlayout.EmbeddedAssetsDir(repoRoot, spec.Name)
	executorsDir := filepath.Join(embedRoot, "executors")
	pluginsDir := filepath.Join(embedRoot, "plugins")
	distRoot := filepath.Join(repoRoot, distDir)

	for _, target := range []string{executorsDir, pluginsDir} {
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
	}

	if err := os.Remove(filepath.Join(embedRoot, "manifest.json")); err != nil && !os.IsNotExist(err) {
		return err
	}

	for _, arch := range assetSpec.ExecutorArches {
		src := assets.ScopedExecutorBinaryPath(distRoot, spec.Name, arch)
		dst := filepath.Join(executorsDir, "executor-linux-"+arch)
		if err := copyFileWithMode(src, dst, 0o755); err != nil {
			return err
		}
	}

	for _, module := range assetSpec.PluginModules {
		src := assets.ScopedCompressedPluginPath(distRoot, spec.Name, module)
		dst := filepath.Join(pluginsDir, module+".wasm.zst")
		if err := copyFileWithMode(src, dst, 0o644); err != nil {
			return err
		}
	}

	return copyManifest(assets.ScopedManifestPath(distRoot, spec.Name), filepath.Join(embedRoot, "manifest.json"))
}

func copyFile(src, dst string) error {
	return copyFileWithMode(src, dst, 0o755)
}

func copyFileWithMode(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}

func copyManifest(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

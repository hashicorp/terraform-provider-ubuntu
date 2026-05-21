package providerlayout

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

const (
	DefaultDocsSubdir = "docs"

	registryDocsRoot  = "generated/providers"
	providerSourceDir = "provider"
)

func NormalizeDocsSubdir(docsDir string) (string, error) {
	docsDir = strings.TrimSpace(docsDir)
	if docsDir == "" {
		return DefaultDocsSubdir, nil
	}

	cleaned := filepath.ToSlash(filepath.Clean(docsDir))
	if cleaned == "." {
		return DefaultDocsSubdir, nil
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." || path.IsAbs(cleaned) {
		return "", fmt.Errorf("docs dir %q must stay within generated provider docs", docsDir)
	}
	return cleaned, nil
}

func RegistryDocsRepoPath(provider, docsSubdir string) string {
	return path.Join(registryDocsRoot, strings.TrimSpace(provider), docsSubdir)
}

func RegistryDocsDir(repoRoot, provider, docsSubdir string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(RegistryDocsRepoPath(provider, docsSubdir)))
}

func ProviderGuidesRepoPath(provider string) string {
	return path.Join("providers", strings.TrimSpace(provider), "docs")
}

func ProviderGuidesDir(repoRoot, provider string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(ProviderGuidesRepoPath(provider)))
}

func GeneratedProviderSourceRepoPath(provider string) string {
	return path.Join("generated", "providers", strings.TrimSpace(provider), providerSourceDir)
}

func GeneratedProviderSourceDir(repoRoot, provider string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(GeneratedProviderSourceRepoPath(provider)))
}

func EmbeddedAssetsRepoPath(provider string) string {
	return path.Join("generated", "providers", strings.TrimSpace(provider), "runtimeassets", "embeddata")
}

func EmbeddedAssetsDir(repoRoot, provider string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(EmbeddedAssetsRepoPath(provider)))
}

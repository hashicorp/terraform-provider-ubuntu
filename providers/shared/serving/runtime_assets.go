// Copyright IBM Corp. 2026

package serving

import (
	"io/fs"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/providers/shared/assets"
)

func ResolveAssetStore(spec assets.Spec, embeddedFS fs.FS, embeddedRoot, overrideDir string) (assets.Store, error) {
	overrideDir = strings.TrimSpace(overrideDir)

	var store assets.Store
	if overrideDir != "" {
		store = assets.NewDirStore(overrideDir, spec)
	} else {
		store = assets.NewEmbeddedStore(embeddedFS, embeddedRoot, spec)
	}

	if err := store.Validate(); err != nil {
		return nil, err
	}

	return store, nil
}

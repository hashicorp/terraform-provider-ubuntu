package assets

import (
	"fmt"

	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
)

func (a Asset) DigestForAlgorithm(algorithm string) (string, error) {
	if digest := a.Digests[algorithm]; digest != "" {
		return digest, nil
	}
	if a.Digest != "" {
		digestAlgorithm, err := digestutil.Algorithm(a.Digest)
		if err == nil && digestAlgorithm == algorithm {
			return a.Digest, nil
		}
	}
	return "", fmt.Errorf("asset missing %s digest", algorithm)
}

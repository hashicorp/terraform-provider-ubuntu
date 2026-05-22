package assetmanifest

import digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"

const DigestAlgorithm = digestutil.AlgorithmBlake3

func DigestBytes(data []byte) string {
	return digestutil.MustDigestBytes(DigestAlgorithm, data)
}

func VerifyDigest(data []byte, want string) error {
	return digestutil.VerifyDigest(data, want)
}

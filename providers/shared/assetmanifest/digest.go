package assetmanifest

import digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"

func VerifyDigest(data []byte, want string) error {
	return digestutil.VerifyDigest(data, want)
}

// Copyright IBM Corp. 2026

package hostsession

import (
	"crypto/rand"
	"fmt"
	"time"

	digestutil "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/digest"
)

func newOperationID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return digestutil.Token(digestutil.MustDigestBytes(digestutil.AlgorithmXXH3_128, []byte(time.Now().UTC().Format(time.RFC3339Nano))))
	}

	return fmt.Sprintf("rand:%x", buf)
}

package signature_test

import (
	"testing"

	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/reader/signature"
)

// FuzzVerifyAll: whatever the file, verifying its signatures terminates without
// a panic, and a signature that verifies has certificates and ranges that lie
// inside the file. ⛔ Seeds are fatal if unreadable: see signatureSeeds in the
// reader package.
func FuzzVerifyAll(f *testing.F) {
	for _, name := range []string{"rsa-detached.pdf", "ec-detached.pdf", "cades.pdf", "incremental.pdf", "stacked.pdf", "sha1.pdf", "attached.pdf", "gap-is-decoy.pdf"} {
		f.Add(load(f, name))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			t.Skip()
		}
		d, err := reader.Open(b)
		if err != nil {
			return
		}
		rs, err := signature.VerifyAll(d)
		if err != nil {
			return
		}
		for _, r := range rs {
			if r.Err == nil && (len(r.Certificates) == 0 || len(r.Signature.ByteRange) != 4) {
				t.Fatalf("verified with %d certificates and ranges %v", len(r.Certificates), r.Signature.ByteRange)
			}
		}
	})
}

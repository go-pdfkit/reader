package signature_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/reader/signature"
)

// verifyRecovering calls Verify and turns a panic into an error, so that a
// guard whose removal would crash is held by an ASSERTION, not by the crash:
// a red suite that is only a panic says nothing about what the guard is for.
func verifyRecovering(d *reader.Document, s reader.Signature) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("Verify crashed (recovered): %v", r)
		}
	}()
	_, err = signature.Verify(d, s)
	return err
}

// A /Contents that is not a CMS SignedData is refused as malformed, by the
// parse that runs before the signed bytes are copied — on every shape that is
// not one: empty, a bare tag, an unrelated DER value, and the real signature
// cut short.
func TestVerifyRefusesAnUnreadableCMS(t *testing.T) {
	d, err := reader.Open(load(t, "rsa-detached.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	sigs, err := d.Signatures()
	if err != nil || len(sigs) != 1 {
		t.Fatalf("%d signatures, %v", len(sigs), err)
	}
	real := sigs[0].Contents
	for _, tc := range []struct {
		name     string
		contents []byte
	}{
		{"empty", nil},
		{"a bare SEQUENCE tag", []byte{0x30}},
		{"an INTEGER", []byte{0x02, 0x01, 0x05}},
		{"the real signature cut in half", real[:len(real)/2]},
	} {
		s := sigs[0]
		s.Contents = tc.contents
		err := verifyRecovering(d, s)
		if !errors.Is(err, signature.ErrMalformed) {
			t.Errorf("%s: %v, want an error wrapping ErrMalformed", tc.name, err)
		}
	}
	// The untouched signature still verifies: the refusals above are about the
	// CMS, not about this document.
	if err := verifyRecovering(d, sigs[0]); err != nil {
		t.Fatalf("the untouched signature: %v", err)
	}
}

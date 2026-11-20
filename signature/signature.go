// Package signature verifies the digital signatures of a PDF file (ISO 32000-1
// §12.8) on top of go-authn/cms. reader finds the signature fields and the
// bytes each one covers, refusing a /ByteRange that does not bear out what it
// claims; this package checks the CMS signature over those bytes.
//
// What is verified: for adbe.pkcs7.detached and ETSI.CAdES.detached, that every
// signer's signature is a valid signature, by the certificate the CMS names, over
// exactly the bytes of the file's /ByteRange, and that the signed attributes
// carry their digest.
//
// What is refused, with an error that wraps [ErrUnsupported]: adbe.pkcs7.sha1
// and adbe.x509.rsa_sha1, whose digests are SHA-1, which go-authn/cms refuses;
// and every other /SubFilter (ETSI.RFC3161 document timestamps among them).
//
// What is the caller's, as in cms (RFC 5652 §5.6): whether the certificate
// that signed is TRUSTED — its chain, its validity period at the time of
// signing, its revocation — and what an incremental update after the signature
// changed. [Result.Certificates] is what to hand to crypto/x509 and to a
// revocation checker; [reader.Signature.CoversWholeFile] says whether anything
// followed the signed bytes. The claimed signing time (/M) is not evidence.
package signature

import (
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/go-authn/cms"
	"github.com/go-pdfkit/reader"
)

// The three answers go-authn/cms keeps apart, re-exported so a caller need not
// import it: errors.Is works on every error this package returns.
var (
	// ErrMalformed: the signature or the ranges it names cannot be read.
	ErrMalformed = cms.ErrMalformed
	// ErrUnsupported: well-formed but not something this package checks.
	ErrUnsupported = cms.ErrUnsupported
	// ErrInvalid: readable, supported, and the signature does not hold.
	ErrInvalid = cms.ErrInvalid
)

// A Result is the outcome for one signature.
type Result struct {
	Signature reader.Signature
	// Certificates are the certificates that signed, one per signer, when Err
	// is nil. Their trust is not judged here.
	Certificates []*x509.Certificate
	// Err is nil when the signature holds over its bytes.
	Err error
}

// Verify checks one signature of d.
func Verify(d *reader.Document, s reader.Signature) ([]*x509.Certificate, error) {
	switch s.SubFilter {
	case "adbe.pkcs7.detached", "ETSI.CAdES.detached":
	case "adbe.pkcs7.sha1", "adbe.x509.rsa_sha1":
		return nil, fmt.Errorf("%w: /SubFilter %s signs a SHA-1 digest, which is refused", ErrUnsupported, s.SubFilter)
	default:
		return nil, fmt.Errorf("%w: /SubFilter %q is not a detached CMS signature this package checks", ErrUnsupported, s.SubFilter)
	}
	if s.Err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, s.Err)
	}
	// ⛔ The CMS is parsed BEFORE the file is copied: SignedBytes copies every
	// signed byte, and a signature that cannot be read must not cost that.
	sd, err := cms.Parse(s.Contents)
	if err != nil {
		return nil, err
	}
	if sd.Content != nil {
		return nil, fmt.Errorf("%w: the CMS carries its content, but a %s signature is detached", ErrMalformed, s.SubFilter)
	}
	// SignedBytes validates the /ByteRange and the gap before reading a byte.
	signed, err := d.SignedBytes(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	return sd.Verify(signed)
}

// ErrTooManySignatures is wrapped by the error of every signature VerifyAll
// refuses to verify because the document has asked for more verification work
// than a real document needs.
var ErrTooManySignatures = errors.New("signature: more signatures to verify than this package will check in one document")

var (
	// maxVerifications bounds the distinct signatures VerifyAll verifies in one
	// document: a heavily signed real PDF has tens, not thousands.
	maxVerifications = 64
	// maxVerifyBytes bounds the signed bytes copied and hashed, in total.
	maxVerifyBytes int64 = 1 << 30
)

// VerifyAll lists d's signatures and verifies each. A signature that fails
// does not stop the others. The error is for a document whose signature fields
// cannot be listed at all; a document with none gives an empty slice.
func VerifyAll(d *reader.Document) ([]Result, error) {
	sigs, err := d.Signatures()
	if err != nil {
		return nil, err
	}
	// Verification is a function of the sub-filter, the ranges and the CMS bytes,
	// and it copies and hashes the whole file: fields that share one signature
	// dictionary are verified once, not once each.
	type key struct {
		sub string
		br  [4]int64
		cms [sha256.Size]byte
	}
	type verdict struct {
		certs []*x509.Certificate
		err   error
	}
	done := map[key]verdict{}
	var spent int64
	out := make([]Result, len(sigs))
	for i, s := range sigs {
		out[i].Signature = s
		if s.Err != nil { // refused cheaply, and carries no ranges worth keying
			out[i].Certificates, out[i].Err = Verify(d, s)
			continue
		}
		k := key{sub: s.SubFilter, cms: sha256.Sum256(s.Contents)}
		copy(k.br[:], s.ByteRange)
		v, ok := done[k]
		if !ok {
			// s.ByteRange[1] and [3] are lengths the file has already borne out.
			n := s.ByteRange[1] + s.ByteRange[3]
			if len(done) >= maxVerifications || n > maxVerifyBytes-spent {
				out[i].Err = fmt.Errorf("%w: the limit is %d distinct signatures and %d signed bytes per document", ErrTooManySignatures, maxVerifications, maxVerifyBytes)
				continue
			}
			spent += n
			v.certs, v.err = Verify(d, s)
			done[k] = v
		}
		// Each result owns its certificates: a caller editing one must not edit another.
		out[i].Certificates, out[i].Err = append([]*x509.Certificate(nil), v.certs...), v.err
	}
	return out, nil
}

// IsUnsupported reports whether err is a refusal to check, rather than a
// failed check.
func IsUnsupported(err error) bool { return errors.Is(err, ErrUnsupported) }

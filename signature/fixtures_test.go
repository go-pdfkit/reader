package signature_test

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔⛔ EVERY SIGNATURE UNDER testdata/ WAS MADE BY OPENSSL, NOT BY THIS MODULE.
// A fixture written by the code under test can only agree with it. The PDFs are
// built here with a /Contents placeholder, the bytes the /ByteRange leaves in
// are handed to `openssl cms -sign -binary -outform DER`, and the DER is spliced
// back (ISO 32000-1 §12.8.3.3). Private keys live in a temporary directory and
// are discarded: only certificates and signed files are committed.
//
// Regenerate with SIGNATURE_REGENERATE=1; the committed files are the record,
// because a fresh key every run would make every diff total.

// hexDigits is the size of the /Contents placeholder: 6000 bytes of DER.
const hexDigits = 12000

// openssl finds an OpenSSL with the cms command (macOS ships LibreSSL).
func openssl(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"/opt/homebrew/bin/openssl", "/usr/local/bin/openssl"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("openssl"); err == nil && exec.Command(p, "cms", "-help").Run() == nil {
		return p
	}
	t.Skip("SKIPPED, NOT PASSED: no openssl with the cms command; fixtures were not regenerated")
	return ""
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// pdfBuilder lays out a file of numbered objects and a classic xref table.
type pdfBuilder struct {
	buf     bytes.Buffer
	offsets map[int]int
}

func (b *pdfBuilder) obj(n int, body string) {
	if b.offsets == nil {
		b.offsets = map[int]int{}
	}
	b.offsets[n] = b.buf.Len()
	fmt.Fprintf(&b.buf, "%d 0 obj\n%s\nendobj\n", n, body)
}

// xref writes a table for the objects defined since last (nil: all of them,
// objects 0..size-1 in one section) and the trailer, and returns its offset.
func (b *pdfBuilder) xref(size int, nums []int, trailer string) int {
	at := b.buf.Len()
	b.buf.WriteString("xref\n")
	if nums == nil {
		fmt.Fprintf(&b.buf, "0 %d\n0000000000 65535 f \n", size)
		for n := 1; n < size; n++ {
			fmt.Fprintf(&b.buf, "%010d 00000 n \n", b.offsets[n])
		}
	} else {
		for _, n := range nums {
			fmt.Fprintf(&b.buf, "%d 1\n%010d 00000 n \n", n, b.offsets[n])
		}
	}
	fmt.Fprintf(&b.buf, "trailer\n<< /Size %d /Root 1 0 R %s>>\nstartxref\n%d\n%%%%EOF\n", size, trailer, at)
	return at
}

func zeros() string { return strings.Repeat("0", hexDigits) }

// sigDict is a signature dictionary with placeholders. With decoy, a second
// hex string comes BEFORE /Contents; the signer is told to leave out the decoy
// and the file holds an unsigned /Contents: a cryptographically valid signature
// in the wrong place.
func sigDict(subFilter string, decoy bool) string {
	d := "<< /Type /Sig /Filter /Adobe.PPKLite /SubFilter /" + subFilter +
		" /ByteRange [0 0000000000 0000000000 0000000000] "
	if decoy {
		d += "/Decoy <" + zeros() + "> "
	}
	return d + "/Contents <" + zeros() + "> /Name (Test Signer) /Reason (fixture) " +
		"/Location (Montpellier) /M (D:20261120120000Z) >>"
}

// base builds revision 1: catalogue, page, one signature field (4) with its
// value (6). It returns the file and the offset of its xref.
func base(subFilter string, decoy bool) ([]byte, int) {
	var b pdfBuilder
	b.buf.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R /AcroForm 5 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Annots [4 0 R] >>")
	b.obj(4, "<< /Type /Annot /Subtype /Widget /FT /Sig /T (Sig1) /Rect [0 0 0 0] /F 132 /P 3 0 R /V 6 0 R >>")
	b.obj(5, "<< /Fields [4 0 R] /SigFlags 3 >>")
	b.obj(6, sigDict(subFilter, decoy))
	at := b.xref(7, nil, "")
	return b.buf.Bytes(), at
}

// update appends an incremental update that redefines the given objects, with
// bodies in order, and returns the whole file and the new xref's offset.
func update(file []byte, prev int, size int, nums []int, bodies []string, trailer string) ([]byte, int) {
	b := pdfBuilder{}
	b.buf.Write(file)
	for i, n := range nums {
		b.obj(n, bodies[i])
	}
	at := b.xref(size, nums, fmt.Sprintf("/Prev %d %s", prev, trailer))
	return b.buf.Bytes(), at
}

// signSlot is the /ByteRange and /Contents of the signature dictionary found at
// or after from: key is "/Contents " or "/Decoy ", whose string is left out.
type signSlot struct {
	br         int // offset of the "[" of the /ByteRange
	start, end int // the gap, "<" through ">"
}

func findSlot(file []byte, from int, key string) signSlot {
	br := from + bytes.Index(file[from:], []byte("/ByteRange ["))
	k := from + bytes.Index(file[from:], []byte(key))
	start := k + len(key)
	return signSlot{br: br + len("/ByteRange "), start: start, end: start + 1 + hexDigits + 1}
}

// signed is the data the signature covers.
func (s signSlot) signed(file []byte) []byte {
	return append(append([]byte(nil), file[:s.start]...), file[s.end:]...)
}

// setByteRange writes the ranges for this slot into the file, in place; the
// numbers are fixed-width so nothing moves.
func (s signSlot) setByteRange(file []byte) {
	br := fmt.Sprintf("[0 %010d %010d %010d]", s.start, s.end, len(file)-s.end)
	copy(file[s.br:], br)
}

// splice puts the DER, hex-encoded and zero-padded, into the gap.
func (s signSlot) splice(t *testing.T, file, der []byte) {
	h := fmt.Sprintf("%X", der)
	if len(h) > hexDigits {
		t.Fatalf("signature of %d bytes does not fit the %d byte placeholder", len(der), hexDigits/2)
	}
	copy(file[s.start+1:], h)
}

type signer struct {
	key, crt string
	args     []string
}

// sign signs the slot's bytes with openssl. For adbe.pkcs7.sha1 the content of
// the CMS is the SHA-1 of those bytes, as §12.8.3.2 has it.
func sign(t *testing.T, ossl, dir string, s signSlot, file []byte, sg signer, sha1Sub bool) []byte {
	t.Helper()
	s.setByteRange(file)
	content := s.signed(file)
	args := []string{"cms", "-sign", "-binary", "-signer", sg.crt, "-inkey", sg.key, "-outform", "DER", "-out", "sig.der"}
	if sha1Sub {
		sum := sha1.Sum(content)
		content = sum[:]
		args = append(args, "-nodetach", "-md", "sha1")
	}
	if err := os.WriteFile(filepath.Join(dir, "content"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, ossl, append(args, append([]string{"-in", "content"}, sg.args...)...)...)
	der, err := os.ReadFile(filepath.Join(dir, "sig.der"))
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func write(t *testing.T, name string, b []byte) {
	t.Helper()
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("testdata", name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRegenerateFixtures rewrites testdata/ from OpenSSL, when asked.
func TestRegenerateFixtures(t *testing.T) {
	if os.Getenv("SIGNATURE_REGENERATE") != "1" {
		t.Skip("set SIGNATURE_REGENERATE=1 to rewrite testdata/ from openssl")
	}
	ossl := openssl(t)
	dir := t.TempDir()
	for k, opts := range map[string][]string{
		"rsa": {"-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048"},
		"ec":  {"-algorithm", "EC", "-pkeyopt", "ec_paramgen_curve:P-256"},
	} {
		run(t, dir, ossl, append([]string{"genpkey", "-out", k + ".key"}, opts...)...)
		run(t, dir, ossl, "req", "-x509", "-new", "-key", k+".key", "-out", k+".crt",
			"-subj", "/CN=go-pdfkit signature test "+k, "-days", "36500",
			"-addext", "subjectKeyIdentifier=hash")
		crt, err := os.ReadFile(filepath.Join(dir, k+".crt"))
		if err != nil {
			t.Fatal(err)
		}
		write(t, k+".crt", crt)
	}
	rsa := signer{"rsa.key", "rsa.crt", nil}
	ec := signer{"ec.key", "ec.crt", nil}
	cades := signer{"rsa.key", "rsa.crt", []string{"-cades"}}

	one := func(name, subFilter string, sg signer, decoy bool) {
		file, _ := base(subFilter, decoy)
		key := "/Contents "
		if decoy {
			key = "/Decoy "
		}
		slot := findSlot(file, 0, key)
		der := sign(t, ossl, dir, slot, file, sg, subFilter == "adbe.pkcs7.sha1")
		slot.splice(t, file, der)
		write(t, name, file)
	}
	one("rsa-detached.pdf", "adbe.pkcs7.detached", rsa, false)
	one("ec-detached.pdf", "adbe.pkcs7.detached", ec, false)
	one("cades.pdf", "ETSI.CAdES.detached", cades, false)
	one("sha1.pdf", "adbe.pkcs7.sha1", rsa, false)
	// A CMS that carries the signed bytes itself, under a SubFilter that says
	// detached: the signature checks out against ITS copy, which is not the file.
	one("attached.pdf", "adbe.pkcs7.detached", signer{"rsa.key", "rsa.crt", []string{"-nodetach"}}, false)
	one("gap-is-decoy.pdf", "adbe.pkcs7.detached", rsa, true)

	// An incremental update after the signature: the signature still holds over
	// what it covered, and no longer covers the whole file.
	file, at := base("adbe.pkcs7.detached", false)
	slot := findSlot(file, 0, "/Contents ")
	slot.splice(t, file, sign(t, ossl, dir, slot, file, rsa, false))
	inc, _ := update(file, at, 8, []int{7}, []string{"<< /Producer (edited after signing) >>"}, "/Info 7 0 R ")
	write(t, "incremental.pdf", inc)

	// Two signatures, the second made over the file including the first.
	st, at2 := update(file, at, 9, []int{3, 5, 7, 8}, []string{
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Annots [4 0 R 7 0 R] >>",
		"<< /Fields [4 0 R 7 0 R] /SigFlags 3 >>",
		"<< /Type /Annot /Subtype /Widget /FT /Sig /T (Sig2) /Rect [0 0 0 0] /F 132 /P 3 0 R /V 8 0 R >>",
		sigDict("adbe.pkcs7.detached", false),
	}, "")
	_ = at2
	slot2 := findSlot(st, len(file), "/Contents ")
	slot2.splice(t, st, sign(t, ossl, dir, slot2, st, ec, false))
	write(t, "stacked.pdf", st)
}

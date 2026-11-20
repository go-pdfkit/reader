package signature_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/reader/signature"
)

func load(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func results(t *testing.T, file []byte) []signature.Result {
	t.Helper()
	d, err := reader.Open(file)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rs, err := signature.VerifyAll(d)
	if err != nil {
		t.Fatalf("VerifyAll: %v", err)
	}
	return rs
}

// want is what a signature of a fixture must come out as.
type want struct {
	field       string
	subFilter   string
	valid       bool
	unsupported bool // refused, not failed
	whole       bool
}

var fixtures = []struct {
	file string
	sigs []want
}{
	{"rsa-detached.pdf", []want{{"Sig1", "adbe.pkcs7.detached", true, false, true}}},
	{"ec-detached.pdf", []want{{"Sig1", "adbe.pkcs7.detached", true, false, true}}},
	{"cades.pdf", []want{{"Sig1", "ETSI.CAdES.detached", true, false, true}}},
	{"incremental.pdf", []want{{"Sig1", "adbe.pkcs7.detached", true, false, false}}},
	{"stacked.pdf", []want{
		{"Sig1", "adbe.pkcs7.detached", true, false, false},
		{"Sig2", "adbe.pkcs7.detached", true, false, true},
	}},
	// SHA-1: poppler calls it valid, this package refuses by name.
	{"sha1.pdf", []want{{"Sig1", "adbe.pkcs7.sha1", false, true, true}}},
	// The CMS carries its own copy of the content: refused as malformed for a
	// detached SubFilter, whatever that copy would verify against.
	{"attached.pdf", []want{{"Sig1", "adbe.pkcs7.detached", false, false, true}}},
	// A valid CMS signature, in a gap that is not the /Contents.
	{"gap-is-decoy.pdf", []want{{"Sig1", "adbe.pkcs7.detached", false, false, false}}},
}

func TestFixtures(t *testing.T) {
	for _, f := range fixtures {
		t.Run(f.file, func(t *testing.T) {
			rs := results(t, load(t, f.file))
			if len(rs) != len(f.sigs) {
				t.Fatalf("%d signatures, want %d", len(rs), len(f.sigs))
			}
			for i, w := range f.sigs {
				r := rs[i]
				if r.Signature.Field != w.field || r.Signature.SubFilter != w.subFilter {
					t.Errorf("#%d is %q %q, want %q %q", i, r.Signature.Field, r.Signature.SubFilter, w.field, w.subFilter)
				}
				if (r.Err == nil) != w.valid {
					t.Errorf("#%d verdict err=%v, want valid=%v", i, r.Err, w.valid)
				}
				if w.unsupported != signature.IsUnsupported(r.Err) {
					t.Errorf("#%d unsupported=%v, err=%v", i, signature.IsUnsupported(r.Err), r.Err)
				}
				if w.valid {
					if !r.Signature.DictionarySigned {
						t.Errorf("#%d: its dictionary sits in the signed bytes, but DictionarySigned is false", i)
					}
					if r.Signature.CoversWholeFile != w.whole {
						t.Errorf("#%d CoversWholeFile=%v, want %v", i, r.Signature.CoversWholeFile, w.whole)
					}
					if len(r.Certificates) != 1 || !strings.HasPrefix(r.Certificates[0].Subject.CommonName, "go-pdfkit signature test") {
						t.Errorf("#%d certificates: %v", i, r.Certificates)
					}
				}
			}
		})
	}
}

func TestSha1NamesSHA1(t *testing.T) {
	rs := results(t, load(t, "sha1.pdf"))
	if !errors.Is(rs[0].Err, signature.ErrUnsupported) || !strings.Contains(rs[0].Err.Error(), "SHA-1") {
		t.Fatalf("want an unsupported error naming SHA-1, got %v", rs[0].Err)
	}
}

func TestRSASHA1SubFilterRefused(t *testing.T) {
	d, err := reader.Open(load(t, "rsa-detached.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	sigs, _ := d.Signatures()
	s := sigs[0]
	s.SubFilter = "adbe.x509.rsa_sha1"
	if _, err := signature.Verify(d, s); !errors.Is(err, signature.ErrUnsupported) || !strings.Contains(err.Error(), "SHA-1") {
		t.Fatalf("got %v", err)
	}
	s.SubFilter = "ETSI.RFC3161"
	if _, err := signature.Verify(d, s); !errors.Is(err, signature.ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
}

// A Signature edited by hand cannot name bytes the file's ranges do not.
func TestVerifyRefusesAnAlteredSignatureValue(t *testing.T) {
	d, _ := reader.Open(load(t, "rsa-detached.pdf"))
	sigs, _ := d.Signatures()
	s := sigs[0]
	s.ByteRange = append([]int64(nil), s.ByteRange...)
	s.ByteRange[3]-- // a byte short of the end
	if _, err := signature.Verify(d, s); err == nil {
		// shrinking the second range is legal for the validator; the signature
		// then covers other bytes and cannot verify
		t.Fatal("a shortened range verified")
	}
	s = sigs[0]
	s.Contents = append([]byte(nil), s.Contents...)
	s.Contents[len(s.Contents)-1] ^= 1
	if _, err := signature.Verify(d, s); !errors.Is(err, reader.ErrByteRange) {
		t.Fatalf("a Contents that is not the gap's: %v", err)
	}
}

// ---- tampering, judged by this package and by poppler ----

type variant struct {
	name string
	file []byte
}

// variants makes the tamperings both judges must refuse from one good file.
func variants(t *testing.T) []variant {
	good := load(t, "rsa-detached.pdf")
	patch := func(f func(b []byte)) []byte {
		b := append([]byte(nil), good...)
		f(b)
		return b
	}
	at := func(b []byte, s string) int {
		i := bytes.Index(b, []byte(s))
		if i < 0 {
			t.Fatalf("%q not in fixture", s)
		}
		return i
	}
	br := func(b []byte) (i int, nums [3]int) {
		i = at(b, "/ByteRange [") + len("/ByteRange [")
		fmt.Sscanf(string(b[i:]), "0 %d %d %d", &nums[0], &nums[1], &nums[2])
		return
	}
	setBR := func(b []byte, nums [3]int) {
		i, _ := br(b)
		copy(b[i:], fmt.Sprintf("0 %010d %010d %010d]", nums[0], nums[1], nums[2]))
	}
	return []variant{
		{"untouched", good},
		{"byte-flipped-in-first-range", patch(func(b []byte) { b[at(b, "Reason (fixture)")+9] ^= 1 })},
		{"byte-flipped-in-second-range", patch(func(b []byte) { b[at(b, "Test Signer")+1] ^= 1 })},
		{"gap-widened-past-the-string", patch(func(b []byte) {
			_, n := br(b)
			n[1] += 4
			n[2] -= 4
			setBR(b, n)
		})},
		{"gap-starts-inside-the-string", patch(func(b []byte) {
			_, n := br(b)
			n[1] -= 4
			n[2] += 4
			setBR(b, n)
		})},
		{"second-range-past-the-end", patch(func(b []byte) {
			_, n := br(b)
			n[2] += 1000
			setBR(b, n)
		})},
		{"second-range-overlaps-the-first", patch(func(b []byte) {
			_, n := br(b)
			n[1] = 100
			setBR(b, n)
		})},
		{"signature-value-flipped", patch(func(b []byte) {
			// the first digit of the DER's hex: it changes the outer SEQUENCE tag
			b[at(b, "/Contents <")+len("/Contents <")+3] ^= 1
		})},
	}
}

func TestTamperedVariantsAreRefused(t *testing.T) {
	for _, v := range variants(t) {
		t.Run(v.name, func(t *testing.T) {
			d, err := reader.Open(v.file)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			rs, err := signature.VerifyAll(d)
			if err != nil || len(rs) != 1 {
				t.Fatalf("VerifyAll: %v, %d results", err, len(rs))
			}
			// Refused for the RIGHT reason: a range the file does not bear
			// out is a /ByteRange refusal, never a digest mismatch found
			// after hashing bytes nobody validated.
			wantErr := map[string]error{
				"untouched":                       nil,
				"byte-flipped-in-first-range":     signature.ErrInvalid,
				"byte-flipped-in-second-range":    signature.ErrInvalid,
				"gap-widened-past-the-string":     reader.ErrByteRange,
				"gap-starts-inside-the-string":    reader.ErrByteRange,
				"second-range-past-the-end":       reader.ErrByteRange,
				"second-range-overlaps-the-first": reader.ErrByteRange,
				"signature-value-flipped":         signature.ErrMalformed,
			}[v.name]
			if wantErr == nil && rs[0].Err != nil || wantErr != nil && !errors.Is(rs[0].Err, wantErr) {
				t.Fatalf("verdict %v, want %v", rs[0].Err, wantErr)
			}
		})
	}
}

// ---- the oracle ----

type verdict struct {
	valid, whole bool
}

// pdfsigVerdicts runs poppler's pdfsig and reads one verdict per signature.
// -nocert: certificate trust is out of scope on both sides, as in go-authn/cms.
func pdfsigVerdicts(t *testing.T, path string) []verdict {
	t.Helper()
	cmd := exec.Command("pdfsig", "-nocert", path)
	out, _ := cmd.CombinedOutput() // a refused file still prints what it can
	var vs []verdict
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Signature #"):
			vs = append(vs, verdict{})
		case line == "- Total document signed" && len(vs) > 0:
			vs[len(vs)-1].whole = true
		case line == "- Signature Validation: Signature is Valid." && len(vs) > 0:
			vs[len(vs)-1].valid = true
		}
	}
	return vs
}

// TestPdfsigAgrees is the oracle: for every fixture and every tampering,
// poppler's pdfsig and this package must give the same verdict, and, when the
// signature holds, the same answer to "does it cover the whole file". The one
// named exception is SHA-1, which poppler accepts and this package refuses by
// design.
//
// ⛔ A SKIP IS NOT A PASS: without pdfsig the test says so loudly, and
// PDFSIG_REQUIRE=1 makes its absence a failure (CI sets it).
func TestPdfsigAgrees(t *testing.T) {
	if _, err := exec.LookPath("pdfsig"); err != nil {
		if os.Getenv("PDFSIG_REQUIRE") == "1" {
			t.Fatal("PDFSIG_REQUIRE=1 and pdfsig (poppler-utils) was not found")
		}
		t.Skip("SKIPPED, NOT PASSED: pdfsig is not installed, so the oracle comparison did not run")
	}
	dir := t.TempDir()
	type file struct {
		name string
		b    []byte
		sha1 bool
	}
	var files []file
	for _, f := range fixtures {
		files = append(files, file{f.file, load(t, f.file), f.file == "sha1.pdf"})
	}
	for _, v := range variants(t) {
		files = append(files, file{"rsa-detached/" + v.name, v.file, false})
	}
	var table strings.Builder
	for _, f := range files {
		p := filepath.Join(dir, strings.ReplaceAll(f.name, "/", "_")+".pdf")
		if err := os.WriteFile(p, f.b, 0o644); err != nil {
			t.Fatal(err)
		}
		theirs := pdfsigVerdicts(t, p)
		ours := results(t, f.b)
		if len(theirs) != len(ours) {
			t.Errorf("%s: pdfsig lists %d signatures, this package %d", f.name, len(theirs), len(ours))
			continue
		}
		for i := range ours {
			o := verdict{ours[i].Err == nil, ours[i].Signature.CoversWholeFile}
			note := ""
			if f.sha1 {
				if !signature.IsUnsupported(ours[i].Err) || !theirs[i].valid {
					t.Errorf("%s: expected poppler valid and ours unsupported; got %+v, %v", f.name, theirs[i], ours[i].Err)
				}
				note = "  (SHA-1: refused by design)"
			} else {
				if o.valid != theirs[i].valid {
					t.Errorf("%s #%d: pdfsig valid=%v, ours valid=%v (%v)", f.name, i+1, theirs[i].valid, o.valid, ours[i].Err)
				}
				// Compared only when both say valid, and the README says no more:
				// for a refused signature the two disagree about "whole" (poppler
				// reports it from the ranges it read; this package reports false
				// for a range it refused, and true for a bad CMS in sound ranges).
				if o.valid && theirs[i].valid && o.whole != theirs[i].whole {
					t.Errorf("%s #%d: pdfsig whole=%v, ours whole=%v", f.name, i+1, theirs[i].whole, o.whole)
				}
			}
			fmt.Fprintf(&table, "%-52s pdfsig valid=%-5v whole=%-5v | ours valid=%-5v whole=%-5v%s\n",
				fmt.Sprintf("%s #%d", f.name, i+1), theirs[i].valid, theirs[i].whole, o.valid, o.whole, note)
		}
	}
	t.Logf("\n%s", table.String())
}

// A form too deep to walk is an error for the whole listing, not a silent
// "no signatures": that would read as an unsigned document.
func TestVerifyAllRefusesAFormTooDeepToWalk(t *testing.T) {
	const depth = 64
	nested := strings.Repeat("<< /T (x) /Kids [", depth) + strings.Repeat("] >>", depth)
	body := "%PDF-1.7\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [" + nested + "] >> >>\nendobj\n" +
		"2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n"
	o2 := strings.Index(body, "2 0 obj")
	file := body + fmt.Sprintf("xref\n0 3\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \ntrailer\n<< /Size 3 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		strings.Index(body, "1 0 obj"), o2, len(body))
	d, err := reader.Open([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signature.VerifyAll(d); !errors.Is(err, reader.ErrTooManyFields) {
		t.Fatalf("got %v", err)
	}
}

func TestAttachedContentIsRefusedBeforeVerifying(t *testing.T) {
	rs := results(t, load(t, "attached.pdf"))
	if !errors.Is(rs[0].Err, signature.ErrMalformed) || !strings.Contains(rs[0].Err.Error(), "carries its content") {
		t.Fatalf("got %v", rs[0].Err)
	}
}

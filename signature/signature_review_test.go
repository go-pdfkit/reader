package signature_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/reader/signature"
)

// relocated is rsa-detached.pdf plus an incremental update that re-points the
// field's /V at a NEW signature dictionary inside an object stream, copying the
// original /ByteRange and /Contents and forging /Name, /Reason and /M.
func relocated(tb testing.TB) []byte {
	tb.Helper()
	orig := load(tb, "rsa-detached.pdf")
	sx := bytes.LastIndex(orig, []byte("startxref"))
	f := bytes.Fields(orig[sx+len("startxref"):])
	prev, err := strconv.Atoi(string(f[0]))
	if err != nil {
		tb.Fatal(err)
	}
	d0, err := reader.Open(orig)
	if err != nil {
		tb.Fatal(err)
	}
	s0, err := d0.Signatures()
	if err != nil || len(s0) != 1 {
		tb.Fatal(s0, err)
	}
	br := s0[0].ByteRange
	gap := orig[br[1]:br[2]]

	var b bytes.Buffer
	b.Write(orig)
	off := map[int]int{}
	off[4] = b.Len()
	b.WriteString("4 0 obj\n<< /Type /Annot /Subtype /Widget /FT /Sig /T (CEO-Approval) /Rect [0 0 0 0] /F 132 /P 3 0 R /V 8 0 R >>\nendobj\n")
	sd := fmt.Sprintf("<< /Type /Sig /Filter /Adobe.PPKLite /SubFilter /adbe.pkcs7.detached /ByteRange [%d %d %d %d] /Contents %s /Name (Forged Name) /Reason (Forged reason) /M (D:20991231000000Z) >>", br[0], br[1], br[2], br[3], gap)
	hdr := "8 0 "
	data := hdr + sd
	off[7] = b.Len()
	fmt.Fprintf(&b, "7 0 obj\n<< /Type /ObjStm /N 1 /First %d /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(hdr), len(data), data)
	off[9] = b.Len()
	var xs bytes.Buffer
	ent := func(typ byte, f2 uint32, f3 uint16) {
		xs.WriteByte(typ)
		var x [4]byte
		binary.BigEndian.PutUint32(x[:], f2)
		xs.Write(x[:])
		var y [2]byte
		binary.BigEndian.PutUint16(y[:], f3)
		xs.Write(y[:])
	}
	ent(1, uint32(off[4]), 0)
	ent(1, uint32(off[7]), 0)
	ent(2, 7, 0)
	ent(1, uint32(off[9]), 0)
	fmt.Fprintf(&b, "9 0 obj\n<< /Type /XRef /Size 10 /W [1 4 2] /Index [4 1 7 3] /Prev %d /Root 1 0 R /Length %d >>\nstream\n", prev, xs.Len())
	b.Write(xs.Bytes())
	fmt.Fprintf(&b, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", off[9])
	return b.Bytes()
}

// The forged claims verify, as the CMS signature does hold over the ranges; what
// the package must say is that the dictionary they came from is not inside the
// signed bytes, and that the original is.
func TestRelocatedDictionaryIsNotSigned(t *testing.T) {
	rs := results(t, relocated(t))
	if len(rs) != 1 {
		t.Fatalf("%d results", len(rs))
	}
	r := rs[0]
	if r.Err != nil || r.Signature.CoversWholeFile {
		t.Fatalf("the probe's premise changed: err=%v whole=%v", r.Err, r.Signature.CoversWholeFile)
	}
	if r.Signature.Field != "CEO-Approval" {
		t.Fatalf("the probe's forged field was not read: %+v", r.Signature)
	}
	// The forged claims are withheld, not merely flagged.
	if s := r.Signature; s.Name != "" || s.Reason != "" || s.Location != "" || s.M != "" {
		t.Fatalf("forged claims handed out: %q %q %q %q", s.Name, s.Reason, s.Location, s.M)
	}
	if r.Signature.DictionarySigned {
		t.Fatal("a dictionary supplied by an incremental update, in an object stream, was reported as covered by the signature")
	}
	if orig := results(t, load(t, "rsa-detached.pdf")); !orig[0].Signature.DictionarySigned ||
		orig[0].Signature.Name != "Test Signer" || orig[0].Signature.Reason != "fixture" ||
		orig[0].Signature.Location != "Montpellier" || orig[0].Signature.M != "D:20261120120000Z" {
		t.Fatalf("the original dictionary is inside the signed bytes, and its claims are handed out: %+v", orig[0].Signature)
	}
}

// pdfsig's answer for the same file is recorded, not asserted here: it says
// valid and "Not total document signed", reads the forged field name, and shows
// the signing time of the CMS, not /M.
func TestRelocatedDictionaryPdfsigAnswer(t *testing.T) {
	p := filepath.Join(t.TempDir(), "relocated.pdf")
	if err := os.WriteFile(p, relocated(t), 0o644); err != nil {
		t.Fatal(err)
	}
	vs := pdfsigVerdicts(t, p)
	if len(vs) == 1 && !vs[0].valid {
		t.Skip("pdfsig is not installed or refuses the file")
	}
	if len(vs) == 1 && vs[0].whole {
		t.Fatalf("pdfsig says the relocated file is total document signed: %+v", vs)
	}
}

// 2000 fields on one signature dictionary: verified once, one copy of the file.
func TestVerifyAllHashesOneDictionaryOnce(t *testing.T) {
	const n, gapHex = 2000, 200000
	var f strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&f, "<</T(f%d)/FT/Sig/V 4 0 R>>", i)
	}
	zeros := strings.Repeat("0", gapHex)
	dict := "<< /Type /Sig /SubFilter /adbe.pkcs7.detached /ByteRange [0 0000000000 0000000000 0000000000] /Contents <" + zeros + "> >>"
	cat := "1 0 obj\n<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [" + f.String() + "] >> >>\nendobj\n"
	pages := "2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n"
	sig := "4 0 obj\n" + dict + "\nendobj\n"
	body := "%PDF-1.7\n" + cat + pages + sig
	xr := len(body)
	file := []byte(body + fmt.Sprintf("xref\n0 5\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \n0000000000 65535 f \n%010d 00000 n \ntrailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len("%PDF-1.7\n"), len("%PDF-1.7\n"+cat), len("%PDF-1.7\n"+cat+pages), xr))
	start := bytes.Index(file, []byte("/Contents <")) + len("/Contents ")
	end := start + gapHex + 2
	copy(file[bytes.Index(file, []byte("/ByteRange "))+len("/ByteRange "):],
		fmt.Sprintf("[0 %010d %010d %010d]", start, end, len(file)-end))
	d, err := reader.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	rs, err := signature.VerifyAll(d)
	runtime.ReadMemStats(&m1)
	if err != nil || len(rs) != n {
		t.Fatalf("%d results, %v", len(rs), err)
	}
	for _, r := range rs {
		if r.Err == nil || r.Signature.Err != nil {
			t.Fatalf("%s: %v / %v", r.Signature.Field, r.Err, r.Signature.Err)
		}
	}
	// Before the cache: a copy of the 260 KB file per entry, over 500 MB in all.
	if got := (m1.TotalAlloc - m0.TotalAlloc) >> 20; got > 24 {
		t.Fatalf("VerifyAll allocated %d MB for %d fields on one dictionary", got, n)
	}
}

// TestWriteRelocatedFuzzSeed writes the relocated-dictionary file into the
// FuzzVerifyAll corpus when asked (FUZZ_SEEDS_WRITE=1).
func TestWriteRelocatedFuzzSeed(t *testing.T) {
	if os.Getenv("FUZZ_SEEDS_WRITE") != "1" {
		t.Skip("set FUZZ_SEEDS_WRITE=1 to rewrite testdata/fuzz/FuzzVerifyAll/relocated-dictionary")
	}
	dir := filepath.Join("testdata", "fuzz", "FuzzVerifyAll")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "go test fuzz v1\n[]byte(" + strconv.Quote(string(relocated(t))) + ")\n"
	if err := os.WriteFile(filepath.Join(dir, "relocated-dictionary"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// manyDirect builds a file whose catalogue holds n direct signature fields, each
// with its own direct dictionary, its own /Contents string and so its own
// /ByteRange, and padding that makes the file bigger.
func manyDirect(tb testing.TB, n int, contents string, pad int) []byte {
	tb.Helper()
	var f strings.Builder
	f.WriteString("%PDF-1.7\n")
	o1 := f.Len()
	f.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&f, "<< /T (f%d) /FT /Sig /V << /SubFilter /adbe.pkcs7.detached /ByteRange [0 0000000000 0000000000 0000000000] /Contents <%s> >> >>\n", i, contents)
	}
	f.WriteString("] >> >>\nendobj\n")
	f.WriteString(strings.Repeat("% padding to make the file bigger\n", pad))
	o2 := f.Len()
	f.WriteString("2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n")
	x := f.Len()
	fmt.Fprintf(&f, "xref\n0 3\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \ntrailer\n<< /Size 3 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", o1, o2, x)
	file := []byte(f.String())
	pos := 0
	for i := 0; i < n; i++ {
		br := pos + bytes.Index(file[pos:], []byte("/ByteRange ")) + len("/ByteRange ")
		c := br + bytes.Index(file[br:], []byte("/Contents ")) + len("/Contents ")
		g := len(contents) + 2
		copy(file[br:], fmt.Sprintf("[0 %010d %010d %010d]", c, c+g, len(file)-c-g))
		pos = c + g
	}
	return file
}

func limitedAlloc(tb testing.TB, file []byte) (rs []signature.Result, alloc uint64) {
	tb.Helper()
	d, err := reader.Open(file)
	if err != nil {
		tb.Fatal(err)
	}
	defer debug.SetMemoryLimit(debug.SetMemoryLimit(128 << 20))
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	rs, err = signature.VerifyAll(d)
	runtime.ReadMemStats(&m1)
	if err != nil {
		tb.Fatal(err)
	}
	return rs, m1.TotalAlloc - m0.TotalAlloc
}

func countTooMany(rs []signature.Result) (n int) {
	for _, r := range rs {
		if errors.Is(r.Err, signature.ErrTooManySignatures) {
			n++
		}
	}
	return n
}

// 4000 dictionaries with an empty /Contents each: distinct dedup keys, and the
// whole file copied for each one before the CMS was found unreadable (2.35 GB).
func TestVerifyAllDistinctEmptySignaturesCostNothing(t *testing.T) {
	file := manyDirect(t, 4000, "", 3000)
	rs, alloc := limitedAlloc(t, file)
	if len(rs) != 4000 {
		t.Fatalf("%d results", len(rs))
	}
	// Below what 64 copies of the file would allocate: the CMS is parsed first.
	if alloc > 16<<20 {
		t.Fatalf("%d signatures in a %d KB file allocated %d MB", len(rs), len(file)>>10, alloc>>20)
	}
	if got := countTooMany(rs); got != 4000-64 {
		t.Fatalf("%d refused for the count, want %d", got, 4000-64)
	}
}

// Signatures whose CMS parses are the ones that cost a copy: bounded by count.
func TestVerifyAllBoundsDistinctSignatures(t *testing.T) {
	orig := load(t, "rsa-detached.pdf")
	d0, _ := reader.Open(orig)
	s0, _ := d0.Signatures()
	file := manyDirect(t, 100, fmt.Sprintf("%x", s0[0].Contents), 10)
	rs, _ := limitedAlloc(t, file)
	if len(rs) != 100 || countTooMany(rs) != 36 {
		t.Fatalf("%d results, %d refused for the count", len(rs), countTooMany(rs))
	}
	for _, r := range rs[:64] {
		if !errors.Is(r.Err, signature.ErrInvalid) {
			t.Fatalf("a signature within the bound: %v", r.Err)
		}
	}
	// ...and by the total signed bytes.
	defer signature.LimitVerify(1000, 3*int64(len(file)))()
	if rs, _ = limitedAlloc(t, file); countTooMany(rs) != 97 {
		t.Fatalf("%d refused for the bytes, want 97", countTooMany(rs))
	}
}

func TestVerifyAllResultsOwnTheirCertificates(t *testing.T) {
	orig := load(t, "rsa-detached.pdf")
	sx := bytes.LastIndex(orig, []byte("startxref"))
	prev, err := strconv.Atoi(string(bytes.Fields(orig[sx+len("startxref"):])[0]))
	if err != nil {
		t.Fatal(err)
	}
	file, _ := update(orig, prev, 8, []int{5, 7}, []string{
		"<< /Fields [4 0 R 7 0 R] /SigFlags 3 >>",
		"<< /Type /Annot /Subtype /Widget /FT /Sig /T (Sig2) /Rect [0 0 0 0] /F 132 /P 3 0 R /V 6 0 R >>",
	}, "")
	rs := results(t, file)
	if len(rs) != 2 || rs[0].Err != nil || rs[1].Err != nil || len(rs[0].Certificates) != 1 {
		t.Fatalf("%+v", rs)
	}
	rs[0].Certificates[0] = nil
	if rs[1].Certificates[0] == nil {
		t.Fatal("two results share one Certificates slice")
	}
}

package signature_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	if r.Signature.Name != "Forged Name" || r.Signature.Field != "CEO-Approval" {
		t.Fatalf("the probe's forged claims were not read: %+v", r.Signature)
	}
	if r.Signature.DictionarySigned {
		t.Fatal("a dictionary supplied by an incremental update, in an object stream, was reported as covered by the signature")
	}
	if orig := results(t, load(t, "rsa-detached.pdf")); !orig[0].Signature.DictionarySigned {
		t.Fatal("the original dictionary is inside the signed bytes")
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

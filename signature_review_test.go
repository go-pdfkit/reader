package reader

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// Regression tests for the review of PR #76.

// directRootDoc is a file whose /Root is a direct dictionary and whose xref row
// for object 0 is a negative offset. Object 0 is no object: crediting the
// catalogue to it sliced the file at -5.
func directRootDoc(t *testing.T) []byte {
	t.Helper()
	zeros := strings.Repeat("0", 64)
	head := "%PDF-1.7\n"
	o2 := len(head)
	body := head + "2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n"
	xref := len(body)
	tail := fmt.Sprintf("xref\n0 3\n-000000005 00000 n \n0000000000 65535 f \n%010d 00000 n \n", o2) +
		"trailer\n<< /Size 3 /Root << /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [<< /T (s) /FT /Sig /V << /Type /Sig /SubFilter /adbe.pkcs7.detached /ByteRange [0 0000000000 0000000000 0000000000] /Contents <" + zeros + "> >> >>] >> >> >>\n" +
		fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xref)
	file := []byte(body + tail)
	start := bytes.Index(file, []byte("/Contents <")) + len("/Contents ")
	end := start + 66
	copy(file[bytes.Index(file, []byte("/ByteRange "))+len("/ByteRange "):],
		fmt.Sprintf("[0 %010d %010d %010d]", start, end, len(file)-end))
	return file
}

func TestSignaturesDirectRootIsNotObjectZero(t *testing.T) {
	d := mustOpen(t, directRootDoc(t))
	if e := d.xref[0]; e.offset >= 0 {
		t.Fatalf("the fixture lost its negative offset for object 0: %+v", e)
	}
	sigs, err := d.Signatures() // panicked: slice bounds out of range [-5:]
	if err != nil || len(sigs) != 1 || sigs[0].Err != nil {
		t.Fatalf("%+v %v", sigs, err)
	}
	if sigs[0].DictionarySigned {
		t.Error("a dictionary with no object of its own was reported as inside the signed bytes")
	}
}

func TestSignaturesDirectRootInAnEncryptedFileIsRefused(t *testing.T) {
	// Object 0's offset is the attacker's in an encrypted file: nothing ties the
	// gap to a direct dictionary, so it must fall into the refusal.
	d := mustOpen(t, directRootDoc(t))
	markEncrypted(t, d)
	sigs, _ := d.Signatures()
	if len(sigs) != 1 || !errors.Is(sigs[0].Err, ErrByteRange) || !strings.Contains(sigs[0].Err.Error(), "encrypted file") {
		t.Fatalf("%+v", sigs)
	}
}

func TestSignatureDictionaryOffsetIsGuarded(t *testing.T) {
	d := mustOpen(t, miniSigned(t))
	if _, err := d.Signatures(); err != nil {
		t.Fatal(err)
	}
	e := d.xref[4]
	e.offset = -1
	d.xref[4] = e
	sigs, _ := d.Signatures() // would slice the file at -1
	if len(sigs) != 1 || !errors.Is(sigs[0].Err, ErrByteRange) {
		t.Fatalf("%+v", sigs)
	}
}

func TestSignatureDictionaryObjectMayStartAtOffsetZero(t *testing.T) {
	// The edge of the guard: an object whose recorded offset is 0 is sliced from
	// 0, not refused as negative. The dictionary is object 1, the first in the
	// file, and the xref entry is pointed at 0 after the objects are cached.
	zeros := strings.Repeat("0", 64)
	file := miniPDF(map[int]string{
		1: "<< /Type /Sig /SubFilter /adbe.pkcs7.detached /ByteRange [0 0000000000 0000000000 0000000000] /Contents <" + zeros + "> >>",
		2: "<< /Type /Catalog /Pages 3 0 R /AcroForm << /Fields [4 0 R] >> >>",
		3: "<< /Type /Pages /Kids [] /Count 0 >>",
		4: "<< /T (s) /FT /Sig /V 1 0 R >>",
	}, 5)
	file = bytes.Replace(file, []byte("/Root 1 0 R"), []byte("/Root 2 0 R"), 1)
	start := bytes.Index(file, []byte("/Contents <")) + len("/Contents ")
	end := start + 66
	copy(file[bytes.Index(file, []byte("/ByteRange "))+len("/ByteRange "):],
		fmt.Sprintf("[0 %010d %010d %010d]", start, end, len(file)-end))
	d := mustOpen(t, file)
	if sigs, err := d.Signatures(); err != nil || len(sigs) != 1 || sigs[0].Err != nil {
		t.Fatalf("%+v %v", sigs, err)
	}
	e := d.xref[1]
	e.offset = 0
	d.xref[1] = e
	if sigs, _ := d.Signatures(); len(sigs) != 1 || sigs[0].Err != nil || !sigs[0].DictionarySigned {
		t.Fatalf("an object at offset 0 was refused: %+v", sigs)
	}
}

func TestSignatureGapInALaterObjectIsRefusedByItsEndobj(t *testing.T) {
	// The signature dictionary is object 4 and comes FIRST; a decoy string with
	// the same value sits in object 5, after it. The value comparison passes and
	// the object starts before the gap, so only the "endobj" between the object's
	// start and the gap refuses it.
	zeros := strings.Repeat("0", 64)
	dict := "<< /Type /Sig /SubFilter /adbe.pkcs7.detached /ByteRange [0 0000000000 0000000000 0000000000] /Contents <" + zeros + "> >>"
	file := formDoc("[3 0 R]", map[int]string{
		3: "<< /T (s) /FT /Sig /V 4 0 R >>",
		4: dict,
		5: "<< /Decoy <" + zeros + "> >>",
	})
	start := bytes.Index(file, []byte("/Decoy <")) + len("/Decoy ")
	end := start + 66
	copy(file[bytes.Index(file, []byte("/ByteRange "))+len("/ByteRange "):],
		fmt.Sprintf("[0 %010d %010d %010d]", start, end, len(file)-end))
	d := mustOpen(t, file)
	if e := d.xref[4]; e.offset > int64(start) {
		t.Fatalf("object 4 must start before the gap: %+v, gap at %d", e, start)
	}
	sigs, _ := d.Signatures()
	if len(sigs) != 1 || !errors.Is(sigs[0].Err, ErrByteRange) || !strings.Contains(sigs[0].Err.Error(), "not inside object 4") {
		t.Fatalf("%+v", sigs)
	}
}

func TestSignatureDictionarySignedIsReported(t *testing.T) {
	d := mustOpen(t, miniSigned(t))
	sigs, _ := d.Signatures()
	if len(sigs) != 1 || sigs[0].Err != nil || !sigs[0].DictionarySigned {
		t.Fatalf("a dictionary inside its own object's signed bytes: %+v", sigs)
	}
	// In an object stream it has no position: valid for an unencrypted file, but
	// not a dictionary the signature covers.
	d.xref[4] = xrefEntry{kind: 'o', strmNum: 9}
	if sigs, _ = d.Signatures(); len(sigs) != 1 || sigs[0].Err != nil || sigs[0].DictionarySigned {
		t.Fatalf("a dictionary in an object stream reported as signed: %+v", sigs)
	}
}

func TestSignatureByteRangeEntriesAreDirect(t *testing.T) {
	zeros := strings.Repeat("0", 64)
	dict := "<< /Type /Sig /SubFilter /adbe.pkcs7.detached /ByteRange [0 0000000000 0000000000 5 0 R] /Contents <" + zeros + "> >>"
	file := formDoc("[3 0 R]", map[int]string{
		3: "<< /T (s) /FT /Sig /V 4 0 R >>",
		4: dict,
		5: "0000000000",
	})
	start := bytes.Index(file, []byte("/Contents <")) + len("/Contents ")
	end := start + 66
	copy(file[bytes.Index(file, []byte("/ByteRange "))+len("/ByteRange "):], fmt.Sprintf("[0 %010d %010d", start, end))
	o5 := bytes.Index(file, []byte("5 0 obj"))
	copy(file[o5+len("5 0 obj\n"):], fmt.Sprintf("%010d", len(file)-end))
	sigs, err := mustOpen(t, file).Signatures()
	if err != nil || len(sigs) != 1 || !errors.Is(sigs[0].Err, ErrByteRange) || !strings.Contains(sigs[0].Err.Error(), "not an integer") {
		t.Fatalf("an indirect /ByteRange entry was accepted: %+v %v", sigs, err)
	}
}

// ampDoc: nFields direct fields with distinct names, all with /V 4 0 R, whose
// one signature dictionary leaves a gapHex-digit string.
func ampDoc(nFields, gapHex int) []byte {
	var f strings.Builder
	f.WriteString("[")
	for i := 0; i < nFields; i++ {
		fmt.Fprintf(&f, "<</T(f%d)/FT/Sig/V 4 0 R>>", i)
	}
	f.WriteString("]")
	zeros := strings.Repeat("0", gapHex)
	dict := "<< /Type /Sig /SubFilter /adbe.pkcs7.detached /ByteRange [0 0000000000 0000000000 0000000000] /Contents <" + zeros + "> >>"
	file := formDoc(f.String(), map[int]string{4: dict})
	start := bytes.Index(file, []byte("/Contents <")) + len("/Contents ")
	end := start + gapHex + 2
	copy(file[bytes.Index(file, []byte("/ByteRange "))+len("/ByteRange "):],
		fmt.Sprintf("[0 %010d %010d %010d]", start, end, len(file)-end))
	return file
}

// measure runs f under a soft memory limit and returns the bytes it allocated
// and the heap still live after a collection, f's results kept alive.
func measure(f func() any) (alloc, live uint64) {
	defer debug.SetMemoryLimit(debug.SetMemoryLimit(96 << 20))
	runtime.GC()
	var m0, m1, m2 runtime.MemStats
	runtime.ReadMemStats(&m0)
	keep := f()
	runtime.ReadMemStats(&m1)
	runtime.GC()
	runtime.ReadMemStats(&m2)
	runtime.KeepAlive(keep)
	return m1.TotalAlloc - m0.TotalAlloc, m2.HeapAlloc
}

func TestSignaturesManyFieldsOneDictionaryDecodeOnce(t *testing.T) {
	// 2000 fields, 200 KB gap: 205 MB retained and as much again allocated
	// before the dictionary was read once per dictionary reference.
	file := ampDoc(2000, 200000)
	d := mustOpen(t, file)
	var sigs []Signature
	alloc, live := measure(func() any {
		var err error
		if sigs, err = d.Signatures(); err != nil {
			t.Fatal(err)
		}
		return sigs
	})
	if len(sigs) != 2000 {
		t.Fatalf("%d signatures", len(sigs))
	}
	for _, s := range sigs {
		if s.Err != nil {
			t.Fatalf("%s: %v", s.Field, s.Err)
		}
	}
	if alloc > 16<<20 || live > 32<<20 {
		t.Fatalf("allocated %d MB, %d MB live for a %d KB file", alloc>>20, live>>20, len(file)>>10)
	}
	for _, s := range sigs[1:] {
		if len(s.Contents) > 0 && &s.Contents[0] != &sigs[0].Contents[0] {
			t.Fatal("every field decoded and kept its own copy of the gap")
		}
	}
}

func TestSignatureGapsAreBoundedInTotal(t *testing.T) {
	// Direct /V dictionaries each naming a different end for the gap: nothing to
	// cache by reference, and each would allocate half its gap before the first
	// bad digit. The total refuses.
	defer func(old int) { maxGapBytes = old }(maxGapBytes)
	maxGapBytes = 1 << 20
	const n, region = 2000, 200000
	var f strings.Builder
	f.WriteString("[")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&f, "<</T(f%d)/FT/Sig/V<</ByteRange[0 0000000000 0000000000 0000000000]/Contents<00>>>>>", i)
	}
	f.WriteString("]")
	file := formDoc(f.String(), map[int]string{9: "<< /Length " + fmt.Sprint(region+1) + " >>\nstream\n<" + strings.Repeat(">", region) + "\nendstream"})
	s0 := bytes.Index(file, []byte("stream\n<")) + len("stream\n")
	pos := 0
	for i := 0; i < n; i++ {
		at := bytes.Index(file[pos:], []byte("/ByteRange[")) + pos + len("/ByteRange[")
		e := s0 + 2 + i*50
		copy(file[at:], fmt.Sprintf("0 %010d %010d %010d]", s0, e, len(file)-e))
		pos = at
	}
	d := mustOpen(t, file)
	var sigs []Signature
	alloc, _ := measure(func() any {
		var err error
		if sigs, err = d.Signatures(); err != nil {
			t.Fatal(err)
		}
		return sigs
	})
	if len(sigs) != n {
		t.Fatalf("%d signatures", len(sigs))
	}
	refused := 0
	for _, s := range sigs {
		if s.Err == nil {
			t.Fatalf("%s accepted", s.Field)
		}
		if errors.Is(s.Err, ErrByteRange) && strings.Contains(s.Err.Error(), "more than") {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("no signature was refused for the total")
	}
	if alloc > 16<<20 {
		t.Fatalf("allocated %d MB", alloc>>20)
	}
}

// TestWriteSignatureVerifyFuzzSeeds writes the regression inputs into the
// FuzzVerifyAll corpus when asked (FUZZ_SEEDS_WRITE=1).
func TestWriteSignatureVerifyFuzzSeeds(t *testing.T) {
	if os.Getenv("FUZZ_SEEDS_WRITE") != "1" {
		t.Skip("set FUZZ_SEEDS_WRITE=1 to rewrite signature/testdata/fuzz/FuzzVerifyAll")
	}
	dir := filepath.Join("signature", "testdata", "fuzz", "FuzzVerifyAll")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{
		"direct-root-negative-object-zero": directRootDoc(t),
		"many-fields-one-dictionary":       ampDoc(40, 2000),
		"mini-signed":                      miniSigned(t),
	} {
		body := "go test fuzz v1\n[]byte(" + fmt.Sprintf("%q", string(b)) + ")\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

package reader

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// directSigs builds a file whose catalogue (object 1) holds n direct signature
// dictionaries, each with its own one-byte /Contents gap and /ByteRange: n
// positional checks against ONE enclosing object.
func directSigs(t *testing.T, n int) []byte {
	t.Helper()
	var f strings.Builder
	f.WriteString("%PDF-1.7\n")
	o1 := f.Len()
	f.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&f, "<< /T (f%d) /FT /Sig /V << /SubFilter /adbe.pkcs7.detached /ByteRange [0 0000000000 0000000000 0000000000] /Contents <> >> >>\n", i)
	}
	f.WriteString("] >> >>\nendobj\n")
	o2 := f.Len()
	f.WriteString("2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n")
	x := f.Len()
	fmt.Fprintf(&f, "xref\n0 3\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \ntrailer\n<< /Size 3 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", o1, o2, x)
	file := []byte(f.String())
	pos := 0
	for i := 0; i < n; i++ {
		br := pos + bytes.Index(file[pos:], []byte("/ByteRange ")) + len("/ByteRange ")
		c := br + bytes.Index(file[br:], []byte("/Contents ")) + len("/Contents ")
		copy(file[br:], fmt.Sprintf("[0 %010d %010d %010d]", c, c+2, len(file)-c-2))
		pos = c + 2
	}
	return file
}

// N dictionaries in one object were N scans of that object for "endobj", twice
// each: quadratic (16 000 fields in a 2.1 MB file took 17 s). The file is now
// indexed once per Signatures call, whatever N is, and every positional check
// still runs on that index: each dictionary is reported signed.
func TestSignaturesIndexTheFileOnce(t *testing.T) {
	file := directSigs(t, 3000)
	d := mustOpen(t, file)
	orig := indexEndobjs
	defer func() { indexEndobjs = orig }()
	scans := 0
	indexEndobjs = func(buf []byte) []int { scans++; return orig(buf) }
	var sigs []Signature
	var err error
	if crash := recovering(func() { sigs, err = d.Signatures() }); crash != nil {
		t.Fatalf("Signatures crashed: %v", crash)
	}
	if err != nil || len(sigs) != 3000 {
		t.Fatalf("%d signatures, %v", len(sigs), err)
	}
	for _, s := range sigs {
		if s.Err != nil || !s.DictionarySigned {
			t.Fatalf("%s: signed=%v, %v", s.Field, s.DictionarySigned, s.Err)
		}
	}
	if scans != 1 {
		t.Fatalf("the file was scanned for endobj %d times for %d signatures, want once", scans, len(sigs))
	}
}

// recovering runs f and reports a crash as a value, so that a guard whose
// removal crashes is held by an assertion rather than by the crash.
func recovering(f func()) (crash any) {
	defer func() { crash = recover() }()
	f()
	return nil
}

func TestIndexOf(t *testing.T) {
	for _, tc := range []struct {
		buf  string
		want []int
	}{
		{"", []int{}},
		{"no such word", []int{}},
		{"endobj", []int{0}},
		{"x endobjendobj y endobj", []int{2, 8, 17}},
		{"endob", []int{}},
	} {
		var got []int
		if crash := recovering(func() { got = indexOf([]byte(tc.buf), endobj) }); crash != nil {
			t.Errorf("indexOf(%q) crashed: %v", tc.buf, crash)
			continue
		}
		if !slices.Equal(got, tc.want) || got == nil {
			t.Errorf("indexOf(%q) = %#v, want %#v", tc.buf, got, tc.want)
		}
	}
}

// With no "endobj" at or after the object's start, nothing closes the object
// before the gap and nothing places its end: no error, and not signed.
func TestSignaturesWithNoEndobjAfterTheObject(t *testing.T) {
	d := mustOpen(t, miniSigned(t))
	orig := indexEndobjs
	defer func() { indexEndobjs = orig }()
	indexEndobjs = func([]byte) []int { return []int{} }
	var sigs []Signature
	var err error
	if crash := recovering(func() { sigs, err = d.Signatures() }); crash != nil {
		t.Fatalf("Signatures crashed: %v", crash)
	}
	if err != nil || len(sigs) != 1 || sigs[0].Err != nil || sigs[0].DictionarySigned {
		t.Fatalf("%+v, %v: want one signature, no error, not signed", sigs, err)
	}
}

// The object's own "endobj" ends EXACTLY where the gap begins ("endobj<...>"),
// and the gap holds the same value as the dictionary's /Contents: the gap is
// outside the object by zero bytes, and is refused all the same.
func TestSignatureGapRightAfterTheObjectsEndobjIsRefused(t *testing.T) {
	zeros := strings.Repeat("0", 64)
	var f strings.Builder
	var off [5]int
	f.WriteString("%PDF-1.7\n")
	for i, body := range []string{
		"<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [3 0 R] >> >>\nendobj\n",
		"<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n",
		"<< /T (s) /FT /Sig /V 4 0 R >>\nendobj\n",
		"<< /Type /Sig /SubFilter /adbe.pkcs7.detached /ByteRange [0 0000000000 0000000000 0000000000] /Contents <" + zeros + "> >>\nendobj<" + zeros + ">\n",
	} {
		off[i+1] = f.Len()
		fmt.Fprintf(&f, "%d 0 obj\n%s", i+1, body)
	}
	x := f.Len()
	fmt.Fprintf(&f, "xref\n0 5\n0000000000 65535 f \n")
	for _, o := range off[1:] {
		fmt.Fprintf(&f, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&f, "trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", x)
	file := []byte(f.String())
	start := bytes.Index(file, []byte("endobj<")) + len("endobj")
	end := start + len(zeros) + 2
	copy(file[bytes.Index(file, []byte("/ByteRange "))+len("/ByteRange "):],
		fmt.Sprintf("[0 %010d %010d %010d]", start, end, len(file)-end))
	sigs, err := mustOpen(t, file).Signatures()
	if err != nil || len(sigs) != 1 || !errors.Is(sigs[0].Err, ErrByteRange) || !strings.Contains(sigs[0].Err.Error(), "not inside object 4") {
		t.Fatalf("a gap starting right after the object's endobj: %+v, %v", sigs, err)
	}
}

package reader

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

// ---- /ByteRange: every refusal asserts a REFUSAL, and its reason ----

func TestCheckByteRangeAccepts(t *testing.T) {
	r, err := checkByteRange([]int64{0, 10, 20, 30}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if r != [2][2]int{{0, 10}, {20, 50}} {
		t.Fatalf("got %v", r)
	}
	// The second range may be empty, and may end exactly at the end of the file.
	if _, err := checkByteRange([]int64{0, 10, 12, 0}, 12); err != nil {
		t.Fatalf("a gap of exactly <> at the end: %v", err)
	}
}

func TestCheckByteRangeRefuses(t *testing.T) {
	const n = 100
	cases := []struct {
		name string
		br   []int64
		why  string // the refusal says THIS, so the right guard fired
	}{
		{"nil", nil, "0 numbers"},
		{"odd count", []int64{0, 10, 20}, "3 numbers"},
		{"one range", []int64{0, 10}, "2 numbers"},
		{"three ranges", []int64{0, 10, 20, 10, 40, 10}, "6 numbers"},
		{"seventy numbers", make([]int64, 70), "70 numbers"},
		{"negative offset", []int64{-1, 10, 20, 30}, "starts at -1"},
		{"negative length", []int64{0, -10, 20, 30}, "empty or negative"},
		{"negative second length", []int64{0, 10, 20, -5}, "negative length -5"},
		{"first range not at zero", []int64{1, 10, 20, 30}, "starts at 1"},
		{"empty first range", []int64{0, 0, 20, 30}, "empty or negative"},
		{"first range past the end", []int64{0, n + 1, n + 5, 0}, "runs past"},
		{"second range starts at the end of the first", []int64{0, 10, 10, 30}, "inside, touching or before"},
		{"ranges overlap", []int64{0, 10, 5, 30}, "inside, touching or before"},
		{"descending", []int64{0, 50, 20, 30}, "inside, touching or before"},
		{"gap of one byte", []int64{0, 10, 11, 30}, "too few"},
		{"second range starts past the end", []int64{0, 10, n + 1, 0}, "runs past"},
		{"second range runs past the end", []int64{0, 10, 20, 81}, "runs past"},
		{"huge length", []int64{0, 10, 20, math.MaxInt64}, "runs past"},
		{"huge offset and length", []int64{0, 10, math.MaxInt64, math.MaxInt64}, "runs past"},
		{"sum overflows int64", []int64{0, 10, 20, math.MaxInt64 - 10}, "runs past"},
		{"huge first length", []int64{0, math.MaxInt64, math.MaxInt64, 1}, "inside, touching or before"},
		{"huge first length, second just after", []int64{0, math.MaxInt64 - 5, math.MaxInt64, 1}, "runs past"},
		{"exceeds 32-bit", []int64{0, 10, 20, 1 << 32}, "runs past"},
		{"offset exceeds 32-bit", []int64{0, 10, 1<<32 + 20, 30}, "runs past"},
		// A negative second offset: the gap off1-len0 would WRAP to a large
		// positive number if it were formed before off1 was shown to exceed len0.
		{"minimum second offset", []int64{0, 10, math.MinInt64, 30}, "inside, touching or before"},
		{"negative second offset", []int64{0, 10, -5, 30}, "inside, touching or before"},
		{"minimum second offset, huge first length", []int64{0, math.MaxInt64, math.MinInt64, 30}, "inside, touching or before"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := checkByteRange(c.br, n)
			if !errors.Is(err, ErrByteRange) || !strings.Contains(err.Error(), c.why) {
				t.Fatalf("%v: got %v, want a refusal wrapping ErrByteRange and saying %q", c.br, err, c.why)
			}
		})
	}
}

func TestDecodeGap(t *testing.T) {
	got, err := decodeGap([]byte("<0a 0B\n0c1>"))
	if err != nil || !bytes.Equal(got, []byte{0x0a, 0x0b, 0x0c, 0x10}) {
		t.Fatalf("got %x, %v", got, err)
	}
	// An empty string is still a string, and "<>" is the least the gap can be.
	if got, err := decodeGap([]byte("<>")); err != nil || len(got) != 0 {
		t.Fatalf("<>: got %x, %v", got, err)
	}
	for why, gaps := range map[string][]string{
		// not delimited as one string
		"not a single <...> string": {"", "<", ">", "0a>", "<0a", "<0a>x", "x<0a>", "<0a>%c\n", "<0a> ", " <0a>", "(ab)"},
		// delimited, and something else inside
		"not part of a hexadecimal string": {"<>>", "<0a><0b>", "<0g>", "<<0a>>", "<0a>/Foo<0b>", "<0a>>"},
	} {
		for _, g := range gaps {
			if _, err := decodeGap([]byte(g)); !errors.Is(err, ErrByteRange) || !strings.Contains(err.Error(), why) {
				t.Errorf("%q: got %v, want a refusal saying %q", g, err, why)
			}
		}
	}
}

func TestTrimPadding(t *testing.T) {
	der := []byte{0x30, 0x03, 0x02, 0x01, 0x05}
	if got := trimPadding(append(append([]byte(nil), der...), 0, 0, 0)); !bytes.Equal(got, der) {
		t.Fatalf("zero padding kept: %x", got)
	}
	junk := append(append([]byte(nil), der...), 0, 1, 0)
	if got := trimPadding(junk); !bytes.Equal(got, junk) {
		t.Fatalf("non-zero trailing bytes were removed: %x", got)
	}
	notDER := []byte{0x30, 0x80, 1, 2}
	if got := trimPadding(notDER); !bytes.Equal(got, notDER) {
		t.Fatalf("unparseable input changed: %x", got)
	}
}

// ---- a form made by hand: no cryptography, just the structure ----

// miniPDF writes numbered objects and a classic xref table.
func miniPDF(objs map[int]string, size int) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	off := map[int]int{}
	for n := 1; n < size; n++ {
		if body, ok := objs[n]; ok {
			off[n] = b.Len()
			fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", n, body)
		}
	}
	at := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", size)
	for n := 1; n < size; n++ {
		if o, ok := off[n]; ok {
			fmt.Fprintf(&b, "%010d 00000 n \n", o)
		} else {
			b.WriteString("0000000000 65535 f \n")
		}
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", size, at)
	return b.Bytes()
}

func formDoc(fields string, extra map[int]string) []byte {
	objs := map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields " + fields + " >> >>",
		2: "<< /Type /Pages /Kids [] /Count 0 >>",
	}
	for k, v := range extra {
		objs[k] = v
	}
	size := 3
	for k := range objs {
		if k+1 > size {
			size = k + 1
		}
	}
	return miniPDF(objs, size)
}

func mustOpen(t *testing.T, b []byte) *Document {
	t.Helper()
	d, err := Open(b)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return d
}

func TestSignaturesNoForm(t *testing.T) {
	d := mustOpen(t, miniPDF(map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R >>", 2: "<< /Type /Pages /Kids [] /Count 0 >>",
	}, 3))
	sigs, err := d.Signatures()
	if err != nil || len(sigs) != 0 {
		t.Fatalf("got %v, %v", sigs, err)
	}
}

func TestSignaturesInheritanceAndNames(t *testing.T) {
	// /FT and /V are inherited from the parent; the name is the dotted path; the
	// signature dictionary's own claims are listed.
	sigDict := "<< /Type /Sig /Filter /Adobe.PPKLite /SubFilter /adbe.pkcs7.detached /Name (Ann) /Reason (r) /Location (l) /M (D:2026) >>"
	d := mustOpen(t, formDoc("[3 0 R 6 0 R]", map[int]string{
		3: "<< /T (form) /Kids [4 0 R] >>",
		4: "<< /T (sig) /FT /Sig /Kids [5 0 R 5 0 R] /V 7 0 R >>",
		5: "<< /Subtype /Widget /Parent 4 0 R >>",
		6: "<< /T (text) /FT /Tx /V (hello) >>",
		7: sigDict,
	}))
	sigs, err := d.Signatures()
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != 1 {
		t.Fatalf("%d signatures, want 1 (two widgets share one value; a text field is not a signature): %+v", len(sigs), sigs)
	}
	s := sigs[0]
	if s.Field != "form.sig" || s.Filter != "Adobe.PPKLite" || s.SubFilter != "adbe.pkcs7.detached" ||
		s.Name != "Ann" || s.Reason != "r" || s.Location != "l" || s.M != "D:2026" {
		t.Fatalf("got %+v", s)
	}
	// No /ByteRange: listed, with the refusal, and no Contents.
	if !errors.Is(s.Err, ErrByteRange) || s.Contents != nil {
		t.Fatalf("Err=%v Contents=%x", s.Err, s.Contents)
	}
	if _, err := d.SignedBytes(s); !errors.Is(err, ErrByteRange) {
		t.Fatalf("SignedBytes of a refused signature: %v", err)
	}
}

func TestSignaturesUnsignedFieldIsNotListed(t *testing.T) {
	d := mustOpen(t, formDoc("[3 0 R]", map[int]string{3: "<< /T (empty) /FT /Sig >>"}))
	if sigs, err := d.Signatures(); err != nil || len(sigs) != 0 {
		t.Fatalf("got %v, %v", sigs, err)
	}
}

func TestSignaturesNeedsACatalogue(t *testing.T) {
	d := mustOpen(t, miniSigned(t))
	d.trailer = nil
	if _, err := d.Signatures(); err == nil {
		t.Fatal("a document with no trailer listed signatures")
	}
}

func TestSignaturesTwoWidgetsOneValue(t *testing.T) {
	d := mustOpen(t, formDoc("[4 0 R]", map[int]string{
		4: "<< /T (sig) /FT /Sig /Kids [5 0 R 6 0 R] /V 7 0 R >>",
		5: "<< /Subtype /Widget /Parent 4 0 R >>",
		6: "<< /Subtype /Widget /Parent 4 0 R >>",
		7: "<< /Type /Sig >>",
	}))
	if sigs, err := d.Signatures(); err != nil || len(sigs) != 1 {
		t.Fatalf("%d signatures, %v: one value is one signature however many widgets show it", len(sigs), err)
	}
}

func TestSignaturesCycleTerminates(t *testing.T) {
	d := mustOpen(t, formDoc("[3 0 R]", map[int]string{
		3: "<< /T (a) /Kids [4 0 R] >>",
		4: "<< /T (b) /Kids [3 0 R 4 0 R] >>",
	}))
	sigs, err := d.Signatures()
	if err != nil || len(sigs) != 0 {
		t.Fatalf("got %v, %v", sigs, err)
	}
}

func TestSignaturesDepthIsBounded(t *testing.T) {
	// A chain of direct dictionaries: no reference to recognise a cycle by,
	// only the depth bound stops it. Exactly maxFieldDepth levels are walked;
	// one more is refused.
	chain := func(levels int) string {
		return strings.Repeat("<< /T (x) /Kids [", levels) + strings.Repeat("] >>", levels)
	}
	d := mustOpen(t, formDoc("["+chain(maxFieldDepth)+"]", nil))
	if _, err := d.Signatures(); err != nil {
		t.Fatalf("%d levels: %v", maxFieldDepth, err)
	}
	d = mustOpen(t, formDoc("["+chain(maxFieldDepth+1)+"]", nil))
	if _, err := d.Signatures(); !errors.Is(err, ErrTooManyFields) {
		t.Fatalf("%d levels: got %v, want ErrTooManyFields", maxFieldDepth+1, err)
	}
}

func TestSignaturesWidthIsBounded(t *testing.T) {
	// Exactly maxFieldNodes fields are walked; one more is refused.
	fields := func(n int) string { return "[" + strings.Repeat("<< >> ", n) + "]" }
	d := mustOpen(t, formDoc(fields(maxFieldNodes), nil))
	if _, err := d.Signatures(); err != nil {
		t.Fatalf("%d fields: %v", maxFieldNodes, err)
	}
	d = mustOpen(t, formDoc(fields(maxFieldNodes+1), nil))
	if _, err := d.Signatures(); !errors.Is(err, ErrTooManyFields) {
		t.Fatalf("%d fields: got %v, want ErrTooManyFields", maxFieldNodes+1, err)
	}
}

func TestSignaturesOnlyAFieldOfTypeSigIsASignature(t *testing.T) {
	// A button whose /V is a dictionary is not a signature, whatever the
	// dictionary looks like; and a /Kids entry that is not a dictionary does not
	// inherit its parent's type and value into a signature of its own.
	d := mustOpen(t, formDoc("[3 0 R 4 0 R]", map[int]string{
		3: "<< /T (btn) /FT /Btn /V 7 0 R >>",
		4: "<< /T (sig) /FT /Sig /V 7 0 R /Kids [5] >>",
		7: "<< /Type /Sig /SubFilter /adbe.pkcs7.detached >>",
	}))
	if sigs, err := d.Signatures(); err != nil || len(sigs) != 0 {
		t.Fatalf("got %+v, %v", sigs, err)
	}
}

// signedByHand builds a file whose signature dictionary has a real placeholder
// and returns it with the offsets of the gap.
func signedByHand(t *testing.T, byteRangeOverride string) ([]byte, int, int) {
	t.Helper()
	zeros := strings.Repeat("0", 64)
	dict := "<< /Type /Sig /SubFilter /adbe.pkcs7.detached /ByteRange [0 0000000000 0000000000 0000000000] /Contents <" + zeros + "> >>"
	file := formDoc("[3 0 R]", map[int]string{
		3: "<< /T (s) /FT /Sig /V 4 0 R >>",
		4: dict,
	})
	start := bytes.Index(file, []byte("/Contents <")) + len("/Contents ")
	end := start + 1 + 64 + 1
	br := fmt.Sprintf("[0 %010d %010d %010d]", start, end, len(file)-end)
	if byteRangeOverride != "" {
		br = byteRangeOverride
	}
	copy(file[bytes.Index(file, []byte("/ByteRange "))+len("/ByteRange "):], br)
	return file, start, end
}

func TestSignedBytesAreTheTwoRanges(t *testing.T) {
	file, start, end := signedByHand(t, "")
	d := mustOpen(t, file)
	sigs, err := d.Signatures()
	if err != nil || len(sigs) != 1 || sigs[0].Err != nil {
		t.Fatalf("%+v %v", sigs, err)
	}
	if !sigs[0].CoversWholeFile {
		t.Error("ranges that end at the end of the file are not reported whole")
	}
	got, err := d.SignedBytes(sigs[0])
	want := append(append([]byte(nil), file[:start]...), file[end:]...)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("SignedBytes wrong: %v", err)
	}
}

func TestSignatureGapMustBeTheContentsString(t *testing.T) {
	// ⛔ The signature-wrapping attack: the ranges leave out a string that is not
	// this dictionary's /Contents. Here the gap is moved four bytes to the left,
	// so it no longer starts at the "<".
	file, start, end := signedByHand(t, "")
	shifted := fmt.Sprintf("[0 %010d %010d %010d]", start-4, end-4, len(file)-end+4)
	file2, _, _ := signedByHand(t, shifted)
	_ = file
	d := mustOpen(t, file2)
	sigs, _ := d.Signatures()
	if len(sigs) != 1 || !errors.Is(sigs[0].Err, ErrByteRange) || sigs[0].Contents != nil ||
		!strings.Contains(sigs[0].Err.Error(), "not a single <...> string") {
		t.Fatalf("%+v", sigs)
	}
}

func TestSignatureGapMustBeInsideItsObjectToTheByte(t *testing.T) {
	// The check is on the object's recorded position: an object that STARTS at
	// the gap (no bytes of it before the string) is the edge that is allowed,
	// one that starts after it is not.
	d := mustOpen(t, miniSigned(t))
	sigs, _ := d.Signatures()
	start := bytes.Index(d.buf, []byte("/Contents <")) + len("/Contents ")
	if len(sigs) != 1 || sigs[0].Err != nil {
		t.Fatalf("%+v", sigs)
	}
	e := d.xref[4]
	e.offset = int64(start)
	d.xref[4] = e
	if sigs, _ = d.Signatures(); len(sigs) != 1 || sigs[0].Err != nil {
		t.Fatalf("an object that starts exactly at the gap was refused: %+v", sigs)
	}
	e.offset = int64(start) + 1
	d.xref[4] = e
	if sigs, _ = d.Signatures(); len(sigs) != 1 || !errors.Is(sigs[0].Err, ErrByteRange) {
		t.Fatalf("an object that starts after the gap was accepted: %+v", sigs)
	}
}

func TestSignatureGapMustBeInsideTheSignatureObject(t *testing.T) {
	// A decoy string in ANOTHER object, with the real dictionary's /Contents left
	// inside the covered bytes. The decoy has the same value as the /Contents, so
	// the value comparison alone passes; the position check refuses it.
	zeros := strings.Repeat("0", 64)
	dict := "<< /Type /Sig /SubFilter /adbe.pkcs7.detached /ByteRange [0 0000000000 0000000000 0000000000] /Contents <" + zeros + "> >>"
	file := formDoc("[3 0 R]", map[int]string{
		3: "<< /T (s) /FT /Sig /V 5 0 R >>",
		4: "<< /Decoy <" + zeros + "> >>",
		5: dict,
	})
	start := bytes.Index(file, []byte("/Decoy <")) + len("/Decoy ")
	end := start + 66
	br := fmt.Sprintf("[0 %010d %010d %010d]", start, end, len(file)-end)
	copy(file[bytes.Index(file, []byte("/ByteRange "))+len("/ByteRange "):], br)
	d := mustOpen(t, file)
	sigs, _ := d.Signatures()
	if len(sigs) != 1 || !errors.Is(sigs[0].Err, ErrByteRange) {
		t.Fatalf("%+v", sigs)
	}
	if !strings.Contains(sigs[0].Err.Error(), "not inside object 5") && !strings.Contains(sigs[0].Err.Error(), "before object 5") {
		t.Fatalf("refused for another reason: %v", sigs[0].Err)
	}
}

func TestSignatureParsedContentsMustMatchTheGap(t *testing.T) {
	// The file's gap holds one value and the dictionary's /Contents another.
	file, start, _ := signedByHand(t, "")
	file[start+1] = '1' // the gap now reads 1000...; /Contents is the same bytes
	d := mustOpen(t, file)
	sigs, _ := d.Signatures()
	// same bytes in the file ARE the parsed value: this must still be listed
	if len(sigs) != 1 || sigs[0].Err != nil {
		t.Fatalf("%+v", sigs)
	}
	// Now make the dictionary hold a different /Contents than the gap, by
	// pointing the ranges at a second, identical-length string earlier in it.
	zeros := strings.Repeat("0", 64)
	ones := strings.Repeat("1", 64)
	dict := "<< /Type /Sig /SubFilter /adbe.pkcs7.detached /ByteRange [0 0000000000 0000000000 0000000000] /Other <" + ones + "> /Contents <" + zeros + "> >>"
	f2 := formDoc("[3 0 R]", map[int]string{3: "<< /T (s) /FT /Sig /V 4 0 R >>", 4: dict})
	s2 := bytes.Index(f2, []byte("/Other <")) + len("/Other ")
	e2 := s2 + 66
	copy(f2[bytes.Index(f2, []byte("/ByteRange "))+len("/ByteRange "):], fmt.Sprintf("[0 %010d %010d %010d]", s2, e2, len(f2)-e2))
	sigs, _ = mustOpen(t, f2).Signatures()
	if len(sigs) != 1 || !errors.Is(sigs[0].Err, ErrByteRange) || !strings.Contains(sigs[0].Err.Error(), "not the dictionary's /Contents") {
		t.Fatalf("%+v", sigs)
	}
}

func TestSignedBytesRevalidatesAHandMadeSignature(t *testing.T) {
	file, _, _ := signedByHand(t, "")
	d := mustOpen(t, file)
	sigs, _ := d.Signatures()
	good := sigs[0]
	for name, c := range map[string]struct {
		mutate func(s *Signature)
		why    string
	}{
		"range past the end": {func(s *Signature) { s.ByteRange = []int64{0, s.ByteRange[1], s.ByteRange[2], 1 << 40} }, "runs past"},
		"overlapping":        {func(s *Signature) { s.ByteRange = []int64{0, s.ByteRange[2], s.ByteRange[2] - 1, 5} }, "inside, touching or before"},
		"another contents":   {func(s *Signature) { s.Contents = []byte{1, 2, 3} }, "is not the string in the gap"},
		"no ranges":          {func(s *Signature) { s.ByteRange = nil }, "0 numbers"},
		"carries an Err":     {func(s *Signature) { s.Err = fmt.Errorf("%w: carried", ErrByteRange) }, "carried"},
	} {
		s := good
		s.ByteRange = append([]int64(nil), good.ByteRange...)
		c.mutate(&s)
		if _, err := d.SignedBytes(s); !errors.Is(err, ErrByteRange) || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: got %v, want %q", name, err, c.why)
		}
	}
}

func TestSignaturesSkipsWhatIsNotASignature(t *testing.T) {
	// An integer where a field should be, a signature field whose /V is a string,
	// and a /V that is a reference to nothing: none is a signature, none an error.
	d := mustOpen(t, formDoc("[5 3 0 R 4 0 R]", map[int]string{
		3: "<< /T (a) /FT /Sig /V (not a dictionary) >>",
		4: "<< /T (b) /FT /Sig /V 99 0 R >>",
	}))
	if sigs, err := d.Signatures(); err != nil || len(sigs) != 0 {
		t.Fatalf("got %v, %v", sigs, err)
	}
}

func TestSignatureByteRangeEntries(t *testing.T) {
	long := "[" + strings.Repeat("0 ", 70) + "]"
	for name, c := range map[string]struct{ br, why string }{
		"not an integer": {"[0 (ten) 20 30]", "entry 1 is not an integer"},
		"too long":       {long, "70 numbers"},
		"not an array":   {"(0 10 20 30)", "no /ByteRange array"},
	} {
		d := mustOpen(t, formDoc("[3 0 R]", map[int]string{
			3: "<< /T (s) /FT /Sig /V 4 0 R >>",
			4: "<< /Type /Sig /ByteRange " + c.br + " /Contents <00> >>",
		}))
		sigs, _ := d.Signatures()
		if len(sigs) != 1 || !errors.Is(sigs[0].Err, ErrByteRange) || !strings.Contains(sigs[0].Err.Error(), c.why) {
			t.Errorf("%s: %+v, want %q", name, sigs, c.why)
		}
		if sigs[0].Contents != nil {
			t.Errorf("%s: a refused signature has Contents %x", name, sigs[0].Contents)
		}
	}
}

func TestSignatureWithoutContentsString(t *testing.T) {
	// The ranges leave out a string that is in the dictionary, but the dictionary
	// has no /Contents at all.
	zeros := strings.Repeat("0", 64)
	dict := "<< /Type /Sig /ByteRange [0 0000000000 0000000000 0000000000] /Other <" + zeros + "> >>"
	f := formDoc("[3 0 R]", map[int]string{3: "<< /T (s) /FT /Sig /V 4 0 R >>", 4: dict})
	s := bytes.Index(f, []byte("/Other <")) + len("/Other ")
	e := s + 66
	copy(f[bytes.Index(f, []byte("/ByteRange "))+len("/ByteRange "):], fmt.Sprintf("[0 %010d %010d %010d]", s, e, len(f)-e))
	sigs, _ := mustOpen(t, f).Signatures()
	if len(sigs) != 1 || !errors.Is(sigs[0].Err, ErrByteRange) || !strings.Contains(sigs[0].Err.Error(), "no /Contents string") {
		t.Fatalf("%+v", sigs)
	}
}

// markEncrypted makes the document behave as an encrypted one for the checks
// that depend on it: the objects are already cached, decrypted or not, and the
// signature code only asks whether a decryptor is present.
func markEncrypted(t *testing.T, d *Document) {
	t.Helper()
	if _, err := d.Signatures(); err != nil {
		t.Fatal(err)
	}
	d.decrypt = &decryptor{}
}

func TestEncryptedFilePlacesTheGapByPosition(t *testing.T) {
	// The parsed /Contents of an encrypted file is not the file's bytes, so the
	// value comparison does not run; the position check still must.
	d := mustOpen(t, miniSigned(t))
	markEncrypted(t, d)
	sigs, _ := d.Signatures()
	if len(sigs) != 1 || sigs[0].Err != nil {
		t.Fatalf("a gap inside its own object was refused: %+v", sigs)
	}

	// The decoy in another object, whose value equals nothing the parser checks.
	zeros := strings.Repeat("0", 64)
	f := formDoc("[3 0 R]", map[int]string{
		3: "<< /T (s) /FT /Sig /V 5 0 R >>",
		4: "<< /Decoy <" + zeros + "> >>",
		5: "<< /Type /Sig /ByteRange [0 0000000000 0000000000 0000000000] /Contents <" + zeros + "> >>",
	})
	s := bytes.Index(f, []byte("/Decoy <")) + len("/Decoy ")
	e := s + 66
	copy(f[bytes.Index(f, []byte("/ByteRange "))+len("/ByteRange "):], fmt.Sprintf("[0 %010d %010d %010d]", s, e, len(f)-e))
	d = mustOpen(t, f)
	markEncrypted(t, d)
	sigs, _ = d.Signatures()
	if len(sigs) != 1 || !errors.Is(sigs[0].Err, ErrByteRange) {
		t.Fatalf("%+v", sigs)
	}

	// A dictionary that lives inside an object stream has no byte position of
	// its own in the file, so in an encrypted file nothing places the gap.
	d = mustOpen(t, miniSigned(t))
	markEncrypted(t, d)
	d.xref[4] = xrefEntry{kind: 'o', strmNum: 9}
	sigs, _ = d.Signatures()
	if len(sigs) != 1 || !errors.Is(sigs[0].Err, ErrByteRange) || !strings.Contains(sigs[0].Err.Error(), "encrypted file") {
		t.Fatalf("%+v", sigs)
	}
	// ...while an unencrypted one is judged by its parsed /Contents.
	d = mustOpen(t, miniSigned(t))
	if _, err := d.Signatures(); err != nil {
		t.Fatal(err)
	}
	d.xref[4] = xrefEntry{kind: 'o', strmNum: 9}
	if sigs, _ = d.Signatures(); len(sigs) != 1 || sigs[0].Err != nil {
		t.Fatalf("%+v", sigs)
	}
}

func TestSignedBytesRefusesAGapThatIsNotAString(t *testing.T) {
	file, start, end := signedByHand(t, "")
	d := mustOpen(t, file)
	sigs, _ := d.Signatures()
	s := sigs[0]
	s.ByteRange = []int64{0, int64(start - 4), int64(end - 4), int64(len(file) - end + 4)}
	if _, err := d.SignedBytes(s); !errors.Is(err, ErrByteRange) {
		t.Fatalf("got %v", err)
	}
}

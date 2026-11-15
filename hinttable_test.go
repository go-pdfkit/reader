// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ⛔⛔ THE HINT TABLES ARE WHERE #40 SAID THE CHECKER WAS BLIND, and this is the
// first step into them: "a file can pass this and still have hint tables that
// send a reader to the wrong offset". Every parameter check so far compares
// numbers at the FRONT of the file against the file; this reads the index a
// reader would actually fetch by.
//
// ⭐ AND IT HAS THE BEST JUDGE IN THE WHOLE ARC. `qpdf --show-linearization`
// prints every field of the decoded table by name, so each of Table F.3's
// thirteen items is compared against the reference implementation's reading of
// the same bytes — not against my arithmetic, and not against a fixture I
// wrote to agree with me.
func TestThePageOffsetHeaderAgreesWithQpdfFieldByField(t *testing.T) {
	qpdf := qpdfOrSkip(t)

	for _, shape := range []struct {
		name  string
		extra []string
	}{
		{"as qpdf writes it", nil},
		{"with object streams", []string{"--object-streams=generate"}},
		{"with streams uncompressed", []string{"--compress-streams=n"}},
	} {
		for _, pages := range []int{1, 2, 5, 25} {
			t.Run(shape.name+", "+strconv.Itoa(pages)+" pages", func(t *testing.T) {
				qpdfExtra = shape.extra
				defer func() { qpdfExtra = nil }()
				_, linear := linearizedFixture(t, pages)

				d, err := Open(linear)
				if err != nil {
					t.Fatalf("this package cannot open qpdf's linearized output: %v", err)
				}
				lin, err := Linearized(linear)
				if err != nil {
					t.Fatalf("qpdf's linearized output is not linearized: %v", err)
				}
				got, err := d.readHintTables(lin)
				if err != nil {
					t.Fatalf("the hint tables of a file qpdf linearized do not read: %v", err)
				}

				path := filepath.Join(t.TempDir(), "linear.pdf")
				if err := os.WriteFile(path, linear, 0o644); err != nil {
					t.Fatal(err)
				}
				want := qpdfHintFields(t, qpdf, path)

				h := got.pageOffset
				inFile, err := h.firstPageOffsetInFile(lin)
				if err != nil {
					t.Fatalf("the first page's offset is not addressable: %v", err)
				}
				// Every one of Table F.3's thirteen items, by qpdf's name for
				// it. A field left out of this list is a field nothing checks.
				for _, c := range []struct {
					field string
					got   uint32
				}{
					{"min_nobjects", h.MinNObjects},
					// ⛔⛔ qpdf REPORTS THE EFFECTIVE OFFSET AND THE TABLE
					// STORES A DIFFERENT NUMBER, which cost this test one
					// failing field across all twelve shapes to find. The
					// stored value is computed as if the primary hint stream
					// were not there; FirstPageOffsetInFile adds its length
					// when it lies at or before that point, and says why.
					// Comparing the raw field here would have been comparing
					// two different questions — the same shape as the object
					// streams in #52.
					{"first_page_offset", uint32(inFile)},
					{"nbits_delta_nobjects", uint32(h.NBitsDeltaNObjects)},
					{"min_page_length", h.MinPageLength},
					{"nbits_delta_page_length", uint32(h.NBitsDeltaPageLength)},
					{"min_content_offset", h.MinContentOffset},
					{"nbits_delta_content_offset", uint32(h.NBitsDeltaContentOff)},
					{"min_content_length", h.MinContentLength},
					{"nbits_delta_content_length", uint32(h.NBitsDeltaContentLen)},
					{"nbits_nshared_objects", uint32(h.NBitsNSharedObjects)},
					{"nbits_shared_identifier", uint32(h.NBitsSharedIdent)},
					{"nbits_shared_numerator", uint32(h.NBitsSharedNumerator)},
					{"shared_denominator", h.SharedDenominator},
				} {
					w, ok := want[c.field]
					if !ok {
						t.Errorf("qpdf printed no %s, so this field is compared against nothing",
							c.field)
						continue
					}
					if c.got != w {
						t.Errorf("%s = %d, qpdf says %d", c.field, c.got, w)
					}
				}
			})
		}
	}
}

// qpdfHintFields reads the `name: number` lines qpdf prints for the page
// offset hint table.
//
// ⛔ IT STOPS AT THE SHARED OBJECT TABLE, because the two tables share field
// NAMES — both print nbits_nobjects-shaped keys — and a parser that read the
// whole output would silently compare a page-offset field against a
// shared-object one. The header is the boundary and it is asserted present.
func qpdfHintFields(t *testing.T, qpdf, path string) map[string]uint32 {
	t.Helper()

	cmd := exec.Command(qpdf, "--show-linearization", path)
	cmd.Stdin = nil
	out, _ := cmd.CombinedOutput()
	text := string(out)

	const start = "Page Offsets Hint Table"
	const stop = "Shared Objects Hint Table"
	from := strings.Index(text, start)
	if from < 0 {
		ver, _ := exec.Command(qpdf, "--version").Output()
		t.Fatalf("qpdf printed no %q section, so there is nothing to compare against — "+
			"this needs qpdf 12 or newer and this one says %q:\n%s",
			start, strings.TrimSpace(string(ver)), text)
	}
	section := text[from+len(start):]
	if to := strings.Index(section, stop); to >= 0 {
		section = section[:to]
	} else {
		t.Fatalf("qpdf printed no %q section, so the page offset fields have no end and "+
			"a shared-object field could be compared as a page-offset one:\n%s", stop, text)
	}

	fields := map[string]uint32{}
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		// ⛔⛔ THE PER-PAGE ENTRIES END THE HEADER, AND THE PREFIX IS CHECKED
		// BEFORE THE SPLIT. qpdf writes "Page 0:" with a colon and NO trailing
		// space, so `Cut(line, ": ")` does not match it — the first version
		// checked the prefix on Cut's output, the break never fired, and the
		// indented per-page fields (nobjects, length, content_offset,
		// content_length, nshared_objects, "identifier 0", "numerator 0") were
		// collected as header fields. Measured: 18 to 20 fields where Table
		// F.3 has thirteen, and the count is what caught it.
		//
		// ⛔ The per-page names OVERLAP the header's in meaning but not in
		// spelling (nobjects against min_nobjects), so the over-collection did
		// not corrupt any compared value — it only added entries. That is luck,
		// not design, and it is exactly why the count is asserted rather than
		// just the thirteen lookups.
		if strings.HasPrefix(trimmed, "Page ") {
			break
		}
		name, value, ok := strings.Cut(trimmed, ": ")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
		if err != nil {
			continue
		}
		fields[name] = uint32(n)
	}
	// ⛔ THIRTEEN, ASSERTED. A parser that matched nothing would hand back an
	// empty map, every lookup would miss, and the test above would report
	// thirteen "compared against nothing" errors rather than passing — but
	// asserting the count here names the cause once instead of thirteen times.
	if len(fields) != 13 {
		t.Fatalf("read %d header field(s) from qpdf's page offset table, want Table F.3's "+
			"thirteen: %v\n%s", len(fields), fields, text)
	}
	return fields
}

// ⛔⛔ AND THE GHOSTSCRIPT FIXTURE'S TABLE READS, WHICH IS WORTH PINNING
// SEPARATELY. qpdf refuses that file's hint tables with five warnings, so it
// would be easy to assume the bytes are unreadable. They are not: the page
// offset header parses and its values are sane. What is wrong is in the
// SHARED OBJECT table and in the per-page lengths, neither of which this
// change reads yet — so "qpdf complains" and "this cannot decode" are
// different facts, and conflating them would make the next step look done.
func TestTheGhostscriptFixturesPageOffsetHeaderStillReads(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "linearized-gs.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	lin, err := Linearized(b)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := d.readHintTables(lin)
	if err != nil {
		t.Fatalf("the Ghostscript fixture's page offset header does not read, which is a "+
			"stronger claim than qpdf's warnings support: %v", err)
	}
	// Measured from qpdf --show-linearization on that fixture: it reports
	// min_nobjects 3 and first_page_offset 577.
	if got := tables.pageOffset.MinNObjects; got != 3 {
		t.Errorf("min_nobjects = %d, qpdf reads 3", got)
	}
	if got := tables.pageOffset.FirstPageOffset; got != 577 {
		t.Errorf("first_page_offset = %d, qpdf reads 577", got)
	}

	// ⛔⛔ AND THIS FIXTURE IS THE CONTROL FOR THE HINT-STREAM ADJUSTMENT, which
	// no qpdf file in the corpus can be. Ghostscript places /H AFTER the first
	// page (/H = /E = 829), so the stored offset needs NO adjustment and qpdf
	// reports it unchanged at 577. On every qpdf fixture the hint stream comes
	// first and the length IS added. Adding it unconditionally would have
	// agreed with all twelve qpdf shapes and been wrong here.
	got, err := tables.pageOffset.firstPageOffsetInFile(lin)
	if err != nil {
		t.Fatal(err)
	}
	if got != 577 {
		t.Errorf("firstPageOffsetInFile = %d, want 577 unadjusted: /H is at %d, after the "+
			"first page, so its length must not be added", got, lin.HintOffset)
	}
}

// ⛔⛔ AND A FIRST-PAGE OFFSET THIS BUILD CANNOT ADDRESS IS REFUSED, NOT
// TRUNCATED — the class #42 fixed one release earlier, in another file, which
// review found I had written again. `int(uint32)` on a value of 0x80000000 or
// above goes NEGATIVE on a 32-bit build, so both the comparison against /H and
// the length addition give a wrong answer, and the 386 and arm lanes would not
// see it because no fixture holds such a value.
//
// ⛔ intMax is the same seam #42 introduced for exactly this: a variable, so a
// 64-bit test can stand in for a 32-bit build. Without it the refusal is
// unreachable from any file small enough to commit.
// ⛔⛔⛔ AND THIS TEST'S FIRST VERSION WAS ITSELF ARCHITECTURE-DEPENDENT, which
// the 386 and arm lanes caught and my 64-bit machine could not. Its control
// asserted that 2^31+5 IS addressable — true where it was written, false on a
// 32-bit build, where the code correctly refuses. So the code was right and
// the control asserted the opposite of the property under test.
//
// ⛔ The shape is "an absence on the tools installed here is not a verdict",
// one level in: a test must not take its EXPECTATION from the machine it runs
// on. The refusal is exercised by shrinking intMax, which works everywhere,
// and the control uses a value every build can address.
func TestAFirstPageOffsetThisBuildCannotAddressIsRefused(t *testing.T) {
	lin := &Linearization{HintOffset: 0, HintLength: 5}

	saved := intMax
	defer func() { intMax = saved }()

	// ⛔ The refusal, reached by shrinking the limit rather than by choosing a
	// value only some builds cannot hold.
	intMax = 1 << 20
	h := &pageOffsetHeader{FirstPageOffset: 1 << 21}
	_, err := h.firstPageOffsetInFile(lin)
	if !errors.Is(err, errHintTable) {
		t.Fatalf("an unaddressable offset was returned rather than refused: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot address") {
		t.Errorf("it was refused, but not for being unaddressable: %v", err)
	}

	// ⛔ THE CONTROL, with a value EVERY build can address, so the refusal is
	// about the bound rather than about the method always failing — and so
	// that this test says the same thing on 386, arm and amd64.
	intMax = saved
	h = &pageOffsetHeader{FirstPageOffset: 1000}
	got, err := h.firstPageOffsetInFile(lin)
	if err != nil {
		t.Fatalf("an offset every build can address was refused: %v", err)
	}
	if want := int64(1005); got != want {
		t.Errorf("firstPageOffsetInFile = %d, want %d", got, want)
	}
}

// ⛔⛔ AND /H PAST THE END OF THE FILE IS A REFUSAL AND NOT A PANIC, which is
// the most serious thing review found today: `d.buf[lin.HintOffset:]` was
// unchecked, and it crashed on an offset taken straight from an untrusted
// dictionary. Every other refusal in that file is a message; this one was a
// crash.
//
// ⛔ "It is only reached after CheckLinearized" is not a defence. A function
// that panics on its argument is one nobody may call in a new place, and the
// next caller will not know.
func TestAHintStreamOffsetPastTheEndOfTheFileIsRefused(t *testing.T) {
	for _, off := range []int{-1, 3, 99} {
		d := bareDoc(nil, nil, nil)
		d.buf = []byte("abc")
		_, err := d.readHintTables(&Linearization{HintOffset: off})
		if !errors.Is(err, errHintTable) {
			t.Fatalf("/H at byte %d of a 3-byte file was accepted: %v", off, err)
		}
		if !strings.Contains(err.Error(), "3-byte file") {
			t.Errorf("/H at %d was refused, but without naming the file's size: %v", off, err)
		}
	}
}

// ⛔⛔ A WIDTH THAT IS NOT A WIDTH IS REFUSED BEFORE IT IS USED, which is the
// one check in this file that is not a comparison. F.4.1 bounds every
// bit-count item to 0..32 but has them written as 16-bit numbers, so the field
// can hold 65535 — and a width of 65535 would make the reader consume 65535
// bits of whatever follows and report it as a page's object count.
//
// ⭐ Not hypothetical: the committed Ghostscript fixture writes 65536 into the
// OTHER table's min_group_length, which is 0x10000 and plainly a placeholder
// nobody filled in. A producer that leaves one field unset leaves others
// unset.
func TestABitWidthThatIsNotAWidthIsRefused(t *testing.T) {
	// Table F.3's first two items are 32 bits each; item 3 is the first width.
	header := make([]byte, 36)
	header[8], header[9] = 0xFF, 0xFF // item 3 = 65535

	_, err := readPageOffsetHeader(&bits{b: header})
	if !errors.Is(err, errHintTable) {
		t.Fatalf("a 65535-bit field width was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "nbits_delta_nobjects is 65535") {
		t.Errorf("it was refused, but the message does not name the field and its value: %v", err)
	}
	if !strings.Contains(err.Error(), "0..32") {
		t.Errorf("it was refused, but without naming the bound F.4.1 gives: %v", err)
	}
}

// ⛔ EVERY ONE OF THE SEVEN WIDTH ITEMS IS CHECKED, not just the first. The
// loop that checks them is a table, and a table with one row exercised is a
// table whose other rows could be absent.
func TestEveryBitWidthItemIsChecked(t *testing.T) {
	// Byte offsets of Table F.3's seven width items within the 36-byte header,
	// derived from the widths 32,32,16,32,16,32,16,32,16,16,16,16,16.
	for _, c := range []struct {
		field string
		at    int
	}{
		{"nbits_delta_nobjects", 8},
		{"nbits_delta_page_length", 14},
		{"nbits_delta_content_offset", 20},
		{"nbits_delta_content_length", 26},
		{"nbits_nshared_objects", 28},
		{"nbits_shared_identifier", 30},
		{"nbits_shared_numerator", 32},
	} {
		t.Run(c.field, func(t *testing.T) {
			header := make([]byte, 36)
			header[c.at], header[c.at+1] = 0xFF, 0xFF
			_, err := readPageOffsetHeader(&bits{b: header})
			if !errors.Is(err, errHintTable) {
				t.Fatalf("%s = 65535 was accepted: %v", c.field, err)
			}
			if !strings.Contains(err.Error(), c.field) {
				t.Errorf("it was refused, but the message blames another field: %v", err)
			}
		})
	}
}

// ⛔ A HEADER SHORTER THAN 36 BYTES IS REFUSED WITH THE ITEM THAT RAN OUT, not
// with a generic short read: the item number is what tells the next person
// whether the table is truncated or their widths are wrong.
func TestAHeaderThatRunsOutNamesTheItem(t *testing.T) {
	_, err := readPageOffsetHeader(&bits{b: make([]byte, 35)})
	if !errors.Is(err, errHintTable) {
		t.Fatalf("a 35-byte header was accepted for a 36-byte table: %v", err)
	}
	if !strings.Contains(err.Error(), "item 13 of 13") {
		t.Errorf("it was refused, but without naming which item ran out: %v", err)
	}
}

// ⛔⛔ AND EVERY WAY THE HINT STREAM ITSELF CAN FAIL TO YIELD A TABLE, each
// named for what went wrong. "The hint tables cannot be read" without saying
// whether the object does not parse, is not a stream, does not decode, or
// decodes to something too short is a refusal nobody can act on — and these
// are four different repairs.
func TestEveryWayTheHintStreamFailsToYieldATableIsNamed(t *testing.T) {
	for _, c := range []struct {
		name string
		buf  string
		want string
	}{
		{
			name: "the object does not parse",
			buf:  "%PDF-1.7\nnot an object at all\n",
			want: "does not parse",
		},
		{
			name: "the object is not a stream",
			buf:  "%PDF-1.7\n9 0 obj\n<< /S 43 >>\nendobj\n",
			want: "not a stream",
		},
		{
			name: "the stream does not decode",
			buf: "%PDF-1.7\n9 0 obj\n<< /Length 4 /Filter /FlateDecode >>\nstream\n" +
				"junk\nendstream\nendobj\n",
			want: "does not decode",
		},
		{
			name: "it decodes to less than Table F.3's header",
			buf:  "%PDF-1.7\n9 0 obj\n<< /Length 4 >>\nstream\nabcd\nendstream\nendobj\n",
			want: "bits were asked for",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			const head = "%PDF-1.7\n"
			d := bareDoc(nil, nil, nil)
			d.buf = []byte(c.buf)
			_, err := d.readHintTables(&Linearization{HintOffset: len(head)})
			if !errors.Is(err, errHintTable) {
				t.Fatalf("it was accepted (or refused oddly): %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("it was refused, but the message does not say %q: %v", c.want, err)
			}
		})
	}
}

// ⛔⛔ THE BIT READER IS TESTED AS A BIT READER, because every number this file
// produces comes out of it and an off-by-one in it would shift whole tables
// while leaving them parseable. Reading a table a byte at a time gives
// plausible wrong numbers, which is the failure that has no symptom.
func TestTheBitReaderReadsBigEndianAcrossByteBoundaries(t *testing.T) {
	// 0xA5 = 1010 0101, 0x3C = 0011 1100.
	for _, c := range []struct {
		name string
		b    []byte
		take []int
		want []uint32
	}{
		{"whole bytes", []byte{0xA5, 0x3C}, []int{8, 8}, []uint32{0xA5, 0x3C}},
		{"one bit at a time", []byte{0xA5, 0x3C}, []int{1, 1, 1, 1}, []uint32{1, 0, 1, 0}},
		{"a field straddling the boundary", []byte{0xA5, 0x3C}, []int{4, 8, 4}, []uint32{0xA, 0x53, 0xC}},
		{"a zero-width field reads nothing", []byte{0xA5, 0x3C}, []int{0, 8}, []uint32{0, 0xA5}},
		{"all sixteen bits at once", []byte{0xA5, 0x3C}, []int{16}, []uint32{0xA53C}},

		// ⛔⛔ THE FULL-WIDTH READS WITH THE HIGH BIT SET, which the table did
		// not have and which review supplied by running them. They are not
		// tautological: an accumulator of type int, or a signed one, fails
		// these on a 32-bit build — and Table F.3's items 1, 2, 4, 6 and 8 are
		// all 32 bits wide, so a file with a min_* value above 2^31 would be
		// misread with no fixture to show it.
		{"thirty-two ones", []byte{0xFF, 0xFF, 0xFF, 0xFF}, []int{32}, []uint32{0xFFFFFFFF}},
		{"only the high bit", []byte{0x80, 0x00, 0x00, 0x00}, []int{32}, []uint32{0x80000000}},
		{
			"a thirty-two bit field straddling a byte boundary",
			[]byte{0xA5, 0xFF, 0xFF, 0xFF, 0xF0},
			[]int{4, 32},
			[]uint32{0xA, 0x5FFFFFFF},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := &bits{b: c.b}
			for i, n := range c.take {
				got, err := r.read(n)
				if err != nil {
					t.Fatalf("read(%d) at step %d: %v", n, i, err)
				}
				if got != c.want[i] {
					t.Errorf("step %d: read(%d) = %#x, want %#x", i, n, got, c.want[i])
				}
			}
		})
	}
}

// ⛔ AND ITS EDGES: a width outside 0..32, and a read past the end. Both are
// refusals rather than zeroes, because a zero from a reader that ran out is
// indistinguishable from a zero the file actually holds.
func TestTheBitReaderRefusesWhatItCannotRead(t *testing.T) {
	for _, c := range []struct {
		name string
		b    []byte
		n    int
		want string
	}{
		{"more than 32 bits", []byte{1, 2, 3, 4, 5}, 33, "bounds every width"},
		{"a negative width", []byte{1}, -1, "bounds every width"},
		{"past the end", []byte{1}, 9, "remain"},
		{"past the end of nothing", nil, 1, "remain"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := &bits{b: c.b}
			_, err := r.read(c.n)
			if !errors.Is(err, errHintTable) {
				t.Fatalf("read(%d) of %d byte(s) was accepted: %v", c.n, len(c.b), err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("it was refused, but not for %q: %v", c.want, err)
			}
		})
	}
}

// ⛔ align AND seekByte move in BYTES, which is what /S and the other table
// positions are counted in — F.4.2 places the shared object table at a byte
// offset, not a bit one. A seek that landed on a bit boundary would read every
// field of that table shifted.
func TestAligningAndSeekingMoveInBytes(t *testing.T) {
	r := &bits{b: []byte{0xFF, 0x00, 0xAA, 0x55}}
	if _, err := r.read(3); err != nil {
		t.Fatal(err)
	}
	r.align()
	if r.pos != 8 {
		t.Errorf("align from bit 3 left pos at %d, want 8", r.pos)
	}
	// ⛔ align on a boundary must NOT advance a byte: a table whose first
	// section ends exactly on a byte would otherwise lose one.
	r.align()
	if r.pos != 8 {
		t.Errorf("align on a boundary moved pos to %d, want it unchanged at 8", r.pos)
	}
	if err := r.seekByte(2); err != nil {
		t.Fatal(err)
	}
	got, err := r.read(8)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0xAA {
		t.Errorf("after seekByte(2), read(8) = %#x, want 0xAA", got)
	}
	// Seeking to the very end is legal and leaves nothing to read; past it is
	// not.
	if err := r.seekByte(len(r.b)); err != nil {
		t.Errorf("seeking to the end was refused: %v", err)
	}
	if !r.atEnd() {
		t.Error("at the end, atEnd says otherwise")
	}
	for _, off := range []int{-1, 5} {
		if err := r.seekByte(off); !errors.Is(err, errHintTable) {
			t.Errorf("seekByte(%d) of a 4-byte table was accepted: %v", off, err)
		}
	}
}

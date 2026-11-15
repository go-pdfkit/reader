// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"bytes"
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

// ⛔⛔ AND EVERY PAGE'S ENTRY, FIELD BY FIELD, WHICH IS WHAT THE COLUMN-MAJOR
// READING HAD TO GET RIGHT. qpdf prints each page's nobjects, length,
// content_offset, content_length and nshared_objects, plus an identifier and a
// numerator per shared reference — so a reading that mixed one page's bits
// with another's is caught per page rather than in aggregate.
//
// ⭐ This is the test that found the byte alignment. Without the per-page
// comparison the header test stayed green while the entries were nonsense.
func TestEveryPageEntryAgreesWithQpdf(t *testing.T) {
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
					t.Fatal(err)
				}
				lin, err := Linearized(linear)
				if err != nil {
					t.Fatal(err)
				}
				got, err := d.readHintTables(lin)
				if err != nil {
					t.Fatalf("the hint tables of a file qpdf linearized do not read: %v", err)
				}

				path := filepath.Join(t.TempDir(), "linear.pdf")
				if err := os.WriteFile(path, linear, 0o644); err != nil {
					t.Fatal(err)
				}
				want := qpdfPageEntries(t, qpdf, path)

				if len(got.pages) != len(want) {
					t.Fatalf("read %d page entr(ies), qpdf prints %d", len(got.pages), len(want))
				}
				for i, g := range got.pages {
					w := want[i]
					for _, c := range []struct {
						field    string
						got, exp uint32
					}{
						{"nobjects", g.NObjects, w.nobjects},
						{"length", g.Length, w.length},
						{"content_offset", g.ContentOffset, w.contentOffset},
						{"content_length", g.ContentLength, w.contentLength},
						{"nshared_objects", uint32(len(g.Shared)), w.nshared},
					} {
						if c.got != c.exp {
							t.Errorf("page %d: %s = %d, qpdf says %d", i, c.field, c.got, c.exp)
						}
					}
					// ⛔ The identifiers and numerators are compared in ORDER,
					// which Table F.4 item 5 requires ("in the same order as
					// the preceding item"). A set comparison would accept a
					// decoder that paired the right values with the wrong
					// references.
					for j, s := range g.Shared {
						if j >= len(w.identifiers) {
							break
						}
						if s.Identifier != w.identifiers[j] {
							t.Errorf("page %d: identifier %d = %d, qpdf says %d",
								i, j, s.Identifier, w.identifiers[j])
						}
						if s.Numerator != w.numerators[j] {
							t.Errorf("page %d: numerator %d = %d, qpdf says %d",
								i, j, s.Numerator, w.numerators[j])
						}
					}
				}
			})
		}
	}
}

// ⛔⛔ THE SHARED OBJECT HINT TABLE, WHICH TABLE F.2 MARKS Required AND WHICH
// THE GHOSTSCRIPT FIXTURE GETS WRONG. qpdf prints all seven of Table F.5's
// header fields and each group's length, so the comparison is field by field
// against the reference implementation's reading of the same bytes.
//
// ⚠ EVERY FILE IN THIS CORPUS HAS nshared_total == nshared_first_page, so the
// SECOND sequence F.4.2 describes — the groups for objects in part 8 — is
// empty throughout, and nothing here exercises it. That is a fact about the
// corpus and it is stated rather than left for someone to discover: these
// fixtures have no object shared between later pages but not the first.
func TestTheSharedObjectTableAgreesWithQpdf(t *testing.T) {
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
					t.Fatal(err)
				}
				lin, err := Linearized(linear)
				if err != nil {
					t.Fatal(err)
				}
				got, err := d.readHintTables(lin)
				if err != nil {
					t.Fatalf("the hint tables of a file qpdf linearized do not read: %v", err)
				}

				path := filepath.Join(t.TempDir(), "linear.pdf")
				if err := os.WriteFile(path, linear, 0o644); err != nil {
					t.Fatal(err)
				}
				fields, lengths := qpdfSharedTable(t, qpdf, path)

				h := got.shared
				for _, c := range []struct {
					field string
					got   uint32
				}{
					{"first_shared_obj", h.FirstSharedObj},
					{"first_shared_offset", h.FirstSharedOffset},
					{"nshared_first_page", h.NSharedFirstPage},
					{"nshared_total", h.NSharedTotal},
					{"nbits_nobjects", uint32(h.NBitsNObjects)},
					{"min_group_length", h.MinGroupLength},
					{"nbits_delta_group_length", uint32(h.NBitsDeltaGroupLength)},
				} {
					w, ok := fields[c.field]
					if !ok {
						t.Errorf("qpdf printed no %s, so it is compared against nothing", c.field)
						continue
					}
					if c.got != w {
						t.Errorf("%s = %d, qpdf says %d", c.field, c.got, w)
					}
				}

				if len(got.sharedGroups) != len(lengths) {
					t.Fatalf("read %d group(s), qpdf prints %d", len(got.sharedGroups), len(lengths))
				}
				for i, g := range got.sharedGroups {
					if g.Length != lengths[i] {
						t.Errorf("group %d: length = %d, qpdf says %d", i, g.Length, lengths[i])
					}
				}
			})
		}
	}
}

// ⛔⛔ AND THE THREE ITEMS QPDF DOES NOT PRINT, WHICH THE TEST ABOVE THEREFORE
// DOES NOT CHECK. qpdf's shared-table output is the seven header fields and
// one "group length" per group — nothing else. So Table F.6's item 2 (the
// signature flag), item 3 (the 16-byte MD5) and item 4 (the object count) are
// decoded and compared against NOTHING, and so is the byte alignment between
// groups (b), (c) and (d).
//
// ⭐ ITEM 4 HAS A SECOND WITNESS IN THIS PACKAGE, and it is independent of the
// decoder: F.4.2 says "All objects associated with the first page (part 6)
// shall have entries in the shared object hint table, regardless of whether
// they are actually shared", so the first nshared_first_page groups' object
// counts must sum to the number of objects part 6 holds — and Partition
// computes part 6 from the page tree without reading a hint table at all. Two
// quantities derived by different routes agreeing is worth more than either
// one agreeing with itself.
//
// ⚠ Items 2 and 3 have NO witness: see the test below, which records that no
// fixture in this corpus sets the flag.
func TestTheFirstPageGroupsAccountForEveryObjectInPartSix(t *testing.T) {
	qpdfOrSkip(t)

	for _, shape := range []struct {
		name  string
		extra []string
	}{
		{"as qpdf writes it", nil},
		{"with streams uncompressed", []string{"--compress-streams=n"}},
	} {
		for _, pages := range []int{1, 2, 5, 25} {
			t.Run(shape.name+", "+strconv.Itoa(pages)+" pages", func(t *testing.T) {
				qpdfExtra = shape.extra
				defer func() { qpdfExtra = nil }()
				_, linear := linearizedFixture(t, pages)

				d, err := Open(linear)
				if err != nil {
					t.Fatal(err)
				}
				lin, err := Linearized(linear)
				if err != nil {
					t.Fatal(err)
				}
				tables, err := d.readHintTables(lin)
				if err != nil {
					t.Fatal(err)
				}
				p, err := d.Partition()
				if err != nil {
					t.Fatalf("the partition this is checked against refused the file: %v", err)
				}

				inPartSix := 0
				for _, part := range p.Of {
					if part == PartFirstPage {
						inPartSix++
					}
				}

				first := int(tables.shared.NSharedFirstPage)
				if first > len(tables.sharedGroups) {
					t.Fatalf("nshared_first_page is %d and only %d group(s) were read",
						first, len(tables.sharedGroups))
				}
				sum := 0
				for _, g := range tables.sharedGroups[:first] {
					sum += int(g.NObjects)
				}
				if sum != inPartSix {
					t.Errorf("the first %d shared group(s) account for %d object(s) and the "+
						"partition puts %d in part 6 — Table F.6 item 4 (the object count, "+
						"which qpdf does not print) or the partition is wrong",
						first, sum, inPartSix)
				}
			})
		}
	}
}

// ⛔⛔ AND NO FIXTURE IN THIS CORPUS SETS THE SIGNATURE FLAG, which is recorded
// here rather than left for someone to assume otherwise. Table F.6 item 2 is
// one bit and item 3 is the 16-byte MD5 it guards; with the flag clear
// throughout, item 3's read is never taken and the alignment after group (c)
// is never exercised by a real file.
//
// ⛔ THE RISK IS SPECIFIC: a reader that always consumed 128 bits there, or
// never did, would agree with every file here and be 16 bytes out of step on
// the first signed group it met. This test is the marker for that gap — if it
// ever fails because a producer started signing, the decoder has gained a
// witness and this comment should go.
func TestNoFixtureSetsASharedObjectSignature(t *testing.T) {
	qpdfOrSkip(t)

	signed := 0
	for _, pages := range []int{1, 2, 5, 25} {
		_, linear := linearizedFixture(t, pages)
		d, err := Open(linear)
		if err != nil {
			t.Fatal(err)
		}
		lin, err := Linearized(linear)
		if err != nil {
			t.Fatal(err)
		}
		tables, err := d.readHintTables(lin)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range tables.sharedGroups {
			if g.Signature != nil {
				signed++
			}
		}
	}
	if signed != 0 {
		t.Errorf("%d group(s) carry a signature, so Table F.6 item 3 now HAS a witness in "+
			"the corpus — delete the comment that says it does not", signed)
	}
}

// sharedTableBytes builds a Table F.5 header followed by whatever the caller
// appends, so that a crafted shared object table can be handed to the reader
// directly.
//
// ⛔ The header is written as the annex lays it out — four 32-bit items, a
// 16-bit one, a 32-bit one, a 16-bit one — so a test reading it back is
// checking the reader against the annex's layout rather than against another
// copy of the reader's own idea of it.
func sharedTableBytes(firstObj, firstOff, nFirstPage, nTotal uint32, nbitsNObjects uint16, minLen uint32, nbitsDelta uint16, rest ...byte) []byte {
	be32 := func(v uint32) []byte { return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)} }
	be16 := func(v uint16) []byte { return []byte{byte(v >> 8), byte(v)} }

	var b []byte
	b = append(b, be32(firstObj)...)
	b = append(b, be32(firstOff)...)
	b = append(b, be32(nFirstPage)...)
	b = append(b, be32(nTotal)...)
	b = append(b, be16(nbitsNObjects)...)
	b = append(b, be32(minLen)...)
	b = append(b, be16(nbitsDelta)...)
	return append(b, rest...)
}

// ⛔⛔ THE SIGNATURE PATH, WHICH NO FILE IN THE CORPUS EXERCISES. Table F.6 item
// 2 is a one-bit flag and item 3 is the 16-byte MD5 it guards; every fixture
// here leaves the flag clear, so this is the only thing that reads item 3 at
// all. A synthetic witness is weaker than a real file and far better than
// none: a reader that always consumed 128 bits there, or never did, would
// agree with every fixture and be sixteen bytes out of step on the first
// signed group it met.
func TestASharedGroupsSignatureIsReadWhenItsFlagIsSet(t *testing.T) {
	sig := []byte{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	}
	// One group; no bits for its length or object count, so the only bits
	// after the header are the flag byte and the signature.
	body := append([]byte{0x80}, sig...)
	b := sharedTableBytes(0, 0, 1, 1, 0, 0, 0, body...)

	_, groups, err := readSharedTable(&bits{b: b}, 0, 100)
	if err != nil {
		t.Fatalf("a signed group was refused: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("read %d group(s), want 1", len(groups))
	}
	if got := groups[0].Signature; !bytes.Equal(got, sig) {
		t.Errorf("signature = % x, want % x", got, sig)
	}
	// ⛔ AND ITEM 4's +1: the stored count is 0 bits wide and therefore 0, and
	// F.4.2 calls item 4 "1 less than the number of objects in the group", so
	// the group holds ONE object. A reader that kept the raw value would
	// report every group as holding one too few.
	if got := groups[0].NObjects; got != 1 {
		t.Errorf("NObjects = %d, want 1: item 4 is one LESS than the count", got)
	}
}

// ⛔ AND AN UNSIGNED GROUP KEEPS NO SIGNATURE, which is the control: without it
// the case above would hold against a reader that invented sixteen bytes for
// every group.
func TestAnUnsignedSharedGroupHasNoSignature(t *testing.T) {
	b := sharedTableBytes(0, 0, 1, 1, 0, 0, 0, 0x00)
	_, groups, err := readSharedTable(&bits{b: b}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if groups[0].Signature != nil {
		t.Errorf("an unsigned group carries % x", groups[0].Signature)
	}
}

// ⛔⛔ EVERY WAY THE SHARED OBJECT TABLE CAN BE REFUSED, each named for what is
// wrong. The table is Required by Table F.2, so a reader that fell back to
// "no shared objects" on a broken one would quietly report a document as
// having none — which is indistinguishable from a conforming single-page file.
func TestEveryWayTheSharedObjectTableIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		b    []byte
		at   int
		want string
	}{
		{
			name: "/S points outside the stream",
			b:    make([]byte, 24),
			at:   99,
			want: "a table was placed at byte 99",
		},
		{
			name: "the header is short of Table F.5's 24 bytes",
			b:    make([]byte, 23),
			want: "Table F.5, item 7 of 7",
		},
		{
			name: "nbits_nobjects is not a width",
			b:    sharedTableBytes(0, 0, 1, 1, 65535, 0, 0),
			want: "nbits_nobjects is 65535",
		},
		{
			name: "nbits_delta_group_length is not a width",
			b:    sharedTableBytes(0, 0, 1, 1, 0, 0, 65535),
			want: "nbits_delta_group_length is 65535",
		},
		{
			// ⛔ Table F.5 item 4 INCLUDES item 3, so a total below the
			// first-page count is a contradiction rather than a small number.
			name: "the total is below the first page's count",
			b:    sharedTableBytes(0, 0, 5, 2, 0, 0, 0),
			want: "item 4 includes item 3",
		},
		{
			name: "more groups than the table has bits for",
			b:    sharedTableBytes(0, 0, 0, 255, 0, 0, 8),
			want: "255 shared object group(s)",
		},
		{
			// ⛔ One byte beyond the header, and an eight-bit length delta:
			// the bound passes (one group needs one bit of flag and eight
			// remain) and group (a) then wants eight bits for the length and
			// a ninth for the flag. That the bound counts only item 2's bit
			// is what leaves this reachable.
			name: "a group length runs out",
			b:    sharedTableBytes(0, 0, 1, 1, 0, 0, 16, 0x00),
			want: "Table F.6 item 1, the group length",
		},
		{
			// ⛔ A one-bit length delta over one group: (a) reads one bit and
			// ALIGNS to the next byte, leaving nothing for the flag. The
			// alignment is why this differs from the case above.
			name: "a signature flag runs out",
			b:    sharedTableBytes(0, 0, 1, 1, 0, 0, 1, 0x00),
			want: "Table F.6 item 2, the signature flag",
		},
		{
			name: "a signature runs out",
			b:    sharedTableBytes(0, 0, 1, 1, 0, 0, 0, 0x80, 0x01, 0x02),
			want: "Table F.6 item 3, the signature",
		},
		{
			name: "an object count runs out",
			b:    sharedTableBytes(0, 0, 1, 1, 8, 0, 0, 0x00),
			want: "Table F.6 item 4, the object count",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := readSharedTable(&bits{b: c.b}, c.at, 1000)
			if !errors.Is(err, errHintTable) {
				t.Fatalf("it was accepted (or refused oddly): %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("it was refused, but the message does not say %q: %v", c.want, err)
			}
		})
	}
}

// ⛔⛔⛔ ONE BIT OF INPUT MUST NOT BECOME A 32-BYTE STRUCT, which review found by
// MEASURING rather than reading: the bit bound is satisfied by 16.7 million
// groups in 2 MiB of zeros, and allocating them cost 656 MiB — a 256-fold
// amplification that a 1 GiB decoded stream takes into the hundreds of
// gigabytes. A trivial deflate bomb reaches that.
//
// ⛔ A BIT BOUND CANNOT CATCH IT, because the input is not lying: 2 MiB really
// does hold 16.7 million one-bit flags. What is absurd is the CLAIM — F.4.2's
// groups describe "a group of adjacent objects", so a table naming more groups
// than the document has objects is describing objects the file does not have.
//
// ⚠ The numbers here are small on purpose: the defect is the RATIO, and
// asserting a refusal needs no 2 MiB fixture. The real measurement is in the
// comment where it can be read, rather than in a test that costs a second.
func TestATableClaimingMoreGroupsThanTheDocumentHasObjectsIsRefused(t *testing.T) {
	// Sixty-four bytes hold 512 bits, so 200 one-bit flags fit and the bit
	// bound passes. A document of five objects cannot have 200 groups.
	b := sharedTableBytes(0, 0, 200, 200, 0, 0, 0, make([]byte, 40)...)
	_, _, err := readSharedTable(&bits{b: b}, 0, 5)
	if !errors.Is(err, errHintTable) {
		t.Fatalf("200 groups in a 5-object document were accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "the document has 5 object(s)") {
		t.Errorf("it was refused, but not against the document's object count: %v", err)
	}

	// ⛔ THE CONTROL: the same table in a document large enough to hold those
	// groups passes the cap, so the refusal is about the ratio and not about
	// the table. It then fails further on for want of bits, which is the other
	// bound doing its job.
	_, _, err = readSharedTable(&bits{b: b}, 0, 1000)
	if err != nil && strings.Contains(err.Error(), "the document has") {
		t.Errorf("a document of 1000 objects was still refused by the object cap: %v", err)
	}
}

// ⛔⛔⛔ F.4.2's "TWO SEQUENCES" ARE ONE COLUMN-MAJOR RUN, AND THIS IS THE TEST
// THAT SETTLED IT. The clause says there are two sequences of group entries —
// first-page objects, then the shared objects section — and then gives the
// item order "in EACH SEQUENCE". Read strictly, each would be column-major and
// byte-aligned on its own, which differs from one run of nshared_total entries
// whenever the first sequence's widths do not end on a byte.
//
// ⛔ The other committed fixtures all have nshared_total == nshared_first_page,
// so their second sequence is empty and BOTH readings agree on every one of
// them. testdata/linearized-shared.pdf has a non-empty part 8 and the two
// readings then disagree:
//
//	min_group_length 99, nbits_delta_group_length 9, lengths 108, 99, 601
//	bits after the 24-byte header: 00000100 10000000 00111110 11000000
//	ONE RUN:       bits 0-8 = 9, 9-17 = 0, 18-26 = 111110110 = 502  -> 601  ✓
//	TWO SEQUENCES: would align to bit 24 and read 110000000 = 384   -> 483  ✗
//
// ⭐ 601 came OUT OF THE DECODE and was not chosen to fit, which is what makes
// this evidence rather than arithmetic. An earlier version of readSharedTable
// REFUSED a non-empty second sequence because it could not justify either
// reading; the refusal is gone because the question turned out to be
// answerable with a file nobody had built.
//
// ⛔⛔ THE FIXTURE IS COMMITTED BYTES AND NOT GENERATED AT TEST TIME, which is
// the difference between pinning a FORMAT question and tracking a TOOL. The
// twelve generated shapes exist to check agreement with whatever qpdf is
// installed; this file exists to hold one answer still, so a qpdf upgrade that
// changed its output cannot silently change what is measured here.
//
// ⛔ ITS PROVENANCE, so it can be remade rather than reverse-engineered: a
// three-page document written by this package's own writer, where pages 2 and
// 3 point at ONE /Resources dictionary of eight Helvetica-BoldOblique fonts
// and page 1 has its own empty one — F.3.9's definition of a part 8 object —
// then `qpdf --linearize`, qpdf 12.4.2. The dictionary is deliberately FAT:
// with a small one its group length IS min_group_length, so its delta is zero
// and the padding bits are zero too, and both readings produce IDENTICAL
// numbers. The first attempt at this fixture had a non-empty part 8 and still
// settled nothing for exactly that reason.
func TestTheSecondSequenceOfSharedGroupsIsOneRunWithTheFirst(t *testing.T) {
	qpdf := qpdfOrSkip(t)

	const name = "linearized-shared.pdf"
	path := filepath.Join("testdata", name)
	linear, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	d, err := Open(linear)
	if err != nil {
		t.Fatal(err)
	}
	lin, err := Linearized(linear)
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.readHintTables(lin)
	if err != nil {
		t.Fatalf("a file with a non-empty part 8 does not read: %v", err)
	}

	// ⛔ THE FIXTURE'S OWN PREMISE, ASSERTED: without a second sequence this
	// test is the same as the ones above it and proves nothing new. If a
	// future edit flattens the fixture, this says so rather than passing.
	h := got.shared
	if h.NSharedTotal <= h.NSharedFirstPage {
		t.Fatalf("nshared_total %d and nshared_first_page %d: %s has an EMPTY second "+
			"sequence, so it cannot distinguish the two readings",
			h.NSharedTotal, h.NSharedFirstPage, name)
	}

	_, lengths := qpdfSharedTable(t, qpdf, path)
	if len(got.sharedGroups) != len(lengths) {
		t.Fatalf("read %d group(s), qpdf prints %d", len(got.sharedGroups), len(lengths))
	}
	// ⛔ AND THE LAST GROUP IS THE ONE THAT DECIDES: it lives in the second
	// sequence, and the two readings give it different bits.
	for i, g := range got.sharedGroups {
		if g.Length != lengths[i] {
			t.Errorf("group %d: length = %d, qpdf says %d — a group in the second sequence "+
				"disagreeing means the one-run reading is wrong", i, g.Length, lengths[i])
		}
	}
}

// ⛔ AND THE TWO WAYS /S CAN FAIL TO LOCATE THE TABLE AT ALL. checkHintStream
// already requires /S to be a direct integer, so these are reached only by a
// caller that skipped it — which is exactly why they are refusals rather than
// assumptions.
func TestAHintStreamWhoseSlashSCannotLocateTheTableIsRefused(t *testing.T) {
	const head = "%PDF-1.7\n"
	// ⛔ bytes is the stream's length, 36 (Table F.3's header exactly) unless a
	// case needs room past it.
	for _, c := range []struct {
		name, body, want string
		bytes            int
	}{
		{
			name: "no /S at all",
			body: "<< /Length 36 >>",
			want: "has no /S",
		},
		{
			name: "an /S that is not an integer",
			body: "<< /S /x /Length 36 >>",
			want: "not a direct integer",
		},
		{
			// ⛔ The page offset table reads and the SHARED one does not, so
			// the failure has to come out of readHintTables rather than be
			// swallowed: a thirty-six byte stream is the header exactly, and
			// /S 99 is past it.
			//
			// ⛔ THE MESSAGE MOVED, deliberately. This used to be caught
			// downstream by seekByte ("a table was placed at byte 99"); /S is
			// now bounded against the stream before it is used in arithmetic
			// at all, so the refusal names /S itself. The earlier wording was
			// the symptom of a check that had not happened yet.
			name: "an /S past the end of the stream",
			body: "<< /S 99 /Length 36 >>",
			want: "/S is 99 and the decoded hint stream holds 36 byte(s)",
		},
		{
			// ⛔⛔ AN /S THAT OVERFLOWS THE OVERRUN CHECK'S OWN ARITHMETIC,
			// which is the THIRD defect of this class in this file (#42, #53,
			// and the overrun check as first written). 2^61+40 times eight
			// wraps to 320, so `r.pos > 8*int64(pos)` compared the entries'
			// end against 320 and did not fire; seekByte then caught it by
			// luck, and on a 32-bit build int(pos) keeps the low bits — 40 —
			// which seekByte can ACCEPT.
			//
			// ⛔ The number is written out rather than computed, so the case
			// cannot drift with the expression it defends.
			name: "an /S whose bit offset overflows int64",
			body: "<< /S 2305843009213693992 /Length 36 >>",
			want: "the decoded hint stream holds 36 byte(s)",
		},
		{
			// ⛔ And the other side of the same bound: a negative /S would
			// index before the stream.
			name: "a negative /S",
			body: "<< /S -8 /Length 36 >>",
			want: "/S is -8",
		},
		{
			// ⛔⛔ AN /S INSIDE THE STREAM WHOSE TABLE IS STILL BAD, which is
			// the only way left to reach readSharedTable's failure from here.
			// The bound added above catches /S 99 before readSharedTable runs
			// at all, so that case no longer witnesses this propagation — and
			// an error returned by a function nothing can make fail is an
			// error nobody has checked.
			//
			// Forty bytes: the page offset header takes the first thirty-six
			// and every width in it is zero, so one page's entries consume no
			// bits and the table ends exactly at /S = 36. Table F.5's header
			// then wants twenty-four bytes and four remain.
			name:  "an /S inside the stream with no room for the shared header",
			body:  "<< /S 36 /Length 40 >>",
			want:  "Table F.5",
			bytes: 40,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			n := c.bytes
			if n == 0 {
				n = 36
			}
			d := bareDoc(nil, nil, nil)
			d.buf = []byte(head + "9 0 obj\n" + c.body + "\nstream\n" +
				strings.Repeat("\x00", n) + "\nendstream\nendobj\n")
			_, err := d.readHintTables(&Linearization{HintOffset: len(head), PageCount: 1})
			if !errors.Is(err, errHintTable) {
				t.Fatalf("it was accepted: %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("it was refused, but not for %q: %v", c.want, err)
			}
		})
	}
}

// qpdfSharedTable reads the shared object hint table's header fields and each
// group's length out of --show-linearization.
//
// ⛔ IT READS ONLY FROM THE SECTION HEADING ONWARDS, because the page offset
// table above it prints a field named nbits_nobjects too — a parser taking the
// whole output would compare one table's width against the other's.
func qpdfSharedTable(t *testing.T, qpdf, path string) (map[string]uint32, []uint32) {
	t.Helper()

	cmd := exec.Command(qpdf, "--show-linearization", path)
	cmd.Stdin = nil
	out, _ := cmd.CombinedOutput()
	text := string(out)

	const start = "Shared Objects Hint Table"
	from := strings.Index(text, start)
	if from < 0 {
		t.Fatalf("qpdf printed no %q section:\n%s", start, text)
	}
	section := text[from+len(start):]
	if to := strings.Index(section, "Computed Linearization Part Assignments"); to >= 0 {
		section = section[:to]
	}

	fields := map[string]uint32{}
	var lengths []uint32
	inGroup := false
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Shared Object ") && strings.HasSuffix(trimmed, ":") {
			inGroup = true
			continue
		}
		name, value, ok := strings.Cut(trimmed, ": ")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
		if err != nil {
			continue
		}
		if inGroup {
			if name == "group length" {
				lengths = append(lengths, uint32(n))
			}
			continue
		}
		fields[name] = uint32(n)
	}
	// ⛔ SEVEN, ASSERTED, for the reason the page offset header's thirteen are:
	// a parser that matched nothing hands back an empty map and every lookup
	// misses, which reads as seven separate "compared against nothing" errors
	// rather than naming the cause once.
	if len(fields) != 7 {
		t.Fatalf("read %d header field(s) from qpdf's shared object table, want Table F.5's "+
			"seven: %v\n%s", len(fields), fields, text)
	}
	if len(lengths) == 0 {
		t.Fatalf("qpdf printed no group lengths, so the group comparison would be vacuous:\n%s", text)
	}
	return fields, lengths
}

// qpdfPage is one page as qpdf prints it.
type qpdfPage struct {
	nobjects, length, contentOffset, contentLength, nshared uint32
	identifiers, numerators                                 []uint32
}

// qpdfPageEntries reads qpdf's per-page block out of --show-linearization.
//
// ⛔ IT ASSERTS IT FOUND PAGES, because an empty slice compares equal to an
// empty slice and would read as agreement — the shape "a scan that could not
// read reported zero" is named for.
func qpdfPageEntries(t *testing.T, qpdf, path string) []qpdfPage {
	t.Helper()

	cmd := exec.Command(qpdf, "--show-linearization", path)
	cmd.Stdin = nil
	out, _ := cmd.CombinedOutput()
	text := string(out)

	const stop = "Shared Objects Hint Table"
	section := text
	if to := strings.Index(section, stop); to >= 0 {
		section = section[:to]
	} else {
		t.Fatalf("qpdf printed no %q, so the page entries have no end:\n%s", stop, text)
	}

	var pages []qpdfPage
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Page ") && strings.HasSuffix(trimmed, ":") {
			pages = append(pages, qpdfPage{})
			continue
		}
		if len(pages) == 0 {
			continue
		}
		name, value, ok := strings.Cut(trimmed, ": ")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 32)
		if err != nil {
			continue
		}
		p := &pages[len(pages)-1]
		switch {
		case name == "nobjects":
			p.nobjects = uint32(n)
		case name == "length":
			p.length = uint32(n)
		case name == "content_offset":
			p.contentOffset = uint32(n)
		case name == "content_length":
			p.contentLength = uint32(n)
		case name == "nshared_objects":
			p.nshared = uint32(n)
		case strings.HasPrefix(name, "identifier "):
			p.identifiers = append(p.identifiers, uint32(n))
		case strings.HasPrefix(name, "numerator "):
			p.numerators = append(p.numerators, uint32(n))
		}
	}
	if len(pages) == 0 {
		t.Fatalf("qpdf printed no page entries, so the comparison would be vacuous:\n%s", text)
	}
	return pages
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
// readHeaderOnly decodes just Table F.3's header out of a file's hint stream,
// so that "the header reads" can be asserted about a file whose ENTRIES are
// refused.
//
// ⛔ It exists because readHintTables is all-or-nothing by design: a caller
// wants both tables or neither. Taking the header alone is a test's business,
// not an API's, which is why this lives here rather than in the package.
func readHeaderOnly(t *testing.T, d *Document, lin *Linearization) (*pageOffsetHeader, error) {
	t.Helper()
	_, o, _, err := ParseIndirectObject(d.buf[lin.HintOffset:], d.Get)
	if err != nil {
		return nil, err
	}
	s, ok := o.(*Stream)
	if !ok {
		return nil, errHintTable
	}
	data, _, err := d.DecodeStream(s)
	if err != nil {
		return nil, err
	}
	return readPageOffsetHeader(&bits{b: data})
}

// ⛔⛔ AND ITS PER-PAGE ENTRIES OVERRUN ITS OWN /S, WHICH IS A SIXTH THING WRONG
// WITH THAT FILE AND WAS FOUND BY THE ENCODER. qpdf warns about five things on
// linearized-gs.pdf; this is not among them. Its /S is 38 — bit 304 — and its
// per-page entries end at bit 330, so 26 bits of what this package read as
// page lengths were really the shared object table's bytes.
//
// ⭐ EVERY TEST THAT COMPARED NUMBERS WAS BLIND TO IT. The header's thirteen
// fields matched qpdf, the entries parsed, and nothing said the table had run
// past where /S puts the next one. The encoder could not reproduce those bytes,
// which is what surfaced it — a stronger oracle finding what a weaker one
// cannot, rather than a new idea about the format.
func TestTheGhostscriptFixturesEntriesOverrunItsOwnSharedTable(t *testing.T) {
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
	_, err = d.readHintTables(lin)
	if !errors.Is(err, errHintTable) {
		t.Fatalf("the Ghostscript fixture's tables were accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "really the next table") {
		t.Errorf("it was refused, but not for the entries overrunning /S: %v", err)
	}

	// ⛔ AND THE HEADER ITSELF STILL READS, pinned separately so that "qpdf
	// warns about this file" and "nothing in it can be decoded" stay
	// different claims. The thirteen header fields are sound; what is wrong
	// is where the entries end.
	tables, err := readHeaderOnly(t, d, lin)
	if err != nil {
		t.Fatalf("the Ghostscript fixture's page offset HEADER does not read, which is a "+
			"stronger claim than the overrun supports: %v", err)
	}
	// Measured from qpdf --show-linearization on that fixture: it reports
	// min_nobjects 3 and first_page_offset 577.
	if got := tables.MinNObjects; got != 3 {
		t.Errorf("min_nobjects = %d, qpdf reads 3", got)
	}
	if got := tables.FirstPageOffset; got != 577 {
		t.Errorf("first_page_offset = %d, qpdf reads 577", got)
	}

	// ⛔⛔ AND THIS FIXTURE IS THE CONTROL FOR THE HINT-STREAM ADJUSTMENT, which
	// no qpdf file in the corpus can be. Ghostscript places /H AFTER the first
	// page (/H = /E = 829), so the stored offset needs NO adjustment and qpdf
	// reports it unchanged at 577. On every qpdf fixture the hint stream comes
	// first and the length IS added. Adding it unconditionally would have
	// agreed with all twelve qpdf shapes and been wrong here.
	got, err := tables.firstPageOffsetInFile(lin)
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
		{
			// ⛔ THE HEADER READS AND THE ENTRIES DO NOT, which is a different
			// failure from a short stream and must come out of readHintTables
			// rather than be swallowed: thirty-six bytes is exactly Table
			// F.3's header, so the first stage succeeds and the second has
			// nothing. This fixture's /N is 0, which readPageEntries refuses.
			//
			// ⛔ THE FILLER IS NUL AND NOT '0'. The first version used ASCII
			// zeros, and 0x3030 is 12336 — so the width check refused the
			// table before the entries were ever reached, and the test was
			// measuring the wrong refusal. An all-zero header is the one whose
			// thirteen items are all valid.
			name: "the header reads but there are no entries behind it",
			buf: "%PDF-1.7\n9 0 obj\n<< /Length 36 >>\nstream\n" +
				strings.Repeat("\x00", 36) + "\nendstream\nendobj\n",
			want: "hint table for 0 pages",
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

// entriesHeader is a header whose every width is 8 bits, so that one page's
// item is exactly one byte and a truncation can be aimed at a chosen group.
//
// ⛔ The widths are all equal ON PURPOSE. With real widths the group boundaries
// land mid-byte and a test aiming at group (e) would be aiming at arithmetic
// rather than at a group.
func entriesHeader() *pageOffsetHeader {
	return &pageOffsetHeader{
		NBitsDeltaNObjects:   8,
		NBitsDeltaPageLength: 8,
		NBitsNSharedObjects:  8,
		NBitsSharedIdent:     8,
		NBitsSharedNumerator: 8,
		NBitsDeltaContentOff: 8,
		NBitsDeltaContentLen: 8,
	}
}

// ⛔⛔ A TRUNCATION IN EVERY ONE OF F.4.1'S SEVEN GROUPS, each refused with the
// item that ran out. The item number is the whole value of the message: told
// only "the hint table cannot be read", nobody can tell a truncated table from
// a decoder whose widths are wrong — and those are the two things that go wrong
// here.
//
// ⛔ The layout for two pages, all widths 8 bits: group (a) takes bytes 0-1,
// (b) 2-3, (c) 4-5, (d) 6, (e) 7, (f) 8-9, (g) 10-11. The counts in byte 4 and
// 5 are 0 and 1, so page two has one shared reference and groups (d) and (e)
// are one byte each.
func TestATruncationInEveryPerPageGroupNamesItsItem(t *testing.T) {
	full := []byte{
		0, 0, // (a) object count deltas
		0, 0, // (b) page length deltas
		0, 1, // (c) shared counts: the first page MUST be 0
		0,    // (d) page two's one identifier
		0,    // (e) page two's one numerator
		0, 0, // (f) content offset deltas
		0, 0, // (g) content length deltas
	}
	// The control: the whole thing reads.
	if _, err := readPageEntries(&bits{b: full}, entriesHeader(), 2); err != nil {
		t.Fatalf("the untruncated table does not read, so no case below is about its cut: %v", err)
	}

	for _, c := range []struct {
		name string
		keep int
		want string
	}{
		{"group (a), the object count", 1, "item 1, the object count"},
		{"group (b), the page length", 3, "item 2, the page length"},
		{"group (c), the shared count", 5, "item 3, the shared object count"},
		{"groups (d) and (e), the references", 6, "shared object reference(s)"},
		{"group (f), the content offset", 9, "item 6, the content stream offset"},
		{"group (g), the content length", 11, "item 7, the content stream length"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := readPageEntries(&bits{b: full[:c.keep]}, entriesHeader(), 2)
			if !errors.Is(err, errHintTable) {
				t.Fatalf("a table cut to %d byte(s) was accepted: %v", c.keep, err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("it was refused, but not naming %q: %v", c.want, err)
			}
		})
	}
}

// ⛔⛔ AND THE FIRST PAGE'S SHARED COUNT MUST BE ZERO, which Table F.4 item 3
// states — "For the first page, this number shall be 0; the next two items
// start with the second page". A non-zero one is not merely non-conforming: it
// would make groups (d) and (e) consume bits belonging to the pages after it,
// so every later value would be wrong while the table still parsed.
func TestANonZeroSharedCountOnTheFirstPageIsRefused(t *testing.T) {
	b := []byte{0, 0, 0, 0, 3, 1, 0, 0, 0, 0, 0, 0}
	_, err := readPageEntries(&bits{b: b}, entriesHeader(), 2)
	if !errors.Is(err, errHintTable) {
		t.Fatalf("a first page claiming shared objects was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "the first page claims 3 shared object(s)") {
		t.Errorf("it was refused, but without naming the count: %v", err)
	}
}

// ⛔⛔ AND A COUNT THAT CANNOT FIT IS REFUSED BEFORE ANY OF IT IS READ, which is
// what makes discarding the errors in groups (d) and (e) sound.
// nbits_nshared_objects can be 32, so a page can claim four billion references
// and each costs two reads; without this bound a twelve-byte table could ask
// for billions of iterations and be refused only afterwards.
func TestSharedReferencesThatCannotFitAreRefusedBeforeBeingRead(t *testing.T) {
	// ⛔ THERE ARE TWO BOUNDS AND THEY CATCH DIFFERENT LIES, so each gets a
	// case. The first caps the COUNT against the bits remaining, which is the
	// only thing that stops a four-billion count whose fields are ZERO bits
	// wide — a hole the shared object table's tests exposed in the original,
	// which bounded the bit cost alone and so passed anything when that cost
	// was nought. The second bounds the bit cost, for counts small enough to
	// be plausible and still too expensive.
	for _, c := range []struct {
		name  string
		count byte
		want  string
	}{
		{"a count larger than the bits that remain", 255, "describe nothing"},
		{"a count that is plausible and still too expensive", 6, "bits, and"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := []byte{0, 0, 0, 0, 0, c.count, 0, 0, 0, 0, 0, 0}
			_, err := readPageEntries(&bits{b: b}, entriesHeader(), 2)
			if !errors.Is(err, errHintTable) {
				t.Fatalf("%d references in a 12-byte table were accepted: %v", c.count, err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("it was refused, but not by the bound this case is about (%q): %v",
					c.want, err)
			}
		})
	}
}

// ⛔⛔⛔ THE COUNTEREXAMPLE TO A PROOF THIS FILE ONCE CARRIED, which review ran
// and which decoded SILENTLY with a fabricated value.
//
// An earlier version discarded the read errors in groups (d) and (e), on the
// stated ground that the count bound above proved the bits were there. The
// proof forgot the r.align() BETWEEN those two groups: real consumption is
// ceil(total*identBits/8)*8 + total*numeratorBits, up to seven bits more than
// the bound allows.
//
// ⛔ With one-bit widths for the count, the identifier and the numerator, two
// pages, and the two bytes 0x40 0x80: the counts read as 0 and 1, the bound
// passes (two bits needed, eight remain), group (d) reads one bit and then
// ALIGNS TO THE NEXT BYTE, and group (e) has nothing left. The old code
// returned err == nil with Numerator 0 — a number no byte of the file ever
// held.
//
// ⭐ The lesson is not "check the arithmetic again". It is that a justification
// for discarding an error is worth less than the returned error, because the
// justification is the thing that silently stops being true.
func TestTheTruncationThatOnceDecodedSilently(t *testing.T) {
	h := &pageOffsetHeader{
		NBitsDeltaNObjects:   0,
		NBitsDeltaPageLength: 0,
		NBitsNSharedObjects:  1,
		NBitsSharedIdent:     1,
		NBitsSharedNumerator: 1,
		NBitsDeltaContentOff: 0,
		NBitsDeltaContentLen: 0,
	}
	// 0x40 = 0100_0000: the two count bits are 0 (first page) and 1 (second).
	_, err := readPageEntries(&bits{b: []byte{0x40, 0x80}}, h, 2)
	if err == nil {
		t.Fatal("it decoded, which is the defect: group (e) has no bits and the numerator " +
			"would be invented")
	}
	if !errors.Is(err, errHintTable) {
		t.Fatalf("it failed, but not as a hint table refusal: %v", err)
	}
	if !strings.Contains(err.Error(), "item 5, a fractional position") {
		t.Errorf("it was refused, but not at group (e), which is where the bits run out: %v", err)
	}
}

// ⛔⛔ AND GROUP (d)'s OWN READ ERROR, which needs a header the uniform-width
// fixture cannot produce. The bound is measured BEFORE group (c)'s alignment,
// so it over-estimates what remains by the slack; with every width a multiple
// of eight there is no slack and the bound always fires first. Reaching (d)'s
// error therefore takes a count width that leaves group (c) mid-byte.
//
// ⛔ Three-bit counts over two pages consume six bits, so two bits of slack.
// The buffer is two bytes: after the counts, ten bits remain by the bound's
// reckoning and only eight after the alignment. One nine-bit identifier passes
// the bound (9 <= 10) and then cannot be read (9 > 8).
//
// ⭐ That this case exists at all is the argument for the bound being
// conservative rather than exact: an exact bound makes this line unreachable,
// and an unreachable line is one claiming to be tested that is not.
func TestAnIdentifierThatTheAlignmentLeavesNoRoomForIsRefused(t *testing.T) {
	h := &pageOffsetHeader{
		NBitsNSharedObjects: 3,
		NBitsSharedIdent:    9,
	}
	// 0x04 = 000_001_00: page one's count is 0, page two's is 1, then slack.
	_, err := readPageEntries(&bits{b: []byte{0x04, 0x00}}, h, 2)
	if !errors.Is(err, errHintTable) {
		t.Fatalf("an identifier with no room was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "item 4, a shared object identifier") {
		t.Errorf("it was refused, but not at group (d): %v", err)
	}
}

// ⛔ A hint table for no pages is refused rather than returning an empty
// slice: /N is checked against the document before this runs, so zero pages
// here means the caller is wrong, and an empty result would be mistaken for a
// table that holds nothing.
func TestAHintTableForNoPagesIsRefused(t *testing.T) {
	for _, pages := range []int{0, -1} {
		_, err := readPageEntries(&bits{b: make([]byte, 12)}, entriesHeader(), pages)
		if !errors.Is(err, errHintTable) {
			t.Fatalf("a table for %d pages was accepted: %v", pages, err)
		}
		if !strings.Contains(err.Error(), "pages") {
			t.Errorf("it was refused, but not for the page count: %v", err)
		}
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

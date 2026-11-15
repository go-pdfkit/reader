// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ⛔⛔ THE STRONGEST ORACLE IN THIS ARC: RE-ENCODE QPDF'S OWN TABLES AND COMPARE
// BYTES. Every test before this one compared NUMBERS against what qpdf prints,
// which cannot see the layout between them — the padding, the group order, the
// widths of the fields the numbers sit in. Reproducing qpdf's bytes exactly
// from the values decoded out of them can.
//
// ⛔ It is also the equality oracle the writer needs. #40 orders the work
// "port the reference exactly (it is the equality oracle) → measure →
// optimise", and for a bit-packed format the reference is the bytes a
// reference implementation emits.
//
// ⛔⛔ BUT IT IS SILENT ABOUT THREE OF THE ELEVEN LETTERED GROUPS, and an
// earlier version of this comment claimed it proved "the whole layout". It does
// not. Measured over all fourteen shapes below, qpdf writes:
//
//	Table F.3 item 7  (nbits_delta_content_offset) = 0
//	Table F.3 item 12 (nbits_shared_numerator)     = 0
//	Table F.5 item 5  (nbits_nobjects)             = 0, every NObjects = 1
//
// So F.4's groups (e) and (f) and F.6's group (d) write ZERO BITS on every
// file in the corpus, their pads are unobservable, and Table F.6 item 4's
// "1 less than the number of objects" has no byte witness at all. Swapping (e)
// with (f), or deleting either of their align() calls, leaves this test green.
//
// ⭐ So the synthetic cases below are not decoration: they carry the three
// groups the corpus cannot reach, with their bytes spelled out from the
// annex's item sizes. See TestTheGroupsQpdfLeavesEmptyAreLaidOutFromTheAnnex.
//
// ⚠ What this does NOT prove either: that the VALUES are right for a document
// this package laid out itself. That is the layout and the fixpoint, still to
// come. This proves the serialisation of the groups it can see.
func TestReEncodingQpdfsHintTablesReproducesItsBytes(t *testing.T) {
	qpdfOrSkip(t)

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
				assertHintTablesRoundTrip(t, linear)
			})
		}
	}

	// ⛔ AND THE COMMITTED FIXTURES, which the generated shapes cannot stand in
	// for:
	//
	//   - linearized-shared.pdf is the only file with a non-empty second
	//     sequence of shared object groups. If the encoder's group order were
	//     wrong, that is the file that would show it.
	//   - linearized.pdf is qpdf's own committed output, so a change to the
	//     generated shapes cannot quietly take the whole comparison with it.
	//
	// ⛔⛔ AND linearized-gs.pdf IS NOT HERE, WHICH TRYING TO ADD IT EXPLAINED.
	// It is the one DIFFERENT PRODUCER in the corpus, so it looked like the
	// most valuable case — and it failed, because its per-page entries OVERRUN
	// /S by 26 bits and so are partly the shared object table's bytes. There
	// is nothing for an encoder to reproduce: the file does not hold a page
	// offset table that ends where it says it does. readHintTables refuses it
	// now, and TestTheGhostscriptFixturesEntriesOverrunItsOwnSharedTable is
	// where that is pinned. Finding this is what the byte oracle bought.
	for _, name := range []string{"linearized.pdf", "linearized-shared.pdf"} {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			assertHintTablesRoundTrip(t, b)
		})
	}
}

// assertHintTablesRoundTrip decodes a file's hint stream, re-encodes both
// tables from what came out, and requires the bytes back.
//
// ⛔ THE COMPARISON IS AGAINST THE DECODED STREAM'S OWN BYTES, sliced at the
// extents the tables occupy: the page offset table from 0 to /S, and the
// shared object table from /S onwards. Comparing whole streams would fail on
// anything a producer appended after the required tables — F.4's optional
// thumbnail, outline and generic tables — which is a difference in the FILE
// and not in this encoder.
func assertHintTablesRoundTrip(t *testing.T, linear []byte) {
	t.Helper()

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
		t.Fatalf("the hint tables do not read, so nothing can be re-encoded: %v", err)
	}

	_, o, _, err := ParseIndirectObject(d.buf[lin.HintOffset:], d.Get)
	if err != nil {
		t.Fatal(err)
	}
	s := o.(*Stream)
	data, _, err := d.DecodeStream(s)
	if err != nil {
		t.Fatal(err)
	}
	sv, ok := s.Dict["S"].(Integer)
	if !ok {
		t.Fatalf("/S is %T, not an integer", s.Dict["S"])
	}
	at := int(sv)

	gotPages, err := writePageOffsetTable(tables.pageOffset, tables.pages)
	if err != nil {
		t.Fatalf("the page offset table does not re-encode: %v", err)
	}
	// ⛔ The page offset table runs from byte 0 to /S, which F.3.6 and F.4.2
	// fix: it "shall be the first table in the stream and shall start at
	// offset 0", and /S names where the next one begins.
	if want := data[:at]; !bytes.Equal(gotPages, want) {
		t.Errorf("the re-encoded page offset table differs from qpdf's %d bytes:\n"+
			" got % x\nwant % x", at, gotPages, want)
	}

	gotShared, err := writeSharedTable(tables.shared, tables.sharedGroups)
	if err != nil {
		t.Fatalf("the shared object table does not re-encode: %v", err)
	}
	// ⛔⛔ AND ITS LENGTH IS ASSERTED, NOT JUST ITS PREFIX. An earlier version
	// compared data[at:at+len(gotShared)], which a SHORT encoding passes: drop
	// a trailing group and the prefix still matches. The shared object table is
	// the last thing F.3.6 requires in the primary stream, and on every shape
	// here it runs to the end of the decoded data, so the remainder is the
	// exact expectation.
	//
	// ⚠ If a future fixture carries one of F.4's OPTIONAL tables after it this
	// will fail, and that is the right outcome: it would be telling me the
	// stream holds something this package does not yet account for, which is a
	// fact worth a failing test rather than a silent allowance.
	if got, want := len(gotShared), len(data)-at; got != want {
		t.Fatalf("the re-encoded shared table is %d bytes and %d remain after /S=%d",
			got, want, at)
	}
	if want := data[at:]; !bytes.Equal(gotShared, want) {
		t.Errorf("the re-encoded shared object table differs from qpdf's:\n got % x\nwant % x",
			gotShared, want)
	}
}

// ⛔⛔ AND THE BIT WRITER IS TESTED AS A BIT WRITER, against the reader it has
// to invert. Every byte the encoder emits comes out of it, and an off-by-one
// would shift whole tables while still producing something that parses — the
// failure with no symptom, which is why the reader has the same test.
func TestTheBitWriterIsTheReadersInverse(t *testing.T) {
	for _, c := range []struct {
		name  string
		write []struct {
			n int
			v uint32
		}
		want []byte
	}{
		{
			name: "whole bytes",
			write: []struct {
				n int
				v uint32
			}{{8, 0xA5}, {8, 0x3C}},
			want: []byte{0xA5, 0x3C},
		},
		{
			name: "a field straddling the boundary",
			write: []struct {
				n int
				v uint32
			}{{4, 0xA}, {8, 0x53}, {4, 0xC}},
			want: []byte{0xA5, 0x3C},
		},
		{
			name: "a zero-width field writes nothing",
			write: []struct {
				n int
				v uint32
			}{{0, 0}, {8, 0xA5}},
			want: []byte{0xA5},
		},
		{
			name: "thirty-two bits with the high bit set",
			write: []struct {
				n int
				v uint32
			}{{32, 0x80000000}},
			want: []byte{0x80, 0x00, 0x00, 0x00},
		},
		{
			name: "thirty-two ones",
			write: []struct {
				n int
				v uint32
			}{{32, 0xFFFFFFFF}},
			want: []byte{0xFF, 0xFF, 0xFF, 0xFF},
		},
		{
			// ⛔ The tail is padded with ZEROES, which a byte comparison
			// against qpdf can see even though no reader interprets it.
			name: "an odd number of bits pads with zeroes",
			write: []struct {
				n int
				v uint32
			}{{3, 0b101}},
			want: []byte{0b10100000},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := &bitw{}
			for i, f := range c.write {
				if err := w.write(f.n, f.v); err != nil {
					t.Fatalf("write %d of %d bits: %v", i, f.n, err)
				}
			}
			got := w.bytes()
			if !bytes.Equal(got, c.want) {
				t.Fatalf("bytes = % x, want % x", got, c.want)
			}

			// ⛔⛔ AND THE READER GETS THE SAME VALUES BACK, which is the real
			// assertion: the two halves agreeing about the byte layout is
			// what makes either trustworthy, and a shared misunderstanding of
			// the BIT ORDER would cancel out here — which is why the expected
			// bytes above are spelled out as well.
			r := &bits{b: got}
			for i, f := range c.write {
				v, err := r.read(f.n)
				if err != nil {
					t.Fatalf("read back %d of %d bits: %v", i, f.n, err)
				}
				if v != f.v {
					t.Errorf("read back %d = %#x, wrote %#x", i, v, f.v)
				}
			}
		})
	}
}

// ⛔⛔ A VALUE THAT DOES NOT FIT ITS WIDTH IS REFUSED, NOT TRUNCATED, and that
// is the encoder's most important refusal. Table F.3's widths are "the number
// of bits needed to represent" a difference, so a value that does not fit means
// the width was computed wrongly — and truncating it writes a table whose
// numbers are not the ones the caller intended, which parses perfectly.
func TestAValueThatDoesNotFitItsWidthIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		n    int
		v    uint32
		want string
	}{
		{"one bit too narrow", 3, 8, "8 does not fit in 3 bits"},
		{"a zero-width field with a value", 0, 1, "1 does not fit in a zero-bit field"},
		{"wider than thirty-two bits", 33, 0, "bounds every width"},
		{"a negative width", -1, 0, "bounds every width"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := &bitw{}
			err := w.write(c.n, c.v)
			if !errors.Is(err, errHintTable) {
				t.Fatalf("write(%d, %d) was accepted: %v", c.n, c.v, err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("it was refused, but not for %q: %v", c.want, err)
			}
		})
	}

	// ⛔ THE CONTROL: the widest value a field can hold IS written, so the
	// refusals above are about overflow and not about the boundary itself.
	w := &bitw{}
	if err := w.write(3, 7); err != nil {
		t.Errorf("7 was refused by a 3-bit field, which holds 0..7: %v", err)
	}
}

// ⛔⛔ AND EVERY WAY THE TABLES THEMSELVES ARE REFUSED, each because writing it
// would produce a file that reads back as something else.
func TestEveryWayTheHintTablesRefuseToBeWritten(t *testing.T) {
	t.Run("the page offset table", func(t *testing.T) {
		for _, c := range []struct {
			name  string
			h     *pageOffsetHeader
			pages []pageEntry
			want  string
		}{
			{
				name: "no pages at all",
				h:    &pageOffsetHeader{},
				want: "a hint table for no pages",
			},
			{
				// Table F.4 item 3 requires 0 there, and a non-zero count
				// would make groups (d) and (e) consume bits belonging to
				// later pages.
				name: "the first page claims a shared object",
				h:    &pageOffsetHeader{NBitsNSharedObjects: 4, NBitsSharedIdent: 4},
				pages: []pageEntry{
					{Shared: []sharedRef{{Identifier: 1}}},
					{},
				},
				want: "Table F.4 item 3 requires 0 there",
			},
			{
				// ⛔ An unsigned subtraction would make this a delta of about
				// four billion, which writes and parses and is wrong.
				name: "a page shorter than the table's least length",
				h:    &pageOffsetHeader{MinPageLength: 100, NBitsDeltaPageLength: 8},
				pages: []pageEntry{
					{Length: 50},
				},
				want: "page 1's length is 50 and the table's least is 100",
			},
			// ⛔⛔ ONE CASE PER LETTERED GROUP, each naming its own item. A
			// single "a delta too wide" case would leave six of the seven
			// error paths untested — and they are the lines that tell the
			// caller WHICH field it computed a width for wrongly, which is
			// the whole value of the message.
			{
				name:  "(a) an object count too wide for item 1",
				h:     &pageOffsetHeader{NBitsDeltaNObjects: 2},
				pages: []pageEntry{{NObjects: 9}},
				want:  "Table F.4 item 1 for page 1",
			},
			{
				name:  "(b) a length too wide for item 2",
				h:     &pageOffsetHeader{NBitsDeltaPageLength: 2},
				pages: []pageEntry{{Length: 9}},
				want:  "Table F.4 item 2 for page 1",
			},
			{
				name: "(c) a shared count too wide for item 3",
				h:    &pageOffsetHeader{NBitsNSharedObjects: 1},
				pages: []pageEntry{
					{},
					{Shared: []sharedRef{{}, {}, {}}},
				},
				want: "Table F.4 item 3 for page 2",
			},
			{
				name: "(d) an identifier too wide for item 4",
				h:    &pageOffsetHeader{NBitsNSharedObjects: 4, NBitsSharedIdent: 1},
				pages: []pageEntry{
					{},
					{Shared: []sharedRef{{Identifier: 5}}},
				},
				want: "Table F.4 item 4 for page 2",
			},
			{
				name: "(e) a numerator too wide for item 5",
				h: &pageOffsetHeader{
					NBitsNSharedObjects: 4, NBitsSharedIdent: 4, NBitsSharedNumerator: 1,
				},
				pages: []pageEntry{
					{},
					{Shared: []sharedRef{{Numerator: 5}}},
				},
				want: "Table F.4 item 5 for page 2",
			},
			{
				name:  "(f) a content offset too wide for item 6",
				h:     &pageOffsetHeader{NBitsDeltaContentOff: 2},
				pages: []pageEntry{{ContentOffset: 9}},
				want:  "Table F.4 item 6 for page 1",
			},
			{
				name:  "(g) a content length too wide for item 7",
				h:     &pageOffsetHeader{NBitsDeltaContentLen: 2},
				pages: []pageEntry{{ContentLength: 9}},
				want:  "Table F.4 item 7 for page 1",
			},
			// ⛔ And the other two minima, so that every delta() caller is
			// exercised rather than just the page length.
			{
				name:  "a content offset below the table's least",
				h:     &pageOffsetHeader{MinContentOffset: 100, NBitsDeltaContentOff: 8},
				pages: []pageEntry{{ContentOffset: 50}},
				want:  "page 1's content stream offset is 50",
			},
			{
				name:  "a content length below the table's least",
				h:     &pageOffsetHeader{MinContentLength: 100, NBitsDeltaContentLen: 8},
				pages: []pageEntry{{ContentLength: 50}},
				want:  "page 1's content stream length is 50",
			},
			{
				name:  "an object count below the table's least",
				h:     &pageOffsetHeader{MinNObjects: 100, NBitsDeltaNObjects: 8},
				pages: []pageEntry{{NObjects: 50}},
				want:  "page 1's object count is 50",
			},
			// ⛔⛔ AND A HEADER FIELD THAT IS NOT A WIDTH — item 13, the shared
			// denominator, which is the ONE place Table F.3's header can
			// overflow. Its 32-bit items hold a uint32 by construction, and its
			// other sixteen-bit items are the seven widths, now bounded to
			// 0..32 before they are written.
			//
			// ⛔ THIS CASE USED TO USE NBitsDeltaNObjects: 70000, and the width
			// check added for the reader/writer asymmetry intercepts that with
			// a better message. 70000 is not a width, so the width refusal is
			// the right one — but it left item 13 as the only live witness that
			// the header's own overflow path works at all, which is why the
			// case moved here rather than being deleted.
			{
				name:  "a header field that does not fit sixteen bits",
				h:     &pageOffsetHeader{SharedDenominator: 70000},
				pages: []pageEntry{{}},
				want:  "Table F.3, item 13 of 13",
			},
		} {
			t.Run(c.name, func(t *testing.T) {
				_, err := writePageOffsetTable(c.h, c.pages)
				if !errors.Is(err, errHintTable) {
					t.Fatalf("it was accepted: %v", err)
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("it was refused, but not for %q: %v", c.want, err)
				}
			})
		}
	})

	t.Run("the shared object table", func(t *testing.T) {
		for _, c := range []struct {
			name   string
			h      *sharedHeader
			groups []sharedGroup
			want   string
		}{
			{
				name:   "the header's count disagrees with the groups given",
				h:      &sharedHeader{NSharedTotal: 3},
				groups: []sharedGroup{{NObjects: 1}},
				want:   "says 3 group(s) and 1 were given",
			},
			{
				name:   "a total below the first page's count",
				h:      &sharedHeader{NSharedFirstPage: 5, NSharedTotal: 2},
				groups: []sharedGroup{{NObjects: 1}, {NObjects: 1}},
				want:   "item 4 includes item 3",
			},
			{
				name:   "a group shorter than the table's least",
				h:      &sharedHeader{NSharedTotal: 1, MinGroupLength: 100, NBitsDeltaGroupLength: 8},
				groups: []sharedGroup{{Length: 50, NObjects: 1}},
				want:   "group 1 is 50 bytes and the table's least is 100",
			},
			{
				name:   "a signature that is not sixteen bytes",
				h:      &sharedHeader{NSharedTotal: 1},
				groups: []sharedGroup{{NObjects: 1, Signature: []byte{1, 2, 3}}},
				want:   "signature is 3 bytes",
			},
			{
				// Item 4 stores one LESS than the count, so a group of none
				// would have to store minus one.
				name:   "a group of no objects",
				h:      &sharedHeader{NSharedTotal: 1},
				groups: []sharedGroup{{NObjects: 0}},
				want:   "holds no objects",
			},
			{
				name:   "a group length delta too wide for item 1",
				h:      &sharedHeader{NSharedTotal: 1, NBitsDeltaGroupLength: 2},
				groups: []sharedGroup{{Length: 9, NObjects: 1}},
				want:   "Table F.6 item 1 for group 1",
			},
			{
				name:   "an object count too wide for item 4",
				h:      &sharedHeader{NSharedTotal: 1, NBitsNObjects: 2},
				groups: []sharedGroup{{NObjects: 9}},
				want:   "Table F.6 item 4 for group 1",
			},
			// ⛔⛔ TABLE F.5'S HEADER HAS NO OVERFLOW CASE LEFT, and the case
			// that used to sit here (NBitsNObjects: 70000, expecting "Table
			// F.5, item 5 of 7") is gone deliberately rather than by neglect.
			//
			// Its seven items are five uint32 in 32-bit fields and two widths
			// now bounded to 0..32 before they are written, so every one fits
			// its field by construction and writeSharedTable pushes them
			// without a refusal there would be no way to reach. 70000 is not a
			// width, and the refusal it now gets is the width one, asserted
			// item by item in
			// TestTheWriterRefusesEveryWidthItsReaderWouldRefuse.
			//
			// ⚠ Table F.3's header kept its overflow case, because its item 13
			// is a uint32 in a sixteen-bit field. The asymmetry is real.
		} {
			t.Run(c.name, func(t *testing.T) {
				_, err := writeSharedTable(c.h, c.groups)
				if !errors.Is(err, errHintTable) {
					t.Fatalf("it was accepted: %v", err)
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("it was refused, but not for %q: %v", c.want, err)
				}
			})
		}
	})
}

// ⛔⛔ THE WRITER REFUSES EVERY WIDTH ITS OWN READER WOULD REFUSE, which it did
// not when this file was first written. Table F.3's items 3..12 and Table F.5's
// items 5 and 7 are SIXTEEN-BIT FIELDS holding a number F.4.1 bounds to 0..32,
// so a width of 40 fits the field perfectly. The per-value `write` guard cannot
// catch it, because the `n > 32` refusal lives in the lettered group that USES
// the width — and an empty group writes nothing.
//
// ⛔ Measured before the fix: writePageOffsetTable with NBitsSharedIdent 40 and
// one page returned a 36-byte header ending `00 28 00 00 00 00`, which
// readPageOffsetHeader then refused with "nbits_shared_identifier is 40". An
// encoder whose output its own decoder rejects is a defect even with no caller
// that supplies such a header: the two directions are supposed to describe one
// format, and for a one-page table groups (d) and (e) never run.
func TestTheWriterRefusesEveryWidthItsReaderWouldRefuse(t *testing.T) {
	t.Run("Table F.3's seven width items", func(t *testing.T) {
		for _, c := range []struct {
			name string
			set  func(*pageOffsetHeader, int)
			want string
		}{
			{"item 3", func(h *pageOffsetHeader, n int) { h.NBitsDeltaNObjects = n }, "nbits_delta_nobjects"},
			{"item 5", func(h *pageOffsetHeader, n int) { h.NBitsDeltaPageLength = n }, "nbits_delta_page_length"},
			{"item 7", func(h *pageOffsetHeader, n int) { h.NBitsDeltaContentOff = n }, "nbits_delta_content_offset"},
			{"item 9", func(h *pageOffsetHeader, n int) { h.NBitsDeltaContentLen = n }, "nbits_delta_content_length"},
			{"item 10", func(h *pageOffsetHeader, n int) { h.NBitsNSharedObjects = n }, "nbits_nshared_objects"},
			{"item 11", func(h *pageOffsetHeader, n int) { h.NBitsSharedIdent = n }, "nbits_shared_identifier"},
			{"item 12", func(h *pageOffsetHeader, n int) { h.NBitsSharedNumerator = n }, "nbits_shared_numerator"},
		} {
			t.Run(c.name, func(t *testing.T) {
				// ⛔ ONE PAGE, so that every group which would consume the
				// width is empty. That is the shape the guard missed.
				h := &pageOffsetHeader{}
				c.set(h, 40)
				_, err := writePageOffsetTable(h, []pageEntry{{}})
				if !errors.Is(err, errHintTable) {
					t.Fatalf("a width of 40 was written: %v", err)
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("it was refused, but not for %q: %v", c.want, err)
				}

				// ⛔ AND 32 IS ACCEPTED, which is the control: without it this
				// case would pass against a writer that refused every width.
				h = &pageOffsetHeader{}
				c.set(h, 32)
				if _, err := writePageOffsetTable(h, []pageEntry{{}}); err != nil {
					t.Errorf("a width of 32 was refused, and F.4.1 allows 0..32: %v", err)
				}
			})
		}
	})

	t.Run("Table F.5's two width items", func(t *testing.T) {
		for _, c := range []struct {
			name string
			set  func(*sharedHeader, int)
			want string
		}{
			{"item 5", func(h *sharedHeader, n int) { h.NBitsNObjects = n }, "nbits_nobjects"},
			{"item 7", func(h *sharedHeader, n int) { h.NBitsDeltaGroupLength = n }, "nbits_delta_group_length"},
		} {
			t.Run(c.name, func(t *testing.T) {
				// ⛔ NO GROUPS AT ALL, so every group that would use the width
				// is empty.
				h := &sharedHeader{}
				c.set(h, 40)
				_, err := writeSharedTable(h, nil)
				if !errors.Is(err, errHintTable) {
					t.Fatalf("a width of 40 was written: %v", err)
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("it was refused, but not for %q: %v", c.want, err)
				}

				h = &sharedHeader{}
				c.set(h, 32)
				if _, err := writeSharedTable(h, nil); err != nil {
					t.Errorf("a width of 32 was refused, and F.4.1 allows 0..32: %v", err)
				}
			})
		}
	})
}

// ⛔⛔ THE THREE GROUPS QPDF LEAVES EMPTY ON EVERY FILE IN THE CORPUS. F.4's
// groups (d) and (e) carry a shared reference's identifier and numerator, and
// F.6's group (d) carries a group's object count; qpdf writes width 0 for
// nbits_shared_numerator and nbits_nobjects and gives every shared group one
// object, so (e) and F.6 (d) emit nothing at all and the byte oracle is blind
// to their order, their pads and item 4's "1 less than the number".
//
// ⛔ THE EXPECTATION IS SPELLED OUT, not computed by the code under test. Every
// width here is 4 bits so the arithmetic fits in this comment and can be
// checked by eye against Table F.3, Table F.4 and Table F.6.
func TestTheGroupsQpdfLeavesEmptyAreLaidOutFromTheAnnex(t *testing.T) {
	t.Run("the page offset table's groups (d), (e) and (f)", func(t *testing.T) {
		h := &pageOffsetHeader{
			MinNObjects:          1,
			FirstPageOffset:      0,
			NBitsDeltaNObjects:   4,
			MinPageLength:        10,
			NBitsDeltaPageLength: 4,
			MinContentOffset:     0,
			NBitsDeltaContentOff: 4, // item 7, zero in every fixture
			MinContentLength:     0,
			NBitsDeltaContentLen: 4,
			NBitsNSharedObjects:  4,
			NBitsSharedIdent:     4,
			NBitsSharedNumerator: 4, // item 12, zero in every fixture
			SharedDenominator:    1,
		}
		pages := []pageEntry{
			// Page 1 is at every minimum, and F.4 item 3 requires 0 shared
			// references for it.
			{NObjects: 1, Length: 10, ContentOffset: 0, ContentLength: 0},
			// Page 2 is one above each minimum and carries one reference.
			{
				NObjects: 2, Length: 11, ContentOffset: 1, ContentLength: 1,
				Shared: []sharedRef{{Identifier: 3, Numerator: 5}},
			},
		}

		got, err := writePageOffsetTable(h, pages)
		if err != nil {
			t.Fatalf("it was refused: %v", err)
		}

		// Table F.3's thirteen items, widths 32 32 16 32 16 32 16 32 16 16 16
		// 16 16 = 288 bits = 36 bytes.
		want := []byte{
			0x00, 0x00, 0x00, 0x01, // 1  min_nobjects            = 1
			0x00, 0x00, 0x00, 0x00, // 2  first_page_offset       = 0
			0x00, 0x04, // ........... 3  nbits_delta_nobjects    = 4
			0x00, 0x00, 0x00, 0x0a, // 4  min_page_length         = 10
			0x00, 0x04, // ........... 5  nbits_delta_page_len    = 4
			0x00, 0x00, 0x00, 0x00, // 6  min_content_offset      = 0
			0x00, 0x04, // ........... 7  nbits_delta_content_off = 4
			0x00, 0x00, 0x00, 0x00, // 8  min_content_length      = 0
			0x00, 0x04, // ........... 9  nbits_delta_content_len = 4
			0x00, 0x04, // ...........10  nbits_nshared_objects   = 4
			0x00, 0x04, // ...........11  nbits_shared_ident      = 4
			0x00, 0x04, // ...........12  nbits_shared_numerator  = 4
			0x00, 0x01, // ...........13  shared_denominator      = 1
			// Then F.4.1's seven lettered groups, column-major, each padded to
			// a byte boundary. Two pages at 4 bits each fills one byte exactly;
			// the single reference in (d) and (e) fills a nibble and pads.
			0x01, // (a) item 1: deltas 0, 1       -> 0000 0001
			0x01, // (b) item 2: deltas 0, 1       -> 0000 0001
			0x01, // (c) item 3: counts 0, 1       -> 0000 0001
			0x30, // (d) item 4: identifier 3      -> 0011 +pad
			0x50, // (e) item 5: numerator 5       -> 0101 +pad
			0x01, // (f) item 6: deltas 0, 1       -> 0000 0001
			0x01, // (g) item 7: deltas 0, 1       -> 0000 0001
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("the page offset table was written as\n % x\nand the annex lays it out as\n % x",
				got, want)
		}
	})

	t.Run("the shared object table's group (d) and item 4's minus one", func(t *testing.T) {
		h := &sharedHeader{
			NSharedFirstPage:      1,
			NSharedTotal:          2,
			NBitsNObjects:         4, // item 5, zero in every fixture
			MinGroupLength:        100,
			NBitsDeltaGroupLength: 4,
		}
		groups := []sharedGroup{
			{Length: 100, NObjects: 3},
			{Length: 105, NObjects: 1},
		}

		got, err := writeSharedTable(h, groups)
		if err != nil {
			t.Fatalf("it was refused: %v", err)
		}

		// Table F.6's groups (a)..(d), each padded to a byte boundary:
		//   (a) item 1: length deltas 0 and 5      -> 0000 0101
		//   (b) item 2: signature flags 0 and 0    -> 00 +pad
		//   (c) item 3: no signatures, nothing
		//   (d) item 4: NObjects-1, so 2 and 0     -> 0010 0000
		//
		// ⛔ Group (d) is the ONLY witness in this package that item 4 stores
		// one LESS than the count: 3 objects are written as 2. An encoder that
		// stored the count itself would put 0x30 there and no fixture would
		// notice.
		want := sharedTableBytes(0, 0, 1, 2, 4, 100, 4, 0x05, 0x00, 0x20)
		if !bytes.Equal(got, want) {
			t.Fatalf("the shared object table was written as\n % x\nand the annex lays it out as\n % x",
				got, want)
		}
	})
}

// ⛔⛔ THE SIGNATURE PATH, WHICH THE BYTE ORACLE CANNOT REACH. Table F.6 item 3
// is the 16-byte MD5 that item 2's flag guards, and NOT ONE FILE in the corpus
// sets that flag — qpdf 12.4.2 and Ghostscript 10.08.0 both leave it clear on
// every fixture here. So the round-trip against qpdf's bytes, which is this
// encoder's only foreign oracle, is silent about sixteen bytes of Table F.6.
//
// ⛔⛔ AND IT IS THE ONLY THING DEFENDING THE PAD AFTER GROUP (b). Measured by
// mutation: deleting the align() that follows item 2's flag bits leaves EVERY
// qpdf fixture byte-identical, because group (c) writes nothing when no group
// is signed and (c)'s own unconditional align() then restores the boundary the
// deleted one would have set. The two pads are redundant on every file in the
// corpus, and distinguishable only once item 3 is actually present. A suite
// without this case would have carried a missing pad under a green oracle.
//
// ⛔ THE EXPECTATION IS BUILT FROM THE ANNEX, NOT FROM MY READER. The bytes
// below are the same ones TestASharedGroupsSignatureIsReadWhenItsFlagIsSet
// feeds the reader, laid out by sharedTableBytes from F.4.2's item sizes. That
// makes this a two-sided check on one construction: the reader turns those
// bytes into these values, the writer turns these values back into those
// bytes. A round trip through writeSharedTable and readSharedTable would pass
// on any pair of functions that agreed with each other and with neither annex.
func TestASignedSharedGroupIsWrittenAsTheAnnexLaysItOut(t *testing.T) {
	sig := []byte{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	}
	// One group, no bits for its length or its object count: after the header
	// the only bits are item 2's flag — alone in its byte, because each
	// lettered group is padded to a boundary — and then item 3.
	h := &sharedHeader{NSharedFirstPage: 1, NSharedTotal: 1}
	groups := []sharedGroup{{Signature: sig, NObjects: 1}}

	got, err := writeSharedTable(h, groups)
	if err != nil {
		t.Fatalf("a signed group was refused: %v", err)
	}
	want := sharedTableBytes(0, 0, 1, 1, 0, 0, 0, append([]byte{0x80}, sig...)...)
	if !bytes.Equal(got, want) {
		t.Fatalf("a signed group wrote\n % x\nand the annex's layout is\n % x", got, want)
	}

	// ⛔ AND THE CONTROL, which is the whole reason the case above can fail: a
	// writer that emitted sixteen bytes for EVERY group would reproduce the
	// signed table exactly and be sixteen bytes long here. The unsigned table
	// is one byte of flag and nothing else.
	got, err = writeSharedTable(h, []sharedGroup{{NObjects: 1}})
	if err != nil {
		t.Fatalf("an unsigned group was refused: %v", err)
	}
	if want := sharedTableBytes(0, 0, 1, 1, 0, 0, 0, 0x00); !bytes.Equal(got, want) {
		t.Fatalf("an unsigned group wrote\n % x\nwant\n % x", got, want)
	}
}

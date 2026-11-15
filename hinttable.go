// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"errors"
	"fmt"
)

// errHintTable reports a hint table this package cannot read.
//
// ⛔ IT IS SEPARATE FROM ErrLinearizationStale ON PURPOSE. A stale parameter
// says the front of the file disagrees with the file; a broken hint table says
// the index a reader would fetch is wrong. Both make a file unusable for fast
// web view, and they are different repairs — the first is a rewrite of eight
// integers, the second a rewrite of the tables.
//
// ⛔⛔ AND IT IS UNEXPORTED, WITH EVERYTHING ELSE IN THIS FILE, BECAUSE NOTHING
// PUBLIC RETURNS IT YET. readHintTables is unexported, so an exported
// ErrHintTable, HintTables and PageOffsetHeader would be names no caller could
// obtain a value of — API surface that exists only to be renamed later.
// CheckLinearized does not consult these tables yet; when it does, exporting
// them is a deliberate change with a caller to justify each name.
// ("Speculative API costs more than it saves", and review caught it here.)
var errHintTable = errors.New("reader: the hint table cannot be read")

// bits reads a big-endian bit stream, which is what Annex F.4's tables are.
//
// ⛔⛔ EVERY ITEM IS BIT-ALIGNED AND NOT BYTE-ALIGNED, which is why this type
// exists rather than encoding/binary. Table F.3's header happens to be whole
// bytes (thirteen items of 32 and 16 bits), but Table F.4's per-page items
// take their widths FROM that header — "See Table F.3, item 3" — and those
// widths are any value in 0..32. A two-page document with nbits_delta_nobjects
// of 1 writes one bit per page, so page two's item begins mid-byte. Reading
// such a table a byte at a time gives numbers that look plausible and are
// wrong, which is the worst failure available here.
// ⛔⛔ pos AND THE BIT COUNTS ARE int64 AND NOT int, WHICH IS A 32-BIT DEFECT
// REVIEW FOUND AND NOT A STYLE. `8*len(r.b)` overflows a 32-bit int once the
// decoded stream passes 256 MiB, and filter.go's expansion cap is 1 GiB — so
// the overflow is reachable from a file, on the 386 and arm lanes this
// repository builds for. A negative bit count makes left() report space that
// is not there and read() hand back whatever follows.
//
// ⛔ This package has paid for this class before: #42 is "a value this build
// cannot address is refused, not truncated", about int(Integer) in the
// parameter dictionary. The same mistake arrived in a new file one release
// later, which is why the counts are a type rather than a convention.
type bits struct {
	b   []byte
	pos int64 // the next bit to read, counted from the first bit of b
}

// atEnd reports whether every bit has been read.
func (r *bits) atEnd() bool { return r.pos >= r.size() }

// size reports how many bits the buffer holds, in int64 so that a buffer
// larger than 256 MiB does not overflow the count on a 32-bit build.
func (r *bits) size() int64 { return 8 * int64(len(r.b)) }

// left reports how many bits remain.
func (r *bits) left() int64 { return r.size() - r.pos }

// read takes the next n bits as an unsigned value, most significant bit first.
//
// ⛔ n == 0 IS LEGAL AND RETURNS 0, which F.4.1 requires rather than merely
// permits: the bit-count items "have values in the range 0 through 32", and
// zero is what a document whose pages all have the same object count writes.
// qpdf emits nbits_delta_nobjects 0 for the one-page fixtures. A reader that
// treated 0 as an error, or read one bit for it, would misread every page
// entry after it.
func (r *bits) read(n int) (uint32, error) {
	if n < 0 || n > 32 {
		return 0, fmt.Errorf("%w: asked for %d bits, and F.4.1 bounds every width to 0..32",
			errHintTable, n)
	}
	if n == 0 {
		return 0, nil
	}
	if r.left() < int64(n) {
		return 0, fmt.Errorf("%w: %d bits were asked for and %d remain",
			errHintTable, n, r.left())
	}
	var v uint32
	for i := int64(0); i < int64(n); i++ {
		byteAt := (r.pos + i) / 8
		bitAt := 7 - uint((r.pos+i)%8)
		bit := (r.b[byteAt] >> bitAt) & 1
		v = v<<1 | uint32(bit)
	}
	r.pos += int64(n)
	return v, nil
}

// align moves to the next byte boundary, which the tables need between
// sections: F.4.2 places the shared object hint table at the byte offset /S
// names, not at a bit offset.
func (r *bits) align() { r.pos = (r.pos + 7) / 8 * 8 }

// seekByte moves to a byte offset, which is the unit /S and the other hint
// table positions are counted in.
func (r *bits) seekByte(off int) error {
	if off < 0 || off > len(r.b) {
		return fmt.Errorf("%w: a table was placed at byte %d of %d",
			errHintTable, off, len(r.b))
	}
	r.pos = 8 * int64(off)
	return nil
}

// PageOffsetHeader is the page offset hint table's header section, Table F.3's
// thirteen items in order.
//
// ⛔ THE FIELD NAMES ARE QPDF'S, NOT THE ANNEX'S. The annex names no fields —
// it numbers thirteen items and describes each in a sentence — so any name
// here is invented. Taking qpdf's means a field can be compared with
// `qpdf --show-linearization` by reading, which is the only judge this table
// has, and it spares the next person a mapping table between two vocabularies.
type pageOffsetHeader struct {
	MinNObjects          uint32 // item 1: the least number of objects in a page
	FirstPageOffset      uint32 // item 2: the location of the first page's page object
	NBitsDeltaNObjects   int    // item 3: bits for the object-count delta
	MinPageLength        uint32 // item 4: the least length of a page in bytes
	NBitsDeltaPageLength int    // item 5: bits for the page-length delta
	MinContentOffset     uint32 // item 6: the least content stream offset within a page
	NBitsDeltaContentOff int    // item 7: bits for the content-offset delta
	MinContentLength     uint32 // item 8: the least content stream length
	NBitsDeltaContentLen int    // item 9: bits for the content-length delta
	NBitsNSharedObjects  int    // item 10: bits for the shared-object count
	NBitsSharedIdent     int    // item 11: bits for a shared object identifier
	NBitsSharedNumerator int    // item 12: bits for a fractional-position numerator
	SharedDenominator    uint32 // item 13: the denominator, once for the document
}

// FirstPageOffsetInFile returns Table F.3's item 2 as an offset into the file,
// which is NOT the number the table stores.
//
// ⛔⛔ THE STORED OFFSET IS COMPUTED AS IF THE PRIMARY HINT STREAM WERE NOT
// THERE, and that is the single thing this decoder had to be told rather than
// read. Table F.4 item 2 says it outright for page lengths — "A conforming
// product shall skip over the primary hint stream, wherever it is located" —
// and the same convention governs item 2 of the header.
//
// ⭐ MEASURED, NOT INFERRED, ACROSS TWELVE SHAPES. For every qpdf fixture
// (1/2/5/25 pages x plain, object streams, uncompressed) the stored value
// equals /H's own offset and qpdf reports stored + /H's length, exactly:
//
//	plain  2 pages: stored 550, /H [550 127], qpdf says 677
//	objstm 25 pages: stored 613, /H [613 141], qpdf says 754
//
// ⛔ AND THE CONDITION IS LOAD-BEARING, WHICH ONLY A SECOND PRODUCER COULD
// SHOW. Ghostscript places /H AFTER the first page: testdata/linearized-gs.pdf
// stores 577 with /H at 829, and qpdf reports 577 — unadjusted. So the length
// is added only when the hint stream lies at or before the point being named.
// Adding it unconditionally would have agreed with every qpdf file in the
// corpus and been wrong about every Ghostscript one.
// ⛔⛔ IT RETURNS int64 AND REFUSES WHAT THIS BUILD CANNOT ADDRESS, which review
// found it did not. `int(h.FirstPageOffset)` on a uint32 of 0x80000000 or above
// goes NEGATIVE on a 32-bit build, so the comparison below and the addition
// both give a wrong answer — and the 386 and arm lanes this repository builds
// for would not see it, because no fixture holds such a value.
//
// ⛔ #42 is this exact class, one release earlier and one file over: "a value
// this build cannot address is refused, not truncated", about int(Integer) in
// the parameter dictionary. The README says so. I wrote it again anyway, which
// is why the arithmetic here is int64 throughout and the conversion is a
// refusal rather than a cast.
func (h *pageOffsetHeader) firstPageOffsetInFile(lin *Linearization) (int64, error) {
	off := int64(h.FirstPageOffset)
	if lin != nil && int64(lin.HintOffset) <= off {
		off += int64(lin.HintLength)
	}
	if off > intMax {
		return 0, fmt.Errorf("%w: the first page would be at byte %d, which this build "+
			"cannot address (its int holds up to %d)", errHintTable, off, intMax)
	}
	return off, nil
}

// readPageOffsetHeader reads Table F.3 from the start of the decoded hint
// stream, where F.3.6 requires the page offset hint table to begin: it "shall
// be the first table in the stream and shall start at offset 0".
//
// ⛔ THE THIRTEEN WIDTHS ARE A TABLE AND NOT THIRTEEN CALLS, so that the
// sequence is checkable against Table F.3 by eye. Getting one width wrong
// shifts every later item without any single value looking impossible — a
// misread table is a table that still parses.
func readPageOffsetHeader(r *bits) (*pageOffsetHeader, error) {
	// Table F.3, items 1..13, in the annex's order, widths in bits.
	const (
		wide  = 32
		small = 16
	)
	widths := []int{wide, wide, small, wide, small, wide, small, wide, small, small, small, small, small}

	v := make([]uint32, len(widths))
	for i, n := range widths {
		got, err := r.read(n)
		if err != nil {
			return nil, fmt.Errorf("%w (Table F.3, item %d of 13)", err, i+1)
		}
		v[i] = got
	}

	h := &pageOffsetHeader{
		MinNObjects:          v[0],
		FirstPageOffset:      v[1],
		NBitsDeltaNObjects:   int(v[2]),
		MinPageLength:        v[3],
		NBitsDeltaPageLength: int(v[4]),
		MinContentOffset:     v[5],
		NBitsDeltaContentOff: int(v[6]),
		MinContentLength:     v[7],
		NBitsDeltaContentLen: int(v[8]),
		NBitsNSharedObjects:  int(v[9]),
		NBitsSharedIdent:     int(v[10]),
		NBitsSharedNumerator: int(v[11]),
		SharedDenominator:    v[12],
	}

	// ⛔⛔ AND THE WIDTHS ARE CHECKED BEFORE THEY ARE USED, because they are
	// the ones that decide how the REST of the table is read. F.4.1: all the
	// items "that specify a number of bits needed ... have values in the range
	// 0 through 32. Although that range requires only 6 bits, 16-bit numbers
	// shall be used." So a 16-bit field can hold 65535, and a width of 65535
	// is not a wide field — it is a table this package must refuse before it
	// reads 65535 bits of something else and reports whatever that was.
	//
	// ⭐ This is not hypothetical: the Ghostscript fixture in testdata writes
	// min_group_length 65536 in the OTHER table, which is 0x10000 and plainly
	// a placeholder nobody filled in. A producer that leaves one field unset
	// leaves others unset.
	for _, c := range []struct {
		name string
		item int
		n    int
	}{
		{"nbits_delta_nobjects", 3, h.NBitsDeltaNObjects},
		{"nbits_delta_page_length", 5, h.NBitsDeltaPageLength},
		{"nbits_delta_content_offset", 7, h.NBitsDeltaContentOff},
		{"nbits_delta_content_length", 9, h.NBitsDeltaContentLen},
		{"nbits_nshared_objects", 10, h.NBitsNSharedObjects},
		{"nbits_shared_identifier", 11, h.NBitsSharedIdent},
		{"nbits_shared_numerator", 12, h.NBitsSharedNumerator},
	} {
		if c.n > 32 {
			return nil, fmt.Errorf("%w: %s is %d, and F.4.1 bounds every bit width to 0..32 "+
				"(Table F.3, item %d, which is 16 bits wide and so can hold a number that is "+
				"not a width at all)", errHintTable, c.name, c.n, c.item)
		}
	}
	return h, nil
}

// sharedRef is one page's reference to a shared object: Table F.4's items 4
// and 5, which come in step.
type sharedRef struct {
	Identifier uint32 // item 4: an index into the shared object hint table
	Numerator  uint32 // item 5: where in the content stream it is first used
}

// pageEntry is one page's Table F.4 entry, with the header's minima already
// added back.
//
// ⛔ THE DELTAS ARE NOT KEPT. Every item of Table F.4 is "a number that, when
// added to" one of Table F.3's minima "shall give" the real value, so a delta
// on its own means nothing and a caller holding one would have to remember
// which minimum to add. The decoder does the addition once; what it returns is
// the quantity the annex names.
type pageEntry struct {
	NObjects      uint32 // item 1 + Table F.3 item 1
	Length        uint32 // item 2 + Table F.3 item 4
	ContentOffset uint32 // item 6 + Table F.3 item 6
	ContentLength uint32 // item 7 + Table F.3 item 8
	Shared        []sharedRef
}

// readPageEntries reads Table F.4's per-page entries, which follow the header.
//
// ⛔⛔ THE ENTRIES ARE COLUMN-MAJOR AND THIS IS THE WHOLE DIFFICULTY. F.4.1 says
// it outright — "The items making up each per-page entry are not contiguous;
// they are broken up with items from entries for other pages" — and then gives
// the order as (a) to (g): item 1 for ALL pages, then item 2 for all pages, and
// so on. A decoder that read one page's seven items together would parse
// without complaint and return numbers belonging to other pages.
//
// ⛔ So the reads below are in F.4.1's lettered order, with each letter named,
// because that order IS the format and a reordering of these statements is a
// silent defect. Items 4 and 5 sit between items 3 and 6, which no
// page-at-a-time reading would produce.
//
// ⛔⛔⛔ AND EACH LETTERED GROUP IS PADDED TO A BYTE BOUNDARY, WHICH ISO 32000-1
// DOES NOT SAY ANYWHERE. F.4.1 gives the order (a) to (g) and the widths, and
// says nothing about alignment between groups; a search of the whole of
// 32000-1 finds "byte boundary" only in the filter and image-sample clauses,
// never in Annex F or G. A decoder written from the text alone — which is what
// the first version of this function was — is wrong on every real file.
//
// ⚠ ISO 32000-2 WAS NOT SEARCHED, because no copy is on this machine. Its
// Annex F may state the alignment outright, so "the specification does not say
// so" above means 32000-1 specifically.
//
// ⭐ MEASURED, BY HAND, FROM THE BYTES. For the two-page qpdf fixture the bits
// after the 36-byte header are:
//
//	byte 36: 10000000   item 1: page 0 delta 1, page 1 delta 0, SIX BITS PADDING
//	byte 37: 11111000   item 2: page 0 delta 31 (5 bits), then
//	byte 38: 00000000     page 1 delta 0, then padding
//	byte 39: 01000000   item 3: page 0 count 0, page 1 count 1, padding
//	byte 40: 10000000   item 4: page 1's identifier 2 (2 bits), padding
//	byte 41: 11111000   item 7: page 0 delta 31, then
//	byte 42: 00000000     page 1 delta 0, padding
//
// Item 3 landing on byte 39 is what proves it: read without the padding, page
// 0's count comes out as 1, and qpdf reports 0. That mismatch is how this was
// found — the refusal of a non-zero first-page count fired on five of twelve
// files qpdf had produced, which is the shape of a decoder error rather than a
// producer error.
//
// ⚠ So the alignment is a PRODUCER CONVENTION this package follows rather than
// a rule it can cite, and it is written down here as such. qpdf agrees across
// twelve shapes, which is the evidence. The Ghostscript fixture corroborates
// only the items whose values it and qpdf agree on — its per-page LENGTHS are
// among the five things qpdf warns about on that file — so it is not a second
// independent witness to the layout in general, and saying otherwise would be
// borrowing confidence from a file that is wrong nearby.
func readPageEntries(r *bits, h *pageOffsetHeader, pages int) ([]pageEntry, error) {
	if pages <= 0 {
		return nil, fmt.Errorf("%w: a hint table for %d pages", errHintTable, pages)
	}

	read := func(n int, what string, i int) (uint32, error) {
		v, err := r.read(n)
		if err != nil {
			return 0, fmt.Errorf("%w (%s for page %d of %d)", err, what, i+1, pages)
		}
		return v, nil
	}

	out := make([]pageEntry, pages)

	// (a) Item 1 for all pages: the object count, as a delta on Table F.3's
	// least.
	for i := range out {
		d, err := read(h.NBitsDeltaNObjects, "item 1, the object count", i)
		if err != nil {
			return nil, err
		}
		out[i].NObjects = h.MinNObjects + d
	}
	r.align()

	// (b) Item 2 for all pages: the page's length in bytes.
	for i := range out {
		d, err := read(h.NBitsDeltaPageLength, "item 2, the page length", i)
		if err != nil {
			return nil, err
		}
		out[i].Length = h.MinPageLength + d
	}
	r.align()

	// (c) Item 3 for all pages: how many shared objects the page references.
	//
	// ⛔ IT IS READ FOR THE FIRST PAGE TOO, AND MUST BE ZERO THERE. Table F.4
	// item 3: "For the first page, this number shall be 0; the next two items
	// start with the second page." So the count is present for every page —
	// skipping it for page one would shift every later count — and the first
	// page's value is a `shall` worth refusing, because a non-zero one would
	// make the identifier reads below consume somebody else's bits.
	counts := make([]uint32, pages)
	for i := range out {
		n, err := read(h.NBitsNSharedObjects, "item 3, the shared object count", i)
		if err != nil {
			return nil, err
		}
		counts[i] = n
	}
	if counts[0] != 0 {
		return nil, fmt.Errorf("%w: the first page claims %d shared object(s), and Table F.4 "+
			"item 3 requires 0 there — items 4 and 5 start at the second page",
			errHintTable, counts[0])
	}

	// ⛔ AND THE COUNTS ARE BOUNDED BEFORE ANY OF THEM IS USED, BECAUSE
	// nbits_nshared_objects CAN BE 32. A count can therefore be four billion,
	// and each reference costs two reads below, so without this a twelve-byte
	// table could ask for billions of iterations and be refused only after the
	// loops had run.
	//
	// ⛔⛔ IT IS A GUARD AGAINST AN ABSURD COUNT AND NOT A PROOF THAT THE READS
	// CANNOT FAIL, which is the correction this comment exists for. An earlier
	// version claimed exactly that proof and used it to DISCARD the errors in
	// groups (d) and (e) — and the proof was false, because r.align() sits
	// between those groups, so real consumption is
	// ceil(total*identBits/8)*8 + total*numeratorBits, up to seven bits more
	// than this bound allows. Review ran the counterexample: widths of 1 for
	// the count, the identifier and the numerator, two pages, bytes 40 80. The
	// bound passes with two bits needed and eight remaining, (d) reads one bit
	// and aligns to the next byte, and (e) has nothing left — and the discarded
	// error made that decode SILENTLY, with a fabricated numerator.
	//
	// ⛔ So the reads below return their errors, and this bound only has to be
	// cheap and conservative rather than exact. Nothing here rests on
	// arithmetic I could get wrong a second time.
	var total int64
	for _, n := range counts {
		total += int64(n)
	}
	// ⛔⛔ AND IT IS MEASURED BEFORE THE ALIGNMENT, DELIBERATELY, so that it
	// over-estimates what remains by up to seven bits and is therefore NOT a
	// proof about either group. An exact bound would make group (d)'s read
	// error unreachable — a branch no input can take, which this package's
	// exact coverage gate rightly refuses to carry. Conservative here, errors
	// returned below: that combination has no unreachable line and no
	// justification to be wrong about.
	// ⛔⛔ AND THE COUNT ITSELF IS CAPPED, NOT ONLY ITS BIT COST — a hole the
	// shared object table's tests exposed in this bound. When
	// nbits_shared_identifier and nbits_shared_numerator are BOTH 0, the bit
	// cost is zero, so the check below passes for any count whatever: a page
	// claiming four billion references of zero width would loop four billion
	// times reading nothing. A reference is at most one per remaining bit
	// before it is absurd, and that is all this cap claims.
	if total > r.left() {
		return nil, fmt.Errorf("%w: the pages claim %d shared object reference(s) and only %d "+
			"bits remain, so most of them describe nothing",
			errHintTable, total, r.left())
	}
	if want := total * int64(h.NBitsSharedIdent+h.NBitsSharedNumerator); want > r.left() {
		return nil, fmt.Errorf("%w: the pages claim %d shared object reference(s), which need "+
			"%d bits, and %d remain", errHintTable, total, want, r.left())
	}
	r.align()

	// (d) Item 4 for all shared objects in the second page, then the third,
	// and so on.
	//
	// ⛔⛔ THESE READS RETURN THEIR ERRORS, and an earlier version discarded
	// them on a proof that was WRONG. See the bound above: the alignment
	// between this group and the next makes real consumption up to seven bits
	// more than the bound allows, so a read here CAN fail — and discarding the
	// error made a truncated table decode silently with a fabricated
	// numerator. A justification is worth less than a returned error.
	for i := 1; i < pages; i++ {
		out[i].Shared = make([]sharedRef, counts[i])
		for j := range out[i].Shared {
			v, err := read(h.NBitsSharedIdent, "item 4, a shared object identifier", i)
			if err != nil {
				return nil, err
			}
			out[i].Shared[j].Identifier = v
		}
	}
	r.align()

	// (e) Item 5 for the same, in the same order — "which shall be in the same
	// order as the preceding item".
	for i := 1; i < pages; i++ {
		for j := range out[i].Shared {
			v, err := read(h.NBitsSharedNumerator, "item 5, a fractional position", i)
			if err != nil {
				return nil, err
			}
			out[i].Shared[j].Numerator = v
		}
	}
	r.align()

	// (f) Item 6 for all pages: where the content stream starts within the
	// page.
	for i := range out {
		d, err := read(h.NBitsDeltaContentOff, "item 6, the content stream offset", i)
		if err != nil {
			return nil, err
		}
		out[i].ContentOffset = h.MinContentOffset + d
	}
	r.align()

	// (g) Item 7 for all pages: the content stream's length.
	for i := range out {
		d, err := read(h.NBitsDeltaContentLen, "item 7, the content stream length", i)
		if err != nil {
			return nil, err
		}
		out[i].ContentLength = h.MinContentLength + d
	}

	return out, nil
}

// sharedHeader is the shared object hint table's header section, Table F.5's
// seven items in order. The field names are qpdf's, for the reason given on
// pageOffsetHeader.
type sharedHeader struct {
	FirstSharedObj        uint32 // item 1: first object number in part 8
	FirstSharedOffset     uint32 // item 2: that object's location
	NSharedFirstPage      uint32 // item 3: entries for the first page
	NSharedTotal          uint32 // item 4: entries in all, INCLUDING item 3
	NBitsNObjects         int    // item 5: bits for a group's object count
	MinGroupLength        uint32 // item 6: least group length in bytes
	NBitsDeltaGroupLength int    // item 7: bits for the group-length delta
}

// sharedGroup is one Table F.6 entry: a run of adjacent objects of which only
// the first is referenced from outside (F.4.2).
type sharedGroup struct {
	Length    uint32 // item 1 + Table F.5 item 6
	Signature []byte // item 3: a 16-byte MD5, nil when item 2's flag is 0
	NObjects  uint32 // item 4 + 1, because item 4 is "1 less than the number"
}

// readSharedTable reads the shared object hint table, which Table F.2 marks
// Required and /S locates.
//
// ⛔ /S IS A BYTE OFFSET INTO THE DECODED STREAM, which is why the reader
// seeks rather than continues: F.4.2's table does not begin where the page
// offset table happens to end. CheckLinearized has already refused an /S below
// 36 or past the data, so by the time this runs the position is inside the
// stream — and seekByte refuses it again rather than trusting that.
// ⛔⛔ maxGroups IS THE DOCUMENT'S OBJECT COUNT, AND IT IS A MEMORY BOUND
// RATHER THAN A PARSING ONE. Review measured the gap it closes: the bit bound
// below costs one bit per group, while each group is a 32-byte struct, so 2 MiB
// of zeros declaring 2^24-1000 groups returned no error and allocated 656 MiB
// — a 256-fold amplification that a 1 GiB decoded stream (a trivial deflate
// bomb) takes into the hundreds of gigabytes.
//
// ⛔ A BIT BOUND CANNOT FIX THIS, because the input is not lying: 2 MiB really
// does hold 16.7 million one-bit flags. What is absurd is not the bit count but
// the CLAIM — F.4.2's groups describe "a group of adjacent objects", so a table
// cannot name more groups than the document has objects. That ceiling comes
// from the cross-reference table, which is outside this function, so it is
// passed in.
func readSharedTable(r *bits, at int, maxGroups int64) (*sharedHeader, []sharedGroup, error) {
	if err := r.seekByte(at); err != nil {
		return nil, nil, err
	}

	// Table F.5, items 1..7, widths in bits. Their sum is 192 bits, which is
	// where sharedTableHeaderBytes comes from.
	widths := []int{32, 32, 32, 32, 16, 32, 16}
	v := make([]uint32, len(widths))
	for i, n := range widths {
		got, err := r.read(n)
		if err != nil {
			return nil, nil, fmt.Errorf("%w (Table F.5, item %d of 7)", err, i+1)
		}
		v[i] = got
	}
	h := &sharedHeader{
		FirstSharedObj:        v[0],
		FirstSharedOffset:     v[1],
		NSharedFirstPage:      v[2],
		NSharedTotal:          v[3],
		NBitsNObjects:         int(v[4]),
		MinGroupLength:        v[5],
		NBitsDeltaGroupLength: int(v[6]),
	}

	// ⛔ The two width items are checked before they are used, for the reason
	// Table F.3's are: they are 16 bits wide and so can hold a number that is
	// not a width.
	for _, c := range []struct {
		name string
		item int
		n    int
	}{
		{"nbits_nobjects", 5, h.NBitsNObjects},
		{"nbits_delta_group_length", 7, h.NBitsDeltaGroupLength},
	} {
		if c.n > 32 {
			return nil, nil, fmt.Errorf("%w: %s is %d, and F.4.1 bounds every bit width to "+
				"0..32 (Table F.5, item %d)", errHintTable, c.name, c.n, c.item)
		}
	}

	// ⛔⛔ ITEM 4 INCLUDES ITEM 3, which F.4.2 states and which a reader that
	// added them would get wrong: item 4 is "the number of shared object
	// entries for the shared objects section, INCLUDING the number of shared
	// object entries for the first page (that is, the value of item 3)". So
	// the total is item 4, and the second sequence holds item 4 - item 3.
	if h.NSharedTotal < h.NSharedFirstPage {
		return nil, nil, fmt.Errorf("%w: the shared object table says %d entries in all and %d "+
			"for the first page, and Table F.5 item 4 includes item 3",
			errHintTable, h.NSharedTotal, h.NSharedFirstPage)
	}

	total := int64(h.NSharedTotal)
	// ⛔ Bounded before it is used, as the per-page counts are: item 4 is 32
	// bits, so it can claim four billion groups, each costing up to three
	// reads.
	//
	// ⛔⛔ AND IT COUNTS ONLY ITEM 2's ONE BIT PER GROUP, DELIBERATELY, not the
	// lengths as well. Every group carries a signature flag — item 2 is a
	// fixed one bit, the only item here with no configurable width — so one
	// bit per group is a bound that always holds and never over-reaches.
	// Including item 1's width would make groups (a) and (b)'s own read errors
	// unreachable, which is the mistake the page offset table's bound was
	// corrected for: an exact bound is a stronger claim than the job needs,
	// and a branch no input can take is a line claiming to be tested.
	if total > r.left() {
		return nil, nil, fmt.Errorf("%w: the table claims %d shared object group(s) and only "+
			"%d bits remain, and every group carries at least item 2's one-bit flag",
			errHintTable, total, r.left())
	}
	// ⛔⛔ AND THE CLAIM IS CAPPED BY THE DOCUMENT'S OBJECTS, which is what stops
	// one bit of input becoming a 32-byte struct. See the note on maxGroups:
	// the bit bound above is satisfied by 16.7 million groups in 2 MiB, and
	// allocating them cost 656 MiB.
	if total > maxGroups {
		return nil, nil, fmt.Errorf("%w: the table claims %d shared object group(s) and the "+
			"document has %d object(s) — F.4.2's groups describe objects, so there cannot be "+
			"more groups than there are", errHintTable, total, maxGroups)
	}

	// ⛔⛔ AND THE SECOND SEQUENCE IS REFUSED RATHER THAN GUESSED AT. F.4.2 says
	// "There shall be two sequences of shared object group entries: the ones
	// for objects located in the first page, followed by the ones for objects
	// located in the shared objects section", and then "The order of items in
	// EACH SEQUENCE shall be as follows". Read strictly, each sequence is
	// column-major and aligned on its own, which differs from one run of
	// nshared_total entries whenever the first sequence's widths do not end on
	// a byte.
	//
	// ⛔ NO FILE IN THIS PACKAGE'S CORPUS CAN SETTLE IT: every fixture has
	// nshared_total == nshared_first_page, so the second sequence is empty
	// throughout and both readings agree. Decoding it under a guess would
	// produce group lengths and object counts that parse and are wrong — the
	// silent failure this whole arc exists to avoid — so a non-empty second
	// sequence is refused, by name, until a file that exercises it exists.
	if h.NSharedTotal > h.NSharedFirstPage {
		return nil, nil, fmt.Errorf("%w: the table has %d group(s) for the shared objects "+
			"section beyond the first page's %d, and F.4.2's two sequences may each be "+
			"column-major on their own — no file in this package's corpus settles which, so "+
			"this is refused rather than decoded under a guess",
			errHintTable, h.NSharedTotal-h.NSharedFirstPage, h.NSharedFirstPage)
	}

	groups := make([]sharedGroup, total)

	// ⛔⛔ COLUMN-MAJOR AGAIN, and byte-aligned between groups as F.4.1's are:
	// F.4.2 says "the items making up each shared object group entry need not
	// be contiguous; they may be broken up with items from entries for other
	// shared object groups", then gives the order (a) to (d). The alignment is
	// the same undocumented producer convention the page offset table uses —
	// measured, not read.
	//
	// (a) Item 1 for every group: the length, as a delta on Table F.5 item 6.
	for i := range groups {
		d, err := r.read(h.NBitsDeltaGroupLength)
		if err != nil {
			return nil, nil, fmt.Errorf("%w (Table F.6 item 1, the group length, for group %d "+
				"of %d)", err, i+1, total)
		}
		groups[i].Length = h.MinGroupLength + d
	}
	r.align()

	// (b) Item 2 for every group: one bit saying whether a signature follows.
	flags := make([]bool, total)
	for i := range groups {
		f, err := r.read(1)
		if err != nil {
			return nil, nil, fmt.Errorf("%w (Table F.6 item 2, the signature flag, for group %d "+
				"of %d)", err, i+1, total)
		}
		flags[i] = f == 1
	}
	r.align()

	// (c) Item 3 for every group that has one: a 16-byte MD5.
	//
	// ⛔ ONLY WHERE THE FLAG IS SET, which is what makes item 2 worth reading
	// rather than skipping: a reader that always consumed 128 bits here would
	// be 16 bytes out of step for every group after the first unsigned one.
	for i := range groups {
		if !flags[i] {
			continue
		}
		sig := make([]byte, 16)
		for j := range sig {
			b, err := r.read(8)
			if err != nil {
				return nil, nil, fmt.Errorf("%w (Table F.6 item 3, the signature, for group %d "+
					"of %d)", err, i+1, total)
			}
			sig[j] = byte(b)
		}
		groups[i].Signature = sig
	}
	r.align()

	// (d) Item 4 for every group: "a number equal to 1 less than the number of
	// objects in the group".
	//
	// ⛔ THE +1 IS THE ANNEX'S, not a convenience. A group of one object
	// stores 0, so a reader that kept the raw value would report every group
	// as holding one object too few — and a group of zero objects is not a
	// thing F.4.2 describes.
	for i := range groups {
		n, err := r.read(h.NBitsNObjects)
		if err != nil {
			return nil, nil, fmt.Errorf("%w (Table F.6 item 4, the object count, for group %d "+
				"of %d)", err, i+1, total)
		}
		groups[i].NObjects = n + 1
	}

	return h, groups, nil
}

// hintTables is what this package reads out of a primary hint stream.
type hintTables struct {
	pageOffset   *pageOffsetHeader
	pages        []pageEntry
	shared       *sharedHeader
	sharedGroups []sharedGroup
}

// readHintTables decodes the hint stream at /H and returns what it holds.
//
// ⛔⛔ THIS IS THE FIRST PLACE THIS PACKAGE DECODES A HINT STREAM, and #40 is
// explicit that it is the step the cheap checks could not take: "a file can
// pass this and still have hint tables that send a reader to the wrong
// offset". The cost is the one the /S upper bound declined to pay — decoding
// an attacker's bytes — and it is paid here because reading the tables IS the
// purpose. The expansion cap in filter.go bounds it.
func (d *Document) readHintTables(lin *Linearization) (*hintTables, error) {
	// ⛔⛔ THE OFFSET IS CHECKED BEFORE IT SLICES, which it was not: review ran
	// this with /H at byte 99 of a three-byte buffer and it PANICKED. Every
	// other refusal in this file is a message; that one was a crash on a
	// number taken straight from an untrusted dictionary.
	//
	// ⛔ "It is only called after CheckLinearized" is not a defence. A
	// function that panics on its argument is one nobody may call in a new
	// place, and the next caller will not know that. The check costs two
	// comparisons.
	if lin.HintOffset < 0 || lin.HintOffset >= len(d.buf) {
		return nil, fmt.Errorf("%w: /H puts the hint stream at byte %d of a %d-byte file",
			errHintTable, lin.HintOffset, len(d.buf))
	}
	_, o, _, err := ParseIndirectObject(d.buf[lin.HintOffset:], d.Get)
	if err != nil {
		return nil, fmt.Errorf("%w: the hint stream at byte %d does not parse: %v",
			errHintTable, lin.HintOffset, err)
	}
	s, ok := o.(*Stream)
	if !ok {
		return nil, fmt.Errorf("%w: the object at byte %d is a %s, not a stream",
			errHintTable, lin.HintOffset, o.Kind())
	}
	data, _, err := d.DecodeStream(s)
	if err != nil {
		return nil, fmt.Errorf("%w: the hint stream does not decode: %v", errHintTable, err)
	}

	r := &bits{b: data}
	h, err := readPageOffsetHeader(r)
	if err != nil {
		return nil, err
	}
	// ⛔ THE PAGE COUNT COMES FROM /N AND NOT FROM THE TABLE, because the table
	// does not carry one: Table F.4's entries are a sequence whose length only
	// the parameter dictionary states. CheckLinearized has already compared /N
	// against the document, so by the time this runs the count is one the file
	// agrees with — and a table written for a different number of pages is
	// exactly what that comparison protects this from.
	entries, err := readPageEntries(r, h, lin.PageCount)
	if err != nil {
		return nil, err
	}

	// ⛔ /S COMES FROM THE STREAM'S OWN DICTIONARY, not from the table: F.3.6
	// makes it a dictionary entry, and checkHintStream has already required it
	// to be a direct integer at or above 36 with room for its 24-byte header.
	sv, ok := s.Dict["S"]
	if !ok {
		return nil, fmt.Errorf("%w: the primary hint stream has no /S, so the shared object "+
			"table cannot be located", errHintTable)
	}
	pos, ok := sv.(Integer)
	if !ok {
		return nil, fmt.Errorf("%w: /S is %T, not a direct integer", errHintTable, sv)
	}
	// ⛔ The object count is the cap on how many groups the table may claim;
	// see readSharedTable's note on maxGroups. len(d.xref) is the document's
	// own count, and a table naming more groups than that is describing
	// objects the file does not have.
	sh, groups, err := readSharedTable(r, int(pos), int64(len(d.xref)))
	if err != nil {
		return nil, err
	}

	return &hintTables{
		pageOffset:   h,
		pages:        entries,
		shared:       sh,
		sharedGroups: groups,
	}, nil
}

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

// hintTables is what this package reads out of a primary hint stream.
type hintTables struct {
	pageOffset *pageOffsetHeader
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
	return &hintTables{pageOffset: h}, nil
}

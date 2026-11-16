// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"fmt"
	"math"
)

// ⛔⛔ Pages[i][0] IS NOT THE PAGE OBJECT, which is worth recording where the
// extents are built because the first version of measurePages assumed it was.
// Partition builds each set with the page's own object first and then calls
// sort.Ints on it, so index 0 is the LOWEST OBJECT NUMBER. On the fixtures here
// the page object often is the lowest, so the assumption would have measured
// correctly on every shape in the corpus and been wrong on a document whose
// content stream was numbered below its page. d.pageRefs() is the authority for
// which object is the page, and partition.go:146 is where Partition itself
// takes it from.

// pageExtent is what the emission measured about one page: how many objects it
// has and where their bytes are. It is kept separate from pageEntry because
// pageEntry speaks Table F.4's deltas and this speaks the file.
type pageExtent struct {
	objects int

	// start and end bracket every object of the page, and length is the SUM of
	// their individual lengths.
	//
	// ⛔⛔ THE TWO ARE NOT THE SAME NUMBER, and Table F.4 item 2 is the sum.
	// qpdf's checker computes a page's length with Lin::lengthNextN, which adds
	// up (end - start) per object and counts no gap between them; an extent
	// would include anything that landed in the middle. With the page-major
	// ordering nothing does, so they agree here — but they agree as a
	// consequence of the layout, not by definition, and `start` and `end` are
	// kept because the content stream's offset is relative to the page's first
	// byte.
	start, end, length int

	// contentStart and contentEnd bracket the page's content streams.
	// hasContent is false for a page with no /Contents, which is a legal page
	// that draws nothing.
	contentStart, contentEnd int
	hasContent               bool
}

// ⛔⛔ THESE WIDTHS ARE FIXED, AND THAT IS THE WHOLE REASON THE FILE CONVERGES.
// The hint stream sits before the first page, so if its LENGTH changed between
// the two passes every offset after it would move and the second pass would
// describe a file that no longer exists.
//
// ⭐ The fix is to make the encoded length depend on nothing an offset can
// change. Of Table F.4's seven items only three are measured in bytes — item 2
// (page length), item 6 (content offset) and item 7 (content length) — and a
// width derived from THOSE would move with the layout. Pinning them at 32 bits
// makes the table's length a function of the page count and the shared-reference
// counts alone, and neither depends on where anything landed.
//
// ⛔ The other widths are still derived from the data, because they are already
// pass-invariant: an object count is the same number wherever the bytes go. So
// the table stays compact where it can.
//
// ⚠ qpdf solves the same problem differently, by PADDING the hint stream to a
// fixed size — `static int const pad = 200;` in its source, which this arc read
// in #63. Padding works, and it is what this writer would need if any
// byte-measured width were derived; 32-bit deltas cost four bytes per page per
// item and buy an exact fixpoint with nothing reserved.
const hintByteWidth = 32

// ⛔⛔ TWO SEAMS, FOR TWO ERRORS buildHintTables CANNOT PRODUCE ON ITS OWN.
// writePageOffsetTable refuses an empty page list, a first page carrying shared
// references, a bit width outside 0..32 and a delta below its minimum — and
// buildHintTables constructs none of those: it guards the empty list first,
// never sets Shared, pins or derives every width inside the bound, and takes
// each minimum from the very entries it subtracts it from. writeSharedTable is
// handed a literal header with no groups, which cannot fail either.
//
// ⛔ The error paths are kept rather than dropped, because the encoders are
// shared with the rest of the package and a later caller of THIS function will
// supply shared references. But an untested propagation is an untested claim,
// and this package's coverage gate refuses one — so the tests replace these two
// vars instead of pretending the inputs can be malformed.
var (
	writePageOffset = writePageOffsetTable
	writeShared     = writeSharedTable
)

// buildHintTables derives Annex F's two hint tables from the extents the
// emission measured, and returns the hint stream's data with /S, the byte offset
// at which the shared object hint table begins.
//
// ⛔ IT IS GIVEN THE PREVIOUS PASS'S EXTENTS AND THAT IS NOT A DEFECT. On the
// first pass the extents are of a file whose hint stream held zeros, so the
// values are wrong; the LENGTH is right, because of hintByteWidth above, so
// nothing moves and the second pass's values describe the second pass's file
// exactly. This is the same two-pass argument as /L and /T, one table along.
func buildHintTables(pages []pageExtent, firstPageObjOffset, hintLen int) ([]byte, int, error) {
	if len(pages) == 0 {
		return nil, 0, fmt.Errorf("%w: a hint table for no pages", errHintTable)
	}

	entries := make([]pageEntry, len(pages))
	for i, p := range pages {
		if p.objects <= 0 {
			return nil, 0, fmt.Errorf("%w: page %d has %d object(s), and every page has at "+
				"least its own page object", errHintTable, i+1, p.objects)
		}
		if p.end < p.start {
			return nil, 0, fmt.Errorf("%w: page %d ends at %d and starts at %d",
				errHintTable, i+1, p.end, p.start)
		}
		if p.length < 0 {
			return nil, 0, fmt.Errorf("%w: page %d's objects sum to %d bytes",
				errHintTable, i+1, p.length)
		}
		// ⛔⛔ A VALUE TOO WIDE FOR ITS FIELD IS REFUSED, NOT TRUNCATED, and this
		// file had already named that class three times before doing it:
		// firstPageOffsetInFile's own comment cites #42 — "a value this build
		// cannot address is refused, not truncated" — and #53 and #58 are the
		// same lesson. Table F.4's items are 32-bit, so a page past 4 GiB wrapped
		// SILENTLY and the hint table would have sent a reader to the wrong
		// offset in the one structure whose whole job is to be trusted.
		//
		// ⛔ No fixture can reach this: a 4 GiB document is not something this
		// suite builds. That is the reason to refuse rather than to cast — an
		// untestable truncation is a defect waiting for someone else's file.
		for _, c := range []struct {
			name string
			v    int
		}{
			{"object count", p.objects},
			{"length in bytes", p.length},
			{"content stream offset", p.contentEnd - p.start},
			{"content stream length", p.contentEnd - p.contentStart},
		} {
			if int64(c.v) > math.MaxUint32 {
				return nil, 0, fmt.Errorf("%w: page %d's %s is %d, and Table F.4 holds up "+
					"to %d", errHintTable, i+1, c.name, c.v, uint32(math.MaxUint32))
			}
		}
		e := pageEntry{
			NObjects: uint32(p.objects),
			Length:   uint32(p.length),
		}
		// ⛔ A PAGE WITH NO CONTENT STREAM GETS ZEROS, not the page's own
		// extent. Items 6 and 7 name the content stream; a page whose
		// /Contents is absent has none, and saying "it is the whole page" would
		// send a streaming reader to fetch bytes that draw nothing.
		if p.hasContent {
			if p.contentStart < p.start || p.contentEnd < p.contentStart ||
				p.contentEnd > p.end {
				return nil, 0, fmt.Errorf("%w: page %d spans %d..%d and its content stream "+
					"%d..%d, which is not inside it", errHintTable, i+1,
					p.start, p.end, p.contentStart, p.contentEnd)
			}
			e.ContentOffset = uint32(p.contentStart - p.start)
			e.ContentLength = uint32(p.contentEnd - p.contentStart)
		}
		entries[i] = e
	}

	// ⛔ THE STORED ITEM 2 IS NOT THE FIRST PAGE'S OFFSET. F.4 computes it as if
	// the primary hint stream were not in the file, and this writer puts the
	// hint stream BEFORE the first page, so the stored value is the offset less
	// the stream's length. firstPageOffsetInFile adds it back when /H lies at or
	// before the stored value, which it does here by construction: the stream
	// ends before the first page's object begins.
	if firstPageObjOffset < hintLen {
		return nil, 0, fmt.Errorf("%w: the first page's object is at %d and the hint stream "+
			"is %d bytes, so item 2 would go negative", errHintTable,
			firstPageObjOffset, hintLen)
	}
	if int64(firstPageObjOffset-hintLen) > math.MaxUint32 {
		return nil, 0, fmt.Errorf("%w: item 2 would be %d, and Table F.3 holds up to %d",
			errHintTable, firstPageObjOffset-hintLen, uint32(math.MaxUint32))
	}

	nobjects := func(e pageEntry) uint32 { return e.NObjects }
	length := func(e pageEntry) uint32 { return e.Length }
	contentOff := func(e pageEntry) uint32 { return e.ContentOffset }
	contentLen := func(e pageEntry) uint32 { return e.ContentLength }
	nshared := func(e pageEntry) uint32 { return uint32(len(e.Shared)) }

	leastObjects := leastOf(entries, nobjects)
	h := &pageOffsetHeader{
		MinNObjects:      leastObjects,
		FirstPageOffset:  uint32(firstPageObjOffset - hintLen),
		MinPageLength:    leastOf(entries, length),
		MinContentOffset: leastOf(entries, contentOff),
		MinContentLength: leastOf(entries, contentLen),

		// Derived: pass-invariant, so they may be as narrow as the data allows.
		NBitsDeltaNObjects:  widthFor(entries, nobjects, leastObjects),
		NBitsNSharedObjects: widthFor(entries, nshared, 0),

		// Fixed: measured in bytes, so a derived width would move the stream.
		NBitsDeltaPageLength: hintByteWidth,
		NBitsDeltaContentOff: hintByteWidth,
		NBitsDeltaContentLen: hintByteWidth,

		// ⛔ NO SHARED REFERENCES AT ALL, so items 11 and 12 describe nothing and
		// groups (d) and (e) write nothing. The widths are still declared,
		// because Table F.3's header is fixed-width and the reader reads them
		// whether or not any entry uses them.
		NBitsSharedIdent:     1,
		NBitsSharedNumerator: 1,

		// ⛔ ITEM 13 IS 1 AND NOT 0. It is the denominator of a fractional
		// position, and a reader that divides by it would divide by zero. With
		// no shared references nothing reads it, so this is a safe value rather
		// than a meaningful one — which is worth more than a zero that looks
		// deliberate.
		SharedDenominator: 1,
	}

	offsetTable, err := writePageOffset(h, entries)
	if err != nil {
		return nil, 0, err
	}

	// ⛔⛔ THE SHARED TABLE IS REQUIRED EVEN WITH NOTHING TO SAY. Table F.2 marks
	// it Required and /S locates it, so a hint stream without one is malformed
	// however empty the document's part 8 is. This writer's partition puts
	// nothing in part 8 — measured in #64, where part 8 held zero objects on
	// every shape — so the table is a header and no groups.
	shared, err := writeShared(&sharedHeader{
		// ⛔ ITEMS 1 AND 2 LOCATE PART 8, WHICH DOES NOT EXIST HERE. With zero
		// groups nothing dereferences them, and naming an object that is not
		// shared would be a worse lie than naming none.
		FirstSharedObj:    0,
		FirstSharedOffset: 0,
		NSharedFirstPage:  0,
		NSharedTotal:      0,
		MinGroupLength:    0,
		// ⛔ 1 AND NOT 0 FOR THE SAME REASON AS ITEMS 11 AND 12 ABOVE.
		NBitsNObjects:         1,
		NBitsDeltaGroupLength: 1,
	}, nil)
	if err != nil {
		return nil, 0, err
	}

	return append(offsetTable, shared...), len(offsetTable), nil
}

func leastOf(entries []pageEntry, of func(pageEntry) uint32) uint32 {
	least := of(entries[0])
	for _, e := range entries[1:] {
		if v := of(e); v < least {
			least = v
		}
	}
	return least
}

// widthFor returns the bits needed for the largest delta, and never 0.
//
// ⛔ NEVER 0, DELIBERATELY. A width of zero is legal in F.4.1's 0..32 and means
// "every delta is the minimum", which is true whenever a column is constant —
// the common case for a one-page table. But bitw.write(0, 0) is a path no
// fixture in this package has ever exercised, and a hint stream is not the place
// to find out what it does. One bit costs one bit per page.
func widthFor(entries []pageEntry, of func(pageEntry) uint32, least uint32) int {
	widest := uint32(0)
	for _, e := range entries {
		if d := of(e) - least; d > widest {
			widest = d
		}
	}
	// ⛔⛔ THE BOUND IS ON n, NOT ON THE COMPARISON, because `1<<n` takes
	// `widest`'s type: at n == 32 a uint32 shift gives 0, the comparison is
	// always true, and the loop never ends. Measured on a delta of 2^31 — it
	// spins. No caller reaches it today, since the three byte-measured widths
	// are pinned and the derived ones are counts; this is the function a derived
	// byte width would go through, which is exactly when a 32-bit delta appears.
	//
	// ⛔ F.4.1 bounds every width to 0..32, so 32 is both the ceiling and the
	// widest a uint32 delta can need.
	n := 1
	for n < 32 && widest >= 1<<n {
		n++
	}
	return n
}

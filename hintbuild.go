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

	// shared is Table F.4's items 3, 4 and 5 for this page: one reference per
	// shared object it reaches. It is empty for the first page, which item 3
	// requires.
	shared []sharedRef
}

// sharedInput is what the shared object hint table needs beyond the plan: the
// measured groups, and where part 8 begins.
type sharedInput struct {
	// lengths is one byte length per planned object, in table order. It is int
	// rather than the uint32 a group stores, because the conversion is where a
	// document larger than 4 GiB truncates and buildHintTables is where this
	// package refuses such a document — the same division as pageExtent, whose
	// guards are reachable from a test that calls the builder directly.
	//
	// ⛔ ONE OBJECT PER GROUP, so this slice determines the groups entirely and
	// the builder constructs them. It used to carry []sharedGroup with
	// NObjects: 1 written by the caller; external review found the resulting
	// uint32 conversion unguarded, and a length the builder cannot see is a
	// length it cannot refuse.
	lengths []int

	// firstObj and firstOff are Table F.5's items 1 and 2, which locate part 8.
	// Both are 0 when part 8 is empty — there is then no first shared object to
	// name, and no group dereferences them.
	firstObj, firstOff int

	// firstPage is item 3, and len(lengths) is item 4.
	firstPage int
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
func buildHintTables(pages []pageExtent, shared sharedInput,
	firstPageObjOffset, hintLen int) ([]byte, int, error) {
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
			// ⛔⛔ THE GUARD NAMES THE QUANTITY IT STORES. The first version
			// checked contentEnd-start for item 6 while the stored value is
			// contentStart-start: conservative, so no truncation escaped, but it
			// REFUSED a describable page — offset 3e9 and length 3e9 both fit a
			// uint32 and their sum does not. And the message printed 6e9 under
			// the name "content stream offset", which is neither value.
			//
			// ⛔ The test could not see it either: its stored offset was 1, and
			// a `want` of "content stream" matched both guard names. They are
			// asserted separately now.
			{"content stream offset", p.contentStart - p.start},
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
		// ⛔ THE FIRST PAGE CARRIES NONE, which Table F.4 item 3 requires and
		// writePageOffsetTable refuses otherwise. Partition leaves Shared[0]
		// empty for the same reason, so this is a second statement of one rule
		// rather than a correction of the first.
		if i > 0 {
			e.Shared = p.shared
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

		// ⛔ ITEM 11 IS DERIVED AND PASS-INVARIANT: an identifier is an index
		// into the shared table, which depends on which objects are shared and
		// not on where any byte landed.
		NBitsSharedIdent: identifierWidth(entries),

		// ⛔ ITEM 12 IS 1 BECAUSE EVERY NUMERATOR IS 0 — see refsFor: this
		// writer says a shared object is used "at the beginning of the page",
		// which costs a reader an early fetch and never a late one. widthFor
		// would return 1 for a constant column anyway; this says why.
		NBitsSharedNumerator: 1,

		// ⛔ ITEM 13 IS 1 AND NOT 0. It is the denominator of that fraction, and
		// a reader that divides by it would divide by zero.
		SharedDenominator: 1,
	}

	offsetTable, err := writePageOffset(h, entries)
	if err != nil {
		return nil, 0, err
	}

	// ⛔⛔ THE SHARED SIDE IS REFUSED ABOVE 32 BITS TOO, and it was not until
	// external review: the page guard above covers a page's own quantities,
	// which stay small in a document whose SIZE is what overflows. Part 8's
	// offset is the opposite case — every page can be a few kilobytes while the
	// offset of the shared section is past 4 GiB — so a file larger than
	// MaxUint32 wrote a wrapped item 2 and said nothing.
	//
	// ⛔ Item 2 is checked on the ADJUSTED value, because that is what is
	// stored: the hint stream's length is subtracted first, and refusing the
	// raw offset would refuse a file whose stored value fits.
	groups := make([]sharedGroup, len(shared.lengths))
	for i, n := range shared.lengths {
		if int64(n) > math.MaxUint32 {
			return nil, 0, fmt.Errorf("%w: shared group %d is %d bytes, and Table F.6 "+
				"holds up to %d", errHintTable, i, n, uint32(math.MaxUint32))
		}
		groups[i] = sharedGroup{NObjects: 1, Length: uint32(n)}
	}
	for _, c := range []struct {
		name string
		v    int
	}{
		{"first shared object's number", shared.firstObj},
		{"first shared object's offset", sharedOffset(shared.firstOff, hintLen)},
	} {
		if int64(c.v) > math.MaxUint32 {
			return nil, 0, fmt.Errorf("%w: the %s is %d, and Table F.5 holds up to %d",
				errHintTable, c.name, c.v, uint32(math.MaxUint32))
		}
	}

	// ⛔⛔ THE SHARED TABLE IS REQUIRED EVEN WITH NOTHING TO SAY. Table F.2 marks
	// it Required and /S locates it, so a hint stream without one is malformed
	// however empty the document's part 8 is.
	hdr := &sharedHeader{
		// ⛔ ITEMS 1 AND 2 LOCATE PART 8, and are 0 when it is empty: there is
		// then no first shared object to name, and no group dereferences them.
		// Naming an object that is not shared would be a worse lie than naming
		// none.
		FirstSharedObj: uint32(shared.firstObj),

		// ⛔⛔ ITEM 2 IS STORED AS IF THE HINT STREAM WERE NOT THERE, exactly
		// like Table F.3's item 2 above — and the first version wrote the raw
		// offset. qpdf named the difference to the byte:
		//
		//	first shared object offset mismatch: hint table = 1472;
		//	computed = 1300
		//
		// and /H's length for that file is 172, which is 1472 - 1300. The two
		// adjustments are kept side by side because they are one convention:
		// an offset in these tables is measured in a file from which the
		// primary hint stream has been removed.
		FirstSharedOffset: uint32(sharedOffset(shared.firstOff, hintLen)),

		// ⛔ ITEM 4 INCLUDES ITEM 3, which writeSharedTable refuses otherwise:
		// the groups are ONE run of entries, the first item-3 of them being the
		// first page's.
		NSharedFirstPage: uint32(shared.firstPage),
		NSharedTotal:     uint32(len(groups)),

		// ⛔ Derived and pass-invariant: every group covers one object here, so
		// this is 1 whatever the layout does.
		NBitsNObjects: groupCountWidth(groups),

		MinGroupLength: leastGroup(groups),
		// ⛔ FIXED, for the same reason as the three page widths: a group's
		// length is measured in BYTES, so a width derived from it would change
		// with the layout and move the stream between the two passes.
		NBitsDeltaGroupLength: hintByteWidth,
	}
	// ⛔ NO `len(groups) == 0` SPECIAL CASE. One stood here setting
	// NBitsNObjects to 1 "because a width of 0 is a path bitw has never been
	// asked for" — but groupCountWidth(nil) already returns 1, as its own test
	// row asserts, so the block restated its result and could never change an
	// emitted byte. Removed on external review, by the standard that removed
	// sharedOffset's clamp: a branch that cannot change the output is a claim
	// nothing can falsify.

	table, err := writeShared(hdr, groups)
	if err != nil {
		return nil, 0, err
	}

	return append(offsetTable, table...), len(offsetTable), nil
}

// identifierWidth returns the bits an identifier needs: Table F.4 item 4 is an
// index into the shared table, so the widest index any page names decides it.
//
// ⛔ NEVER 0, like widthFor and for the same reason — and 1 when no page names
// anything, because the field is still declared in a fixed-width header.
func identifierWidth(entries []pageEntry) int {
	widest := uint32(0)
	for _, e := range entries {
		for _, r := range e.Shared {
			if r.Identifier > widest {
				widest = r.Identifier
			}
		}
	}
	n := 1
	for n < 32 && widest >= 1<<n {
		n++
	}
	return n
}

// groupCountWidth returns the bits Table F.5 item 5 needs for a group's object
// count, which this writer always makes 1.
func groupCountWidth(groups []sharedGroup) int {
	widest := uint32(0)
	for _, g := range groups {
		// Table F.6 item 4 stores "1 less than the number", which is what the
		// width has to hold.
		if g.NObjects > 0 && g.NObjects-1 > widest {
			widest = g.NObjects - 1
		}
	}
	n := 1
	for n < 32 && widest >= 1<<n {
		n++
	}
	return n
}

// sharedOffset adjusts Table F.5 item 2 the way item 2 of Table F.3 is
// adjusted: the stored value is measured in a file without the hint stream.
//
// ⛔ ZERO STAYS ZERO. With part 8 empty there is no first shared object, items
// 1 and 2 are both 0, and subtracting the stream's length from 0 would name a
// byte before the file begins.
//
// ⛔⛔ AND THERE IS NO `at < hintLen` CLAMP. One was written here, returning 0
// for an offset that cannot arise — part 8 is emitted after the hint stream, so
// `at` is past it whenever it is not zero. Nothing could reach the branch, so
// nothing measured it, and what it did was turn a wrong offset into the one
// value that means "there is no shared object".
//
// ⛔⛔⛔ AND WHAT CATCHES A WRONG VALUE HERE IS QPDF, NOT US. This comment said
// "CheckLinearized recomputes this offset and says `first shared object offset
// mismatch`". It does not: FirstSharedObj and FirstSharedOffset are written,
// parsed and compared in a hint-table test, and no checker recomputes either.
// That sentence is qpdf's wording, quoted from its output and credited to our
// own code — found by external review, then confirmed by mutation:
// `return at` here leaves CheckLinearized silent and makes qpdf say
//
//	WARNING: first shared object offset mismatch: hint table = 1472; computed = 1300
//
// ⚠ So the honest division is: our checker (#67) caught the GROUP LENGTH
// defect, qpdf caught this one. Recomputing item 2 in CheckLinearized would
// close the gap and is not in this change.
func sharedOffset(at, hintLen int) int {
	if at == 0 {
		return 0
	}
	return at - hintLen
}

func leastGroup(groups []sharedGroup) uint32 {
	if len(groups) == 0 {
		return 0
	}
	least := groups[0].Length
	for _, g := range groups[1:] {
		if g.Length < least {
			least = g.Length
		}
	}
	return least
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

// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import "fmt"

// bitw writes a big-endian bit stream, which is the inverse of bits.
//
// ⛔⛔ IT IS THE SAME SHAPE AS THE READER ON PURPOSE, down to align() meaning
// "pad to the next byte with zeroes". Annex F.4's tables are bit-packed and
// their groups are byte-aligned by a producer convention this package measured
// (see readPageEntries), so an encoder that padded with anything else, or
// padded in different places, would produce a table qpdf reads as different
// numbers rather than one it rejects.
type bitw struct {
	b   []byte
	pos int64 // the next bit to write, counted from the first bit of b
}

// write appends the low n bits of v, most significant first.
//
// ⛔ n == 0 WRITES NOTHING, which the tables need: Table F.3's bit-count items
// are legitimately 0 when every page agrees, and qpdf emits
// nbits_delta_nobjects 0 for a one-page document. An encoder that wrote a
// single bit for a zero-width field would shift every item after it.
func (w *bitw) write(n int, v uint32) error {
	if n < 0 || n > 32 {
		return fmt.Errorf("%w: asked to write %d bits, and F.4.1 bounds every width to 0..32",
			errHintTable, n)
	}
	if n == 0 {
		// ⛔ AND A NON-ZERO VALUE IN A ZERO-WIDTH FIELD IS A REFUSAL, not a
		// silent truncation. It means the caller computed a width that cannot
		// hold what it is about to store — and the table would then be read
		// back with that value replaced by whatever follows it.
		if v != 0 {
			return fmt.Errorf("%w: %d does not fit in a zero-bit field", errHintTable, v)
		}
		return nil
	}
	// ⛔ THE SAME CHECK FOR EVERY OTHER WIDTH. The widths in Table F.3 are
	// "the number of bits needed to represent" a difference, so a value that
	// does not fit is a width computed wrongly, and writing it truncated
	// produces a table whose numbers are not the ones intended.
	if n < 32 && v >= uint32(1)<<uint(n) {
		return fmt.Errorf("%w: %d does not fit in %d bits", errHintTable, v, n)
	}

	w.push(n, v)
	return nil
}

// push appends n bits without checking them, which only the callers below may
// do.
//
// ⛔⛔ IT EXISTS SO THAT THE FIXED-WIDTH FIELDS HAVE NO ERROR TO IGNORE. Table
// F.6's item 2 is one bit holding 0 or 1, and its item 3 is sixteen bytes
// through an 8-bit field: neither can overflow, so wrapping them in `if err
// != nil` leaves branches no input can take — and this package's coverage gate
// rightly refuses to carry those.
//
// ⛔ The alternative was to call write and DISCARD the error on the grounds
// that it cannot happen. That is the shape that already cost this file a
// silent wrong value once, when a proof about an alignment turned out to be
// false. A justification can stop being true; `bit` and `byte8` taking types
// that cannot be out of range cannot.
func (w *bitw) push(n int, v uint32) {
	for i := n - 1; i >= 0; i-- {
		if w.pos/8 >= int64(len(w.b)) {
			w.b = append(w.b, 0)
		}
		if (v>>uint(i))&1 == 1 {
			w.b[w.pos/8] |= 1 << uint(7-w.pos%8)
		}
		w.pos++
	}
}

// bit writes Table F.6's item 2: one bit, 0 or 1 by construction.
func (w *bitw) bit(set bool) {
	var v uint32
	if set {
		v = 1
	}
	w.push(1, v)
}

// byte8 writes one byte of Table F.6's item 3, which an 8-bit field holds by
// construction.
func (w *bitw) byte8(b byte) { w.push(8, uint32(b)) }

// align pads to the next byte boundary with zeroes.
//
// ⛔ ZEROES, AND THAT IS OBSERVABLE. The padding is not "don't care": a reader
// that trusted the convention reads the next group from the next byte, so the
// pad bits are never interpreted — but a BYTE COMPARISON against qpdf's output
// is, and that is the only oracle an encoder has. qpdf pads with zeroes.
// ⛔ IT NEED NOT GROW THE BUFFER, and the first version did. push allocates
// each byte before writing into it, so a position that is NOT on a byte
// boundary is always inside a byte that already exists — and a position that
// is on one does not enter the loop. The append was a branch no input could
// take, which the coverage gate caught.
func (w *bitw) align() {
	for w.pos%8 != 0 {
		w.pos++
	}
}

// bytes returns what has been written, padded to a whole byte.
func (w *bitw) bytes() []byte {
	w.align()
	return w.b
}

// writePageOffsetTable writes Table F.3's header and Table F.4's per-page
// entries, in F.4.1's lettered order with each group byte-aligned.
//
// ⛔⛔ THE ORDER AND THE ALIGNMENT ARE THE FORMAT, and they are written here in
// the same sequence readPageEntries reads them, for the same reason: F.4.1
// says the per-page items "are not contiguous; they are broken up with items
// from entries for other pages", and the byte alignment between its seven
// lettered groups is a producer convention this package measured rather than
// read. An encoder that got either wrong would emit a table that parses and
// means something else.
//
// ⛔ The header's item 2 is written AS STORED — the first page's location
// computed as if the primary hint stream were not there. The caller holds that
// convention, because only the caller knows where /H will land; see
// firstPageOffsetInFile for the reading side.
func writePageOffsetTable(h *pageOffsetHeader, pages []pageEntry) ([]byte, error) {
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: a hint table for no pages", errHintTable)
	}
	if pages[0].Shared != nil && len(pages[0].Shared) != 0 {
		return nil, fmt.Errorf("%w: the first page carries %d shared object reference(s), and "+
			"Table F.4 item 3 requires 0 there", errHintTable, len(pages[0].Shared))
	}

	w := &bitw{}

	// Table F.3, items 1..13, in the annex's order.
	for i, f := range []struct {
		n int
		v uint32
	}{
		{32, h.MinNObjects},
		{32, h.FirstPageOffset},
		{16, uint32(h.NBitsDeltaNObjects)},
		{32, h.MinPageLength},
		{16, uint32(h.NBitsDeltaPageLength)},
		{32, h.MinContentOffset},
		{16, uint32(h.NBitsDeltaContentOff)},
		{32, h.MinContentLength},
		{16, uint32(h.NBitsDeltaContentLen)},
		{16, uint32(h.NBitsNSharedObjects)},
		{16, uint32(h.NBitsSharedIdent)},
		{16, uint32(h.NBitsSharedNumerator)},
		// ⛔ ITEM 13 IS SIXTEEN BITS, and the first version of this list wrote
		// thirty-two. The header then came to 304 bits rather than Table F.3's
		// 288, and the byte comparison said so immediately — "differs from
		// qpdf's 36 bytes" on every shape. A test comparing NUMBERS could not
		// have seen it: every field still decodes, just two bytes along.
		{16, h.SharedDenominator},
	} {
		if err := w.write(f.n, f.v); err != nil {
			return nil, fmt.Errorf("%w (Table F.3, item %d of 13)", err, i+1)
		}
	}

	// ⛔ A DELTA THAT WOULD GO NEGATIVE IS A REFUSAL, not an underflow. Every
	// Table F.4 item is a value MINUS one of Table F.3's minima, so a minimum
	// above the value it is subtracted from means the header and the entries
	// disagree — and unsigned arithmetic would turn that into a huge delta
	// that writes, parses, and is wrong.
	delta := func(v, min uint32, what string, i int) (uint32, error) {
		if v < min {
			return 0, fmt.Errorf("%w: page %d's %s is %d and the table's least is %d",
				errHintTable, i+1, what, v, min)
		}
		return v - min, nil
	}

	// (a) Item 1 for all pages.
	for i, p := range pages {
		d, err := delta(p.NObjects, h.MinNObjects, "object count", i)
		if err != nil {
			return nil, err
		}
		if err := w.write(h.NBitsDeltaNObjects, d); err != nil {
			return nil, fmt.Errorf("%w (Table F.4 item 1 for page %d)", err, i+1)
		}
	}
	w.align()

	// (b) Item 2 for all pages.
	for i, p := range pages {
		d, err := delta(p.Length, h.MinPageLength, "length", i)
		if err != nil {
			return nil, err
		}
		if err := w.write(h.NBitsDeltaPageLength, d); err != nil {
			return nil, fmt.Errorf("%w (Table F.4 item 2 for page %d)", err, i+1)
		}
	}
	w.align()

	// (c) Item 3 for all pages, the first page's being 0.
	for i, p := range pages {
		if err := w.write(h.NBitsNSharedObjects, uint32(len(p.Shared))); err != nil {
			return nil, fmt.Errorf("%w (Table F.4 item 3 for page %d)", err, i+1)
		}
	}
	w.align()

	// (d) Item 4 for every shared reference of the second page, then the
	// third, and so on.
	for i := 1; i < len(pages); i++ {
		for _, s := range pages[i].Shared {
			if err := w.write(h.NBitsSharedIdent, s.Identifier); err != nil {
				return nil, fmt.Errorf("%w (Table F.4 item 4 for page %d)", err, i+1)
			}
		}
	}
	w.align()

	// (e) Item 5 for the same, in the same order.
	for i := 1; i < len(pages); i++ {
		for _, s := range pages[i].Shared {
			if err := w.write(h.NBitsSharedNumerator, s.Numerator); err != nil {
				return nil, fmt.Errorf("%w (Table F.4 item 5 for page %d)", err, i+1)
			}
		}
	}
	w.align()

	// (f) Item 6 for all pages.
	for i, p := range pages {
		d, err := delta(p.ContentOffset, h.MinContentOffset, "content stream offset", i)
		if err != nil {
			return nil, err
		}
		if err := w.write(h.NBitsDeltaContentOff, d); err != nil {
			return nil, fmt.Errorf("%w (Table F.4 item 6 for page %d)", err, i+1)
		}
	}
	w.align()

	// (g) Item 7 for all pages.
	for i, p := range pages {
		d, err := delta(p.ContentLength, h.MinContentLength, "content stream length", i)
		if err != nil {
			return nil, err
		}
		if err := w.write(h.NBitsDeltaContentLen, d); err != nil {
			return nil, fmt.Errorf("%w (Table F.4 item 7 for page %d)", err, i+1)
		}
	}

	return w.bytes(), nil
}

// writeSharedTable writes Table F.5's header and Table F.6's group entries.
//
// ⛔ ONE RUN OF ALL THE GROUPS, not two sequences — F.4.2's wording admits both
// readings and testdata/linearized-shared.pdf settled which qpdf writes. See
// readSharedTable.
func writeSharedTable(h *sharedHeader, groups []sharedGroup) ([]byte, error) {
	if int(h.NSharedTotal) != len(groups) {
		return nil, fmt.Errorf("%w: the header says %d group(s) and %d were given",
			errHintTable, h.NSharedTotal, len(groups))
	}
	if h.NSharedTotal < h.NSharedFirstPage {
		return nil, fmt.Errorf("%w: %d group(s) in all and %d for the first page, and Table "+
			"F.5 item 4 includes item 3", errHintTable, h.NSharedTotal, h.NSharedFirstPage)
	}

	w := &bitw{}

	for i, f := range []struct {
		n int
		v uint32
	}{
		{32, h.FirstSharedObj},
		{32, h.FirstSharedOffset},
		{32, h.NSharedFirstPage},
		{32, h.NSharedTotal},
		{16, uint32(h.NBitsNObjects)},
		{32, h.MinGroupLength},
		{16, uint32(h.NBitsDeltaGroupLength)},
	} {
		if err := w.write(f.n, f.v); err != nil {
			return nil, fmt.Errorf("%w (Table F.5, item %d of 7)", err, i+1)
		}
	}

	// (a) Item 1 for every group.
	for i, g := range groups {
		if g.Length < h.MinGroupLength {
			return nil, fmt.Errorf("%w: group %d is %d bytes and the table's least is %d",
				errHintTable, i+1, g.Length, h.MinGroupLength)
		}
		if err := w.write(h.NBitsDeltaGroupLength, g.Length-h.MinGroupLength); err != nil {
			return nil, fmt.Errorf("%w (Table F.6 item 1 for group %d)", err, i+1)
		}
	}
	w.align()

	// (b) Item 2 for every group: the one-bit signature flag.
	for _, g := range groups {
		w.bit(g.Signature != nil)
	}
	w.align()

	// (c) Item 3 for every group that has one.
	//
	// ⛔ SIXTEEN BYTES EXACTLY, refused otherwise: F.4.2 calls it "a 16-byte
	// MD5 hash", and a shorter or longer one would shift every item after it
	// while still producing a table that parses.
	for i, g := range groups {
		if g.Signature == nil {
			continue
		}
		if len(g.Signature) != 16 {
			return nil, fmt.Errorf("%w: group %d's signature is %d bytes, and Table F.6 item "+
				"3 is a 16-byte MD5", errHintTable, i+1, len(g.Signature))
		}
		for _, b := range g.Signature {
			w.byte8(b)
		}
	}
	w.align()

	// (d) Item 4 for every group: one LESS than the object count.
	//
	// ⛔ A GROUP OF NO OBJECTS IS REFUSED, because item 4 is "1 less than the
	// number of objects in the group" and zero objects would have to store
	// minus one.
	for i, g := range groups {
		if g.NObjects == 0 {
			return nil, fmt.Errorf("%w: group %d holds no objects, and Table F.6 item 4 "+
				"stores one less than the count", errHintTable, i+1)
		}
		if err := w.write(h.NBitsNObjects, g.NObjects-1); err != nil {
			return nil, fmt.Errorf("%w (Table F.6 item 4 for group %d)", err, i+1)
		}
	}

	return w.bytes(), nil
}

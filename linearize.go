// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"bytes"
	"errors"
	"fmt"
)

// ErrCannotLinearize reports a document this package declines to linearize.
var ErrCannotLinearize = errors.New("reader: this document cannot be linearized")

// xrefEntryBytes is the width of one classic cross-reference entry, which ISO
// 32000-1 7.5.4 fixes at twenty bytes: ten digits, a space, five digits, a
// space, the type, and a two-byte end-of-line.
//
// ⭐⭐ THIS CONSTANT IS WHY THE LAYOUT IS NOT CIRCULAR, and it is worth stating
// because it is the whole reason a two-pass writer converges. Every section's
// size is known from its object COUNT alone, before any offset is decided — so
// the first-page cross-reference section, the main one, and part 2's reserved
// span are all fixed-width, and the only thing in the file whose length depends
// on the offsets it encodes is the hint stream.
const xrefEntryBytes = 20

// frontTrailerSpan is the bytes reserved for part 3's trailer, which sits
// BEFORE every object and so must not change width between the two passes.
//
// ⛔ IT HOLDS THREE VARIABLE-WIDTH NUMBERS: /Size, /Prev and the catalogue's
// object number. Three int64 at 19 digits is 57, the fixed text is under 70,
// and 192 clears both with room. ⛔ A var, like paramSpan, so that the refusal
// is reachable from a test rather than being an untested claim.
var frontTrailerSpan = 192

// Linearize writes a linearized copy of the document: ISO 32000-1 Annex F's
// eleven parts, with the parameter dictionary at the front and the first-page
// cross-reference section before the objects a reader needs first.
//
// ⛔⛔ THE HINT TABLES ARE NOT WRITTEN YET, AND THIS IS DELIBERATE RATHER THAN
// UNFINISHED. #40 names five steps; this is the fifth, and splitting it is what
// makes the layout measurable on its own. The file carries a hint stream that
// is syntactically a stream and holds no valid tables, so:
//
//	`qpdf --check` validates the parameters, both cross-reference sections,
//	/O, /E, /T and the object structure — and reports the hint tables as
//	wrong, which they are.
//
// ⭐ That isolation is the same move every earlier step used: the partition was
// judged before the layout, the layout before the fixpoint, the encoder's bytes
// before any file existed. A writer that emitted everything at once could only
// be asked "is this file right", and qpdf's answer would name a hint-table
// problem while a wrong /T sat underneath it.
//
// ⚠ So a file from this function is NOT a usable linearized PDF yet. The
// guarantee it does carry is the one `qpdf --check` can confirm: the layout,
// the parameters and the two cross-reference sections.
func (d *Document) Linearize() ([]byte, error) {
	p, err := d.Partition()
	if err != nil {
		return nil, err
	}
	return d.linearizeFrom(p)
}

// linearizeFrom is Linearize with the partition supplied.
//
// ⛔⛔ IT IS A SEAM, AND THE REASON IS THE COVERAGE GATE. Four error paths
// below propagate failures whose preconditions Partition guarantees — an
// unknown part, an object that does not read — so from Linearize they are
// branches no input can take. Removing them would mean ignoring a returned
// error, which this package refuses; making them unreachable-but-present is
// what the gate refuses. A seam lets a test hand in the partition Partition
// would never produce, and the errors stay where a future caller needs them.
func (d *Document) linearizeFrom(p *Partition) ([]byte, error) {
	order, err := p.Order()
	if err != nil {
		return nil, err
	}
	// ⛔ NO GUARD FOR AN EMPTY ORDER, AND THAT IS DELIBERATE. Partition refuses
	// a document with no pages by name, and Order returns one entry per placed
	// object, so an empty order cannot arrive here. A guard for it would be a
	// branch no input can take — which this package's exact coverage gate
	// rightly refuses to carry, and which review has already removed from this
	// repository twice.

	// ⛔ THE OBJECTS ARE RENUMBERED 1..n IN LAYOUT ORDER, which the format
	// needs rather than prefers: Annex F's first-page cross-reference section
	// is a CONTIGUOUS range of object numbers, and part 6's objects must fall
	// inside it. The source document's numbers are in whatever order it was
	// written, so keeping them would leave the front section describing a
	// scattered set it cannot express.
	num := map[int]int{}
	for i, old := range order {
		num[old] = i + 1
	}

	// The first-page section covers the catalogue and the first page: parts 4
	// and 6, which Order puts first and contiguously.
	firstPageCount := 0
	for _, old := range order {
		if part := p.Of[old]; part == PartCatalog || part == PartFirstPage {
			firstPageCount++
		} else {
			break
		}
	}
	// ⛔ AND NO GUARD FOR A ZERO COUNT, for the same reason: Partition always
	// assigns the catalogue to part 4 — it refuses a document whose /Root has
	// no object number — and Order puts part 4 first, so the count is at least
	// one.

	// ⛔⛔ TWO PASSES, AND THE SECOND IS NOT A CORRECTION — it is the same
	// emission run again with the offsets the first one measured. The
	// parameters /L, /E and /T are offsets into the very file being written, so
	// the first pass cannot know them; part 2's reserved span means the second
	// pass puts every byte in the same place regardless of how many digits they
	// turn out to need.
	//
	// ⛔ THE RESULT OF PASS ONE IS DISCARDED, deliberately. Patching the first
	// pass's bytes in place would work only while every value's width happened
	// to fit, and the span exists precisely so that nothing has to be true
	// about the widths. Emitting twice is cheaper than a claim about digits.
	var out []byte
	m := linMeasured{params: linParams{
		N: int64(len(p.Pages)),
		O: int64(num[p.FirstPageObject]),
	}}
	for pass := 0; pass < 2; pass++ {
		b, measured, err := d.emitLinearized(p, order, num, firstPageCount, m)
		if err != nil {
			return nil, err
		}
		out, m = b, measured
	}
	return out, nil
}

// linMeasured is what one emission pass learns about the file it produced.
//
// ⛔⛔ /T AND /Prev ARE DIFFERENT OFFSETS AND THE FIRST VERSION USED ONE FOR
// BOTH. Table F.1's /T is "the offset of the first entry in the main
// cross-reference table"; the front trailer's /Prev must point at that table's
// `xref` KEYWORD, because that is where a reader starts parsing a section.
// They differ by len("xref\n0 1\n") — nine bytes here — and conflating them
// made qpdf report "(offset 914): xref not found" at the /T value, then
// reconstruct the table and compute /T as 0 from the wreckage.
type linMeasured struct {
	params     linParams
	mainXrefAt int64 // where the main table's `xref` keyword is, for /Prev
}

// emitLinearized writes the file once with the parameters it is given, and
// returns the parameters the result actually implies.
func (d *Document) emitLinearized(p *Partition, order []int, num map[int]int,
	firstPageCount int, prev linMeasured) ([]byte, linMeasured, error) {

	in := prev.params

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")
	// The four bytes above 127 that mark the file as binary, as the writer's
	// own header does.
	buf.Write([]byte{'%', 0xE2, 0xE3, 0xCF, 0xD3, '\n'})

	// Part 2: the parameter dictionary, in its reserved span. Its object
	// number is one past everything else, so the renumbering above is not
	// disturbed.
	paramNum := len(order) + 1
	obj, err := in.object(paramNum)
	if err != nil {
		return nil, linMeasured{}, err
	}
	buf.Write(obj)

	// Part 3: the first-page cross-reference section. ⛔ Its own offsets are
	// not known yet on the first pass, and its SIZE is — see xrefEntryBytes —
	// so a placeholder of the right width keeps everything after it in place.
	firstXrefAt := buf.Len()
	firstHeader := fmt.Sprintf("xref\n%d %d\n", 1, firstPageCount)
	buf.WriteString(firstHeader)
	entriesAt := buf.Len()
	buf.Write(bytes.Repeat([]byte(" "), firstPageCount*xrefEntryBytes))
	// The trailer of the front section. /Prev points at the main table, which
	// is what makes a reader that starts here find the rest.
	//
	// ⛔⛔⛔ IT GETS A RESERVED SPAN OF ITS OWN, and the first version did not —
	// which broke the fixpoint in a way qpdf named exactly. /Prev is 0 on the
	// first pass and the main table's offset on the second, so the trailer grew
	// by two bytes between them and shifted EVERY object after it. Measured:
	// qpdf reported "(offset 773): xref not found" while our main table sat at
	// 775, two along.
	//
	// ⭐ The parameter dictionary's span was not enough because it is not the
	// only variable-width thing in front of the objects: /Size and /Prev are
	// both numbers here, and this trailer precedes part 4. Anything before the
	// objects whose width can change needs a reservation, not just part 2 —
	// which is the general form of the rule, and the first version had the
	// special case.
	// ⛔⛔ THE CATALOGUE COMES FROM THE PARTITION, NOT FROM THE TRAILER, which
	// removes a guard rather than testing one. The first version asserted
	// `d.trailer["Root"].(Ref)` and refused a direct /Root — a refusal no input
	// could reach, because Partition declines such a document first and by
	// name ("the catalogue has no object number and part 4 cannot name it").
	// Part 4 holds exactly the catalogue, so the partition already knows its
	// number and no assertion is needed here.
	catalogue := 0
	for _, old := range order {
		if p.Of[old] == PartCatalog {
			catalogue = num[old]
			break
		}
	}
	// ⛔ /Size IS ONE PAST THE HIGHEST OBJECT NUMBER, and both trailers must
	// agree about it. The first version wrote paramNum+1 here and paramNum+2 in
	// the main trailer; qpdf read THIS one and said "reported number of objects
	// (6) is not one plus the highest object number (6)". The highest number is
	// the hint stream's, paramNum+1, so /Size is paramNum+2.
	trailer := fmt.Sprintf("trailer\n<< /Size %d /Prev %d /Root %d 0 R >>\nstartxref\n0\n%%%%EOF\n",
		paramNum+2, prev.mainXrefAt, catalogue)
	if len(trailer) > frontTrailerSpan {
		return nil, linMeasured{}, fmt.Errorf("%w: the first-page trailer needs %d bytes and %d "+
			"are reserved", ErrCannotLinearize, len(trailer), frontTrailerSpan)
	}
	buf.WriteString(trailer)
	buf.Write(bytes.Repeat([]byte(" "), frontTrailerSpan-len(trailer)))

	// Parts 4 and 6 through 9, in Order's sequence, with part 5 — the primary
	// hint stream — between them.
	// ⛔ len(order)+3 and not +2: the highest index written is paramNum+1,
	// which is len(order)+2, and a slice of that length stops one short. The
	// first version panicked here — index out of range [6] with length 6 — on
	// a four-object document, which is the arithmetic this comment exists to
	// stop being rediscovered.
	// Which objects are page objects, so the inheritable attributes can be
	// pushed onto them. See pushInherited.
	pageNums := map[int]bool{}
	for _, ref := range d.pageRefs() {
		pageNums[ref.Num] = true
	}
	nodeNums := map[int]bool{}
	if cat, err := d.Catalog(); err == nil {
		if nodes, err := d.pageTreeNodes(cat); err == nil {
			for n := range nodes {
				nodeNums[n] = true
			}
		}
	}

	offsets := make([]int, len(order)+3)
	hintAt, hintLen := 0, 0
	// ⛔⛔ THREE PHASES AND NOT A CONDITIONAL INSIDE THE LOOP. The first version
	// placed part 5 when the index reached firstPageCount, and carried a
	// fallback for "every object was part 4 or 6, so the boundary never came".
	// That fallback was unreachable — every document has a page tree node, and
	// F.3.10 puts it in part 9 — so it was a branch no input could take, in a
	// package whose coverage gate refuses those. Emitting the first-page
	// objects, then the hint stream, then the rest says the same thing with
	// nothing to be wrong about.
	emit := func(old int) error {
		o, err := d.Get(Ref{Num: old})
		if err != nil {
			return fmt.Errorf("%w: object %d does not read: %v",
				ErrCannotLinearize, old, err)
		}
		switch {
		case pageNums[old]:
			o = d.pushInherited(Ref{Num: old}, o)
		case nodeNums[old]:
			// ⛔⛔ AND THE NODE LOSES THEM, which is the half the first version
			// missed. qpdf's warning is about the attribute being ON THE NODE,
			// not about the page lacking it: "detected an inheritable
			// attribute when called in no-change mode" means it found one it
			// would have had to push down and was forbidden to modify
			// anything. Copying down without stripping left the warning
			// exactly as it was — measured, twice.
			o = stripInheritable(o)
		}
		offsets[num[old]] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n", num[old])
		buf.Write(AppendObject(nil, renumber(o, num)))
		buf.WriteString("\nendobj\n")

		// /E is the end of the first page: the last byte of part 6.
		if p.Of[old] == PartFirstPage || p.Of[old] == PartCatalog {
			in.E = int64(buf.Len())
		}
		return nil
	}

	// Parts 4 and 6, which Order puts first.
	for _, old := range order[:firstPageCount] {
		if err := emit(old); err != nil {
			return nil, linMeasured{}, err
		}
	}
	// Part 5, between the first page and the rest, which is where both
	// producers put it.
	hintAt = buf.Len()
	hintLen = writeHintStreamPlaceholder(&buf, paramNum+1)
	// Parts 7, 8 and 9.
	for _, old := range order[firstPageCount:] {
		if err := emit(old); err != nil {
			return nil, linMeasured{}, err
		}
	}
	offsets[paramNum+1] = hintAt

	// Part 11: the main cross-reference table. /T names its first entry, which
	// is the one after the free-list head.
	mainAt := buf.Len()
	buf.WriteString("xref\n0 1\n")
	// ⛔⛔ /T IS THE FIRST ENTRY, NOT THE `xref` KEYWORD, and the first version
	// pointed it at the keyword. Table F.1 calls /T "the offset of the first
	// entry in the main cross-reference table", and qpdf computed the
	// difference exactly: "(/T) mismatch (computed = 914; file = 905)" — nine
	// bytes, which is len("xref\n0 1\n"). The free-list head is an entry like
	// any other and it is the first one.
	mainFirstEntry := buf.Len()
	fmt.Fprintf(&buf, "0000000000 65535 f \n%d %d\n", 1, paramNum+1)
	for n := 1; n <= paramNum+1; n++ {
		if n == paramNum {
			// The parameter object itself lives at the front.
			fmt.Fprintf(&buf, "%010d 00000 n \n", len("%PDF-1.7\n")+6)
			continue
		}
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[n])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		paramNum+2, catalogue, firstXrefAt)

	out := buf.Bytes()

	// Now fill the front section's entries, which could not be written until
	// the offsets existed. ⛔ The span was reserved above, so this writes over
	// spaces and moves nothing.
	var entries bytes.Buffer
	for n := 1; n <= firstPageCount; n++ {
		fmt.Fprintf(&entries, "%010d 00000 n \n", offsets[n])
	}
	copy(out[entriesAt:], entries.Bytes())

	measured := in
	measured.L = int64(len(out))
	measured.H0 = int64(hintAt)
	measured.H1 = int64(hintLen)
	measured.T = int64(mainFirstEntry)
	return out, linMeasured{params: measured, mainXrefAt: int64(mainAt)}, nil
}

// writeHintStreamPlaceholder writes part 5 as a stream that parses and holds no
// valid hint tables, and returns its length in bytes.
//
// ⛔⛔ IT IS A PLACEHOLDER AND SAYS SO. Writing real tables needs the per-page
// byte extents of the file being emitted, which is the step after this one;
// emitting them half-right would make `qpdf --check` report a hint-table
// problem that this code could not distinguish from a layout problem. A stream
// of zeroes is refused by the table reader for a reason it names, which is the
// honest state to be in.
func writeHintStreamPlaceholder(buf *bytes.Buffer, num int) int {
	const body = 36 + 24 // Table F.3's header plus Table F.5's, both zeroed
	start := buf.Len()
	fmt.Fprintf(buf, "%d 0 obj\n<< /Length %d /S %d >>\nstream\n", num, body, 36)
	buf.Write(bytes.Repeat([]byte{0}, body))
	buf.WriteString("\nendstream\nendobj\n")
	return buf.Len() - start
}

// pushInherited copies the page attributes a page INHERITS onto the page
// itself, so that nothing it needs lives outside part 6 or part 7.
//
// ⛔⛔⛔ THIS IS A REQUIREMENT OF THE FORMAT'S PURPOSE AND QPDF IS WHAT TAUGHT
// IT. ISO 32000-1 Table 30 makes /Resources, /MediaBox, /CropBox and /Rotate
// inheritable: a page may omit them and take its parent's. But F.3.10 puts the
// page TREE in part 9 — "a reader never needs to consult it" — while F.3.7
// requires part 6 to hold everything needed to display the first page. An
// inherited /MediaBox therefore lives in part 9, and a reader that fetched only
// the front of the file could not lay the page out.
//
// ⭐ MEASURED, not read: `qpdf --check` on the first file this writer produced
// said "(/Pages object 4 0): pushInheritedAttributesToPage detected an
// inheritable attribute when called in no-change mode". The fixture builder
// puts /MediaBox on the Pages node and nowhere else, so every page inherited
// it. That single warning was the last thing between the layout and a file qpdf
// accepts, and it is not a rule stated anywhere in Annex F — it follows from
// what the annex is FOR.
//
// ⛔ The page keeps its /Parent, which still points into part 9. That is
// correct: F.3.10 places the tree there precisely because displaying a page
// does not need it, and the reference costs a reader nothing unless it walks up.
func (d *Document) pushInherited(page Ref, o Object) Object {
	dict, ok := ToDict(o)
	if !ok {
		return o
	}
	out := cloneDict(dict)
	for _, key := range inheritablePageKeys {
		if _, has := out[key]; has {
			continue
		}
		// Walk up the /Parent chain for the nearest definition.
		//
		// ⛔ The walk is bounded by the objects seen, not by a depth: a
		// /Parent cycle in a crafted file would otherwise spin here, and this
		// package has already paid for one unbounded walk (#52).
		seen := map[int]bool{page.Num: true}
		at := out["Parent"]
		for {
			ref, isRef := at.(Ref)
			if !isRef || seen[ref.Num] {
				break
			}
			seen[ref.Num] = true
			parent, err := d.Get(ref)
			if err != nil {
				break
			}
			pd, isDict := ToDict(parent)
			if !isDict {
				break
			}
			if v, has := pd[key]; has {
				out[key] = v
				break
			}
			at = pd["Parent"]
		}
	}
	return out
}

// inheritablePageKeys are the page attributes ISO 32000-1 Table 30 lets a page
// take from its parent.
var inheritablePageKeys = []Name{"Resources", "MediaBox", "CropBox", "Rotate"}

// stripInheritable removes the inheritable attributes from a page tree node,
// after pushInherited has copied them onto the pages.
//
// ⛔ THE TWO HALVES ARE ONE OPERATION. Copying down without stripping leaves
// the file semantically identical and qpdf's warning untouched; stripping
// without copying down loses the attribute entirely. Either alone is worse than
// neither, which is why they are written next to each other.
func stripInheritable(o Object) Object {
	dict, ok := ToDict(o)
	if !ok {
		return o
	}
	out := cloneDict(dict)
	for _, key := range inheritablePageKeys {
		delete(out, key)
	}
	return out
}

// renumber rewrites every indirect reference in an object through the map.
func renumber(o Object, num map[int]int) Object {
	switch v := o.(type) {
	case Ref:
		if n, ok := num[v.Num]; ok {
			return Ref{Num: n, Gen: v.Gen}
		}
		// ⛔ An object the partition did not place cannot be referenced from
		// one it did: the layout would have dropped it. Null is what a
		// dangling reference means to a reader, and it is better than a
		// reference to a number this file does not define.
		return Null{}
	case Array:
		out := make(Array, len(v))
		for i, e := range v {
			out[i] = renumber(e, num)
		}
		return out
	case Dict:
		out := Dict{}
		for k, e := range v {
			out[k] = renumber(e, num)
		}
		return out
	case *Stream:
		return &Stream{Dict: renumber(v.Dict, num).(Dict), Raw: v.Raw}
	}
	return o
}

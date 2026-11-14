// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"errors"
	"fmt"
)

// Reading the linearization of a file: ISO 32000-1:2008 Annex F, "Linearized
// PDF" — Adobe calls it Fast Web View.
//
// A linearized file is ordinary PDF rearranged so that a reader with a
// byte-range-capable transport can show the first page before the rest has
// arrived. The rearrangement is described by a parameter dictionary at the
// very front of the file (F.3.3, Table F.1) and by hint tables (F.4) that say
// where each page's objects are.
//
// ⛔ THIS READS AND CHECKS; IT DOES NOT LINEARIZE. The checker comes first
// because a writer cannot be trusted without one: "qpdf says it is linearized"
// is a judgement from outside, and what a writer needs during development is
// the same judgement from inside, on the bytes it has just produced.
//
// ⛔ AND IT IS DELIBERATELY NOT A "yes/no". Table F.1 says of the file length
// that "a mismatch indicates that the file is not linearized and shall be
// treated as ordinary PDF, ignoring linearization information". A file can
// carry a perfectly well-formed parameter dictionary and still not be
// linearized, because something was appended after it was written — which is
// the normal fate of these files, since F.3.6 says any incremental update
// invalidates the hint tables. So the result says WHICH claim failed.

// A Linearization is the parameter dictionary at the front of a linearized
// file (ISO 32000-1 Table F.1). Offsets and lengths are in bytes from the
// beginning of the file.
type Linearization struct {
	// Version is /Linearized, "a version identification for the linearized
	// format". Files in the wild carry 1 and 1.0; nothing is known to depend
	// on it.
	Version float64

	// FileLength is /L, which "shall be exactly equal to the actual length of
	// the PDF file".
	FileLength int

	// HintOffset and HintLength are the first pair of /H: where the primary
	// hint stream starts — "the beginning of the stream object, not the
	// beginning of the stream data" — and how long it is including object
	// overhead.
	HintOffset, HintLength int

	// OverflowHintOffset and OverflowHintLength are the optional second pair
	// of /H, for the overflow hint stream (part 10). HasOverflowHint says
	// whether they were given, because zero is a legitimate-looking value for
	// an offset that was never written.
	OverflowHintOffset, OverflowHintLength int
	HasOverflowHint                        bool

	// FirstPageObject is /O, "the object number of the first page's page
	// object".
	FirstPageObject int

	// FirstPageEnd is /E, the offset of the end of the first page.
	FirstPageEnd int

	// PageCount is /N, the number of pages in the document.
	PageCount int

	// MainXrefOffset is /T. For a file with a traditional cross-reference
	// table this is "the offset of the white-space character preceding the
	// first entry of the main cross-reference table (the entry for object
	// number 0)"; for a file using cross-reference streams exclusively (PDF
	// 1.5) it is "the offset of the main cross-reference stream object".
	MainXrefOffset int

	// FirstPageNumber is /P, optional, default 0.
	FirstPageNumber int
}

// Errors from [Linearized]. They are distinguishable because the difference
// matters to a caller: a file with no parameter dictionary was never
// linearized, while one whose dictionary disagrees with the file was
// linearized and then changed.
var (
	// ErrNotLinearized says the file carries no linearization parameter
	// dictionary where Annex F requires one.
	ErrNotLinearized = errors.New("reader: the file has no linearization parameter dictionary")

	// ErrLinearizationStale says the dictionary is there and well formed but
	// disagrees with the file around it — which Table F.1 says shall be
	// treated as ordinary PDF. Appending an incremental update does this, and
	// F.3.6 says any such change invalidates the hint tables.
	ErrLinearizationStale = errors.New("reader: the linearization parameters do not describe this file")

	// ErrLinearizationMalformed says the dictionary is present but breaks a
	// rule of Table F.1 — a missing required entry, a wrong type, or a value
	// that is an indirect reference where "all values in this dictionary
	// shall be direct objects".
	ErrLinearizationMalformed = errors.New("reader: the linearization parameter dictionary is malformed")
)

// linearizationWindow is the first 1024 bytes, which F.3.3 says the parameter
// dictionary "shall be entirely contained within" — the limit exists so that a
// reader need not fetch more than that to decide whether a file is linearized,
// which is the whole point of the format.
const linearizationWindow = 1024

// Linearized reads the linearization parameter dictionary of b and checks it
// against the file.
//
// It returns the parameters on success. On failure the error wraps one of
// [ErrNotLinearized], [ErrLinearizationMalformed] or [ErrLinearizationStale],
// and names the claim that failed.
//
// ⛔ It does NOT validate the hint tables. Their format is F.4 and checking
// them is a separate, much larger job; a file can pass this and still have
// hint tables that send a reader to the wrong offset. What this establishes is
// that the front of the file says what Annex F requires and that what it says
// matches the file — which is the part a reader consults first, and the part
// that goes wrong when a file is edited after being linearized.
func Linearized(b []byte) (*Linearization, error) {
	dict, end, err := firstBodyDict(b)
	if err != nil {
		return nil, err
	}

	lin := &Linearization{}
	// ⛔ Every value is required to be DIRECT (F.3.3), so these are read
	// without a resolver on purpose: a /L that is an indirect reference would
	// have to be chased through the cross-reference table, which is exactly
	// the work the first-1024-bytes rule exists to avoid.
	// /Linearized is known present — firstBodyDict refuses the file without it,
	// because its absence means "not linearized" while a wrong TYPE means
	// "malformed", and those are different answers. So the value is passed in
	// rather than looked up again: a second presence check here would be a
	// branch no test could reach.
	num, err := linearizedVersion(dict["Linearized"])
	if err != nil {
		return nil, err
	}
	lin.Version = num

	for _, f := range []struct {
		key Name
		to  *int
	}{
		{"L", &lin.FileLength},
		{"O", &lin.FirstPageObject},
		{"E", &lin.FirstPageEnd},
		{"N", &lin.PageCount},
		{"T", &lin.MainXrefOffset},
	} {
		v, err := directInteger(dict, f.key)
		if err != nil {
			return nil, err
		}
		*f.to = v
	}
	if v, ok := dict["P"]; ok {
		n, ok := v.(Integer)
		if !ok {
			return nil, fmt.Errorf("%w: /P is %T, want an integer", ErrLinearizationMalformed, v)
		}
		// ⛔ /P is never used as an offset, which is exactly why it was the one
		// entry left converting directly — and not a reason to leave it. It is
		// a public field, so a 32-bit build would hand a caller 0 for 2^32 from
		// the same bytes a 64-bit build reads correctly.
		got, err := addressable(int64(n), "P")
		if err != nil {
			return nil, err
		}
		lin.FirstPageNumber = got
	}
	if err := readHintArray(dict, lin); err != nil {
		return nil, err
	}

	// F.3.3: the whole dictionary within the first 1024 bytes. The rule is
	// about what a reader must FETCH, so it is checked against the byte offset
	// the parse ended at rather than against anything in the parse.
	if end > linearizationWindow {
		return nil, fmt.Errorf("%w: the dictionary ends at byte %d, past the %d-byte window Annex F allows",
			ErrLinearizationMalformed, end, linearizationWindow)
	}

	// Table F.1: /L "shall be exactly equal to the actual length of the PDF
	// file. A mismatch indicates that the file is not linearized."
	if lin.FileLength != len(b) {
		return nil, fmt.Errorf("%w: /L says %d bytes and the file is %d",
			ErrLinearizationStale, lin.FileLength, len(b))
	}
	return lin, nil
}

// CheckLinearized reads the parameter dictionary and then checks what it says
// against the document it describes.
//
// ⛔⛔ [Linearized] ON ITS OWN IS A WEAKER TEST THAN IT LOOKS, AND A WRITER
// DEVELOPED AGAINST IT WOULD PASS WITHOUT LINEARIZING ANYTHING. Of Table F.1's
// claims it checks exactly one against the file — /L against the byte count —
// because it is deliberately cheap: a reader deciding whether to stream a file
// has fetched 1024 bytes and has no cross-reference table to resolve anything
// with. /O, /E, /N and /T are read for type and never consulted. So a file
// carrying a correct /L and four invented integers is accepted, which is
// precisely what a first linearizer would emit.
//
// This is the other question, for the other caller: not "may a reader trust
// the front of this file" but "is this file actually what the front says".
// It opens the document, so it costs what opening costs, and it is the
// judgement a writer needs on bytes it has just produced.
//
// ⛔ It still does NOT validate the hint tables (F.4), which is a larger job
// again. What it establishes is that every parameter POINTS AT WHAT IT CLAIMS
// TO: the page count is the real one, /O is the real first page, /H begins at
// a real stream object of the length it declares, /T reaches the main
// cross-reference. A file can pass this and still send a reader to the wrong
// offset inside a hint table — so the error says which claim failed, and a
// caller that needs more must read F.4. qpdf's `--check` remains the foreign
// judge the tests print by.
//
// ⛔⛔ AND /E IS NOT GATED, SAID OUT LOUD BECAUSE A WRITER WILL BE TEMPTED TO
// TRUST IT. Table F.1 calls /E the offset of the end of the first page, and
// qpdf COMPUTES it: on the committed fixture it reports "computed = 901..902"
// and refuses a wrong value. This only bounds /E between 1 and the file
// length, so a writer that miscomputes /E passes here and fails qpdf. Review
// measured the gap. Computing it needs the first-page object set — the same
// partition the hint tables describe — which is why it waits for that work
// rather than being approximated now. Until then: do not read a pass from
// this as evidence about /E.
//
// The error wraps the same sentinels [Linearized] uses: a dictionary that
// disagrees with the document around it is [ErrLinearizationStale], which is
// what Table F.1 says such a file is — ordinary PDF.
func CheckLinearized(b []byte) (*Linearization, error) {
	lin, err := Linearized(b)
	if err != nil {
		return nil, err
	}
	d, err := Open(b)
	if err != nil {
		return nil, fmt.Errorf("%w: the file carries linearization parameters and does not open: %v",
			ErrLinearizationStale, err)
	}
	if err := checkFirstPage(d, lin); err != nil {
		return nil, err
	}
	if err := checkHintStream(b, lin); err != nil {
		return nil, err
	}
	if err := checkOverflowHint(b, lin); err != nil {
		return nil, err
	}
	if err := checkMainXref(b, lin); err != nil {
		return nil, err
	}
	if err := checkFirstPageEnd(b, lin); err != nil {
		return nil, err
	}
	return lin, nil
}

// isHintObject says whether an object at this offset is one of the hint
// streams /H names, and so is not a first-page object whose end could be /E.
//
// ⛔⛔ IT IS A FUNCTION SO THAT THE SECOND HALF CAN BE TESTED AT ALL. The
// primary hint stream is reachable from a committed file — Ghostscript puts
// it at /E — but the OVERFLOW stream is not: it needs a four-entry /H whose
// second pair is both a real stream and the highest-offset object in the
// first-page section, and neither qpdf nor Ghostscript writes one. Left
// inline, that condition would be a branch no fixture could reach and no
// mutation could kill, which is the vacuity this file has already spent
// three review rounds removing. Asked directly it is four plain cases.
func isHintObject(lin *Linearization, off int64) bool {
	if off == int64(lin.HintOffset) {
		return true
	}
	return lin.HasOverflowHint && off == int64(lin.OverflowHintOffset)
}

// checkFirstPageEnd checks /E, Table F.1's "offset of the end of the first
// page", against the objects the file itself says belong to the first page.
//
// ⛔ THE FIRST-PAGE CROSS-REFERENCE SECTION IS THE ANSWER, and it costs
// nothing to read. Annex F puts it at the front (part 3) and the file's own
// `startxref` points at it, which is the whole trick of the format: a reader
// fetches the front of the file and has page one. So its entries ARE the
// first page's objects, and no hint table has to be decoded to learn that.
// This checker used to say the partition "is the hint tables' job" and bound
// /E only to the file's length; that was true of the hint tables and not of
// this section.
//
// ⛔⛔ THE HINT STREAM IS EXCLUDED, AND A SECOND PRODUCER IS WHY THAT IS NOT
// A GUESS. F.3.1 permits the primary hint stream on either side of the first
// page, so the highest-offset entry here is not always a first-page object:
//
//	qpdf          /H at 550, /E 902 — the hint stream comes BEFORE
//	Ghostscript   /H at 829, /E 829 — it comes AFTER, AT /E exactly
//
// On Ghostscript's file the naive maximum is the hint stream itself, ending
// at 967 against an /E of 829, so a rule that took it would refuse every
// file that tool writes. Both committed qpdf fixtures place the hint stream
// before the first page and CANNOT tell the two rules apart — which is why
// testdata/linearized-gs.pdf is committed beside them.
//
// ⛔⛔ AND WHAT THIS PROVES IS THAT /E AGREES WITH THAT SECTION, NOT THAT THE
// SECTION IS RIGHT. A writer that leaves out objects page one genuinely needs
// produces a file that is internally consistent and passes here. Review
// measured Ghostscript doing it on a document with shared fonts, outlines and
// links: /H = /E = 1941, accepted here, while qpdf computes 5500..5501 and
// reports page one as 9 objects where the hint table claims 4. Deciding what
// page one NEEDS means walking its resources — the partition the hint tables
// describe — and that judgement is still outstanding (#40).
func checkFirstPageEnd(b []byte, lin *Linearization) error {
	// ⛔ A SEPARATE Document, not the one CheckLinearized opened. Open walks
	// /Prev and merges every section into one table, and setEntry keeps the
	// first definition it saw, so by then the front section's entries are no
	// longer distinguishable from the rest of the file's. Reading that one
	// section alone is the only way to ask which objects it listed.
	front := &Document{
		buf:     b,
		xref:    map[int]xrefEntry{},
		cache:   map[int]Object{},
		loading: map[int]bool{},
		objStms: map[int]map[int]Object{},
	}
	off, err := front.findStartxref()
	if err != nil {
		return fmt.Errorf("%w: the first-page cross-reference section cannot be found: %v",
			ErrLinearizationStale, err)
	}
	// ⛔⛔ THE SECTION startxref NAMES IS NOT ALWAYS THE FIRST-PAGE ONE, and
	// an earlier draft of this claimed /L had already made it so. It has not:
	// an incremental update appends its own section and points startxref at
	// THAT, and a file whose /L was rewritten to match its new length passes
	// every earlier check. Following it then lands on the update's entries and
	// compares /E with an object that has nothing to do with the first page —
	// a refusal with a reason that is not the reason, which is worse than a
	// miss because a reader believes it.
	//
	// Annex F puts the first-page section at part 3 and the main one at part
	// 11 — part 10 is the OVERFLOW hint stream, which this comment had wrong
	// until F.3.1's layout was read — so the front section precedes /T.
	// checkMainXref has already
	// established /T against the file by the time this runs, and this says
	// nothing about /E, so it still holds when /E is the thing under test.
	if off >= int64(lin.MainXrefOffset) {
		return fmt.Errorf("%w: the cross-reference section at byte %d is not the first-page one — "+
			"it is at or past the main table /T names (%d), so the file has been written to since it was linearized",
			ErrLinearizationStale, off, lin.MainXrefOffset)
	}
	if _, err := front.readXrefSection(off); err != nil {
		return fmt.Errorf("%w: the first-page cross-reference section at byte %d does not read: %v",
			ErrLinearizationStale, off, err)
	}

	// ⛔ THE COMPARISON STAYS IN int64 UNTIL THE VALUE IS KNOWN TO FIT. These
	// offsets come from the file, and int(e.offset) is the same silent
	// truncation `addressable` exists to refuse everywhere else.
	last := int64(-1)
	for _, e := range front.xref {
		// An object inside an object stream has no offset of its own; its
		// container has one and is listed here too, so skipping it loses
		// nothing.
		if e.kind != 'n' || isHintObject(lin, e.offset) {
			continue
		}
		if e.offset > last {
			last = e.offset
		}
	}
	if last < 0 {
		return fmt.Errorf("%w: the first-page cross-reference section lists no object in the file",
			ErrLinearizationStale)
	}
	if last >= int64(len(b)) {
		return fmt.Errorf("%w: the first-page cross-reference section puts an object at byte %d and the file is %d bytes",
			ErrLinearizationStale, last, len(b))
	}
	at := int(last)
	_, _, n, err := ParseIndirectObject(b[at:], nil)
	if err != nil {
		return fmt.Errorf("%w: the last first-page object, at byte %d, does not parse: %v",
			ErrLinearizationStale, at, err)
	}
	// The same end-of-object rule the /H length uses: Table F.1 counts the
	// line ending after `endobj` and the parse stops at it.
	if end := at + n + eolAfter(b, at+n); lin.FirstPageEnd != end {
		return fmt.Errorf("%w: /E says %d and the first page ends at %d",
			ErrLinearizationStale, lin.FirstPageEnd, end)
	}
	return nil
}

// checkFirstPage checks /N and /O against the document's own page tree.
//
// ⛔⛔ THERE IS NO "AND IS IT REALLY A PAGE?" CHECK, AND ITS ABSENCE IS THE
// POINT. A first version asked d.Get for the object /O names and refused it
// unless its /Type was /Page. But [Document.PageRef] is what the document
// layer calls the first page, and it does not require /Type: walkPages treats
// any resolvable dictionary with no /Kids as a leaf. A stricter definition
// here would make this checker disagree with the reader that will actually
// open the file — a judge and a subject that do not count alike — and would
// report the reader's notion of the page tree as the file's defect. What /O
// has to equal is what a reader will treat as page one.
//
// The coverage gate said so first: those branches could not be reached from
// any file, which is what dead code dressed as thoroughness looks like.
func checkFirstPage(d *Document, lin *Linearization) error {
	// ⛔ BEFORE THE COMPARISON, or a /N of 0 is reported as a disagreement
	// with whatever the document really holds, which sends the reader looking
	// for a page tree that is not the problem. Table F.1 calls /N "the number
	// of pages in the document"; a linearized document with none is not a
	// document this format describes.
	if lin.PageCount < 1 {
		return fmt.Errorf("%w: /N says %d page(s), and a linearized document has at least one",
			ErrLinearizationStale, lin.PageCount)
	}
	if n := d.PageCount(); n != lin.PageCount {
		return fmt.Errorf("%w: /N says %d page(s) and the document has %d",
			ErrLinearizationStale, lin.PageCount, n)
	}
	// PageRef counts from one, and the page count has just been established
	// as lin.PageCount, which is at least one — so this cannot fail, and a
	// branch for it would be one no file could reach.
	ref, _ := d.PageRef(1)
	if ref.Num != lin.FirstPageObject {
		return fmt.Errorf("%w: /O says object %d and the first page is object %d",
			ErrLinearizationStale, lin.FirstPageObject, ref.Num)
	}
	// ⛔⛔ AND IT MUST BE A DICTIONARY. This is NOT the /Type check deleted
	// above, and the difference is the whole argument. Requiring /Type /Page
	// would impose a stricter notion of "page" than the reader that opens the
	// file, which is a judge disagreeing with its subject. Requiring a
	// DICTIONARY imposes nothing extra: 7.7.3.3 says a page object is a
	// dictionary, and no stream is one.
	//
	// Review found this reachable and harmful: a file whose page tree names
	// the HINT STREAM as its first kid was accepted here, because walkPages
	// treats any resolvable object with no /Kids as a leaf. qpdf reported
	// that the page tree includes a non-dictionary object. That leniency in
	// the page walker is its own question; what this must not do is repeat it
	// while claiming to have checked the file.
	// ⛔ The error is not branched on: PageRef returned this reference because
	// walkPages had already resolved it, so a failure here is unreachable —
	// and an object that did not read is nil, which is not a Dict and is
	// reported by the one check below. A separate branch would be dead code
	// dressed as thoroughness, which the coverage gate names.
	obj, _ := d.Get(ref)
	if _, ok := obj.(Dict); !ok {
		return fmt.Errorf("%w: /O names object %d, which is %T — a page object is a dictionary",
			ErrLinearizationStale, lin.FirstPageObject, obj)
	}
	return nil
}

// checkHintStream checks the first pair of /H.
//
// Table F.1 is explicit that the offset is "the beginning of the stream
// object, not the beginning of the stream data", so what must be at it is an
// indirect object, and that object must be a stream — the hint tables live in
// its data.
func checkHintStream(b []byte, lin *Linearization) error {
	if lin.HintOffset <= 0 || lin.HintOffset >= len(b) {
		return fmt.Errorf("%w: /H starts at %d and the file is %d bytes",
			ErrLinearizationStale, lin.HintOffset, len(b))
	}
	// ⛔⛔ SUBTRACTION, NOT ADDITION, AND THAT IS A 32-BIT DEFECT RATHER THAN A
	// STYLE. Both values come straight out of the file, so an attacker chooses
	// them. Written `lin.HintOffset+lin.HintLength > len(b)`, a /H length near
	// MaxInt32 OVERFLOWS to a negative number on a 32-bit build, the comparison
	// is false, and the check lets through exactly the file it exists to
	// refuse. The offset is already known to be inside the file, so
	// len(b)-lin.HintOffset is positive and the subtraction cannot wrap.
	if lin.HintLength <= 0 || lin.HintLength > len(b)-lin.HintOffset {
		return fmt.Errorf("%w: /H is %d bytes from %d and the file is %d",
			ErrLinearizationStale, lin.HintLength, lin.HintOffset, len(b))
	}
	// ⛔ No resolver, deliberately: a hint stream whose /Length is an indirect
	// reference cannot be read by a reader that has only fetched the front of
	// the file, which is the reader this format exists for.
	_, obj, n, err := ParseIndirectObject(b[lin.HintOffset:], nil)
	if err != nil {
		return fmt.Errorf("%w: /H points at byte %d, where no indirect object parses: %v",
			ErrLinearizationStale, lin.HintOffset, err)
	}
	s, ok := obj.(*Stream)
	if !ok {
		return fmt.Errorf("%w: /H points at byte %d, where the object is %T and not a stream",
			ErrLinearizationStale, lin.HintOffset, obj)
	}
	// ⛔⛔ /S IS REQUIRED, AND A WRITER THAT FORGETS IT PASSED EVERYTHING ELSE.
	// Table F.2 lists the shared object hint table as "(Required)", and F.3.6
	// says the primary hint stream's dictionary "shall contain entries giving
	// the position of the beginning of each hint table in the stream". So a
	// primary stream with no /S names no shared object hint table, and a
	// reader that fetched the front of the file cannot find one. qpdf reports
	// it; this checked the stream's extent and never its contents.
	//
	// ⛔ ONLY THE PRIMARY. F.3.6 is explicit that "the dictionary of the
	// overflow hint stream shall not contain these entries", so requiring /S
	// in checkOverflowHint would refuse exactly the conforming file. The two
	// halves of /H are not symmetric and this is where they differ.
	//
	// ⛔ Presence only, not its value: the page offset table "shall be the
	// first table in the stream and shall start at offset 0" and has no key at
	// all, so the positions inside the stream are F.4's question, which this
	// does not open.
	if _, ok := s.Dict["S"]; !ok {
		return fmt.Errorf("%w: the primary hint stream at byte %d has no /S, and Table F.2 "+
			"makes the shared object hint table required",
			ErrLinearizationStale, lin.HintOffset)
	}
	// ⛔ AND THE LENGTH IS COMPARED WITH THE OBJECT THAT IS ACTUALLY THERE,
	// which review found it was not: the pair was bounds-checked against the
	// file and never against the stream it describes, so `/H [ 550 100 ]` for
	// a 127-byte object passed while qpdf reported a hint table length
	// mismatch. Table F.1 counts the whole object, so this is the parse's own
	// extent.
	if !spans(b, lin.HintOffset, n, lin.HintLength) {
		return fmt.Errorf("%w: /H says the hint stream is %d bytes and the object at %d is %d",
			ErrLinearizationStale, lin.HintLength, lin.HintOffset, n)
	}
	return nil
}

// spans says whether a declared length describes the object at off, which the
// parse measured as n bytes.
//
// ⛔ IT IS NOT EQUALITY WITH n, AND MEASUREMENT IS WHY. Requiring n ==
// declared refused every file qpdf produces: the parse ends at `endobj`,
// while Table F.1's length counts the object AND the end-of-line that
// terminates it.
//
// ⛔⛔ BUT A WINDOW IS NOT GOOD ENOUGH EITHER, which review measured. A first
// version allowed declared-n in 0..2, reasoning that a writer might use CRLF.
// That accepts /H [ 550 126 ] and /H [ 550 128 ] for a 126-byte object, both
// of which qpdf refuses — "first page object offset mismatch" and "hint table
// length mismatch". A tolerance wide enough to cover every line ending is
// wide enough to hide a wrong length, and this file's whole purpose is to be
// no weaker than the judge it will be measured against.
//
// So the end-of-line is READ rather than guessed, and the length must be
// exactly the object plus it.
func spans(b []byte, off, n, declared int) bool {
	return declared == n+eolAfter(b, off+n)
}

// eolAfter is the length of the line ending at i: 2 for CRLF, 1 for a lone CR
// or LF, 0 for anything else.
func eolAfter(b []byte, i int) int {
	if i >= len(b) {
		return 0
	}
	if b[i] == '\r' {
		if i+1 < len(b) && b[i+1] == '\n' {
			return 2
		}
		return 1
	}
	if b[i] == '\n' {
		return 1
	}
	return 0
}

// checkOverflowHint checks the second pair of /H, which Table F.1 makes
// optional and which review found was read and never examined: a file
// carrying `/H [ 550 127 9 9 ]` was accepted while qpdf reported that the
// overflow hint table is not a stream. An entry present and unchecked is
// worse than one absent, because the file claims something nothing verified.
func checkOverflowHint(b []byte, lin *Linearization) error {
	if !lin.HasOverflowHint {
		return nil
	}
	if lin.OverflowHintOffset <= 0 || lin.OverflowHintOffset >= len(b) {
		return fmt.Errorf("%w: the overflow /H starts at %d and the file is %d bytes",
			ErrLinearizationStale, lin.OverflowHintOffset, len(b))
	}
	if lin.OverflowHintLength <= 0 ||
		lin.OverflowHintLength > len(b)-lin.OverflowHintOffset {
		return fmt.Errorf("%w: the overflow /H is %d bytes from %d and the file is %d",
			ErrLinearizationStale, lin.OverflowHintLength, lin.OverflowHintOffset, len(b))
	}
	_, obj, n, err := ParseIndirectObject(b[lin.OverflowHintOffset:], nil)
	if err != nil {
		return fmt.Errorf("%w: the overflow /H points at byte %d, where no indirect object parses: %v",
			ErrLinearizationStale, lin.OverflowHintOffset, err)
	}
	if _, ok := obj.(*Stream); !ok {
		return fmt.Errorf("%w: the overflow /H points at byte %d, where the object is %T and not a stream",
			ErrLinearizationStale, lin.OverflowHintOffset, obj)
	}
	if !spans(b, lin.OverflowHintOffset, n, lin.OverflowHintLength) {
		return fmt.Errorf("%w: the overflow /H says %d bytes and the object at %d is %d",
			ErrLinearizationStale, lin.OverflowHintLength, lin.OverflowHintOffset, n)
	}
	return nil
}

// checkMainXref checks /T.
//
// ⛔ TABLE F.1 GIVES IT TWO MEANINGS AND BOTH ARE REAL. For a file with a
// traditional cross-reference table it is "the offset of the white-space
// character preceding the first entry of the main cross-reference table (the
// entry for object number 0)"; for a file using cross-reference streams it is
// "the offset of the main cross-reference stream object". Accepting only one
// would call every file of the other kind stale, so both are tried and the
// failure names both.
func checkMainXref(b []byte, lin *Linearization) error {
	if lin.MainXrefOffset <= 0 || lin.MainXrefOffset >= len(b) {
		return fmt.Errorf("%w: /T is %d and the file is %d bytes",
			ErrLinearizationStale, lin.MainXrefOffset, len(b))
	}
	rest := b[lin.MainXrefOffset:]
	if freeEntryFollows(rest) {
		return nil
	}
	if _, obj, _, err := ParseIndirectObject(rest, nil); err == nil {
		if s, ok := obj.(*Stream); ok {
			if t, _ := s.Dict["Type"].(Name); t == "XRef" {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: /T is %d, where there is neither the free entry of a "+
		"cross-reference table nor a cross-reference stream", ErrLinearizationStale,
		lin.MainXrefOffset)
}

// freeEntryFollows says whether the bytes begin with the whitespace and then
// the object-zero entry of a traditional cross-reference table: ten digits, a
// space, 65535, a space and `f`.
func freeEntryFollows(b []byte) bool {
	i := 0
	for i < len(b) && isSpace(b[i]) {
		i++
	}
	if i+18 > len(b) {
		return false
	}
	for j := i; j < i+10; j++ {
		if b[j] < '0' || b[j] > '9' {
			return false
		}
	}
	return string(b[i+10:i+18]) == " 65535 f"
}

// firstBodyDict parses the first indirect object of the body and returns its
// dictionary with the offset one past its end.
//
// F.3.3: "Following the header, the first object in the body of the file
// (part 2) shall be an indirect dictionary object, the linearization parameter
// dictionary". So this does not search for the dictionary — it reads whatever
// is first and reports that the file is not linearized if that is not it.
// Searching would find a linearization dictionary left behind by an earlier
// write and report a file as linearized when the thing a reader looks at first
// is something else entirely.
func firstBodyDict(b []byte) (Dict, int, error) {
	if len(b) < 5 || string(b[:5]) != "%PDF-" {
		return nil, 0, fmt.Errorf("%w: it does not begin with %%PDF-", ErrNotLinearized)
	}
	// Skip the header line and the binary comment line F.3.2 recommends,
	// along with any other comments before the body.
	i := 0
	for i < len(b) && i < linearizationWindow {
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i >= len(b) || b[i] != '%' {
			break
		}
		for i < len(b) && b[i] != '\n' && b[i] != '\r' {
			i++
		}
	}
	// ⛔ No resolver. Every value here is required to be direct (F.3.3), and a
	// reader deciding whether a file is linearized has read only these first
	// bytes — it has no cross-reference table yet to resolve anything with.
	_, obj, n, err := ParseIndirectObject(b[i:], nil)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: the first object does not parse: %v", ErrNotLinearized, err)
	}
	d, ok := obj.(Dict)
	if !ok {
		return nil, 0, fmt.Errorf("%w: the first object is %T, not a dictionary", ErrNotLinearized, obj)
	}
	if _, ok := d["Linearized"]; !ok {
		return nil, 0, fmt.Errorf("%w: the first object has no /Linearized entry", ErrNotLinearized)
	}
	return d, i + n, nil
}

// linearizedVersion reads /Linearized, which Table F.1 calls "a number".
//
// ⛔ BOTH SPELLINGS ARE REAL. The spec's own Example 2 writes
// `/Linearized 1.0` and qpdf writes `/Linearized 1`, so this accepts an
// integer and a real alike. My first tests used only the integer and left the
// real branch uncovered, which is how I noticed that the example and the
// implementation everybody uses disagree about it.
func linearizedVersion(v Object) (float64, error) {
	var got float64
	switch n := v.(type) {
	case Integer:
		got = float64(n)
	case Real:
		got = float64(n)
	default:
		return 0, fmt.Errorf("%w: /Linearized is %T, want a direct number", ErrLinearizationMalformed, v)
	}
	// ⛔⛔ THE VALUE IS CHECKED, AND IT WAS NOT. Review measured the gap: a
	// file whose first object says `/Linearized 9` was ACCEPTED here while
	// qpdf reported "File is not linearized". /Linearized is the one entry
	// that says which format the rest of the dictionary is written in, so a
	// version this code has never seen makes every other entry a guess —
	// reading /H and /O out of it and reporting the file as sound is the
	// checker agreeing with a document it cannot have understood.
	//
	// 1 and 1.0 are both accepted because the specification's own Example 2
	// writes `/Linearized 1.0` and qpdf writes `/Linearized 1`.
	if got != 1 {
		return 0, fmt.Errorf("%w: /Linearized is %v, and the only version this reads is 1",
			ErrLinearizationMalformed, got)
	}
	return got, nil
}

// directInteger reads a required entry that must be a direct integer.
func directInteger(d Dict, key Name) (int, error) {
	v, ok := d[key]
	if !ok {
		return 0, fmt.Errorf("%w: /%s is required", ErrLinearizationMalformed, key)
	}
	n, ok := v.(Integer)
	if !ok {
		return 0, fmt.Errorf("%w: /%s is %T, want a direct integer", ErrLinearizationMalformed, key, v)
	}
	return addressable(int64(n), key)
}

// addressable converts a value the file chose into an int, REFUSING one this
// build cannot represent rather than letting it wrap.
//
// ⛔⛔ SILENT TRUNCATION IS THE 32-BIT HAZARD, NOT THE ADDITION. A plain
// `int(n)` discards the high half where int is 32 bits, so a /H length of
// 2^32+127 reads as 127 — a perfectly plausible value that every later bound
// then accepts. The file would be judged sound on a 32-bit build and refused
// on a 64-bit one, from the same bytes.
//
// Review named this hazard; CI proved it a different way first, with the 386
// and arm lanes failing because 9223372036854775807 and 4294967295 BOTH
// arrive as -1. That is the same truncation, caught only because the value
// happened to land somewhere a bound rejected.
// ⛔ intMax is a VARIABLE so that a test can shrink it. The refusal below is
// unreachable on a 64-bit build — every int64 fits — so on this workstation
// it would be a branch no test could enter, which the coverage gate reports
// and which is otherwise only exercised by the 386 and arm lanes in CI. A
// seam is how the 32-bit case gets a test where 32-bit machines are not.
var intMax = int64(^uint(0) >> 1)

func addressable(v int64, key Name) (int, error) {
	if v > intMax || v < -intMax-1 {
		return 0, fmt.Errorf("%w: /%s is %d, which this build cannot address "+
			"(its largest int is %d)", ErrLinearizationMalformed, key, v, intMax)
	}
	return int(v), nil
}

// readHintArray reads /H, "an array of two or four integers".
func readHintArray(d Dict, lin *Linearization) error {
	v, ok := d["H"]
	if !ok {
		return fmt.Errorf("%w: /H is required", ErrLinearizationMalformed)
	}
	a, ok := v.(Array)
	if !ok {
		return fmt.Errorf("%w: /H is %T, want an array", ErrLinearizationMalformed, v)
	}
	if len(a) != 2 && len(a) != 4 {
		return fmt.Errorf("%w: /H has %d entries, want two or four", ErrLinearizationMalformed, len(a))
	}
	n := make([]int, len(a))
	for i, e := range a {
		v, ok := e.(Integer)
		if !ok {
			return fmt.Errorf("%w: /H[%d] is %T, want a direct integer", ErrLinearizationMalformed, i, e)
		}
		// ⛔ The same refusal as every other entry: /H is where the hostile
		// value actually arrives, since both halves are used as a byte range.
		got, err := addressable(int64(v), Name(fmt.Sprintf("H[%d]", i)))
		if err != nil {
			return err
		}
		n[i] = got
	}
	lin.HintOffset, lin.HintLength = n[0], n[1]
	if len(n) == 4 {
		lin.OverflowHintOffset, lin.OverflowHintLength = n[2], n[3]
		lin.HasOverflowHint = true
	}
	return nil
}

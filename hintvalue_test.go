// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"fmt"
	"sort"
	"testing"
)

// ⛔⛔⛔ THE VALUES, AGAINST THE DOCUMENT — because qpdf going quiet does NOT
// mean the tables are right, and this arc has already been caught believing
// that once.
//
// ⭐ MEASURED, which is why this test exists in this shape. `qpdf --check`
// validates exactly three things about a page offset table: Table F.3's item 2,
// the per-page object counts, and the per-page lengths. It never reads items 6
// and 7 (the content stream's offset and length), and it never reads the shared
// object hint table's groups at all. So the writer could emit nonsense in those
// fields and every foreign judge in this repository would stay silent — the
// same shape as part 5's placement in #65, where `--check` passed a file whose
// hint stream sat in the wrong part.
//
// ⛔ So the oracle here is the DOCUMENT, not an external tool: decode our own
// tables and hold every field against the bytes we wrote.
func TestTheHintTablesDescribeTheFileWeWrote(t *testing.T) {
	for _, shape := range hintShapes() {
		t.Run(shape.name, func(t *testing.T) {
			d, err := Open(shape.build(t))
			if err != nil {
				t.Fatal(err)
			}
			p, err := d.Partition()
			if err != nil {
				t.Fatal(err)
			}
			order, err := p.Order()
			if err != nil {
				t.Fatal(err)
			}
			out, err := d.Linearize()
			if err != nil {
				t.Fatal(err)
			}

			written, err := Open(out)
			if err != nil {
				t.Fatalf("our own file does not open: %v", err)
			}
			lin, err := CheckLinearized(out)
			if err != nil {
				t.Fatalf("our own checker refuses it: %v", err)
			}
			tables, err := written.readHintTables(lin)
			if err != nil {
				t.Fatalf("our own decoder cannot read the tables we wrote: %v", err)
			}

			if got := len(tables.pages); got != len(p.Pages) {
				t.Fatalf("the table holds %d page entr(ies) for a %d-page document",
					got, len(p.Pages))
			}

			// ⛔ Item 2 reconstructed through the reader's own adjustment, which
			// is the half a stored number cannot witness: the value is written
			// as if the hint stream were absent, so only firstPageOffsetInFile
			// says whether it names the right byte.
			at, err := tables.pageOffset.firstPageOffsetInFile(lin)
			if err != nil {
				t.Fatal(err)
			}
			wantAt, ok := written.xref[lin.FirstPageObject]
			if !ok {
				t.Fatalf("/O names object %d and our xref has no entry", lin.FirstPageObject)
			}
			if at != wantAt.offset {
				t.Errorf("item 2 resolves to byte %d and the first page's object is at %d",
					at, wantAt.offset)
			}

			for i, e := range tables.pages {
				// ⛔ THE OBJECT COUNT, against the partition rather than against
				// itself. qpdf checks this one too, but it checks it against its
				// OWN idea of the page's objects, which differs from ours — its
				// part 6 held three objects where ours holds two.
				if got, want := int(e.NObjects), len(p.Pages[i]); got != want {
					t.Errorf("page %d: the table says %d object(s), the partition has %d",
						i+1, got, want)
				}

				// ⛔⛔ THE LENGTH AS THE SUM OF THE PAGE'S OBJECTS, recomputed
				// here from the WRITTEN file's own cross-reference table. This
				// is the definition qpdf's Lin::lengthNextN uses — a sum, with
				// no gap counted — and the first version of the writer used the
				// byte EXTENT instead. On a two-page document that matched on
				// page 1 (186 == 101 + 85, over a different byte range) and
				// missed on page 2, so one green page and one red page came
				// from the same defect.
				want := 0
				for _, old := range p.Pages[i] {
					want += lengthOf(t, written, out, order, old)
				}
				if int(e.Length) != want {
					t.Errorf("page %d: the table says %d bytes and its objects sum to %d",
						i+1, e.Length, want)
				}

				// ⛔⛔ ITEMS 6 AND 7, WHICH NO FOREIGN JUDGE READS — so they are
				// compared to the EXACT bytes, not to a sanity bound. The first
				// version of this block only checked that the offset was inside
				// the page and the length non-zero, and a mutation setting the
				// offset to 0 survived all of it.
				// ⛔⛔ THE ORACLE IS THE WRITTEN FILE, NOT contentNumbers. The
				// first version called d.contentNumbers(p, i) here — the very
				// function the writer uses — so a page whose content stream the
				// writer failed to find reported "no content stream", the test
				// asserted zeros, and it PASSED. "There is nothing to describe"
				// and "the oracle could not see it" were indistinguishable.
				//
				// ⭐ So this reads /Contents out of the page dictionary in the
				// file we produced, which is a different path from the one under
				// test and is also what a reader would do.
				content := contentsOfWrittenPage(t, written, order, p, i)
				if len(content) == 0 {
					if e.ContentOffset != 0 || e.ContentLength != 0 {
						t.Errorf("page %d has no content stream and the table gives it "+
							"offset %d, length %d", i+1, e.ContentOffset, e.ContentLength)
					}
					continue
				}

				// The page's first byte, and the content stream's own extent.
				pageAt := -1
				for _, old := range p.Pages[i] {
					at := offsetOf(t, written, order, old)
					if pageAt < 0 || at < pageAt {
						pageAt = at
					}
				}
				wantOff, wantLen := -1, 0
				for _, c := range content {
					at := offsetOf(t, written, order, c)
					if wantOff < 0 || at < wantOff {
						wantOff = at
					}
					wantLen += lengthOf(t, written, out, order, c)
				}
				// ⛔⛔ A SHARED CONTENT STREAM LIES BEFORE THE PAGE THAT BORROWS
				// IT, and items 6 and 7 cannot say so. They are an offset
				// "relative to the beginning of the page" and a length, both
				// unsigned: page 2 of the shared-/Contents shape names page 1's
				// stream, which sits in part 6 at −83 bytes from page 2's first
				// byte. There is no representable value, so the writer states
				// NONE — contentNumbers keeps only the streams a page owns.
				//
				// ⛔ This case arrived with the sharing fixtures and is asserted
				// rather than skipped: "no content stream of its own" and "a
				// wrong positive delta" must not read alike. Making the oracle
				// recompute what the writer does would be the usual way to turn
				// a judge into an echo.
				if wantOff < pageAt {
					if e.ContentOffset != 0 || e.ContentLength != 0 {
						t.Errorf("page %d borrows a content stream %d bytes BEFORE its own "+
							"first byte, which items 6 and 7 cannot express, and the table "+
							"says offset %d, length %d", i+1, pageAt-wantOff,
							e.ContentOffset, e.ContentLength)
					}
					continue
				}
				if got, want := int(e.ContentOffset), wantOff-pageAt; got != want {
					t.Errorf("page %d: item 6 says the content stream is %d bytes into the "+
						"page and it is %d", i+1, got, want)
				}
				if got := int(e.ContentLength); got != wantLen {
					t.Errorf("page %d: item 7 says the content stream is %d bytes and it "+
						"is %d", i+1, got, wantLen)
				}
			}

			// ⛔⛔ THE SHARED TABLE DESCRIBES PART 6 THEN PART 8, and this block
			// used to assert it was EMPTY — true while the writer refused every
			// document that shared anything, and false since it writes one.
			//
			// ⛔ The first run is ALL of part 6, not only the objects a later
			// page uses: nothing in the table says which object an entry
			// describes, so the first run is positional, counted from /O. qpdf
			// reports nshared_first_page 3 for a file whose part 6 holds three
			// objects, including the ones no other page touches.
			if tables.shared == nil {
				t.Fatal("no shared object hint table, which Table F.2 marks Required")
			}
			part6, part8 := 0, 0
			for _, part := range p.Of {
				switch part {
				case PartFirstPage:
					part6++
				case PartShared:
					part8++
				}
			}
			if got, want := int(tables.shared.NSharedFirstPage), part6; got != want {
				t.Errorf("item 3 says %d first-page entr(ies) and part 6 holds %d object(s)",
					got, want)
			}
			if got, want := int(tables.shared.NSharedTotal), part6+part8; got != want {
				t.Errorf("item 4 says %d entr(ies) in all and parts 6 and 8 hold %d",
					got, want)
			}
			if got, want := len(tables.sharedGroups), part6+part8; got != want {
				t.Errorf("the table holds %d group(s) and item 4 wants %d", got, want)
			}

			// ⛔⛔⛔ AND EACH IDENTIFIER NAMES THE RIGHT OBJECT. Counts alone let
			// the whole table be WRONG and the suite stay green: reversing
			// refsFor's identifier — every page naming objects it does not
			// reach — passed this suite with REQUIRE_QPDF=1, qpdf included,
			// because the only shapes here shared nothing and the only
			// assertions were counts. External review found that, by mutation,
			// after this block had already been updated once.
			//
			// ⛔ THE EXPECTED OBJECT IS DECODED THE WAY A READER DECODES IT, not
			// by asking planShared: entry j below NSharedFirstPage is the j-th
			// object from /O, and the rest run from Table F.5 item 1. Asking the
			// planner would agree with the writer by construction, which is the
			// defect this very test exists to catch.
			for i, e := range tables.pages {
				named := map[int]bool{}
				for _, r := range e.Shared {
					num := lin.FirstPageObject + int(r.Identifier)
					if r.Identifier >= tables.shared.NSharedFirstPage {
						num = int(tables.shared.FirstSharedObj) +
							int(r.Identifier-tables.shared.NSharedFirstPage)
					}
					named[num] = true
				}
				want := map[int]bool{}
				for _, old := range p.Shared[i] {
					want[numberOf(t, order, old)] = true
				}
				for num := range want {
					if !named[num] {
						t.Errorf("page %d reaches shared object %d and its entry does "+
							"not name it: the entry names %v", i+1, num, named)
					}
				}
				for num := range named {
					if !want[num] {
						t.Errorf("page %d names shared object %d, which it does not "+
							"reach: it reaches %v", i+1, num, want)
					}
				}
			}
		})
	}
}

// hintShapes is the set of documents every hint-table judgement runs over.
//
// ⛔⛔ THE ONE-PAGE INHERITED SHAPE IS HERE BECAUSE THE FIX HAD NO WITNESS. When
// reachFromPage learned to follow Table 30's inherited attributes it repaired
// two defects older than the hint tables — /E and Table F.4 item 1 — but both
// are observable only on a document the writer ACCEPTS, and the
// inherited-attribute fixture has TWO pages, so it stops at the shared-object
// refusal. Deleting inheritedAttributes reddened that test through Pages[0] and
// left /E and item 1 unobserved.
//
// ⭐ One page inheriting an indirect /Resources is the smallest shape where the
// repair shows: the resources object is part 6, item 1 is 3 rather than 2, and
// /E runs past it. Measured clean before being added, so this is a witness that
// can go red and not a known failure.
func hintShapes() []struct {
	name  string
	build func(*testing.T) []byte
} {
	out := []struct {
		name  string
		build func(*testing.T) []byte
	}{}
	for _, n := range []int{1, 2, 5, 25} {
		pages := n
		out = append(out, struct {
			name  string
			build func(*testing.T) []byte
		}{
			name:  fmt.Sprintf("%d pages", pages),
			build: func(t *testing.T) []byte { return buildPages(t, pages) },
		})
	}
	for _, shape := range []struct {
		name  string
		build func(*testing.T) []byte
	}{
		{"one page inheriting an indirect /Resources", onePageInheritedFixture},
		// ⛔⛔⛔ THE THREE SHARING SHAPES ARE HERE BECAUSE WITHOUT THEM THIS
		// WHOLE FILE JUDGES NOTHING ABOUT THE SHARED TABLE. Every shape above
		// gives each page its own objects, so part 8 is empty, every page's
		// entry holds zero identifiers, and both mutations below passed the
		// suite with REQUIRE_QPDF=1:
		//
		//	refsFor's identifier reversed  — every page names the WRONG objects
		//	sharedOffset returning `at`    — the item-2 defect this arc fixed
		//
		// ⛔ That is the README's own recorded lesson — "the corpus could not
		// express the case" — repeating one step later: the refusal was
		// removed, the fixtures were not changed, and a green suite went on
		// meaning "the inputs are silent".
		{"two pages sharing a /Contents", sharedContentsFixture},
		{"two pages inheriting ONE indirect /Resources", inheritedResourcesFixture},
		// ⭐ The only shape that puts anything in PART 8: an object pages 2 and
		// 3 share and page 1 does not reach. Without it the second run is empty
		// in every judged file and `FirstSharedObj` is never exercised.
		{"pages 2 and 3 sharing an object page 1 does not reach", sharedAmongLaterPagesFixture},
	} {
		out = append(out, struct {
			name  string
			build func(*testing.T) []byte
		}{name: shape.name, build: shape.build})
	}
	return out
}

// sharedAmongLaterPagesFixture writes three pages where pages 2 and 3 name one
// /Resources object that page 1 does not reach — the only shape in which the
// partition assigns PartShared, so the only one whose shared table has a second
// run for Table F.5 items 1 and 2 to describe.
func sharedAmongLaterPagesFixture(t *testing.T) []byte {
	t.Helper()
	w := NewWriter("1.7")
	pagesRef := w.Reserve()
	later := w.Add(Dict{"ProcSet": Array{Name("PDF")}})

	first := w.Add(&Stream{Dict: Dict{},
		Raw: []byte("BT /F1 12 Tf 72 720 Td (one) Tj ET")})
	kids := Array{w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef,
		"Contents": first, "Resources": Dict{}})}
	for i := 2; i <= 3; i++ {
		c := w.Add(&Stream{Dict: Dict{},
			Raw: []byte(fmt.Sprintf("BT /F1 12 Tf 72 720 Td (p%d) Tj ET", i))})
		kids = append(kids, w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef,
			"Contents": c, "Resources": later}))
	}
	w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": kids, "Count": Integer(3),
		"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
	root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})

	plain, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

// onePageInheritedFixture writes a ONE-page document whose /Resources is an
// indirect reference on the page tree node — the smallest shape in which the
// page's object set must include an object no page dictionary names.
func onePageInheritedFixture(t *testing.T) []byte {
	t.Helper()
	w := NewWriter("1.7")
	pagesRef := w.Reserve()
	res := w.Add(Dict{"ProcSet": Array{Name("PDF"), Name("Text")}})
	content := w.Add(&Stream{Dict: Dict{}, Raw: []byte("BT /F1 12 Tf 72 720 Td (one) Tj ET")})
	// ⛔ No /Resources on the page: it INHERITS it from the node.
	page := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef, "Contents": content})
	w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": Array{page},
		"Count":     Integer(1),
		"Resources": res,
		"MediaBox":  Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
	root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})

	plain, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

// ⛔⛔ THE PASS-INVARIANT LENGTH, ASSERTED DIRECTLY. The hint stream sits before
// the first page, so if its length changed between the two passes every later
// offset would move. Until now the only witness was INDIRECT — /L, through
// Linearized accepting the file — which reddens for many reasons and localises
// none of them.
//
// ⭐ This compares the two table lengths to each other: the same page objects
// with NO byte extents (what pass 0 has) and with arbitrary non-zero ones (what
// pass 1 has). It needs no file, and it reddens the moment one of the three
// byte-measured widths stops being pinned.
//
// ⚠ An earlier form of this check was rejected before it was written: comparing
// the table length against hintLen would compare two different quantities, since
// hintLen is the whole stream object — `N 0 obj`, /Length, /S, endstream and
// all — not the table bytes.
func TestTheHintStreamIsTheSameLengthWhateverTheOffsets(t *testing.T) {
	for _, pages := range []int{1, 2, 5, 25} {
		t.Run(fmt.Sprintf("%d pages", pages), func(t *testing.T) {
			d, err := Open(buildPages(t, pages))
			if err != nil {
				t.Fatal(err)
			}
			p, err := d.Partition()
			if err != nil {
				t.Fatal(err)
			}

			order, err := p.Order()
			if err != nil {
				t.Fatal(err)
			}
			blank := blankExtents(p, order)
			first, _, err := buildHintTables(blank, blankShared(p, order), 0, 0)
			if err != nil {
				t.Fatal(err)
			}

			// The same pages, with byte extents a layout might produce: each
			// page a different size, at a different place, with content inside.
			filled := make([]pageExtent, len(blank))
			for i := range blank {
				at := 100000 + i*7919
				filled[i] = pageExtent{
					objects:      blank[i].objects,
					start:        at,
					end:          at + 3000 + i*11,
					length:       3000 + i*11,
					hasContent:   true,
					contentStart: at + 40,
					contentEnd:   at + 900 + i,
					// ⛔ THE SAME REFERENCES, because they are pass-invariant:
					// an identifier indexes the shared table and says nothing
					// about where a byte landed. Dropping them here would make
					// the two calls differ in their INPUT and the comparison
					// would prove nothing about the layout.
					shared: blank[i].shared,
				}
			}

			// ⛔ AND THE SAME GROUPS, with lengths a layout might produce. The
			// count is what fixes the table's size; the lengths ride in a
			// pinned 32-bit field.
			sharedBlank := blankShared(p, order)
			sharedFilled := sharedInput{
				lengths:   make([]int, len(sharedBlank.lengths)),
				firstPage: sharedBlank.firstPage,
			}
			for i := range sharedFilled.lengths {
				sharedFilled.lengths[i] = 700 + i*13
			}
			if len(sharedFilled.lengths) > sharedFilled.firstPage {
				sharedFilled.firstObj, sharedFilled.firstOff = 9, 500000
			}

			second, _, err := buildHintTables(filled, sharedFilled, 1000000, 120)
			if err != nil {
				t.Fatal(err)
			}

			if len(first) != len(second) {
				t.Errorf("the tables are %d bytes with no offsets and %d bytes with them, "+
					"so the hint stream would change size between the two passes and "+
					"move everything after it", len(first), len(second))
			}
		})
	}
}

// ⛔⛔ EACH PAGE'S OBJECTS ARE NUMBERED CONSECUTIVELY FROM ITS PAGE OBJECT, which
// is the assumption qpdf's checker makes and which had NO witness in this
// repository — `git grep consecutiv` found only comments. Lin::lengthNextN takes
// the page object's number and the next nobjects-1 numbers and sums THEIR
// lengths, so a layout that broke this would be measured against objects
// belonging to something else.
//
// ⛔ This is the assertion whose absence let the inherited-attribute defect
// through: there, page 1's real object count was 3 while the table said 2.
func TestEachPageIsNumberedConsecutivelyFromItsPageObject(t *testing.T) {
	for _, pages := range []int{1, 2, 5, 25} {
		t.Run(fmt.Sprintf("%d pages", pages), func(t *testing.T) {
			d, err := Open(buildPages(t, pages))
			if err != nil {
				t.Fatal(err)
			}
			p, err := d.Partition()
			if err != nil {
				t.Fatal(err)
			}
			order, err := p.Order()
			if err != nil {
				t.Fatal(err)
			}

			for i := range p.Pages {
				own := numberOf(t, order, p.PageObjects[i])
				want := make(map[int]bool, len(p.Pages[i]))
				for _, old := range p.Pages[i] {
					want[numberOf(t, order, old)] = true
				}
				for n := own; n < own+len(p.Pages[i]); n++ {
					if !want[n] {
						t.Errorf("page %d is object %d with %d object(s), so qpdf will "+
							"measure objects %d..%d — and %d is not one of the page's %v",
							i+1, own, len(p.Pages[i]), own, own+len(p.Pages[i])-1, n,
							sortedNumbers(t, order, p.Pages[i]))
						break
					}
				}
			}
		})
	}
}

func sortedNumbers(t *testing.T, order []int, olds []int) []int {
	t.Helper()
	out := make([]int, 0, len(olds))
	for _, old := range olds {
		out = append(out, numberOf(t, order, old))
	}
	sort.Ints(out)
	return out
}

// contentsOfWrittenPage returns the ORIGINAL object numbers of page i's content
// streams, read from the page dictionary in the file the writer produced.
//
// ⛔ A SEPARATE PATH FROM THE WRITER'S. contentNumbers walks the SOURCE document
// and is what the writer uses; this opens the OUTPUT, resolves the page object
// there, reads its /Contents and maps the numbers back. If the writer's lookup
// is wrong, these two disagree — which is the whole point of not reusing it.
func contentsOfWrittenPage(t *testing.T, written *Document, order []int,
	p *Partition, i int) []int {
	t.Helper()

	page, err := written.Get(Ref{Num: numberOf(t, order, p.PageObjects[i])})
	if err != nil {
		t.Fatalf("page %d does not read in the file we wrote: %v", i+1, err)
	}
	dict, ok := ToDict(page)
	if !ok {
		t.Fatalf("page %d is %T in the file we wrote, not a dictionary", i+1, page)
	}

	// Back from the written numbering to the document's own, so the caller can
	// compare against p.Pages.
	back := map[int]int{}
	for j, old := range order {
		back[j+1] = old
	}

	var out []int
	switch c := dict["Contents"].(type) {
	case Ref:
		out = append(out, back[c.Num])
	case Array:
		for _, v := range c {
			if r, isRef := v.(Ref); isRef {
				out = append(out, back[r.Num])
			}
		}
	}
	return out
}

// numberOf maps an original object number to the number the written file gave
// it.
//
// ⛔ THE WRITTEN FILE RENUMBERS AND THE MAPPING IS NOT EXPOSED. What is stable
// is the sequence: Order places part 4, then part 6, then parts 7..9, and the
// writer numbers from 1 along it. So this recomputes the mapping the way the
// writer did rather than guessing it from the offsets.
func numberOf(t *testing.T, order []int, old int) int {
	t.Helper()
	for i, o := range order {
		if o == old {
			return i + 1
		}
	}
	t.Fatalf("object %d is not in the layout's order", old)
	return 0
}

// offsetOf returns where the written file put the original document's object
// `old`.
func offsetOf(t *testing.T, written *Document, order []int, old int) int {
	t.Helper()
	n := numberOf(t, order, old)
	e, ok := written.xref[n]
	if !ok || e.kind != 'n' {
		t.Fatalf("object %d became %d and the written file's xref has no entry for it",
			old, n)
	}
	return int(e.offset)
}

// lengthOf returns how many bytes the written file's copy of `old` occupies,
// measured the way qpdf's Lin::lengthNextN measures it: from the object's own
// offset to the start of whatever comes next.
func lengthOf(t *testing.T, written *Document, out []byte, order []int, old int) int {
	t.Helper()
	mine := offsetOf(t, written, order, old)

	// ⛔ The boundary is the lowest offset above this one — and for the last
	// object there is none, so the main cross-reference section is the end of
	// the body.
	next := len(out)
	for _, e := range written.xref {
		if e.kind == 'n' && int(e.offset) > mine && int(e.offset) < next {
			next = int(e.offset)
		}
	}
	if lin, err := Linearized(out); err == nil {
		// /T is the first ENTRY; the `xref` keyword is nine bytes earlier.
		if at := lin.MainXrefOffset - len("xref\n0 1\n"); at > mine && at < next {
			next = at
		}
	}
	return next - mine
}

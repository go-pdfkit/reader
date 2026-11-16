// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"fmt"
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

			if got := len(tables.pages); got != pages {
				t.Fatalf("the table holds %d page entr(ies) for a %d-page document",
					got, pages)
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
				if got, want := int(e.ContentOffset), wantOff-pageAt; got != want {
					t.Errorf("page %d: item 6 says the content stream is %d bytes into the "+
						"page and it is %d", i+1, got, want)
				}
				if got := int(e.ContentLength); got != wantLen {
					t.Errorf("page %d: item 7 says the content stream is %d bytes and it "+
						"is %d", i+1, got, wantLen)
				}
			}

			// ⛔ THE SHARED TABLE IS PRESENT AND EMPTY, and both halves matter.
			// Table F.2 marks it Required, so its absence is malformed; this
			// writer's partition puts nothing in part 8, so it has no groups.
			if tables.shared == nil {
				t.Fatal("no shared object hint table, which Table F.2 marks Required")
			}
			if got := len(tables.sharedGroups); got != 0 {
				t.Errorf("the shared table holds %d group(s) and this writer shares nothing",
					got)
			}
			if tables.shared.NSharedTotal != 0 || tables.shared.NSharedFirstPage != 0 {
				t.Errorf("the shared table counts %d in all and %d for the first page",
					tables.shared.NSharedTotal, tables.shared.NSharedFirstPage)
			}
		})
	}
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

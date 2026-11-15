// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔⛔ THE LAYOUT IS JUDGED AGAINST WHERE QPDF ACTUALLY PUT THE OBJECTS, which
// is the only foreign judge available for a rule this package's author cannot
// read in the annex. For each fixture: partition it, ask Order() for F.3.1's
// sequence, and walk that sequence checking that qpdf's own byte offsets never
// go backwards ACROSS a part boundary.
//
// ⛔ ACROSS a boundary and not within one. Order() sorts inside a part by
// object number, which is this package's choice and not qpdf's; asserting the
// whole sequence would pin a decision nobody made and would fail on qpdf's
// output for no good reason. See Order's own note.
func TestTheLayoutPutsThePartsWhereQpdfPutsThem(t *testing.T) {
	for _, c := range []struct {
		pages, fonts int
		fixture      string
		extra        []string
		name         string
	}{
		{pages: 1}, {pages: 2}, {pages: 5}, {pages: 25},
		{pages: 10, fonts: 4}, {pages: 25, fonts: 8},
		// ⛔ THE ONLY PART 8 IN THE CORPUS. The shared-font shapes have the
		// first page reference the shared dictionary too, and F.3.9 keeps such
		// an object in part 6, so the others cannot see part 8 at all.
		{fixture: "linearized-shared.pdf"},
		// ⛔⛔ AND THE ONLY SHAPE WITH A COMPRESSED OBJECT. Review measured
		// that NONE of the seven above uses object streams — zero
		// compressed-or-absent objects each — so the `continue` for an object
		// without an offset of its own never ran, and with it the
		// carry-forward of the previous part's bound was dead. An object
		// inside a stream has no offset to compare, and a part whose objects
		// are ALL compressed must not reset the bound to -1.
		{pages: 5, extra: []string{"--object-streams=generate"},
			name: "5 pages in object streams"},
	} {
		name := c.name
		if name == "" {
			name = fmt.Sprintf("%d pages %d fonts", c.pages, c.fonts)
			if c.fixture != "" {
				name = c.fixture
			}
		}
		t.Run(name, func(t *testing.T) {
			if c.extra != nil {
				qpdfExtra = c.extra
				defer func() { qpdfExtra = nil }()
			}
			var linear []byte
			switch {
			case c.fixture != "":
				b, err := os.ReadFile(filepath.Join("testdata", c.fixture))
				if err != nil {
					t.Fatal(err)
				}
				linear = b
			case c.fonts == 0:
				_, linear = linearizedFixture(t, c.pages)
			default:
				_, linear = sharedResourceFixture(t, c.pages, c.fonts)
			}

			d, err := Open(linear)
			if err != nil {
				t.Fatal(err)
			}
			p, err := d.Partition()
			if err != nil {
				t.Fatalf("qpdf's own file cannot be partitioned: %v", err)
			}

			order, err := p.Order()
			if err != nil {
				t.Fatalf("qpdf's own file cannot be laid out: %v", err)
			}
			if len(order) != len(p.Of) {
				t.Fatalf("Order() returned %d object(s) and the partition placed %d — "+
					"a layout that drops an object writes a file missing it",
					len(order), len(p.Of))
			}

			// Walk the order, tracking the highest offset seen in the parts
			// already passed. A part may not start before an earlier part ended.
			var lastPart Part
			var maxBefore int64 = -1
			var maxInPart int64 = -1
			compressed := 0
			seen := map[int]bool{}
			for i, num := range order {
				part := p.Of[num]
				if seen[num] {
					t.Fatalf("Order() lists object %d twice", num)
				}
				seen[num] = true

				if i > 0 && part < lastPart {
					t.Fatalf("Order() puts %v before %v", part, lastPart)
				}
				if part != lastPart {
					if maxInPart > maxBefore {
						maxBefore = maxInPart
					}
					maxInPart = -1
					lastPart = part
				}

				e, ok := d.xref[num]
				if !ok || e.kind != 'n' {
					// Inside an object stream: no offset of its own, so it
					// cannot be compared and must not reset the bound.
					compressed++
					continue
				}
				if e.offset <= maxBefore {
					t.Errorf("object %d is in %v at byte %d, which is before the end of an "+
						"earlier part (%d) — qpdf did not lay this file out the way Order() "+
						"would write it", num, part, e.offset, maxBefore)
				}
				if e.offset > maxInPart {
					maxInPart = e.offset
				}
			}

			if len(order) == 0 {
				t.Fatal("Order() returned nothing, so the walk above checked nothing")
			}

			// ⛔⛔ THE OBJECT-STREAM SHAPE MUST ACTUALLY HAVE ONE, asserted
			// rather than hoped. Coverage cannot answer this: the branch it
			// exercises is in THIS FILE, and test files are not instrumented,
			// so a shape that quietly stopped producing compressed objects
			// would leave the carry-forward dead again with every test green —
			// which is how review found it dead in the first place.
			if c.extra != nil && compressed == 0 {
				t.Fatalf("this shape was added for objects with no offset of their own and "+
					"has %d of them; --object-streams=generate is no longer doing what the "+
					"case is named for", compressed)
			}
			// ⛔ AND THE CONTROL: the shapes NOT asking for object streams must
			// have none, or the case above is not the only one reaching that
			// branch and its name is a half-truth.
			if c.extra == nil && compressed != 0 {
				t.Errorf("a shape that did not ask for object streams has %d compressed "+
					"object(s), so the dedicated case is not what covers that branch",
					compressed)
			}
			t.Logf("%d object(s) in F.3.1's order, %d with no offset of their own, "+
				"agreeing with qpdf's offsets", len(order), compressed)
		})
	}
}

// ⛔ AND THE PARTS COME IN F.3.1's ORDER EVEN WHEN THE OBJECT NUMBERS FIGHT IT.
//
// ⚠ THIS COMMENT CLAIMED THE FIXTURES COULD NOT CATCH THAT, AND THE MUTATION
// SAID OTHERWISE. It read "qpdf numbers its objects roughly in layout order, so
// a function that ignored the parts and sorted by number alone would pass every
// case above". Measured: replacing Order's body with a plain sort by object
// number reddens ALL SEVEN qpdf shapes. qpdf does not number in layout order,
// and I had asserted it from the look of the offsets rather than from a run.
//
// The case below is kept because it is still the sharper witness — it fails in
// one line of expected-versus-got instead of as an offset comparison on a
// fixture, and it covers the intra-part sort that each fixture's one-object
// parts cannot reach. That is a smaller claim than the one it replaces.
func TestOrderFollowsThePartsAndNotTheObjectNumbers(t *testing.T) {
	p := &Partition{Of: map[int]Part{
		1: PartOther,        // 9
		2: PartShared,       // 8
		3: PartOtherPrivate, // 7
		4: PartFirstPage,    // 6
		5: PartCatalog,      // 4
	}}
	got, err := p.Order()
	if err != nil {
		t.Fatal(err)
	}
	want := []int{5, 4, 3, 2, 1}
	if len(got) != len(want) {
		t.Fatalf("Order() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Order() = %v, want %v — sorted by number instead of by part", got, want)
		}
	}

	// ⛔ AND WITHIN a part the numbers ascend, which is the half the case above
	// cannot see because each of its parts holds one object.
	p = &Partition{Of: map[int]Part{
		9: PartFirstPage, 7: PartFirstPage, 8: PartFirstPage,
		2: PartOther, 1: PartOther,
	}}
	got, err = p.Order()
	if err != nil {
		t.Fatal(err)
	}
	want = []int{7, 8, 9, 1, 2}
	// ⛔ THE LENGTH IS ASSERTED HERE TOO, which review found missing: without
	// it an Order() that DUPLICATED an object passed this block, since the loop
	// below only indexes the first len(want) entries.
	if len(got) != len(want) {
		t.Fatalf("Order() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Order() = %v, want %v — the segment sort is wrong, or it sorted "+
				"the whole slice and lost the parts", got, want)
		}
	}
}

// ⛔⛔⛔ THE PER-PAGE SETS, JUDGED BY QPDF'S OWN PAGE OFFSET TABLE. Table F.4
// item 1 is "the number of objects in a page", so qpdf publishes exactly the
// figure Partition.Pages has to produce — a foreign judge for the input the
// hint tables need, available before any file is written.
//
// ⛔ Partition was already computing these sets, once per page, and throwing
// them away: Of answers which PART an object is in, and part 7 says "belongs to
// exactly one page that is not the first" without ever saying which page. A
// writer holding only Of knows the order to emit objects in and nothing to say
// about them.
//
// ⚠ COMPARED AS A COUNT AND NOT AS A SET, because that is all qpdf publishes.
// Two different sets of the same size would agree here, so this is weaker than
// the byte comparisons elsewhere in this package — and it is still the only
// outside witness for this quantity. Said plainly rather than implied.
func TestThePerPageObjectCountsAgreeWithQpdf(t *testing.T) {
	// ⛔⛔ TWELVE SHAPES, NOT FOUR. The first version used only plain documents
	// of 1, 2, 5 and 25 pages, all of the same trivial form — one object the
	// first page also reaches, two private objects per page. Review ran eight
	// more looking for a counterexample to the part filter and found none, so
	// they are rows here rather than a claim in a commit message:
	//
	//	sharedResourceFixture 3p/2f, 10p/4f, 25p/8f — the first page shares the
	//	  dictionary too, so part 8 stays EMPTY and part 6 grows to 6, 8 and 12
	//	linearized-shared.pdf — a real part 8, which none of the others have
	//	linearized-objstm.pdf, and generated object streams at 2 and 5 pages —
	//	  compressed objects, which the per-page table also has to account for
	for _, c := range []struct {
		pages, fonts int
		fixture      string
		extra        []string
		name         string
	}{
		{pages: 1}, {pages: 2}, {pages: 5}, {pages: 25},
		{pages: 3, fonts: 2}, {pages: 10, fonts: 4}, {pages: 25, fonts: 8},
		{fixture: "linearized.pdf"},
		{fixture: "linearized-shared.pdf"},
		{fixture: "linearized-objstm.pdf"},
		{pages: 2, extra: []string{"--object-streams=generate"}, name: "2 pages in object streams"},
		{pages: 5, extra: []string{"--object-streams=generate"}, name: "5 pages in object streams"},
	} {
		name := c.name
		if name == "" {
			name = fmt.Sprintf("%d pages %d fonts", c.pages, c.fonts)
			if c.fixture != "" {
				name = c.fixture
			}
		}
		t.Run(name, func(t *testing.T) {
			qpdf := qpdfOrSkip(t)
			if c.extra != nil {
				qpdfExtra = c.extra
				defer func() { qpdfExtra = nil }()
			}

			var linear []byte
			switch {
			case c.fixture != "":
				b, err := os.ReadFile(filepath.Join("testdata", c.fixture))
				if err != nil {
					t.Fatal(err)
				}
				linear = b
			case c.fonts == 0:
				_, linear = linearizedFixture(t, c.pages)
			default:
				_, linear = sharedResourceFixture(t, c.pages, c.fonts)
			}

			d, err := Open(linear)
			if err != nil {
				t.Fatal(err)
			}
			pages := len(d.pageRefs())
			p, err := d.Partition()
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Pages) != pages {
				t.Fatalf("Partition reported %d page(s) and the document has %d",
					len(p.Pages), pages)
			}

			dir := t.TempDir()
			path := filepath.Join(dir, "in.pdf")
			if err := os.WriteFile(path, linear, 0o644); err != nil {
				t.Fatal(err)
			}
			want := qpdfPageEntries(t, qpdf, path)
			if len(want) != pages {
				t.Fatalf("qpdf reported %d page(s) for a %d-page file", len(want), pages)
			}

			for i := range want {
				if got := len(p.Pages[i]); got != int(want[i].nobjects) {
					t.Errorf("page %d: %d object(s), qpdf says %d (ours: %v)",
						i+1, got, want[i].nobjects, p.Pages[i])
				}
			}

			// ⛔ AND THE SETS ARE SORTED, which the count cannot show. A writer
			// emitting in map order would give different bytes every run from
			// the same input.
			for i, set := range p.Pages {
				if len(set) == 0 {
					t.Errorf("page %d has no objects at all", i+1)
					continue
				}
				for j := 1; j < len(set); j++ {
					if set[j-1] >= set[j] {
						t.Errorf("page %d's objects are not ascending: %v", i+1, set)
						break
					}
				}
			}
			assertPerPageInvariants(t, d, p)
		})
	}
}

// assertPerPageInvariants checks the three properties qpdf cannot judge.
//
// ⛔⛔⛔ THE COUNT IS WEAKER THAN I ADMITTED, AND REVIEW MEASURED HOW MUCH. I
// wrote that two different sets of the same size would agree. Worse than that:
// on every fixture the cardinalities are UNIFORM — page one is all of part 6,
// every other page is exactly 2 (its page object and its content stream) — and
// the union of the sets is exactly parts 6 and 7 (11 of 11 objects, 51 of 51).
// So SWAPPING two pages' private objects keeps every count correct and passes.
//
// ⛔ And qpdf publishes nothing per-page that attributes a part-7 object to a
// page, so that judge cannot pin membership EVEN IN PRINCIPLE. It is not a
// matter of reading more of its output.
//
// ⭐ These three hold today, need no qpdf, and a swap breaks all three:
// every page holds its own object, the sets are pairwise disjoint, and their
// union is exactly the part-6 and part-7 objects. Review proposed them; they
// are the attribution evidence the count cannot give.
func assertPerPageInvariants(t *testing.T, d *Document, p *Partition) {
	t.Helper()

	pages := d.pageRefs()
	if len(pages) != len(p.Pages) {
		t.Fatalf("the document has %d page(s) and Pages holds %d", len(pages), len(p.Pages))
	}

	// 1. Every page holds its own object. ⛔ EVERY page: an earlier version of
	// this test checked only Pages[0] while its comment claimed otherwise.
	for i, set := range p.Pages {
		own := false
		for _, num := range set {
			if num == pages[i].Num {
				own = true
			}
		}
		if !own {
			t.Errorf("page %d's set %v does not hold its own object %d, and Table F.4 item 1 "+
				"counts it", i+1, set, pages[i].Num)
		}
	}

	// 2. Pairwise disjoint: a private object belongs to one page by definition,
	// and an object in two sets would be written once and counted twice.
	owner := map[int]int{}
	for i, set := range p.Pages {
		for _, num := range set {
			if prev, dup := owner[num]; dup {
				t.Errorf("object %d is in both page %d's set and page %d's, so it is not "+
					"private to either", num, prev+1, i+1)
				continue
			}
			owner[num] = i
		}
	}

	// 3. The union is exactly parts 6 and 7. ⛔ BOTH DIRECTIONS: a missing
	// object means a page under-reports item 1, and an extra one means a part
	// 4, 8 or 9 object is being claimed by a page that does not own it.
	want := map[int]bool{}
	for num, part := range p.Of {
		if part == PartFirstPage || part == PartOtherPrivate {
			want[num] = true
		}
	}
	for num := range want {
		if _, ok := owner[num]; !ok {
			t.Errorf("object %d is in %v and no page's set holds it", num, p.Of[num])
		}
	}
	for num := range owner {
		if !want[num] {
			t.Errorf("object %d is in a page's set and its part is %v, which no page owns",
				num, p.Of[num])
		}
	}
}

// ⛔⛔ AN OBJECT IN A PART THE LAYOUT DOES NOT PLACE IS REFUSED, AND IT USED TO
// VANISH. Part is an open int and Partition.Of is an exported map, so nothing
// stops a caller — or a future part 3, 5, 10 or 11 — from holding a value the
// five-part walk does not match. Measured by review on the first version:
//
//	Of: {1: PartCatalog, 2: Part(5), 3: PartOther}  ->  [1 3], no error
//
// Three objects in, two out. A layout that loses an object writes a file
// missing it, and the file still OPENS: the cross-reference table simply never
// mentions it, so a reader following a reference to it fails on a document that
// looked written. Partition refuses rather than guesses; so does this.
//
// ⛔ THE LENGTH CHECK IN THE FIXTURE TEST COULD NOT CATCH THIS, which is why
// the refusal is here and not left to it: its only partitions come from
// Document.Partition, which assigns exactly the five, so the guard had no
// input that could fire it.
func TestAnObjectInAPartTheLayoutDoesNotPlaceIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		part Part
	}{
		{"a part this package never assigns", Part(5)},
		{"the zero part, which is what an unset entry holds", Part(0)},
		{"a part number past the eleven", Part(99)},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &Partition{Of: map[int]Part{1: PartCatalog, 2: c.part, 3: PartOther}}
			got, err := p.Order()
			if !errors.Is(err, ErrCannotLayOut) {
				t.Fatalf("Order() = %v, %v — want ErrCannotLayOut", got, err)
			}
			if !strings.Contains(err.Error(), "object 2") {
				t.Errorf("it was refused without naming the object: %v", err)
			}
			if got != nil {
				t.Errorf("it was refused AND returned %v; a caller that ignores the error "+
					"would write a file missing an object", got)
			}
		})
	}

	// ⛔ THE CONTROL: the same shape with every part known is accepted, so the
	// refusal is about the unknown part and not about this partition.
	p := &Partition{Of: map[int]Part{1: PartCatalog, 2: PartShared, 3: PartOther}}
	if _, err := p.Order(); err != nil {
		t.Errorf("a partition using only the five parts was refused: %v", err)
	}
}

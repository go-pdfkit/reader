// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"fmt"
	"os"
	"path/filepath"
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
	}{
		{pages: 1}, {pages: 2}, {pages: 5}, {pages: 25},
		{pages: 10, fonts: 4}, {pages: 25, fonts: 8},
		// ⛔ THE ONLY PART 8 IN THE CORPUS. The shared-font shapes have the
		// first page reference the shared dictionary too, and F.3.9 keeps such
		// an object in part 6, so six of these seven cannot see part 8 at all.
		{fixture: "linearized-shared.pdf"},
	} {
		name := fmt.Sprintf("%d pages %d fonts", c.pages, c.fonts)
		if c.fixture != "" {
			name = c.fixture
		}
		t.Run(name, func(t *testing.T) {
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

			order := p.Order()
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
					continue // inside an object stream: no offset of its own
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
			t.Logf("%d object(s) in F.3.1's order, agreeing with qpdf's offsets", len(order))
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
	got := p.Order()
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
	got = p.Order()
	want = []int{7, 8, 9, 1, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Order() = %v, want %v — the segment sort is wrong, or it sorted "+
				"the whole slice and lost the parts", got, want)
		}
	}
}

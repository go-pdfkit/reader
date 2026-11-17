// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

// ⛔⛔ EVERY WAY buildHintTables REFUSES, reached directly because the emission
// cannot produce any of them: it gives every page its own object, measures from
// a buffer that only grows, and writes the hint stream before the first page so
// its length is always under that page's offset.
//
// ⛔ The refusals stay anyway. The caller that supplies shared references (#68)
// has none of those guarantees — it hands in a plan of its own making — and a
// propagation no test reaches is a claim nothing can falsify.
func TestEveryWayTheHintTablesRefuse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pages []pageExtent
		first int
		hint  int
		want  string
	}{
		{
			name:  "no pages at all",
			pages: nil,
			want:  "a hint table for no pages",
		},
		{
			name:  "a page with no objects",
			pages: []pageExtent{{objects: 0, start: 10, end: 20, length: 10}},
			want:  "least its own page object",
		},
		{
			name:  "an extent that runs backwards",
			pages: []pageExtent{{objects: 1, start: 90, end: 20, length: 10}},
			want:  "ends at 20 and starts at 90",
		},
		{
			name:  "a negative length",
			pages: []pageExtent{{objects: 1, start: 10, end: 20, length: -1}},
			want:  "sum to -1 bytes",
		},
		{
			name: "a content stream before the page",
			pages: []pageExtent{{objects: 1, start: 50, end: 90, length: 40,
				hasContent: true, contentStart: 10, contentEnd: 60}},
			want: "not inside it",
		},
		{
			name: "a content stream past the page",
			pages: []pageExtent{{objects: 1, start: 50, end: 90, length: 40,
				hasContent: true, contentStart: 60, contentEnd: 200}},
			want: "not inside it",
		},
		{
			name: "a content stream that runs backwards",
			pages: []pageExtent{{objects: 1, start: 50, end: 90, length: 40,
				hasContent: true, contentStart: 80, contentEnd: 60}},
			want: "not inside it",
		},
		{
			// ⛔ Item 2 is stored as if the hint stream were not in the file, so
			// a first page below the stream's length would store a negative.
			name:  "a first page above the hint stream's length",
			pages: []pageExtent{{objects: 1, start: 10, end: 20, length: 10}},
			first: 5,
			hint:  100,
			want:  "item 2 would go negative",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := buildHintTables(tc.pages, sharedInput{}, tc.first, tc.hint)
			if !errors.Is(err, errHintTable) {
				t.Fatalf("it was accepted: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refused, but not for %q: %v", tc.want, err)
			}
		})
	}
}

// ⛔⛔⛔ THE CASE THE WHOLE JUDGED CORPUS COULD NOT EXPRESS, which is why qpdf
// was green about a hint table it would have refused. buildPages gives every
// page its own content stream, so no object is reached by two pages, part 6
// holds nothing a later page needs, and the shared object hint table has
// nothing to say. Every shape the suite builds is that shape.
//
// ⛔ Measured on a document whose two pages share ONE content stream:
//
//	WARNING: page 1: shared object 3: in computed list but not hint table
//
// from `qpdf --check`, while our own CheckLinearized accepted the file. So
// nothing in this package would have caught it, and the foreign judge was silent
// only because no fixture could reach the case.
//
// ⚠ The writer refused such a document rather than describing it wrongly until
// #68 filled the table. This test is now the witness that it DESCRIBES it, and
// the fixture it committed is what let the corpus express the case at all.
func TestADocumentWhoseLaterPagesShareAnObjectIsWritten(t *testing.T) {
	plain := sharedContentsFixture(t)
	d, err := Open(plain)
	if err != nil {
		t.Fatal(err)
	}

	// ⛔ THE POSITIVE HALF FIRST: the partition must SEE the sharing, or the
	// table written below would be describing a document with nothing in it and
	// passing for the wrong reason.
	p, err := d.Partition()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Shared) < 2 {
		t.Fatalf("the partition reports %d page(s) of shared objects", len(p.Shared))
	}
	if len(p.Shared[0]) != 0 {
		t.Errorf("page 1 is credited with shared objects %v, and Table F.4 item 3 "+
			"requires 0 there", p.Shared[0])
	}
	if len(p.Shared[1]) == 0 {
		t.Fatal("page 2 reaches page 1's content stream and the partition reports " +
			"no shared object, so this fixture witnesses nothing")
	}

	// ⛔⛔ IT IS WRITTEN NOW, AND THIS TEST USED TO ASSERT THE REFUSAL. While the
	// shared object hint table was emitted required-but-EMPTY, a document whose
	// later pages share an object got a table contradicting the file and
	// `qpdf --check` said so — so refusing was the honest state. #68 writes the
	// table, and the same document is described instead.
	out, err := d.Linearize()
	if err != nil {
		t.Fatalf("a document whose pages share a content stream was refused: %v", err)
	}
	if _, err := CheckLinearized(out); err != nil {
		t.Fatalf("our own checker refuses it: %v", err)
	}

	// ⛔ AND THE TABLE SAYS SO: page 2 must name the object it shares.
	lin, err := Linearized(out)
	if err != nil {
		t.Fatal(err)
	}
	written, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := written.readHintTables(lin)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables.pages) < 2 {
		t.Fatalf("the table holds %d page entr(ies)", len(tables.pages))
	}
	if len(tables.pages[0].Shared) != 0 {
		t.Errorf("page 1 names %d shared object(s), and Table F.4 item 3 requires 0",
			len(tables.pages[0].Shared))
	}
	if len(tables.pages[1].Shared) == 0 {
		t.Error("page 2 shares an object with page 1 and the table names none")
	}
}

// ⛔⛔⛔ AN INHERITED ATTRIBUTE THAT IS AN INDIRECT REFERENCE, which is the hole
// the shared-object refusal did NOT close and which review found by building an
// ordinary PDF. Table 30 lets a page omit /Resources and take the page tree
// node's; the writer pushes it DOWN at emission, so if the node's value is a
// reference, every emitted page names an object the source document's pages did
// not.
//
// ⛔ Measured before reachFromPage accounted for it: Pages was [[3 5] [4 6]]
// with the resources object in part 9, Shared was empty so nothing refused the
// document, and `qpdf --check` on the output said all three of
//
//	end of first page section (/E) mismatch: /E = 905; computed = 1190
//	object count mismatch for page 0: hint table = 2; computed = 3
//	page 1: shared object 7: in computed list but not hint table
//
// ⛔⛔ TWO OF THOSE ARE OLDER THAN THE HINT TABLES. /E and the object count were
// wrong in the layout itself; the tables are simply what let qpdf say so. The
// partition was describing the source document while the writer emitted a
// different one, and no definition over the source's reach could see it.
func TestAPageInheritingAnIndirectAttribute(t *testing.T) {
	plain := inheritedResourcesFixture(t)
	d, err := Open(plain)
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.Partition()
	if err != nil {
		t.Fatal(err)
	}

	// ⛔ THE POSITIVE HALF: the resources object must now be in page 1's set.
	// Without this the refusal below could pass because the partition still
	// cannot see the object — which is exactly the state this test was written
	// to catch.
	res := resourcesObject(t, d)
	if !contains(p.Pages[0], res) {
		t.Fatalf("object %d is the pages node's /Resources and page 1's set is %v, so "+
			"the inherited attribute is still outside the partition's reach",
			res, p.Pages[0])
	}
	if !contains(p.Shared[1], res) {
		t.Fatalf("page 2 inherits the same /Resources and its shared list is %v",
			p.Shared[1])
	}

	// ⛔ AND THE WRITER DESCRIBES IT. This asserted a REFUSAL until #68: the
	// partition seeing the inherited object is what made the refusal correct,
	// and it is now what makes the shared table correct. The reach fix (#66)
	// was the prerequisite either way.
	out, err := d.Linearize()
	if err != nil {
		t.Fatalf("a document whose pages share an inherited object was refused: %v", err)
	}
	if _, err := CheckLinearized(out); err != nil {
		t.Fatalf("our own checker refuses it: %v", err)
	}
}

// ⛔⛔⛔ WHAT EACH INHERITANCE SHAPE PUTS IN THE SHARED TABLE. Until #68 this
// table had an `accept` column and five rows set to false, and the rule it
// witnessed — "refused iff an object a page after the first reaches is also
// reached by another page, other than the catalogue" — was the README's. The
// shared object hint table makes that rule obsolete: every shape here is now
// WRITTEN, so a column of eleven `accept: true` would assert nothing a single
// row does not.
//
// ⛔⛔ SO THE COLUMN WAS REPLACED RATHER THAN DELETED. What a reader needs from
// these files is not that Linearize returned nil — it is that a page after the
// first NAMES the objects it shares, which is the whole point of F.4.1 items 4
// and 5. `laterPageShares` is that claim, and it has witnesses on BOTH sides:
// five shapes where a later page must name something and six where it must name
// NOTHING. A writer that emitted refs for everything, or for nothing, passes the
// accept column and fails this one.
//
// ⭐ The false rows are the ones that keep the writer from over-reaching: a
// direct inherited value adds no object, a page stating its own inherits
// nothing, an object only ONE page reaches is private to it, and the catalogue
// is exempt because part 4 arrives ahead of the first page.
func TestWhatEachInheritanceShapePutsInTheSharedTable(t *testing.T) {
	// A page tree node carrying `nodeKeys`, with `pageKeys` on every page.
	build := func(t *testing.T, pages int, onNode, onPages Dict) []byte {
		t.Helper()
		w := NewWriter("1.7")
		pagesRef := w.Reserve()
		kids := make(Array, 0, pages)
		for i := 0; i < pages; i++ {
			content := w.Add(&Stream{Dict: Dict{},
				Raw: []byte(fmt.Sprintf("BT /F1 12 Tf 72 720 Td (p%d) Tj ET", i+1))})
			page := Dict{"Type": Name("Page"), "Parent": pagesRef, "Contents": content}
			for k, v := range onPages {
				page[k] = v
			}
			kids = append(kids, w.Add(page))
		}
		node := Dict{"Type": Name("Pages"), "Kids": kids, "Count": Integer(pages),
			"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}}
		for k, v := range onNode {
			node[k] = v
		}
		w.Put(pagesRef, node)
		root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})
		plain, err := w.Finish(Dict{"Root": root})
		if err != nil {
			t.Fatal(err)
		}
		return plain
	}

	// An indirect resource dictionary, written into whichever document needs it.
	indirect := func(t *testing.T, pages int, onPages bool) []byte {
		t.Helper()
		w := NewWriter("1.7")
		pagesRef := w.Reserve()
		res := w.Add(Dict{"ProcSet": Array{Name("PDF"), Name("Text")}})
		kids := make(Array, 0, pages)
		for i := 0; i < pages; i++ {
			content := w.Add(&Stream{Dict: Dict{},
				Raw: []byte(fmt.Sprintf("BT /F1 12 Tf 72 720 Td (p%d) Tj ET", i+1))})
			page := Dict{"Type": Name("Page"), "Parent": pagesRef, "Contents": content}
			if onPages {
				// Each page names its OWN resources object, so none is shared.
				page["Resources"] = w.Add(Dict{"ProcSet": Array{Name("PDF")}})
			}
			kids = append(kids, w.Add(page))
		}
		node := Dict{"Type": Name("Pages"), "Kids": kids, "Count": Integer(pages),
			"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}}
		if !onPages {
			node["Resources"] = res
		}
		w.Put(pagesRef, node)
		root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})
		plain, err := w.Finish(Dict{"Root": root})
		if err != nil {
			t.Fatal(err)
		}
		return plain
	}

	// ⛔⛔ A THREE-LEVEL TREE, because every other fixture here is FLAT and the
	// rule's boundary lies exactly where a flat tree cannot reach it. The
	// indirect /Resources sits on a SUB-NODE with `under` pages beneath it: with
	// one page below, the object is that page's alone and the document is
	// accepted; with two, it is shared and refused. Row 4's old wording —
	// "multi-page inheriting one indirect /Resources is refused" — is literally
	// false for the first of those, and the accurate rule is "refused iff an
	// inheritable indirect value is inherited by MORE THAN ONE page".
	deep := func(t *testing.T, under int) []byte {
		t.Helper()
		w := NewWriter("1.7")
		rootNode := w.Reserve()
		subNode := w.Reserve()
		res := w.Add(Dict{"ProcSet": Array{Name("PDF"), Name("Text")}})

		// One page directly under the root node, stating its own resources.
		direct := w.Add(&Stream{Dict: Dict{}, Raw: []byte("BT /F1 12 Tf 72 720 Td (a) Tj ET")})
		first := w.Add(Dict{"Type": Name("Page"), "Parent": rootNode,
			"Contents": direct, "Resources": Dict{}})

		// `under` pages beneath the sub-node, inheriting its /Resources.
		kids := make(Array, 0, under)
		for i := 0; i < under; i++ {
			c := w.Add(&Stream{Dict: Dict{},
				Raw: []byte(fmt.Sprintf("BT /F1 12 Tf 72 720 Td (s%d) Tj ET", i+1))})
			kids = append(kids, w.Add(Dict{"Type": Name("Page"),
				"Parent": subNode, "Contents": c}))
		}
		w.Put(subNode, Dict{"Type": Name("Pages"), "Parent": rootNode,
			"Kids": kids, "Count": Integer(under), "Resources": res})
		w.Put(rootNode, Dict{"Type": Name("Pages"),
			"Kids":     append(Array{first}, subNode),
			"Count":    Integer(1 + under),
			"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
		root := w.Add(Dict{"Type": Name("Catalog"), "Pages": rootNode})

		plain, err := w.Finish(Dict{"Root": root})
		if err != nil {
			t.Fatal(err)
		}
		return plain
	}

	// ⛔⛔ AND A DOCUMENT WHERE NAMING THE KEY WOULD BE FALSE. Two pages name the
	// SAME /Contents explicitly — so the sharing has nothing to do with
	// inheritance — while page 2 sits under a sub-node carrying an indirect
	// /Rotate that is private to it. The first version of the message asked
	// only "does this page inherit anything by reference" and so reported
	// "shares object 4 … which it inherits as /Rotate", where object 4 was
	// written in both page dictionaries and /Rotate's object is not shared.
	falseKey := func(t *testing.T) []byte {
		t.Helper()
		w := NewWriter("1.7")
		rootNode := w.Reserve()
		subNode := w.Reserve()
		rotate := w.Add(Integer(90))
		shared := w.Add(&Stream{Dict: Dict{}, Raw: []byte("BT /F1 12 Tf 72 720 Td (both) Tj ET")})

		first := w.Add(Dict{"Type": Name("Page"), "Parent": rootNode,
			"Contents": shared, "Resources": Dict{}})
		second := w.Add(Dict{"Type": Name("Page"), "Parent": subNode,
			"Contents": shared, "Resources": Dict{}})
		w.Put(subNode, Dict{"Type": Name("Pages"), "Parent": rootNode,
			"Kids": Array{second}, "Count": Integer(1), "Rotate": rotate})
		w.Put(rootNode, Dict{"Type": Name("Pages"), "Kids": Array{first, subNode},
			"Count":    Integer(2),
			"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
		root := w.Add(Dict{"Type": Name("Catalog"), "Pages": rootNode})

		plain, err := w.Finish(Dict{"Root": root})
		if err != nil {
			t.Fatal(err)
		}
		return plain
	}

	// ⛔⛔ A PAGE THAT STATES ITS OWN /Resources AND NAMES THE INHERITED OBJECT
	// INSIDE IT. Page 1 inherits nothing — it has its own /Resources — but that
	// dictionary names the node's object under /XObject, and page 2 inherits it.
	// So EXACTLY ONE page inherits the value and the document is still shared,
	// which is why "inherited by more than one page" is only a SUFFICIENT
	// condition and not the rule.
	viaOwnResources := func(t *testing.T) []byte {
		t.Helper()
		w := NewWriter("1.7")
		pagesRef := w.Reserve()
		shared := w.Add(Dict{"ProcSet": Array{Name("PDF"), Name("Text")}})

		c1 := w.Add(&Stream{Dict: Dict{}, Raw: []byte("BT /F1 12 Tf 72 720 Td (a) Tj ET")})
		c2 := w.Add(&Stream{Dict: Dict{}, Raw: []byte("BT /F1 12 Tf 72 720 Td (b) Tj ET")})
		// Page 1 states its own /Resources — and reaches the node's object
		// through it.
		first := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef, "Contents": c1,
			"Resources": Dict{"XObject": shared}})
		// Page 2 states nothing and inherits the node's /Resources.
		second := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef, "Contents": c2})

		w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": Array{first, second},
			"Count":     Integer(2),
			"Resources": shared,
			"MediaBox":  Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
		root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})

		plain, err := w.Finish(Dict{"Root": root})
		if err != nil {
			t.Fatal(err)
		}
		return plain
	}

	// ⛔⛔ TWO PAGES STATING THE SAME /Resources OBJECT, with no inheritance
	// anywhere. "Each page states its own" is NOT an exemption: the cure is not
	// sharing, and stating your own only removes the INHERITANCE route. The
	// accepting row above passes because its two objects DIFFER, which was a
	// second, unstated reason.
	sameOwnResources := func(t *testing.T) []byte {
		t.Helper()
		w := NewWriter("1.7")
		pagesRef := w.Reserve()
		res := w.Add(Dict{"ProcSet": Array{Name("PDF"), Name("Text")}})

		kids := make(Array, 0, 2)
		for i := 0; i < 2; i++ {
			c := w.Add(&Stream{Dict: Dict{},
				Raw: []byte(fmt.Sprintf("BT /F1 12 Tf 72 720 Td (p%d) Tj ET", i+1))})
			kids = append(kids, w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef,
				"Contents": c, "Resources": res}))
		}
		w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": kids, "Count": Integer(2),
			"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
		root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})

		plain, err := w.Finish(Dict{"Root": root})
		if err != nil {
			t.Fatal(err)
		}
		return plain
	}

	// ⛔⛔ TWO PAGES THAT BOTH REACH THE CATALOGUE, which the rule's words have to
	// exempt by name. Each page carries a key whose value is a reference to the
	// catalogue object, so by "an object a page after the first reaches is also
	// reached by the first page" it is shared — and the document is ACCEPTED,
	// because `of[catNum] = PartCatalog` is assigned after the readers loop and
	// overwrites whatever sharing would have made it.
	//
	// ⭐ That is correct rather than convenient: part 4 precedes part 6 and
	// arrives with the first page, so a later page depending on the catalogue
	// needs no F.4.2 entry. Part 4 holds nothing but the catalogue, so this is
	// the only object the exception covers.
	bothReachCatalogue := func(t *testing.T) []byte {
		t.Helper()
		w := NewWriter("1.7")
		pagesRef := w.Reserve()
		rootRef := w.Reserve()

		kids := make(Array, 0, 2)
		for i := 0; i < 2; i++ {
			c := w.Add(&Stream{Dict: Dict{},
				Raw: []byte(fmt.Sprintf("BT /F1 12 Tf 72 720 Td (p%d) Tj ET", i+1))})
			kids = append(kids, w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef,
				"Contents": c, "Resources": Dict{},
				// ⛔ A reference back to the catalogue, which both pages reach.
				"PieceInfo": Dict{"Private": rootRef}}))
		}
		w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": kids, "Count": Integer(2),
			"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
		w.Put(rootRef, Dict{"Type": Name("Catalog"), "Pages": pagesRef})

		plain, err := w.Finish(Dict{"Root": rootRef})
		if err != nil {
			t.Fatal(err)
		}
		return plain
	}

	for _, tc := range []struct {
		name  string
		plain func(*testing.T) []byte
		// laterPageShares is whether a page AFTER THE FIRST must carry at
		// least one Table F.4 item 4 identifier. False means it must carry
		// none: the shape has nothing for the shared table to describe.
		laterPageShares bool
	}{
		{
			name:  "two pages both reaching the CATALOGUE",
			plain: bothReachCatalogue,
			// ⛔ Part 4 arrives ahead of the first page, so a later page that
			// depends on the catalogue needs no F.4.2 entry. This was the row
			// the old refusal had to exempt BY NAME; the writer inherits the
			// exemption through PartCatalog, which planShared does not plan.
			laterPageShares: false,
		},
		{
			name:            "one page inherits the object the other reaches through its own /Resources",
			plain:           viaOwnResources,
			laterPageShares: true,
		},
		{
			name:            "two pages stating the SAME /Resources object, no inheritance",
			plain:           sameOwnResources,
			laterPageShares: true,
		},
		{
			name:            "one page inheriting an indirect /Resources",
			plain:           func(t *testing.T) []byte { return indirect(t, 1, false) },
			laterPageShares: false, // there is no later page
		},
		{
			name:            "a sub-node's indirect /Resources with ONE page under it",
			plain:           func(t *testing.T) []byte { return deep(t, 1) },
			laterPageShares: false, // the object is that page's alone
		},
		{
			name:            "a sub-node's indirect /Resources with TWO pages under it",
			plain:           func(t *testing.T) []byte { return deep(t, 2) },
			laterPageShares: true,
		},
		{
			name:            "pages sharing a /Contents while one inherits an unrelated key",
			plain:           falseKey,
			laterPageShares: true, // the /Contents, never the private /Rotate
		},
		{
			name:            "two pages with inheritable values all DIRECT",
			plain:           func(t *testing.T) []byte { return build(t, 2, nil, nil) },
			laterPageShares: false, // an inline /MediaBox array adds no object
		},
		{
			// ⛔ "EACH STATING THEIR OWN" WAS THE OLD NAME AND IT NAMED THE
			// WRONG REASON. Nothing is shared here because the two objects
			// DIFFER, not because the pages state them — the row below, where
			// both inherit the SAME object, shares.
			name:            "two pages each naming a DIFFERENT /Resources object",
			plain:           func(t *testing.T) []byte { return indirect(t, 2, true) },
			laterPageShares: false,
		},
		{
			name:            "two pages inheriting ONE indirect /Resources",
			plain:           func(t *testing.T) []byte { return indirect(t, 2, false) },
			laterPageShares: true,
		},
		{
			name:            "twenty-five pages inheriting ONE indirect /Resources",
			plain:           func(t *testing.T) []byte { return indirect(t, 25, false) },
			laterPageShares: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := Open(tc.plain(t))
			if err != nil {
				t.Fatal(err)
			}
			out, err := d.Linearize()
			if err != nil {
				t.Fatalf("this shape was refused: %v", err)
			}
			// ⛔ And it is a file, not merely an absence of error.
			lin, err := CheckLinearized(out)
			if err != nil {
				t.Fatalf("written, but our own checker refuses it: %v", err)
			}

			// ⛔⛔ READ BACK FROM THE BYTES, not from the plan that wrote them.
			// A witness taken from planShared would agree with the writer by
			// construction; this one parses the F.4 entries out of the file and
			// so also exercises the column-major decode.
			ld, err := Open(out)
			if err != nil {
				t.Fatal(err)
			}
			tables, err := ld.readHintTables(lin)
			if err != nil {
				t.Fatalf("the hint tables of our own file do not parse: %v", err)
			}
			if got, want := len(tables.pages), len(d.pageRefs()); got != want {
				t.Fatalf("the page table describes %d page(s) of %d", got, want)
			}

			refs := 0
			for _, e := range tables.pages[1:] {
				refs += len(e.Shared)
			}
			if tc.laterPageShares && refs == 0 {
				t.Errorf("a page after the first shares an object and named none: "+
					"%d shared entr(ies) in all, %d for the first page",
					tables.shared.NSharedTotal, tables.shared.NSharedFirstPage)
			}
			if !tc.laterPageShares && refs != 0 {
				t.Errorf("no page after the first shares anything, yet %d identifier(s) "+
					"were written: a reader will fetch objects it does not need", refs)
			}
		})
	}
}

// ⛔⛔ EVERY WAY THE /Parent CHAIN STOPS, and none of them is a refusal. A page
// whose /Parent dangles or names a non-dictionary inherits nothing, which is the
// same answer a page with no /Parent gets — and that is the right one, because a
// broken page tree is not this function's business to diagnose. pushInherited
// walks the same chain and gives up the same way.
//
// ⛔ THE CYCLE CASE IS THE ONE THAT MATTERS: the walk is bounded by a SEEN SET
// rather than a depth, so a /Parent loop returns instead of hanging. The timeout
// is what makes that a measurement rather than a claim.
func TestTheInheritanceWalkEndsWhereverTheChainDoes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		objects map[int]Object
		badXref int // an object number whose xref entry points past the buffer
		page    Dict
	}{
		{
			name:    "no parent at all",
			objects: nil,
			page:    Dict{"Type": Name("Page")},
		},
		{
			// ⛔ A MISSING OBJECT IS NOT A READ FAILURE. The first version of
			// this case used Ref{Num: 99} with no object behind it, and Get
			// returned a Null without an error — so it exercised the
			// not-a-dictionary branch and left the unreadable one uncovered,
			// while its name claimed otherwise. A bad xref offset is what makes
			// Get actually fail.
			name:    "a parent that does not read",
			objects: nil,
			badXref: 4,
			page:    Dict{"Type": Name("Page"), "Parent": Ref{Num: 4}},
		},
		{
			name:    "a parent that is missing entirely",
			objects: nil,
			page:    Dict{"Type": Name("Page"), "Parent": Ref{Num: 99}},
		},
		{
			name:    "a parent that is not a dictionary",
			objects: map[int]Object{2: Integer(7)},
			page:    Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}},
		},
		{
			name: "a parent chain that loops",
			objects: map[int]Object{
				2: Dict{"Type": Name("Pages"), "Parent": Ref{Num: 3}},
				3: Dict{"Type": Name("Pages"), "Parent": Ref{Num: 2}},
			},
			page: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := bareDoc(nil, nil, tc.objects)
			if tc.badXref != 0 {
				d.buf = []byte("%PDF-1.7\n")
				d.xref[tc.badXref] = xrefEntry{kind: 'n', offset: 100000}
			}
			done := make(chan []Object, 1)
			go func() {
				done <- d.inheritedAttributes(Ref{Num: 1}, tc.page)
			}()
			select {
			case got := <-done:
				if len(got) != 0 {
					t.Errorf("it inherited %v from a chain that leads nowhere", got)
				}
			case <-timeAfterSeconds(5):
				t.Fatal("the inheritance walk did not return")
			}
		})
	}
}

// ⛔ THE MESSAGE HELPER NAMES NOTHING WHEN IT CANNOT READ THE PAGE, and it must
// not panic doing so. It runs only on the refusal path, where Partition has
// already read every page — so these two are unreachable from Linearize and
// reached here instead, the same treatment as the emission's error seams.
//
// ⚠ A helper that builds an error message is the worst place for a second
// failure: it would replace a diagnosis with a crash.
func TestTheInheritedKeyNamesNothingItCannotRead(t *testing.T) {
	t.Run("a page that does not read", func(t *testing.T) {
		d := bareDoc(nil, nil, nil)
		d.buf = []byte("%PDF-1.7\n")
		d.xref[1] = xrefEntry{kind: 'n', offset: 100000}
		if got := d.inheritedByReference(Ref{Num: 1}, []int{2}); got != nil {
			t.Errorf("it named %v for a page that does not read", got)
		}
	})

	t.Run("a page that is not a dictionary", func(t *testing.T) {
		d := bareDoc(nil, nil, map[int]Object{1: Integer(7)})
		if got := d.inheritedByReference(Ref{Num: 1}, []int{2}); got != nil {
			t.Errorf("it named %v for a page that is not a dictionary", got)
		}
	})

	t.Run("a page inheriting a DIRECT value names nothing", func(t *testing.T) {
		// ⛔ The message is about an object the caller cannot find, so a direct
		// inherited value — which adds no object — must not be named.
		d := bareDoc(nil, nil, map[int]Object{
			1: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}},
			2: Dict{"Type": Name("Pages"),
				"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}},
		})
		if got := d.inheritedByReference(Ref{Num: 1}, []int{3}); got != nil {
			t.Errorf("it named %v for an inherited value that is not a reference", got)
		}
	})

	t.Run("an inherited reference that is not the shared object", func(t *testing.T) {
		// ⛔⛔ THE CONJUNCTION REVIEW CONSTRUCTED: the page DOES inherit a
		// reference and that reference is NOT what it shares. Asking only "does
		// this page inherit anything by reference" made both halves true and
		// the sentence false.
		d := bareDoc(nil, nil, map[int]Object{
			1: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}},
			2: Dict{"Type": Name("Pages"), "Rotate": Ref{Num: 9}},
		})
		if got := d.inheritedByReference(Ref{Num: 1}, []int{4}); got != nil {
			t.Errorf("it named %v for a key whose object is not among the shared ones", got)
		}
		// And it DOES name it when the inherited reference is the shared object.
		if got := d.inheritedByReference(Ref{Num: 1}, []int{9}); len(got) != 1 ||
			got[0] != "Rotate" {
			t.Errorf("it named %v when /Rotate's own object is the shared one", got)
		}
	})
}

// ⛔ AND THE NEAREST ANCESTOR WINS. Table 30 takes the first value found walking
// up, so a /Resources on the parent SHADOWS one on the grandparent — and
// collecting both would put an object the page cannot reach into its reach,
// which is this same defect in the opposite direction.
func TestInheritanceTakesTheNearestAncestorOnly(t *testing.T) {
	near := Ref{Num: 10}
	far := Ref{Num: 11}
	d := bareDoc(nil, nil, map[int]Object{
		2: Dict{"Type": Name("Pages"), "Parent": Ref{Num: 3}, "Resources": near},
		3: Dict{"Type": Name("Pages"), "Resources": far},
	})

	got := d.inheritedAttributes(Ref{Num: 1},
		Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}})
	if len(got) != 1 {
		t.Fatalf("it inherited %v, and only the parent's /Resources is inheritable here", got)
	}
	if r, ok := got[0].(Ref); !ok || r.Num != near.Num {
		t.Errorf("it inherited %v, want the PARENT's %v and not the grandparent's %v",
			got[0], near, far)
	}
}

// resourcesObject returns the object number the page tree node's /Resources
// names, read from the document rather than assumed from the fixture's order.
func resourcesObject(t *testing.T, d *Document) int {
	t.Helper()
	cat, err := d.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	node, err := d.Get(cat.Get("Pages").(Ref))
	if err != nil {
		t.Fatal(err)
	}
	dict, ok := ToDict(node)
	if !ok {
		t.Fatalf("the page tree node is %T", node)
	}
	ref, ok := dict["Resources"].(Ref)
	if !ok {
		t.Fatalf("the fixture's /Resources is %T, not a reference, so it witnesses nothing",
			dict["Resources"])
	}
	return ref.Num
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// inheritedResourcesFixture writes a two-page document whose /Resources is an
// INDIRECT REFERENCE on the page tree node, so neither page names it.
func inheritedResourcesFixture(t *testing.T) []byte {
	t.Helper()
	w := NewWriter("1.7")
	pagesRef := w.Reserve()
	res := w.Add(Dict{"ProcSet": Array{Name("PDF"), Name("Text")}})
	c1 := w.Add(&Stream{Dict: Dict{}, Raw: []byte("BT /F1 12 Tf 72 720 Td (one) Tj ET")})
	c2 := w.Add(&Stream{Dict: Dict{}, Raw: []byte("BT /F1 12 Tf 72 720 Td (two) Tj ET")})
	one := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef, "Contents": c1})
	two := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef, "Contents": c2})
	w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": Array{one, two},
		"Count":     Integer(2),
		"Resources": res,
		"MediaBox":  Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
	root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})

	plain, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

// ⛔ AND THE NEGATIVE CONTROL: the documents the suite does judge must have
// nothing shared, or the refusal above would reject them too and the whole
// corpus would be unreachable.
//
// ⭐ "A test suite in which every input passes measures nothing" — and one in
// which every input is refused measures nothing either. This is the line
// between the two.
func TestTheJudgedCorpusSharesNothingBetweenPages(t *testing.T) {
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
			for i, s := range p.Shared {
				if len(s) != 0 {
					t.Errorf("page %d shares %v, so buildPages is no longer the "+
						"independent-pages fixture this corpus assumes", i+1, s)
				}
			}
		})
	}
}

// sharedContentsFixture writes a two-page document whose pages share ONE content
// stream, so that page 2 reaches an object living in part 6.
func sharedContentsFixture(t *testing.T) []byte {
	t.Helper()
	w := NewWriter("1.7")
	pagesRef := w.Reserve()
	shared := w.Add(&Stream{Dict: Dict{}, Raw: []byte("BT /F1 12 Tf 72 720 Td (both) Tj ET")})
	one := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef,
		"Contents": shared, "Resources": Dict{}})
	two := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef,
		"Contents": shared, "Resources": Dict{}})
	w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": Array{one, two},
		"Count":    Integer(2),
		"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
	root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})

	plain, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

// ⛔⛔ A VALUE TOO WIDE FOR ITS 32-BIT FIELD IS REFUSED, NOT TRUNCATED. No
// fixture can reach this — the suite does not build a 4 GiB document — and that
// is precisely the reason: an untestable truncation in the one structure whose
// job is to be trusted would send a reader to a wrapped offset.
//
// ⛔ This package has named that class three times (#42, #53, #58) and then did
// it anyway, which is why the refusal is tested by hand here.
func TestTheHintTablesRefuseAValueTooWideForItsField(t *testing.T) {
	// ⛔⛔ NOT A CONSTANT, AND NOT ON A 32-BIT BUILD. `int(math.MaxUint32) + 1`
	// is a constant expression, so it does not COMPILE where int is 32 bits —
	// caught by `go vet` on linux/386, which is why this repository vets three
	// architectures. A runtime conversion compiles everywhere, and on a 32-bit
	// build the value cannot be represented at all, so there is nothing to test.
	if math.MaxInt <= math.MaxUint32 {
		t.Skip("int is 32 bits here, so a value above MaxUint32 cannot be built — " +
			"the refusal is for 64-bit builds, where it is reachable")
	}
	var limit int64 = math.MaxUint32
	tooBig := int(limit) + 1

	for _, tc := range []struct {
		name  string
		pages []pageExtent
		first int
		want  string
	}{
		{
			name:  "a page longer than 4 GiB",
			pages: []pageExtent{{objects: 1, start: 0, end: tooBig, length: tooBig}},
			first: 10,
			want:  "length in bytes",
		},
		{
			name:  "a page of more than 4 Gi objects",
			pages: []pageExtent{{objects: tooBig, start: 0, end: 10, length: 10}},
			first: 10,
			want:  "object count",
		},
		{
			// ⛔ THE TWO CONTENT GUARDS ARE ASSERTED SEPARATELY, because a
			// `want` of "content stream" matched both names and so could not
			// tell them apart. The first version of the guard checked
			// contentEnd-start for item 6 while the stored value is
			// contentStart-start — conservative, so nothing was truncated, but
			// it REFUSED a describable page whose offset and length each fit a
			// uint32 while their sum did not, under a message printing a third
			// number.
			name: "a content stream offset past 4 GiB",
			pages: []pageExtent{{objects: 1, start: 0, end: tooBig + 2, length: 10,
				hasContent: true, contentStart: tooBig + 1, contentEnd: tooBig + 1}},
			first: 10,
			want:  "content stream offset",
		},
		{
			name: "a content stream longer than 4 GiB",
			pages: []pageExtent{{objects: 1, start: 0, end: tooBig + 2, length: 10,
				hasContent: true, contentStart: 1, contentEnd: tooBig + 1}},
			first: 10,
			want:  "content stream length",
		},
		{
			name:  "a first page past 4 GiB",
			pages: []pageExtent{{objects: 1, start: 0, end: 10, length: 10}},
			first: tooBig + 1,
			want:  "item 2 would be",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := buildHintTables(tc.pages, sharedInput{}, tc.first, 1)
			if !errors.Is(err, errHintTable) {
				t.Fatalf("it was truncated instead of refused: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refused, but not for %q: %v", tc.want, err)
			}
		})
	}
}

// ⛔⛔ widthFor TERMINATES ON A 32-BIT DELTA, which it did not: `1<<n` takes the
// delta's type, so at n == 32 a uint32 shift gives 0, the comparison stays true
// and the loop spins forever. Measured on 2^31 before the bound was added.
//
// ⛔ No caller reaches it today, because the three byte-measured widths are
// pinned and the derived ones are counts. It is the function a derived byte
// width would go through — exactly when a 32-bit delta turns up.
func TestWidthForTerminatesAtThirtyTwoBits(t *testing.T) {
	of := func(e pageEntry) uint32 { return e.Length }
	for _, delta := range []uint32{1 << 30, 1 << 31, math.MaxUint32} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			done := make(chan int, 1)
			go func() {
				done <- widthFor([]pageEntry{{Length: 0}, {Length: delta}}, of, 0)
			}()
			select {
			case got := <-done:
				if got < 1 || got > 32 {
					t.Errorf("widthFor = %d, and F.4.1 bounds every width to 0..32", got)
				}
			case <-timeAfterSeconds(5):
				t.Fatalf("widthFor did not return for a delta of %d", delta)
			}
		})
	}
}

// ⛔ THE TWO ENCODER ERRORS, THROUGH THEIR SEAMS. buildHintTables constructs
// inputs neither encoder can reject, so these are the propagations and not the
// encoders — the encoders have their own tests, judged against qpdf's bytes.
func TestTheHintTablesPropagateAnEncoderRefusal(t *testing.T) {
	pages := []pageExtent{{objects: 2, start: 10, end: 50, length: 40}}
	boom := fmt.Errorf("%w: a refusal planted by the test", errHintTable)

	t.Run("the page offset table", func(t *testing.T) {
		saved := writePageOffset
		defer func() { writePageOffset = saved }()
		writePageOffset = func(*pageOffsetHeader, []pageEntry) ([]byte, error) {
			return nil, boom
		}
		if _, _, err := buildHintTables(pages, sharedInput{}, 100, 10); !errors.Is(err, boom) {
			t.Fatalf("the page offset table's refusal did not reach the caller: %v", err)
		}
	})

	t.Run("the shared table", func(t *testing.T) {
		saved := writeShared
		defer func() { writeShared = saved }()
		writeShared = func(*sharedHeader, []sharedGroup) ([]byte, error) {
			return nil, boom
		}
		if _, _, err := buildHintTables(pages, sharedInput{}, 100, 10); !errors.Is(err, boom) {
			t.Fatalf("the shared table's refusal did not reach the caller: %v", err)
		}
	})
}

// ⛔ AND THE EMISSION PROPAGATES ITS OWN, through the buildHint seam, at BOTH
// call sites: once before part 6 to learn the stream's length, once after the
// file exists to build the tables the next pass writes. A test of one would
// leave the other free to swallow the error.
func TestTheEmissionPropagatesAHintTableRefusal(t *testing.T) {
	boom := fmt.Errorf("%w: a refusal planted by the test", errHintTable)

	for _, tc := range []struct {
		name  string
		after int // let this many calls succeed before failing
	}{
		{"the call that sizes the stream", 0},
		{"the call that builds the next pass's tables", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := buildHint
			defer func() { buildHint = saved }()
			calls := 0
			buildHint = func(p []pageExtent, s sharedInput, first, hint int) ([]byte, int, error) {
				if calls >= tc.after {
					calls++
					return nil, 0, boom
				}
				calls++
				return saved(p, s, first, hint)
			}

			d, err := Open(buildPages(t, 1))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Linearize(); !errors.Is(err, boom) {
				t.Fatalf("the refusal did not reach Linearize: %v", err)
			}
			if calls != tc.after+1 {
				t.Errorf("the seam ran %d time(s) and the case wanted %d",
					calls, tc.after+1)
			}
		})
	}
}

// ⛔⛔ A MINIMUM AND A WIDTH THAT COME FROM A LATER PAGE, which every fixture in
// this package fails to exercise: buildPages makes pages of equal size, so the
// first page is already the smallest and the largest and `v < least` never
// fires. A table built from uniform pages cannot tell leastOf from "return the
// first entry".
//
// ⭐ The expectations are computed here by hand rather than from the code: with
// lengths 40, 10 and 70 the least is 10 and the widest delta is 60, which needs
// six bits because 2^5 is 32 and 2^6 is 64.
func TestTheLeastAndTheWidthComeFromWhicheverPageHoldsThem(t *testing.T) {
	entries := []pageEntry{{Length: 40}, {Length: 10}, {Length: 70}}
	length := func(e pageEntry) uint32 { return e.Length }

	if got := leastOf(entries, length); got != 10 {
		t.Errorf("leastOf = %d, want 10 — it did not look past the first entry", got)
	}
	if got := widthFor(entries, length, 10); got != 6 {
		t.Errorf("widthFor = %d, want 6 bits for a delta of 60", got)
	}

	// ⛔ AND NEVER ZERO, for a column that does not vary: F.4.1 allows a width
	// of 0 and bitw.write(0, 0) is a path nothing here has exercised.
	flat := []pageEntry{{Length: 7}, {Length: 7}}
	if got := widthFor(flat, length, 7); got != 1 {
		t.Errorf("widthFor on a constant column = %d, want 1", got)
	}
}

// ⛔⛔ A PAGE WHOSE /Contents IS AN ARRAY, which is a legal shape no fixture in
// this package had: Table F.4's items 6 and 7 name the content stream, and a
// page may hold several. The extent has to span all of them.
//
// ⛔ And this is not only about coverage. A writer that handled only the single
// Ref would report hasContent false for such a page and send a streaming reader
// to fetch nothing — silently, because qpdf checks neither item.
func TestAPageWithSeveralContentStreams(t *testing.T) {
	w := NewWriter("1.7")
	pagesRef := w.Reserve()
	first := w.Add(&Stream{Dict: Dict{}, Raw: []byte("0 g")})
	second := w.Add(&Stream{Dict: Dict{}, Raw: []byte("1 0 0 RG 10 w")})
	page := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef,
		"Contents": Array{first, second}, "Resources": Dict{}})
	w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": Array{page},
		"Count":    Integer(1),
		"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
	root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})

	plain, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(plain)
	if err != nil {
		t.Fatal(err)
	}

	p, err := d.Partition()
	if err != nil {
		t.Fatal(err)
	}
	if got := d.contentNumbers(p, 0); len(got) != 2 {
		t.Fatalf("contentNumbers found %v, want both content streams", got)
	}

	out, err := d.Linearize()
	if err != nil {
		t.Fatalf("a page with two content streams was refused: %v", err)
	}
	if _, err := CheckLinearized(out); err != nil {
		t.Fatalf("our own checker refuses it: %v", err)
	}
}

// ⛔ EVERY WAY contentNumbers FINDS NOTHING, which the emission cannot reach
// because Partition always supplies as many page objects as page sets.
func TestContentNumbersFindsNothingToReport(t *testing.T) {
	t.Run("a page index with no page object", func(t *testing.T) {
		d := bareDoc(nil, nil, nil)
		p := &Partition{Pages: [][]int{{1}}, PageObjects: nil}
		if got := d.contentNumbers(p, 0); got != nil {
			t.Errorf("it reported %v for a page it cannot identify", got)
		}
	})

	t.Run("a page object that does not read", func(t *testing.T) {
		d := bareDoc(nil, nil, nil)
		d.buf = []byte("%PDF-1.7\n")
		d.xref[1] = xrefEntry{kind: 'n', offset: 100000}
		p := &Partition{Pages: [][]int{{1}}, PageObjects: []int{1}}
		if got := d.contentNumbers(p, 0); got != nil {
			t.Errorf("it reported %v for a page that does not read", got)
		}
	})

	t.Run("a page object that is not a dictionary", func(t *testing.T) {
		d := bareDoc(nil, nil, map[int]Object{1: Integer(7)})
		p := &Partition{Pages: [][]int{{1}}, PageObjects: []int{1}}
		if got := d.contentNumbers(p, 0); got != nil {
			t.Errorf("it reported %v for a page that is not a dictionary", got)
		}
	})

	t.Run("a /Contents the page's own set does not hold", func(t *testing.T) {
		// ⛔ THE FILTER IS NOT A FORMALITY: an extent taken from an offset
		// outside the page would make items 6 and 7 describe bytes that are
		// not the page's.
		d := bareDoc(nil, nil, map[int]Object{
			1: Dict{"Type": Name("Page"), "Contents": Ref{Num: 99}},
		})
		p := &Partition{Pages: [][]int{{1}}, PageObjects: []int{1}}
		if got := d.contentNumbers(p, 0); got != nil {
			t.Errorf("it reported %v for a stream the page does not own", got)
		}
	})

	t.Run("an /Contents array of things that are not references", func(t *testing.T) {
		d := bareDoc(nil, nil, map[int]Object{
			1: Dict{"Type": Name("Page"), "Contents": Array{Integer(1), Name("x")}},
		})
		p := &Partition{Pages: [][]int{{1}}, PageObjects: []int{1}}
		if got := d.contentNumbers(p, 0); got != nil {
			t.Errorf("it reported %v for an array holding no references", got)
		}
	})
}

// ⛔⛔ TABLE F.5 ITEM 5 IS A WIDTH FOR "1 LESS THAN THE NUMBER", and this writer
// cannot reach the part of groupCountWidth that depends on it: measureShared
// makes every group hold exactly ONE object, so the widest value it ever weighs
// is 0 and one bit always suffices.
//
// ⛔ WHICH IS WHY THE TEST CALLS THE FUNCTION DIRECTLY. The alternative was to
// write the constant 1 at the call site and delete the derivation — but the day
// a group holds two objects, a header saying "1 bit" makes a reader misparse
// EVERY group that follows, and a derivation no test can enter is no better
// than the constant. The rows below are the measurement the writer cannot make.
//
// ⭐ The "1 less" is F.6's, not ours: a group of 1 stores 0. So 5 objects need
// the width of 4, which is 3 bits, not the 3 bits of 5 by accident — the row
// with 9 objects separates them, storing 8 in 4 bits where 9 would need 4 too,
// and the row with 17 stores 16 and needs 5.
func TestTheGroupCountWidthHoldsOneLessThanTheCount(t *testing.T) {
	for _, tc := range []struct {
		name   string
		counts []uint32
		want   int
	}{
		{"no groups at all", nil, 1},
		{"every group holding ONE object, which is all this writer emits",
			[]uint32{1, 1, 1}, 1},
		{"a group of two stores 1", []uint32{1, 2}, 1},
		{"a group of three stores 2", []uint32{3, 1}, 2},
		{"a group of five stores 4", []uint32{1, 5, 2}, 3},
		{"a group of nine stores 8", []uint32{9}, 4},
		{"a group of seventeen stores 16", []uint32{17, 3}, 5},
		{
			// ⛔ A GROUP OF ZERO OBJECTS IS NOT A GROUP, and uint32(0)-1 is
			// 4294967295: the guard is what keeps a malformed input from
			// declaring a 32-bit width.
			name:   "a group of NO objects does not widen anything",
			counts: []uint32{0, 1},
			want:   1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groups := make([]sharedGroup, 0, len(tc.counts))
			for _, n := range tc.counts {
				groups = append(groups, sharedGroup{NObjects: n, Length: 10})
			}
			if got := groupCountWidth(groups); got != tc.want {
				t.Errorf("groupCountWidth(%v) = %d bits, want %d", tc.counts, got, tc.want)
			}
		})
	}
}

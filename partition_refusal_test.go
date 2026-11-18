// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"errors"
	"strings"
	"testing"
)

// ⛔ Part's numbers are F.3.1's, so String names the part the annex names and
// a failed comparison reads as prose. The default arm matters because a Part
// is an int: a caller can make one the annex does not define, and a bare "7"
// in a message would be indistinguishable from a part number.
func TestEveryPartNamesItselfTheWayAnnexFDoes(t *testing.T) {
	for _, c := range []struct {
		p    Part
		want string
	}{
		{PartCatalog, "part 4"},
		{PartFirstPage, "part 6"},
		{PartOtherPrivate, "part 7"},
		{PartShared, "part 8"},
		{PartOther, "part 9"},
		{Part(5), "part 5 (not a part this package assigns)"},
		{Part(0), "part 0 (not a part this package assigns)"},
	} {
		got := c.p.String()
		if !strings.Contains(got, c.want) {
			t.Errorf("Part(%d).String() = %q, want it to contain %q", int(c.p), got, c.want)
		}
	}
	// ⛔ AND THE PARTS THE LAYOUT PRODUCES ARE NOT ASSIGNABLE. Parts 1, 2, 3,
	// 5, 10 and 11 are the header, the cross-reference tables, the hint
	// streams and the trailer; naming one as though an object could be placed
	// there would invite exactly that.
	for _, p := range []Part{1, 2, 3, 5, 10, 11} {
		if !strings.Contains(p.String(), "not a part this package assigns") {
			t.Errorf("Part(%d) names itself assignable: %q", int(p), p.String())
		}
	}
}

// bareDoc builds a Document with nothing read from a file, so that a single
// refusal can be reached without a fixture that would have to be valid in
// every other respect.
//
// ⛔ The cache is pre-filled rather than parsed: Get consults it first
// (document.go), so this is the package's own seam for handing a document an
// object, and it keeps each case about the ONE key it is named for.
func bareDoc(trailer Dict, pages []Ref, objects map[int]Object) *Document {
	cache := map[int]Object{}
	for num, o := range objects {
		cache[num] = o
	}
	return &Document{
		trailer: trailer,
		pages:   pages,
		xref:    map[int]xrefEntry{},
		cache:   cache,
		loading: map[int]bool{},
		objStms: map[int]map[int]Object{},
	}
}

// ⛔⛔ EVERY CONSTRUCT THE PARTITION DECLINES, REFUSED BY NAME AND FOR ITS OWN
// REASON. Annex F.3.5, F.3.7 and F.3.10 place objects by what refers to them,
// and each key below moves an object between parts. A partition that guessed
// would produce hint tables pointing a reader at the wrong offsets — which
// does not make the reader fail, it makes it read the wrong bytes.
//
// ⛔ The message is asserted, not only the error, because "cannot partition"
// without the key is a refusal the next person cannot act on. Each case names
// the clause that places the thing.
func TestEveryUnplaceableConstructIsRefusedByName(t *testing.T) {
	for _, c := range []struct {
		name    string
		cat     Dict
		trailer Dict
		pages   []Ref
		objects map[int]Object
		want    string
	}{
		{
			name: "an /OpenAction, which chooses the first page",
			cat:  Dict{"OpenAction": Array{Ref{Num: 9}, Name("Fit")}},
			want: "/OpenAction",
		},
		{
			name: "/Threads, which splits across parts 4, 9 and the pages",
			cat:  Dict{"Threads": Ref{Num: 9}},
			want: "/Threads",
		},
		{
			name: "/AcroForm, whose top level and field hierarchy part company",
			cat:  Dict{"AcroForm": Ref{Num: 9}},
			want: "/AcroForm",
		},
		{
			name: "/PageMode /UseOutlines with an outline hierarchy",
			cat:  Dict{"PageMode": Name("UseOutlines"), "Outlines": Ref{Num: 9}},
			want: "the entire outline hierarchy in part 6",
		},
		{
			name: "/ViewerPreferences, whose value F.3.5 puts in part 4",
			cat:  Dict{"ViewerPreferences": Ref{Num: 9}},
			want: "/ViewerPreferences",
		},
		{
			name: "an indirect /PageMode, which is an object part 4 must hold",
			cat:  Dict{"PageMode": Ref{Num: 9}},
			want: "/PageMode is an indirect object",
		},
		{
			name:    "an encrypted file",
			cat:     Dict{},
			trailer: Dict{"Encrypt": Ref{Num: 9}},
			want:    "the file is encrypted",
		},
		{
			name:    "a page with a /Thumb",
			cat:     Dict{},
			pages:   []Ref{{Num: 7}},
			objects: map[int]Object{7: Dict{"Type": Name("Page"), "Thumb": Ref{Num: 8}}},
			want:    "/Thumb",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := bareDoc(c.trailer, c.pages, c.objects)
			err := d.refuseUnplaceable(c.cat)
			if !errors.Is(err, ErrCannotPartition) {
				t.Fatalf("it was accepted (or refused oddly): %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("it was refused, but the message does not name %q: %v", c.want, err)
			}
		})
	}
}

// ⛔ AND THE NEAR MISSES ARE ACCEPTED, which is what makes the refusals above
// about their key rather than about any key. Each of these differs from a
// refused case in exactly one thing.
func TestTheConstructsThatChangeNoPlacementAreAccepted(t *testing.T) {
	for _, c := range []struct {
		name string
		cat  Dict
	}{
		// ⛔ /PageMode alone moves nothing: it is UseOutlines TOGETHER WITH an
		// outline hierarchy that takes that hierarchy into part 6. Refusing on
		// /PageMode would refuse /UseThumbs, which changes no placement.
		{"/PageMode /UseThumbs", Dict{"PageMode": Name("UseThumbs")}},
		{"/PageMode /UseOutlines with NO outline hierarchy", Dict{"PageMode": Name("UseOutlines")}},
		{"/Outlines with no /PageMode", Dict{"Outlines": Ref{Num: 9}}},
		{"a /PageMode that is not a name", Dict{"PageMode": Integer(2), "Outlines": Ref{Num: 9}}},
		// ⛔ A DIRECT /PageMode is accepted, which is what makes the indirect
		// refusal about its indirectness rather than about the key.
		{"a direct /PageMode name", Dict{"PageMode": Name("UseThumbs")}},
		{"nothing at all", Dict{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := bareDoc(Dict{}, nil, nil)
			if err := d.refuseUnplaceable(c.cat); err != nil {
				t.Errorf("it was refused, so the cases above prove nothing about their keys: %v", err)
			}
		})
	}
}

// ⛔⛔ AND EVERY STAGE OF Partition CARRIES ITS FAILURE OUT, which the pieces
// tested one by one above do not say: a function that refuses correctly and
// whose caller drops the error is a function that returns a partition built on
// a stage that failed. Each case below makes exactly one stage fail and asserts
// Partition itself refuses.
//
// ⛔ The stages are named in the case names rather than by line, because the
// point is the ORDER: Catalog, then placeability, then the page tree, then the
// first page's reach, then the other pages', then the trailer sweep. A stage
// that moved would change which message a file gets, and a message is how the
// next person finds the rule.
func TestPartitionCarriesOutTheFailureOfEveryStage(t *testing.T) {
	// A document that partitions cleanly, which each case then breaks in one
	// place — so a failure is about the break and not about the fixture.
	ok := func() *Document {
		return bareDoc(Dict{"Root": Ref{Num: 1}}, []Ref{{Num: 3}}, map[int]Object{
			1: Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}},
			2: Dict{"Type": Name("Pages"), "Count": Integer(1), "Kids": Array{Ref{Num: 3}}},
			3: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}, "Contents": Ref{Num: 4}},
			4: &Stream{Dict: Dict{}, Raw: []byte("0 g")},
		})
	}
	if _, err := ok().Partition(); err != nil {
		t.Fatalf("the control does not partition, so no case below is about its own break: %v", err)
	}

	for _, c := range []struct {
		name   string
		break_ func(d *Document)
		want   string
	}{
		{
			name:   "the catalogue cannot be read",
			break_: func(d *Document) { d.trailer = nil },
			want:   "trailer",
		},
		{
			name: "the document is unplaceable",
			break_: func(d *Document) {
				d.cache[1] = Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}, "Threads": Ref{Num: 9}}
			},
			want: "/Threads",
		},
		{
			name: "a page tree node cannot be read",
			break_: func(d *Document) {
				// The ROOT must still resolve or Catalog fails first, so the
				// unreadable node is a kid of it.
				d.cache[2] = Dict{"Type": Name("Pages"), "Count": Integer(1), "Kids": Array{Ref{Num: 5}}}
				delete(d.cache, 5)
				d.buf = []byte("not a PDF at all")
				d.xref[5] = xrefEntry{kind: 'n', offset: 9999}
			},
			want: "cannot be read",
		},
		{
			name: "the first page is not a dictionary",
			break_: func(d *Document) {
				d.cache[3] = Integer(7)
			},
			want: "not a page dictionary",
		},
		{
			name: "a later page is not a dictionary",
			break_: func(d *Document) {
				d.pages = []Ref{{Num: 3}, {Num: 6}}
				d.cache[6] = Integer(7)
			},
			want: "not a page dictionary",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := ok()
			c.break_(d)
			_, err := d.Partition()
			if err == nil {
				t.Fatalf("Partition returned a partition built on a stage that failed")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("it refused, but not for %q: %v", c.want, err)
			}
		})
	}
}

// ⛔ AND THE TRAILER SWEEP'S FAILURE TOO, which needs its own case because the
// only way to fail it is the work budget — and the budget has to be small
// enough to stop the trailer walk while leaving the page walks, which run
// first, intact. That the trailer walk is the LARGEST is what makes this
// possible: it starts from /Root and reaches the catalogue, the page tree,
// every page and everything they hold.
func TestPartitionCarriesOutTheTrailerSweepsFailure(t *testing.T) {
	d := bareDoc(Dict{"Root": Ref{Num: 1}}, []Ref{{Num: 3}}, map[int]Object{
		1: Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}},
		2: Dict{"Type": Name("Pages"), "Count": Integer(1), "Kids": Array{Ref{Num: 3}}},
		3: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}, "Contents": Ref{Num: 4}},
		4: &Stream{Dict: Dict{}, Raw: []byte("0 g")},
	})

	// ⛔ THE TRAILER SWEEP NOW HAS NO FAILURE TO CARRY OUT, which is the point:
	// its only one was the work budget, and the budget is gone because it
	// refused real documents. What remains is that the sweep RUNS and places
	// what it finds — asserted by the /Info test below — so this case is a
	// control that the stage is still reached rather than a refusal.
	p, err := d.Partition()
	if err != nil {
		t.Fatalf("the document stopped partitioning: %v", err)
	}
	if got := p.Of[4]; got != PartFirstPage {
		t.Errorf("the first page's content stream is in %v, want %v", got, PartFirstPage)
	}
}

// ⛔ AND WHAT THE TRAILER REACHES BUT NO PAGE DOES LANDS IN PART 9. The
// document information dictionary is the everyday case: nothing on any page
// refers to it, so without the sweep it would be absent from the partition —
// and an object absent from the partition is an object a layout drops.
func TestAnObjectOnlyTheTrailerReachesLandsInPartNine(t *testing.T) {
	d := bareDoc(Dict{"Root": Ref{Num: 1}, "Info": Ref{Num: 9}}, []Ref{{Num: 3}}, map[int]Object{
		1: Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}},
		2: Dict{"Type": Name("Pages"), "Count": Integer(1), "Kids": Array{Ref{Num: 3}}},
		3: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}},
		9: Dict{"Producer": String("a test")},
	})
	p, err := d.Partition()
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Of[9]; got != PartOther {
		t.Errorf("the information dictionary is in %v, want %v", got, PartOther)
	}
}

// ⛔⛔ A PAGE WHOSE OBJECT CANNOT BE READ IS REPORTED ONCE, BY reachFromPage,
// and this test is the one that holds the division of labour. refuseUnplaceable
// SKIPS it — placeability is not readability — and Partition then reports it
// from the one place that cannot proceed without reading it. Before that split
// the fact had two messages and reachFromPage's was unreachable from Partition.
//
// ⛔ Get errors on exactly one path: retryAfterRepair when repair() itself
// fails. So the buffer holds nothing a repair can find and the page's entry
// points outside it. Measured — an entry pointing at bytes that merely fail to
// parse is REPAIRED and succeeds, which is this reader being forgiving by
// design and is why an earlier version of this fixture proved nothing.
// ⛔⛔ AND EACH HALF GETS ITS OWN FIXTURE, which cost a failure to learn: a
// failed Get calls repair(), repair rebuilds the tables FROM THE BUFFER, and
// this buffer holds nothing — so the rebuild empties the hand-filled cache and
// every later lookup, /Root included, returns Null. Probing twice on one
// document measured the SECOND probe against a document the first had
// demolished.
func TestAPageWhoseObjectCannotBeReadIsReportedOnceByReachFromPage(t *testing.T) {
	make := func() *Document {
		d := bareDoc(Dict{"Root": Ref{Num: 1}}, []Ref{{Num: 3}}, map[int]Object{
			1: Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}},
			2: Dict{"Type": Name("Pages"), "Count": Integer(1), "Kids": Array{Ref{Num: 3}}},
		})
		d.buf = []byte("not a PDF at all")
		d.xref[3] = xrefEntry{kind: 'n', offset: 9999}
		return d
	}

	// refuseUnplaceable skips it rather than reporting it.
	if err := make().refuseUnplaceable(Dict{}); err != nil {
		t.Fatalf("refuseUnplaceable reported a readability problem, which is not its question: %v", err)
	}

	// ⛔⛔ AND reachFromPage IS ASKED DIRECTLY, BECAUSE Partition CANNOT REACH
	// THIS MESSAGE AND THAT IS A FACT ABOUT THE READER RATHER THAN A GAP IN
	// THE TEST. Get returns an error only when repair() fails; a repair that
	// SUCCEEDS turns a missing object into Null, which arrives as "not a page
	// dictionary". And a repair that fails rebuilds the tables from a buffer
	// that holds nothing, which empties the document — so by the time
	// Partition reached reachFromPage there would be no pages left to walk.
	// Measured both ways before writing this down.
	_, err := make().reachFromPage(Ref{Num: 3}, nil)
	if !errors.Is(err, ErrCannotPartition) {
		t.Fatalf("an unreadable page object was walked: %v", err)
	}
	if !strings.Contains(err.Error(), "page object 3 cannot be read") {
		t.Errorf("it refused, but not by object number: %v", err)
	}
}

// ⛔⛔ AND PART 8 HAS NO WITNESS IN THE GENERATED CORPUS, which the agreement
// test says out loud: qpdf reports "part 8 (other pages, shared): none" for all
// twelve shapes, because the fixtures it builds give every page its own
// resources. So the one arm that places a SHARED object — F.3.9's whole subject
// — was never taken by any of them, and a partition that put shared objects in
// part 7 would have agreed with qpdf on every file in the corpus.
//
// ⛔ F.3.9 is explicit about the two conditions: "referenced from more than one
// page but ... not referenced (directly or indirectly) from the first page".
// Both halves are tested here — object 20 is shared by pages 2 and 3, and
// object 21 is shared by page 2 and the FIRST page, which keeps it in part 6
// however many other pages want it.
func TestAnObjectLaterPagesShareLandsInPartEightAndOneTheFirstPageWantsDoesNot(t *testing.T) {
	d := bareDoc(Dict{"Root": Ref{Num: 1}}, []Ref{{Num: 3}, {Num: 4}, {Num: 5}}, map[int]Object{
		1: Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}},
		2: Dict{"Type": Name("Pages"), "Count": Integer(3),
			"Kids": Array{Ref{Num: 3}, Ref{Num: 4}, Ref{Num: 5}}},
		// The first page wants object 21.
		3: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}, "Resources": Ref{Num: 21}},
		// Pages 2 and 3 share object 20; page 2 also wants 21, and page 3 has
		// one of its own in 22.
		4: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2},
			"Resources": Array{Ref{Num: 20}, Ref{Num: 21}}},
		5: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2},
			"Resources": Array{Ref{Num: 20}, Ref{Num: 22}}},
		20: Dict{"Font": Name("shared-by-two-later-pages")},
		21: Dict{"Font": Name("wanted-by-the-first-page-too")},
		22: Dict{"Font": Name("private-to-the-last-page")},
	})
	p, err := d.Partition()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		num  int
		want Part
		why  string
	}{
		{20, PartShared, "two later pages reach it and the first page does not"},
		{21, PartFirstPage, "the first page reaches it, which outranks any number of later pages"},
		{22, PartOtherPrivate, "exactly one later page reaches it"},
	} {
		if got := p.Of[c.num]; got != c.want {
			t.Errorf("object %d is in %v, want %v — %s", c.num, got, c.want, c.why)
		}
	}
}

// ⛔⛔ AND A PAGE THAT NAMES ANOTHER PAGE DIRECTLY DOES NOT DRAG IT INTO PART
// 6, which the whole generated corpus cannot see — found by mutation, not by
// review. Emptying the page half of the boundary left all twelve qpdf shapes
// GREEN, because the only route from one page to another in those files runs
// through /Parent, and /Parent is skipped at the page already. So the page
// numbers in the boundary were doing nothing any test could observe.
//
// ⛔ They are not redundant in general, and F.3.7 says why: part 6 holds what
// the page object reaches "except page tree nodes OR OTHER PAGE OBJECTS". A
// link annotation's /Dest names another page, and /B article beads chain
// across pages — both are page-to-page references that do not pass through the
// tree. Without the boundary, one page with a link would pull the linked page,
// its contents and its resources into part 6, which is the opposite of what
// linearizing is for: the first page would carry the document.
func TestAPageThatLinksToAnotherPageDoesNotDragItIntoPartSix(t *testing.T) {
	d := bareDoc(Dict{"Root": Ref{Num: 1}}, []Ref{{Num: 3}, {Num: 4}}, map[int]Object{
		1: Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}},
		2: Dict{"Type": Name("Pages"), "Count": Integer(2), "Kids": Array{Ref{Num: 3}, Ref{Num: 4}}},
		// The first page carries a link annotation whose /Dest names page 2.
		3: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}, "Annots": Array{Ref{Num: 7}}},
		4: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}, "Contents": Ref{Num: 8}},
		7: Dict{"Type": Name("Annot"), "Subtype": Name("Link"),
			"Dest": Array{Ref{Num: 4}, Name("Fit")}},
		8: &Stream{Dict: Dict{}, Raw: []byte("0 g")},
	})
	p, err := d.Partition()
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Of[4]; got != PartOtherPrivate {
		t.Errorf("the linked page is in %v, want %v — a page reached through a /Dest was "+
			"pulled into the first page's section", got, PartOtherPrivate)
	}
	if got := p.Of[8]; got == PartFirstPage {
		t.Errorf("the linked page's content stream landed in part 6, so the first page " +
			"carries the document it links to")
	}
	// ⛔ The control: the annotation ITSELF does belong to part 6, or this
	// would hold against a walk that never left the page at all.
	if got := p.Of[7]; got != PartFirstPage {
		t.Errorf("the link annotation is in %v, want %v", got, PartFirstPage)
	}
}

// ⛔ catalogNumber IS ASKED DIRECTLY for the two ways it can have no answer.
// Partition cannot reach either — Catalog() fails first on a trailerless
// document, and a direct /Root is refused by Partition with its own message —
// so these are the only tests that hold them, and without them the function
// could return a number for a document that has none.
func TestCatalogNumberHasNoAnswerWithoutAnIndirectRoot(t *testing.T) {
	for _, c := range []struct {
		name    string
		trailer Dict
	}{
		{"no trailer at all", nil},
		{"a /Root that is a direct dictionary", Dict{"Root": Dict{"Type": Name("Catalog")}}},
		{"no /Root", Dict{"Size": Integer(4)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := bareDoc(c.trailer, nil, nil)
			if num, ok := d.catalogNumber(); ok {
				t.Errorf("it answered object %d for a document with no indirect catalogue", num)
			}
		})
	}
	// ⛔ THE CONTROL: an indirect /Root does have an answer, or the cases
	// above would hold against a function that always says no.
	d := bareDoc(Dict{"Root": Ref{Num: 12}}, nil, nil)
	num, ok := d.catalogNumber()
	if !ok || num != 12 {
		t.Errorf("catalogNumber() = %d, %v for an indirect /Root, want 12, true", num, ok)
	}
}

// ⛔ A document with no pages has no part 6, and part 6 is the point of the
// format: F.3.7 defines it as "all the objects needed to display the first
// page". Refusing is not a limitation, it is the absence of the thing being
// asked for.
func TestADocumentWithNoPagesIsRefused(t *testing.T) {
	d := bareDoc(Dict{"Root": Ref{Num: 1}}, []Ref{}, map[int]Object{
		1: Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}},
		2: Dict{"Type": Name("Pages"), "Count": Integer(0), "Kids": Array{}},
	})
	_, err := d.Partition()
	if !errors.Is(err, ErrCannotPartition) {
		t.Fatalf("a document with no pages was partitioned: %v", err)
	}
	if !strings.Contains(err.Error(), "no pages") {
		t.Errorf("it was refused, but not for having no pages: %v", err)
	}
}

// ⛔⛔ A PAGE NUMBERED 0 CANNOT BE NAMED BY ANY PAGE TREE WE WRITE. 7.3.10
// requires an indirect object's number to be a positive integer, and `0 0 R`
// is the null reference — so whatever that page holds, no reference in the
// file we write can point at it, and /O would name an object the structure
// cannot reach.
//
// ⭐ FOUND BY FUZZING THE WRITER, not by reading it. The writer emitted a file
// whose /O said object 2 while the page tree in that same file reached object
// 1, and CheckLinearized refused what Linearize had just produced — the first
// real writer/checker disagreement this package has had. The input is kept at
// testdata/fuzz/FuzzLinearize/b9472aa1230f3b85.
//
// ⛔ THE ZERO IS THE CAUSE AND NOT THE COMPANY IT KEEPS. The fuzzer's input
// also had a /Root that was itself typed /Page, no cross-reference table, and
// a malformed `8 00R` reference, so the cheap explanation was "it is garbage".
// Measured instead: a /Root that is ALSO a page, written with legal numbering
// and no repair path, is linearized and checks CLEAN. That control is below.
func TestAPageNumberedZeroIsRefused(t *testing.T) {
	in := []byte("0trailer <</Root 8 00R / 0/000[<0000000000000000000000000000000000000000>] >>" +
		"0000000000000A8 0 obj << /Pages << >>/Type /Page >>0 0 obj <</Type/Page>>")
	d, err := Open(in)
	if err != nil {
		t.Fatalf("the reader should still OPEN it — the refusal belongs to the "+
			"writer, not to Open: %v", err)
	}
	if _, err := d.Partition(); !errors.Is(err, ErrCannotPartition) {
		t.Fatalf("a document whose first page is object 0 was partitioned: %v", err)
	} else if !strings.Contains(err.Error(), "null reference") {
		t.Errorf("refused, but not for the reason that makes it unwritable: %v", err)
	}

	// ⛔ AND THE WHOLE WRITER REFUSES IT, not just the partition: before this
	// guard, Linearize returned a FILE, and only our own checker — run on the
	// bytes afterwards — noticed it contradicted itself.
	if _, err := d.Linearize(); !errors.Is(err, ErrCannotPartition) {
		t.Fatalf("Linearize still produced something for a page numbered 0: %v", err)
	}
}

// ⛔ THE CONTROL FOR THE ROW ABOVE: the same pathological SHAPE — a catalogue
// that is itself typed /Page — with legal object numbers is WRITTEN. Without
// it, "a page numbered 0 is refused" and "a weird page tree is refused" are
// the same sentence, and the guard could be far wider than its reason.
func TestARootThatIsAlsoAPageIsStillWritten(t *testing.T) {
	w := NewWriter("1.7")
	pagesRef := w.Reserve()
	c := w.Add(&Stream{Dict: Dict{}, Raw: []byte("BT /F1 12 Tf 72 720 Td (a) Tj ET")})
	page := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef,
		"Contents": c, "Resources": Dict{}})
	w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": Array{page},
		"Count":    Integer(1),
		"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
	root := w.Add(Dict{"Type": Name("Page"), "Pages": pagesRef})
	in, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Linearize()
	if err != nil {
		t.Fatalf("a catalogue typed /Page, with legal numbering, was refused: %v", err)
	}
	if _, err := CheckLinearized(out); err != nil {
		t.Fatalf("our own checker refuses it: %v", err)
	}
}

// ⛔ A DIRECT CATALOGUE HAS NO OBJECT NUMBER, and part 4 is a set of object
// numbers. The file is unusual rather than malformed — 7.5.5 requires /Root to
// be an indirect reference, so this is a file that breaks that — and the
// refusal says which of the two it is.
func TestACatalogueWithNoObjectNumberIsRefused(t *testing.T) {
	cat := Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}}
	d := bareDoc(Dict{"Root": cat}, []Ref{{Num: 3}}, map[int]Object{
		2: Dict{"Type": Name("Pages"), "Count": Integer(1), "Kids": Array{Ref{Num: 3}}},
		3: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}},
	})
	_, err := d.Partition()
	if !errors.Is(err, ErrCannotPartition) {
		t.Fatalf("a direct catalogue was partitioned: %v", err)
	}
	if !strings.Contains(err.Error(), "part 4 cannot name it") {
		t.Errorf("it was refused, but not for the catalogue having no number: %v", err)
	}
}

// ⛔ AND A PAGE TREE NODE THAT CANNOT BE READ IS A REFUSAL AND NOT AN EMPTY
// SET, because an empty set of interior nodes is indistinguishable from a
// one-level tree, and part 9 would then be missing the page tree.
func TestAPageTreeNodeThatCannotBeReadIsRefused(t *testing.T) {
	// ⛔⛔ GETTING Get TO ERROR AT ALL TOOK MEASURING, and the first attempt
	// failed for a reason worth writing down: this reader is deliberately
	// forgiving. An xref entry pointing at bytes that do not parse does NOT
	// error — getAtOffset calls retryAfterRepair, repair finds the object by
	// scanning, and the walk succeeds. So a fixture whose buffer still
	// contains the object proves nothing.
	//
	// ⛔ Get returns a non-nil error on exactly one path: retryAfterRepair
	// when repair() ITSELF fails. So the buffer must hold nothing a repair can
	// find, and the entry must point outside it.
	d := bareDoc(Dict{"Root": Ref{Num: 1}}, []Ref{{Num: 3}}, map[int]Object{
		1: Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}},
		3: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}},
	})
	d.buf = []byte("not a PDF at all")
	d.xref[2] = xrefEntry{kind: 'n', offset: 9999}

	// ⛔ pageTreeNodes is asked directly, with the catalogue handed to it:
	// Catalog() resolves /Pages and so fails FIRST on this fixture, for a
	// different reason, and a test that stopped there would be about
	// Catalog's error rather than about an unreadable node.
	_, err := d.pageTreeNodes(Dict{"Pages": Ref{Num: 2}})
	if !errors.Is(err, ErrCannotPartition) {
		t.Fatalf("an unreadable page tree node was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot be read") {
		t.Errorf("it was refused, but not for being unreadable: %v", err)
	}
}

// ⛔ A DIRECT PAGE TREE ROOT HAS NO NUMBER EITHER, but unlike the catalogue it
// is not fatal: the interior node simply cannot be placed, and every page and
// every object they reach still can. The partition is weaker, not wrong, so it
// is returned rather than refused — and this test is what says that is
// deliberate.
func TestADirectPageTreeRootLeavesPartNineWithoutTheTree(t *testing.T) {
	d := bareDoc(Dict{}, nil, nil)
	nodes, err := d.pageTreeNodes(Dict{"Pages": Dict{"Type": Name("Pages")}})
	if err != nil {
		t.Fatalf("a direct page tree root was refused: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("it placed %d node(s) for a tree whose root has no object number", len(nodes))
	}
}

// ⛔ AND A CYCLE IN THE PAGE TREE TERMINATES, which is the property the
// iterative walk was written for: a visited set makes a cycle finite without a
// depth limit, so a file that points a node at its own ancestor is partitioned
// rather than hanging. maxPageTreeNodes-style limits protect against size; this
// protects against shape.
func TestACycleInThePageTreeTerminates(t *testing.T) {
	d := bareDoc(Dict{}, nil, map[int]Object{
		2: Dict{"Type": Name("Pages"), "Kids": Array{Ref{Num: 4}}},
		4: Dict{"Type": Name("Pages"), "Kids": Array{Ref{Num: 2}}},
	})
	nodes, err := d.pageTreeNodes(Dict{"Pages": Ref{Num: 2}})
	if err != nil {
		t.Fatalf("a cyclic page tree was refused rather than walked: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("it found %d interior node(s), want both of the cycle's", len(nodes))
	}
}

// ⛔ AND A LEAF REACHED THROUGH THE TREE IS NOT AN INTERIOR NODE. kidsOf
// reports no kids for a /Type /Page, so the walk must skip it rather than
// place it in part 9 — a page object in part 9 is a page a reader would fetch
// last.
func TestAPageReachedThroughTheTreeIsNotPlacedInPartNine(t *testing.T) {
	d := bareDoc(Dict{}, nil, map[int]Object{
		2: Dict{"Type": Name("Pages"), "Kids": Array{Ref{Num: 3}, Integer(9)}},
		3: Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}},
	})
	nodes, err := d.pageTreeNodes(Dict{"Pages": Ref{Num: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if nodes[3] {
		t.Error("the page object was placed in part 9 as though it were an interior node")
	}
	if !nodes[2] {
		t.Error("the interior node was not placed at all")
	}
}

// ⛔ AND A NODE THAT IS NOT A DICTIONARY IS SKIPPED, not refused: an absent
// object resolves to Null and a file may name one. The walk carries on so that
// the rest of the tree is still placed.
func TestANodeThatIsNotADictionaryIsSkipped(t *testing.T) {
	d := bareDoc(Dict{}, nil, map[int]Object{
		2: Dict{"Type": Name("Pages"), "Kids": Array{Ref{Num: 7}}},
		7: Integer(3),
	})
	nodes, err := d.pageTreeNodes(Dict{"Pages": Ref{Num: 2}})
	if err != nil {
		t.Fatalf("a non-dictionary node was refused: %v", err)
	}
	if nodes[7] {
		t.Error("an integer was placed as a page tree node")
	}
}

// ⛔ reach KEEPS A DANGLING REFERENCE AND DOES NOT FOLLOW IT. The object is
// still one the document names, so a layout must account for it; refusing a
// document over one dangling reference would refuse files the rest of this
// package reads happily.
func TestReachKeepsAReferenceThatDoesNotResolve(t *testing.T) {
	d := bareDoc(Dict{}, nil, nil)
	got := d.reach([]Object{Ref{Num: 42}}, nil)
	if !got[42] {
		t.Error("a reference that does not resolve was dropped from the reach set")
	}
}

// ⛔ AND IT ENTERS EVERY SHAPE AN OBJECT CAN HAVE. A dictionary, an array and a
// STREAM's dictionary each hold references, and a walk that missed one would
// leave objects unplaced — which a layout turns into objects dropped. The
// stream arm is the one worth naming: its data holds no references but its
// dictionary does, /Length among them.
func TestReachEntersDictionariesArraysAndStreamDictionaries(t *testing.T) {
	d := bareDoc(Dict{}, nil, map[int]Object{
		1: Dict{"A": Ref{Num: 2}},
		2: Array{Ref{Num: 3}},
		3: &Stream{Dict: Dict{"Length": Ref{Num: 4}}, Raw: []byte("x")},
		4: Integer(1),
	})
	got := d.reach([]Object{Ref{Num: 1}}, nil)
	for _, num := range []int{1, 2, 3, 4} {
		if !got[num] {
			t.Errorf("object %d was not reached, so one of the shapes is not entered", num)
		}
	}
}

// ⛔ AND THE BOUNDARY IS NOT ENTERED, which is how F.3.7 excludes "page tree
// nodes or other page objects" from what part 6 reaches. Without it the first
// page would reach every page in the document through /Parent and /Kids.
func TestReachDoesNotEnterTheBoundary(t *testing.T) {
	d := bareDoc(Dict{}, nil, map[int]Object{
		1: Dict{"Kids": Array{Ref{Num: 2}}},
		2: Dict{"Deep": Ref{Num: 3}},
		3: Integer(1),
	})
	got := d.reach([]Object{Ref{Num: 1}}, map[int]bool{2: true})
	if got[2] {
		t.Error("the boundary object itself was placed in the reach set")
	}
	if got[3] {
		t.Error("the walk passed THROUGH the boundary, so part 6 would reach other pages")
	}
}

// ⛔⛔ A BIG DOCUMENT IS PARTITIONED AND NOT REFUSED, which is the test an
// earlier version of this file failed. The walk used to stop after a million
// units of work — a number that sounds unreachable and is not, because
// reachFromTrailer walks the WHOLE document in one call. Review measured a
// 150 000-page file refused outright, with a message reading "more than
// 1048576 objects from one page" in a walk that has no page.
//
// ⛔ The cap is gone, and the visited set is the bound that was always doing
// the work: every object is entered at most once, so a walk cannot visit more
// objects than the document has. A bound that refuses real documents while
// protecting against nothing is worse than no bound.
//
// ⚠ Ten thousand pages rather than a hundred and fifty thousand: the point is
// that NO arbitrary cap bites at a realistic size, and a test that takes a
// second to build its fixture is one nobody will run.
func TestALargeDocumentIsPartitionedRatherThanRefused(t *testing.T) {
	const pages = 10000

	objects := map[int]Object{}
	kids := make(Array, 0, pages)
	refs := make([]Ref, 0, pages)
	num := 10
	for i := 0; i < pages; i++ {
		pageNum, contentNum := num, num+1
		num += 2
		objects[pageNum] = Dict{"Type": Name("Page"), "Parent": Ref{Num: 2},
			"Contents": Ref{Num: contentNum}}
		objects[contentNum] = &Stream{Dict: Dict{}, Raw: []byte("0 g")}
		kids = append(kids, Ref{Num: pageNum})
		refs = append(refs, Ref{Num: pageNum})
	}
	objects[1] = Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}}
	objects[2] = Dict{"Type": Name("Pages"), "Count": Integer(pages), "Kids": kids}

	d := bareDoc(Dict{"Root": Ref{Num: 1}}, refs, objects)
	p, err := d.Partition()
	if err != nil {
		t.Fatalf("a %d-page document was refused: %v", pages, err)
	}
	// Every page object and every content stream must be placed, or the
	// partition quietly lost part of the document.
	if got, want := len(p.Of), 2*pages+2; got != want {
		t.Errorf("it placed %d objects, want %d — the walk stopped early", got, want)
	}
}

// ⛔ reachFromTrailer ON A DOCUMENT WITH NO TRAILER IS EMPTY AND NOT AN ERROR:
// a document that was repaired far enough to have pages but not a trailer is
// one this package already reads.
func TestReachFromATrailerlessDocumentIsEmpty(t *testing.T) {
	d := bareDoc(nil, nil, nil)
	got := d.reachFromTrailer()
	if len(got) != 0 {
		t.Errorf("it reached %d object(s) with no trailer to reach them from", len(got))
	}
}

// ⛔ AND /Encrypt IS NOT REACHED FROM THE TRAILER, because a partition that
// reached it would place it in part 9 where F.3.5 requires part 4. An
// encrypted file is refused before the walk runs; this keeps the refusal the
// only way such a file is handled, so that lifting it later is a deliberate
// act rather than a silent misplacement.
func TestTheTrailerWalkDoesNotReachTheEncryptionDictionary(t *testing.T) {
	d := bareDoc(Dict{"Encrypt": Ref{Num: 9}, "Root": Ref{Num: 1}}, nil, map[int]Object{
		1: Dict{"Type": Name("Catalog")},
		9: Dict{"Filter": Name("Standard")},
	})
	got := d.reachFromTrailer()
	if got[9] {
		t.Error("the encryption dictionary was placed in part 9, where F.3.5 requires part 4")
	}
	if !got[1] {
		t.Error("the catalogue was not reached, so this proves nothing about /Encrypt")
	}
}

// ⛔ AND A PAGE OBJECT THAT IS NOT A DICTIONARY IS REFUSED BY reachFromPage
// TOO, not only by refuseUnplaceable: the two run at different times and a
// partition reached through the second path alone would place a page's reach
// set as empty.
func TestReachFromAPageThatIsNotADictionaryIsRefused(t *testing.T) {
	d := bareDoc(Dict{}, nil, map[int]Object{7: Integer(1)})
	_, err := d.reachFromPage(Ref{Num: 7}, nil)
	if !errors.Is(err, ErrCannotPartition) {
		t.Fatalf("a non-dictionary page was walked: %v", err)
	}
	if !strings.Contains(err.Error(), "not a page dictionary") {
		t.Errorf("it was refused, but not for its kind: %v", err)
	}
}

// ⛔ AND /Thumb AND /Parent ARE SKIPPED AT THE PAGE, which F.3.7 states
// outright: part 6 includes what "its Contents, Resources, Annots, and B
// entries" reach "but not the Thumb entry". A thumbnail in part 6 puts a
// picture nobody is looking at in front of the bytes that draw the page, which
// is the one thing this format exists to prevent.
func TestThumbAndParentAreNotReachedFromAPage(t *testing.T) {
	d := bareDoc(Dict{}, nil, map[int]Object{
		7: Dict{
			"Type":     Name("Page"),
			"Thumb":    Ref{Num: 8},
			"Parent":   Ref{Num: 9},
			"Contents": Ref{Num: 10},
		},
		8:  &Stream{Dict: Dict{}, Raw: []byte("thumb")},
		9:  Dict{"Type": Name("Pages")},
		10: &Stream{Dict: Dict{}, Raw: []byte("0 g")},
	})
	got, err := d.reachFromPage(Ref{Num: 7}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got[8] {
		t.Error("the thumbnail was pulled into part 6")
	}
	if got[9] {
		t.Error("the parent page tree node was pulled into part 6")
	}
	if !got[10] {
		t.Error("the content stream was not reached, so this proves nothing about the skips")
	}
}

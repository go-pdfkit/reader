// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"errors"
	"fmt"
	"sort"
)

// Part names a section of the eleven-part layout ISO 32000-1 Annex F.3.1
// gives a linearized file. Only the parts that HOLD DOCUMENT OBJECTS are
// named: parts 1, 2, 3, 5, 10 and 11 are the header, the cross-reference
// tables, the hint streams and the trailer, which are produced by the layout
// rather than assigned to.
//
// The numbers are F.3.1's own, not an enumeration of this package's making,
// so that a part can be compared with qpdf's `--show-linearization` output
// and with the annex without a translation table in between.
type Part int

const (
	// PartCatalog is part 4, the document catalogue and the document-level
	// objects a reader needs the moment the file opens (F.3.5).
	PartCatalog Part = 4
	// PartFirstPage is part 6, every object needed to display the first page
	// (F.3.7).
	PartFirstPage Part = 6
	// PartOtherPrivate is part 7, objects belonging to exactly one page that
	// is not the first.
	PartOtherPrivate Part = 7
	// PartShared is part 8, objects referenced from more than one page but
	// not from the first page (F.3.9).
	PartShared Part = 8
	// PartOther is part 9, everything that is part of the document but not
	// needed to display a page (F.3.10).
	PartOther Part = 9
)

// String names the part the way F.3.1 does, so a failed comparison reads as
// prose rather than as two integers.
func (p Part) String() string {
	switch p {
	case PartCatalog:
		return "part 4 (catalogue and document-level objects)"
	case PartFirstPage:
		return "part 6 (first page)"
	case PartOtherPrivate:
		return "part 7 (other pages, private)"
	case PartShared:
		return "part 8 (other pages, shared)"
	case PartOther:
		return "part 9 (everything else)"
	}
	return fmt.Sprintf("part %d (not a part this package assigns)", int(p))
}

// Partition is where every object of a document would go in a linearized
// layout.
type Partition struct {
	// Of maps an object number to its part. An object the document never
	// reaches is absent rather than placed: a linearized file is written from
	// the objects a reader needs, and an unreachable object has no page to
	// belong to.
	Of map[int]Part
	// FirstPage is the page that part 6 holds, counting from one.
	FirstPage int
	// FirstPageObject is the object number of that page's page object, which
	// becomes /O in the linearization parameter dictionary.
	FirstPageObject int

	// Pages[i] is every object page i+1 needs, including its own page object,
	// in ascending object number.
	//
	// ⛔⛔ THE HINT TABLES CANNOT BE BUILT WITHOUT THIS, and this function was
	// computing it and throwing it away. Table F.4 item 1 is "the number of
	// objects in a page" and item 2 is that page's length in bytes: both are
	// PER PAGE, and Of answers only which PART an object is in. Part 7 says
	// "belongs to exactly one page that is not the first" and never says which
	// page, so a writer holding only Of knows the order to emit objects in and
	// not what to say about them.
	//
	// ⛔ A page's own object is included, because item 1 counts it: qpdf
	// reports 4 objects for a page whose reach is 3.
	Pages [][]int

	// PageObjects[i] is page i+1's own page object, in this document's
	// numbering.
	//
	// ⛔⛔ Pages[i][0] IS NOT IT, which is why this field exists. Pages[i] is
	// built with the page's own object first and then SORTED, so its index 0
	// is the lowest object number — and on the fixtures here that usually IS
	// the page, so code assuming it would measure correctly on every shape in
	// the corpus and be wrong on a document whose content stream is numbered
	// below its page.
	//
	// ⛔⛔ AND THE LAYOUT NEEDS IT, not just the hint tables. qpdf's checker
	// computes a page's length by taking the page object's number and the next
	// nobjects-1 CONSECUTIVE numbers and summing their lengths
	// (QPDF_linearization.cc, Lin::lengthNextN) — so a page whose objects are
	// not numbered consecutively from its page object is measured against
	// objects belonging to something else. Order uses this to emit each page's
	// own object first.
	PageObjects []int

	// Shared[i] is every object page i+1 reaches that belongs to another
	// page's part — part 6 or part 8 — and so is a shared object as far as
	// F.4.2 is concerned. Shared[0] is always empty: Table F.4 item 3 requires
	// 0 shared references for the first page.
	//
	// ⛔⛔ THIS IS NOT "WHAT IS IN PART 8". An object page 1 needs stays in
	// part 6 even when page 3 needs it too, and part 8 holds only objects
	// shared between pages that are NOT the first. Measured: for a two-page
	// document whose pages share one content stream, our part 8 is empty and
	// qpdf's is too — yet qpdf reports `nshared_total: 3`, because its shared
	// table covers part 6's shared objects as well.
	//
	// ⛔ The writer needs this to know when it CANNOT describe a document:
	// `qpdf --check` says "page 1: shared object 3: in computed list but not
	// hint table" for exactly the pages whose Shared is non-empty.
	Shared [][]int
}

// ErrCannotPartition reports a document this package declines to place.
//
// ⛔⛔ IT IS A REFUSAL AND NOT A BEST EFFORT, which is the whole design of this
// function. Annex F.3.5, F.3.7 and F.3.10 place objects by what REFERS to
// them — an outline hierarchy belongs to part 6 or part 9 depending on
// /PageMode, thread dictionaries split from their information dictionaries
// across parts 4 and 9, an interactive form's top level separates from its
// field hierarchy — and a partition that guessed any of those would produce a
// file whose hint tables point a reader at the wrong offsets. That is worse
// than no file: the reader does not fail, it reads the wrong bytes.
var ErrCannotPartition = errors.New("reader: this document cannot be partitioned for linearization")

// Partition computes which part of a linearized layout each object belongs
// to, following ISO 32000-1 Annex F.3.5, F.3.7, F.3.9 and F.3.10.
//
// It is the first step of writing a linearized file (go-pdfkit/reader#40) and
// it is deliberately separable from the writing: the partition is a pure
// function of the document, it has a foreign judge that can check it —
// `qpdf --show-linearization` prints its own computed part assignments — and
// getting it wrong is the failure that would be hardest to see in a finished
// file.
//
// ⛔ WHAT IT DECLINES, BY NAME. Every construct below changes which part an
// object belongs to, and none is exercised by any file in this package's
// corpus, so placing them would be writing untested rules into a format where
// a wrong placement is silent:
//
//   - /OpenAction, because F.3.7 says the page it opens at becomes the first
//     page, and part 6 is defined in terms of that page rather than page one.
//   - /PageMode UseOutlines together with /Outlines, because F.3.7 then puts
//     the ENTIRE outline hierarchy in part 6, where F.3.10 otherwise puts it
//     in part 9.
//   - /Threads, because F.3.5 places the thread dictionaries in part 4 while
//     F.3.10 places their /I information dictionaries in part 9 and the bead
//     dictionaries go with the individual pages: one key, three parts.
//   - /AcroForm, because F.3.5 takes only the TOP-LEVEL form dictionary into
//     part 4 and F.3.10 takes the field hierarchy into part 9.
//   - /Encrypt, because F.3.5 requires the encryption dictionary AND all its
//     values in part 4, and because the first-page cross-reference table
//     indexes them.
//   - /Thumb on any page, because F.3.10 gives thumbnails and their shared
//     objects two ordered categories of their own.
//
// A document using any of them is refused with ErrCannotPartition naming
// which, so that the next person to need one knows exactly which rule to
// implement and which part of the annex states it.
func (d *Document) Partition() (*Partition, error) {
	cat, err := d.Catalog()
	if err != nil {
		return nil, err
	}
	if err := d.refuseUnplaceable(cat); err != nil {
		return nil, err
	}

	pages := d.pageRefs()
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: it has no pages", ErrCannotPartition)
	}

	// ⛔ Page objects and page tree nodes are the walk's boundary, not its
	// destination. F.3.7 excludes "page tree nodes or other page objects"
	// from what part 6 reaches, and F.3.10 puts the page tree in part 9
	// because a reader "never needs to consult it".
	boundary := map[int]bool{}
	for _, ref := range pages {
		boundary[ref.Num] = true
	}
	nodes, err := d.pageTreeNodes(cat)
	if err != nil {
		return nil, err
	}
	for num := range nodes {
		boundary[num] = true
	}

	of := map[int]Part{}

	// Part 9 first, so that the parts that follow overwrite it where they
	// claim an object: the page tree is the one group F.3.10 names that this
	// function can identify without reading a category.
	for num := range nodes {
		of[num] = PartOther
	}

	// Part 6: the first page's object and everything it reaches.
	first := pages[0]
	firstReach, err := d.reachFromPage(first, boundary)
	if err != nil {
		return nil, err
	}
	of[first.Num] = PartFirstPage
	for num := range firstReach {
		of[num] = PartFirstPage
	}

	// ⛔ The per-page reach sets are KEPT now, in page order. They are filtered
	// into Partition.Pages at the end, once `of` is final: see the note there.
	reaches := make([]map[int]bool, len(pages))
	reaches[0] = firstReach

	// ⛔ Parts 7 and 8 are decided by HOW MANY PAGES reach an object, which is
	// why the other pages' reach sets are counted rather than merged. F.3.9
	// defines part 8 as "referenced from more than one page but ... not
	// referenced (directly or indirectly) from the first page", so an object
	// the first page reaches stays in part 6 however many other pages want
	// it.
	readers := map[int]int{}
	for i, ref := range pages[1:] {
		reach, err := d.reachFromPage(ref, boundary)
		if err != nil {
			return nil, err
		}
		reaches[i+1] = reach
		// The page's own object is private to it by definition.
		if _, placed := of[ref.Num]; !placed || of[ref.Num] == PartOther {
			of[ref.Num] = PartOtherPrivate
		}
		for num := range reach {
			readers[num]++
		}
	}
	for num, count := range readers {
		if of[num] == PartFirstPage {
			continue
		}
		if count > 1 {
			of[num] = PartShared
		} else {
			of[num] = PartOtherPrivate
		}
	}

	// Part 4 last, because F.3.5's objects are "required present when the
	// document is opened" and that claim outranks any page's: the catalogue
	// is reachable from nothing above it.
	catNum, ok := d.catalogNumber()
	if !ok {
		return nil, fmt.Errorf("%w: /Root is a direct dictionary, so the catalogue has no object "+
			"number and part 4 cannot name it", ErrCannotPartition)
	}
	of[catNum] = PartCatalog

	// ⛔ AND WHAT THE TRAILER REACHES BUT NO PAGE DOES IS PART 9, which is
	// F.3.10's "any other objects that are part of the document but are not
	// required for displaying pages" — the document information dictionary
	// above all. Without this sweep those objects are absent from the
	// partition, and an object absent from the partition is an object a
	// layout would drop.
	for num := range d.reachFromTrailer() {
		if _, placed := of[num]; !placed {
			of[num] = PartOther
		}
	}

	// ⛔⛔ THE PER-PAGE SETS HOLD WHAT IS PRIVATE TO THE PAGE, NOT ITS REACH,
	// and qpdf said so on the first run. The first version kept the whole reach
	// and disagreed with Table F.4 item 1 on every page but the first:
	//
	//	5 pages, page 2: ours [1 2 15] = 3 objects, qpdf says 2
	//	25 pages, page 2: ours [1 2 55] = 3 objects, qpdf says 2
	//
	// ⛔⛔⛔ AND THE REASON FIRST WRITTEN HERE WAS INVENTED. It said the extra
	// number "is a SHARED object, which F.3.9 puts in part 8". Measured: in the
	// five-page fixture object 15 is in PART 6 and part 8 holds ZERO objects;
	// in the twenty-five-page one object 55 is likewise part 6, part 8 again
	// empty. Part 8 cannot be why qpdf excluded it.
	//
	// What it actually is: a part-6 object THE FIRST PAGE ALSO REACHES, which
	// qpdf hands to page 2 as a first-page shared entry — nshared 1,
	// identifier [2]. So item 1 counts the objects private to the page, and an
	// object already placed in part 6 arrives through items 3, 4 and 5 instead.
	// Counting it in both places would make every page claim an object the
	// layout puts at the front.
	//
	// ⛔ The filter below is unchanged and still right, because object 15 is
	// part 6 and pages after the first want part 7. Only the explanation was
	// wrong — which is the harder failure to catch: the rule passed every test
	// while the comment taught the next reader something false.
	//
	// ⛔ SO THE FILTER IS `of`, AND IT HAS TO RUN HERE rather than in the loop
	// above: part 4 and the part 9 sweep are decided after it, so an object's
	// part is not final until this point. Filtering early would have kept the
	// catalogue in page one's set.
	perPage := make([][]int, len(pages))
	pageObjects := make([]int, len(pages))
	for i := range pages {
		pageObjects[i] = pages[i].Num
	}
	shared := make([][]int, len(pages))
	for i, reach := range reaches {
		want := PartOtherPrivate
		if i == 0 {
			want = PartFirstPage
		}
		// The page's own object first: Table F.4 item 1 counts it, and
		// reachFromPage never returns it because the page objects are the
		// walk's boundary.
		set := []int{pages[i].Num}
		var theirs []int
		for num := range reach {
			// ⛔ THE PAGE'S OWN NUMBER IS A CONDITION, NOT A BRANCH, because
			// reachFromPage never returns it — the page objects are the walk's
			// boundary — so an `if num == pages[i].Num { continue }` is a block
			// no input enters, and this package's coverage gate refuses those.
			// Folded into the case below it keeps the guarantee without claiming
			// a path nothing takes.
			switch {
			case of[num] == want && num != pages[i].Num:
				set = append(set, num)
			// ⛔⛔ AN OBJECT THIS PAGE REACHES THAT IS NOT ITS OWN. Part 6 and
			// part 8 are both "reachable from more than one page": an object
			// page 1 needs stays in part 6 even when page 3 needs it too, and
			// F.4.2's shared object hint table is how a later page says which
			// of those it depends on. The page TREE is reached by every page
			// and belongs to neither — F.3.10 puts it in part 9 — so it is not
			// one of these.
			case i > 0 && (of[num] == PartFirstPage || of[num] == PartShared):
				theirs = append(theirs, num)
			}
		}
		// ⛔ SORTED, so two runs over one document give the same slice: a reach
		// is a map, and a writer emitting in map order would produce different
		// bytes every time from the same input.
		sort.Ints(set)
		sort.Ints(theirs)
		perPage[i] = set
		shared[i] = theirs
	}

	return &Partition{
		Of:              of,
		FirstPage:       1,
		FirstPageObject: first.Num,
		Pages:           perPage,
		PageObjects:     pageObjects,
		Shared:          shared,
	}, nil
}

// refuseUnplaceable declines the constructs whose part this function would
// have to guess. Each refusal names the key AND the clause that places it, so
// the message is a pointer into the annex rather than a complaint.
func (d *Document) refuseUnplaceable(cat Dict) error {
	for _, c := range []struct{ key, why string }{
		{"OpenAction", "F.3.7 makes the page it opens at the FIRST page, and part 6 is defined in terms of that page"},
		{"Threads", "F.3.5 puts the thread dictionaries in part 4, F.3.10 their /I information dictionaries in part 9, and the beads with their pages"},
		{"AcroForm", "F.3.5 takes only the top-level form dictionary into part 4 and F.3.10 takes the field hierarchy into part 9"},
		// ⛔⛔ /ViewerPreferences WAS MISSING AND IS THE SAME CLAUSE. F.3.5
		// lists the entries whose values belong in part 4 "if they are present
		// and are indirect objects", and the viewer preferences dictionary is
		// the first of them. Without this it was neither refused NOR placed in
		// part 4: the trailer sweep found it and put it in part 9, which is
		// exactly the silent misplacement this function exists to prevent.
		// Nothing caught it because no file in the corpus has one — review
		// read F.3.5 against the code instead.
		{"ViewerPreferences", "F.3.5 requires its value in part 4 when it is an indirect object, and this places nothing there but the catalogue"},
	} {
		if _, ok := cat[Name(c.key)]; ok {
			return fmt.Errorf("%w: the catalogue has /%s, and %s", ErrCannotPartition, c.key, c.why)
		}
	}
	// ⛔ AND AN INDIRECT /PageMode FOR THE SAME REASON, which is a different
	// test from the /UseOutlines one below: that asks what the VALUE is and so
	// moves the outline hierarchy, while this asks whether the value is an
	// object of its own that part 4 must hold. A direct /PageMode name is
	// neither, and is accepted.
	if _, indirect := cat.Get("PageMode").(Ref); indirect {
		return fmt.Errorf("%w: /PageMode is an indirect object, and F.3.5 requires its value "+
			"in part 4", ErrCannotPartition)
	}
	// ⛔ /PageMode alone is harmless; it is UseOutlines TOGETHER WITH an
	// outline hierarchy that moves that hierarchy from part 9 into part 6.
	// Refusing on /PageMode alone would refuse `/PageMode /UseThumbs`, which
	// changes nothing about where objects go.
	mode, _ := Resolve(cat.Get("PageMode"), d.Get)
	if name, ok := ToName(mode); ok && name == "UseOutlines" {
		if _, has := cat[Name("Outlines")]; has {
			return fmt.Errorf("%w: /PageMode is /UseOutlines and the document has /Outlines, so "+
				"F.3.7 puts the entire outline hierarchy in part 6 rather than part 9",
				ErrCannotPartition)
		}
	}
	if d.trailer != nil {
		if _, ok := d.trailer[Name("Encrypt")]; ok {
			return fmt.Errorf("%w: the file is encrypted, and F.3.5 requires the encryption "+
				"dictionary and every value in it to be in part 4", ErrCannotPartition)
		}
	}
	// ⛔⛔ THIS FUNCTION ANSWERS PLACEABILITY, NOT READABILITY, and keeping the
	// two apart is why a page that cannot be read is SKIPPED here. reachFromPage
	// reports an unreadable or non-dictionary page, by object number, and it
	// has to: it cannot walk one. Reporting it here as well gave one fact two
	// messages — and, because Partition runs this first, made reachFromPage's
	// own report unreachable from Partition, which is a branch no test could
	// have held.
	for i, ref := range d.pageRefs() {
		o, err := d.Get(ref)
		if err != nil {
			continue
		}
		page, ok := ToDict(o)
		if !ok {
			continue
		}
		if _, has := page[Name("Thumb")]; has {
			return fmt.Errorf("%w: page %d has a /Thumb, and F.3.10 gives thumbnails and their "+
				"shared objects two ordered categories of part 9 of their own",
				ErrCannotPartition, i+1)
		}
	}
	return nil
}

// catalogNumber reports the object number /Root points at, which part 4 needs
// to name. A direct catalogue has none.
func (d *Document) catalogNumber() (int, bool) {
	if d.trailer == nil {
		return 0, false
	}
	ref, ok := d.trailer.Get("Root").(Ref)
	if !ok {
		return 0, false
	}
	return ref.Num, true
}

// pageTreeNodes collects the object numbers of the page tree's interior nodes
// — the /Pages root and every intermediate node — which F.3.10 places in
// part 9 and F.3.7 excludes from what part 6 reaches.
func (d *Document) pageTreeNodes(cat Dict) (map[int]bool, error) {
	out := map[int]bool{}
	ref, ok := cat.Get("Pages").(Ref)
	if !ok {
		// A direct page tree root has no object number to place. The document
		// is unusual rather than wrong, and the rest of the partition holds.
		return out, nil
	}
	// ⛔ Iterative for the same reason as reach: the visited set makes a cycle
	// terminate, so there is no depth to limit and no error to carry back
	// through a recursion.
	work := []Ref{ref}
	for len(work) > 0 {
		cur := work[len(work)-1]
		work = work[:len(work)-1]
		if out[cur.Num] {
			continue
		}
		o, err := d.Get(cur)
		if err != nil {
			return nil, fmt.Errorf("%w: page tree node %d cannot be read: %v",
				ErrCannotPartition, cur.Num, err)
		}
		node, ok := ToDict(o)
		if !ok {
			continue
		}
		kids, ok := d.kidsOf(node)
		if !ok {
			// A leaf: it is a page object, not an interior node.
			continue
		}
		out[cur.Num] = true
		for _, kid := range kids {
			if kr, ok := kid.(Ref); ok {
				work = append(work, kr)
			}
		}
	}
	return out, nil
}

// reachFromPage collects every object the given page object refers to, to any
// depth, stopping at the page objects and page tree nodes in boundary.
//
// ⛔ /Thumb IS SKIPPED AT THE PAGE, which F.3.7 states outright: part 6
// includes what "its Contents, Resources, Annots, and B entries" reach "but
// not the Thumb entry". A thumbnail is not needed to display the page, and
// pulling it into part 6 would put a picture nobody is looking at in front of
// the bytes that draw the page — the one thing this format exists to avoid.
//
// ⛔ AND /Parent IS SKIPPED for the same reason the boundary exists: it is a
// page tree node, which part 9 holds.
func (d *Document) reachFromPage(page Ref, boundary map[int]bool) (map[int]bool, error) {
	o, err := d.Get(page)
	if err != nil {
		return nil, fmt.Errorf("%w: page object %d cannot be read: %v", ErrCannotPartition, page.Num, err)
	}
	dict, ok := ToDict(o)
	if !ok {
		return nil, fmt.Errorf("%w: object %d is a %s, not a page dictionary",
			ErrCannotPartition, page.Num, o.Kind())
	}
	start := make([]Object, 0, len(dict))
	for key, v := range dict {
		if key == "Thumb" || key == "Parent" {
			continue
		}
		start = append(start, v)
	}

	// ⛔⛔⛔ THE INHERITED ATTRIBUTES ARE PART OF THE PAGE'S REACH, and leaving
	// them out made this walk measure a graph the writer does not emit. Table 30
	// lets a page omit /Resources, /MediaBox, /CropBox and /Rotate and take its
	// parent's, and the writer PUSHES THEM DOWN at emission (pushInherited),
	// because F.3.7 needs part 6 to hold everything that displays page one. If
	// the ancestor's value is an INDIRECT REFERENCE, the pushed-down copy is
	// that reference — so the emitted page names an object this walk never saw.
	//
	// ⛔ Measured, before this: two pages inheriting an indirect /Resources from
	// the page tree node gave Pages=[[3 5] [4 6]] with the resources object in
	// part 9, and `qpdf --check` on the result said all three of
	//
	//	end of first page section (/E) mismatch: /E = 905; computed = 1190
	//	object count mismatch for page 0: hint table = 2; computed = 3
	//	page 1: shared object 7: in computed list but not hint table
	//
	// — so /E, Table F.4 item 1 and the shared list were ALL wrong, and the
	// first two of those are older than the hint tables. The partition was
	// describing the source document while the writer emitted a different one.
	//
	// ⭐ Fixing it HERE rather than in the hint tables is what makes the rest
	// consistent: once the object is in the page's reach, `of` classifies it,
	// Pages counts it, Shared sees that a later page needs it, and the writer's
	// refusal covers the case it cannot yet describe.
	for _, v := range d.inheritedAttributes(page, dict) {
		start = append(start, v)
	}
	return d.reach(start, boundary), nil
}

// inheritedAttributes returns the values of Table 30's inheritable keys that
// this page does NOT hold itself and so takes from an ancestor.
//
// ⛔ THE WALK IS BOUNDED BY A SEEN SET, not by a depth. A /Parent chain that
// loops is a malformed document this function must not hang on, and a page tree
// deep enough to matter is cheaper to bound by identity than to guess a limit
// for. The same argument as pushInherited's, which walks the same chain.
func (d *Document) inheritedAttributes(page Ref, dict Dict) []Object {
	var out []Object
	seen := map[int]bool{page.Num: true}

	// ⛔⛔ THE NEAREST ANCESTOR WINS, AND ONLY IT. Table 30's inheritance takes
	// the first value found walking UP, so a /Resources on the parent SHADOWS
	// one on the grandparent. Collecting from every ancestor would put an object
	// the page cannot reach into its reach — and that object would then be
	// classified as part of the page, which is the same class of defect this
	// function exists to fix, in the opposite direction.
	resolved := map[Name]bool{}
	for _, key := range inheritablePageKeys {
		if _, has := dict[key]; has {
			// The page states it itself, so it inherits nothing for it.
			resolved[key] = true
		}
	}

	at := dict
	for {
		parent, ok := at["Parent"].(Ref)
		if !ok || seen[parent.Num] {
			return out
		}
		seen[parent.Num] = true

		o, err := d.Get(parent)
		if err != nil {
			return out
		}
		node, ok := ToDict(o)
		if !ok {
			return out
		}
		for _, key := range inheritablePageKeys {
			if resolved[key] {
				continue
			}
			if v, has := node[key]; has {
				out = append(out, v)
				resolved[key] = true
			}
		}
		at = node
	}
}

// reachFromTrailer collects everything the trailer reaches, with no boundary:
// it is used to find the objects no page refers to.
//
// ⛔ THIS IS THE LARGEST WALK IN THE PARTITION — it starts at /Root and reaches
// the catalogue, the page tree, every page and everything they hold — which is
// why an arbitrary work cap bit here first and nowhere else. See reach.
func (d *Document) reachFromTrailer() map[int]bool {
	if d.trailer == nil {
		return map[int]bool{}
	}
	start := make([]Object, 0, len(d.trailer))
	for key, v := range d.trailer {
		// ⛔ /Encrypt is excluded here because a partition that reached it
		// would place it in part 9, where F.3.5 requires part 4. An encrypted
		// file is refused before this runs; the exclusion is so that the
		// refusal stays the only way such a file is handled.
		if key == "Encrypt" {
			continue
		}
		start = append(start, v)
	}
	return d.reach(start, nil)
}

// pageTreeBudget is gone for the same reason reach's is: see reach.

// reach walks the objects the given values refer to, to any depth, and
// returns the object numbers it found. Numbers in boundary are not entered.
//
// ⛔⛔ IT IS ITERATIVE, AND THAT IS WHY IT NEEDS NO LIMIT AT ALL. A recursive
// walk needs a depth limit on every call and an error return on every branch
// to carry it back — eight defensive arms that only a crafted file reaches,
// and a deep file still costs stack before the limit fires. A worklist with a
// visited set cannot recurse, and the visited set is itself the bound: every
// object is entered at most once, so the walk cannot visit more objects than
// the document has, and the elements it pushes came from bytes the file
// already holds. A cycle terminates on the visited set rather than on a count.
//
// ⛔⛔ AND AN ARBITRARY CAP WAS WORSE THAN NONE, WHICH REVIEW MEASURED. An
// earlier version refused after a million units of work, which sounds
// unreachable and is not: reachFromTrailer walks the WHOLE document in one
// call, so a 150 000-page file was refused — with a message reading "more than
// 1048576 objects from one page", naming a page in a walk that has no page.
// A bound that refuses real documents while protecting against nothing the
// visited set does not already bound is a defect with a comment in front of
// it.
//
// ⛔ A REFERENCE THAT DOES NOT RESOLVE IS KEPT AND NOT FOLLOWED. It is still an
// object the document names, so a layout must account for it; refusing a whole
// document over one dangling reference would refuse files every other part of
// this package reads happily.
func (d *Document) reach(start []Object, boundary map[int]bool) map[int]bool {
	seen := map[int]bool{}
	work := append([]Object(nil), start...)

	for len(work) > 0 {
		o := work[len(work)-1]
		work = work[:len(work)-1]

		switch v := o.(type) {
		case Ref:
			if boundary[v.Num] || seen[v.Num] {
				continue
			}
			seen[v.Num] = true
			if target, err := d.Get(v); err == nil {
				work = append(work, target)
			}
		case Dict:
			for _, e := range v {
				work = append(work, e)
			}
		case Array:
			work = append(work, v...)
		case *Stream:
			for _, e := range v.Dict {
				work = append(work, e)
			}
		}
	}
	return seen
}

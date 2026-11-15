// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"errors"
	"fmt"
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

	// ⛔ Parts 7 and 8 are decided by HOW MANY PAGES reach an object, which is
	// why the other pages' reach sets are counted rather than merged. F.3.9
	// defines part 8 as "referenced from more than one page but ... not
	// referenced (directly or indirectly) from the first page", so an object
	// the first page reaches stays in part 6 however many other pages want
	// it.
	readers := map[int]int{}
	for _, ref := range pages[1:] {
		reach, err := d.reachFromPage(ref, boundary)
		if err != nil {
			return nil, err
		}
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

	return &Partition{Of: of, FirstPage: 1, FirstPageObject: first.Num}, nil
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
	return d.reach(start, boundary), nil
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

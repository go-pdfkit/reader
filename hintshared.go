// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

// sharedPlan is which objects F.4.2's shared object hint table describes, in the
// order it describes them, and what identifier each one has.
//
// ⛔⛔ IT IS NOT "WHAT IS IN PART 8". Table F.5's item 3 is the number of entries
// for the FIRST PAGE and item 4 the number in all, INCLUDING item 3 — so the
// table covers two runs: part 6's objects that a later page also reaches, then
// part 8's. An object page 1 needs stays in part 6 however many later pages
// need it (partition.go assigns PartShared only when the readers count exceeds
// one AND the object is not already part 6), so the first run is never empty
// for a document that shares anything with page 1.
//
// ⭐ MEASURED ON QPDF'S OWN OUTPUT rather than inferred from the annex. For a
// two-page file whose pages have their own content streams it reports
// nshared_first_page 3, nshared_total 3 and part 8 EMPTY, with group lengths
// 96, 113 and 31 — and /O is 7 with objects 7, 8, 9 at bytes 677, 773 and 886
// and the first page ending at 917. The three groups are the first page's own
// objects. For testdata/linearized-shared.pdf it reports 2 and 3 with
// first_shared_obj 5, so both runs are populated there.
type sharedPlan struct {
	// objects is the table's order: the first-page run, then part 8's. An
	// object's INDEX here is its Table F.4 item 4 identifier, which is why
	// there is no map beside it: a `identifier map[int]int` was built on every
	// call and read by nothing once refsFor started walking this slice, so it
	// was two statements of the same fact with only one of them used.
	objects []int

	// firstPage is Table F.5 item 3: how many of objects are the first page's.
	firstPage int
}

// planShared decides the table's contents, in EMISSION order.
//
// ⛔⛔⛔ THE FIRST RUN IS ALL OF PART 6, NOT ONLY THE OBJECTS LATER PAGES USE,
// and getting that wrong is what the first version did. Nothing in the table
// says which object an entry describes: Table F.5 names part 8's first object
// and offset, and the first run is positional — entry j is the j-th object of
// the first page, counted from /O. An arbitrary subset would leave a reader no
// way to know which objects were meant.
//
// ⭐ MEASURED ON QPDF'S OWN OUTPUT, which is also how this was caught. For a
// two-page file it reports nshared_first_page 3 with part 6 holding objects 7,
// 8 and 9 and /O = 7 — every first-page object, not the one page 2 shares. And
// our own checker (#67) refused the first version:
//
//	shared group 0 says 83 bytes and its 1 object(s) from object 2 occupy 101
//
// which is the checker-before-writer order of #40 working exactly as intended.
//
// ⛔ THE SECOND LINE THAT STOOD HERE WAS QPDF'S, NOT OURS — "first shared
// object offset mismatch: hint table = 1456; computed = 1292" — and quoting it
// as part of one run by our checker made this comment claim a capability the
// package does not have. See sharedOffset: no checker recomputes item 2.
//
// ⛔ SO THE ORDER IS THE LAYOUT'S, not a sort of object numbers: `order` is what
// the emission writes and what the renumbering follows, so entry j of the first
// run is the j-th part-6 object and part 8's entries follow in the same
// sequence.
func planShared(p *Partition, order []int) sharedPlan {
	var plan sharedPlan

	var part8 []int
	for _, num := range order {
		switch p.Of[num] {
		case PartFirstPage:
			plan.objects = append(plan.objects, num)
		case PartShared:
			part8 = append(part8, num)
		}
	}
	plan.firstPage = len(plan.objects)
	plan.objects = append(plan.objects, part8...)
	return plan
}

// refsFor returns Table F.4's items 4 and 5 for one page: an identifier per
// shared object it reaches, and a numerator for each.
//
// ⛔ THE NUMERATOR IS 0 AND THE DENOMINATOR IS 1. F.4.1's items 5 and 13 are a
// fraction saying WHERE in the page the shared object is first used, which a
// reader may use to start drawing before the whole page has arrived. This
// writer does not track that position — it would mean knowing where in the
// content stream each resource is first referenced — so it says "at the
// beginning", which is true of a reader that waits for the page.
//
// ⚠ Saying 0/1 is a LOSS OF PRECISION, not a lie: a reader that fetches the
// shared object before the point it is used fetches it too early, never too
// late. A wrong non-zero fraction would be the other way round.
//
// ⛔⛔ IT WALKS THE PLAN, NOT THE PAGE'S LIST, and that is what makes it total.
// The first version looked each of the page's objects up in plan.identifier and
// skipped a miss — a branch no caller could reach, so nothing measured whether
// skipping was the right answer, and the alternative (indexing the map anyway)
// would have handed out identifier 0, which names ANOTHER object. Walking the
// plan cannot produce an identifier the table does not define.
//
// ⭐ And the result comes out sorted by identifier, which is what the table
// wants, without a sort: plan.objects is already in identifier order, because
// the identifier IS the index.
func refsFor(plan sharedPlan, shared []int) []sharedRef {
	reached := make(map[int]bool, len(shared))
	for _, num := range shared {
		reached[num] = true
	}
	out := make([]sharedRef, 0, len(shared))
	for id, num := range plan.objects {
		if reached[num] {
			out = append(out, sharedRef{Identifier: uint32(id)})
		}
	}
	return out
}

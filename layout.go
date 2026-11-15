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

// ErrCannotLayOut reports a partition this package declines to put in order.
var ErrCannotLayOut = errors.New("reader: this partition cannot be laid out for linearization")

// Order returns the object numbers of a partition in the order this package
// writes them: part 4, then 6, then 7, then 8, then 9.
//
// It is step 2 of writing a linearized file (go-pdfkit/reader#40) and, like
// [Document.Partition] before it, it is deliberately separable from the
// writing: the order is a pure function of the partition, and it can be judged
// against another producer's file. A layout tangled into the emission could
// only be checked by writing a whole file and asking qpdf about all of it at
// once.
//
// ⛔⛔ THE PART ORDER IS MEASURED, AND THE ANNEX IT COMES FROM WAS NEVER READ.
// An earlier version of this comment opened "the order ISO 32000-1 Annex F.3.1
// lays them out" as plain fact and admitted ten lines later that the annex was
// unavailable — the same contradiction this arc has now produced four times.
// What is true: ISO 32000-1 is not reachable from here, so the rule below was
// established by reading where qpdf 12.4.2 actually puts each part's objects.
//
//	1 page                       part 4 @500,  6 @667..861,  9 @892
//	2 pages                      part 4 @501,  6 @677..871,  7 @902..998,   9 @1096
//	5 pages                      part 4 @503,  6 @683..881,  7 @913..1595,  9 @1693
//	25 pages                     part 4 @503,  6 @695..894,  7 @926..5590,  9 @5689
//	10 pages, 4 shared fonts     part 4 @604,  6 @808..1370, 7 @1402..3090, 9 @3189
//	25 pages, 8 shared fonts     part 4 @684,  6 @896..1786, 7 @1818..6506, 9 @6605
//	testdata/linearized-shared   part 4 @481,  6 @658..766,  7 @865..1175,
//	                             part 8 @1273, 9 @1874
//
// Every part contiguous, no two overlapping, on all seven.
//
// ⚠ THE EVIDENCE IS NOT EVEN ACROSS THE PARTS, and the summary line that said
// "a measured property of real output rather than an inference" was true of
// most of it and not all. Parts 4, 6, 7 and 9 are attested by seven files;
// **part 8 by exactly one**, because the shared-font shapes have the first page
// reference the shared dictionary too and F.3.9 keeps such an object in part 6
// — those documents grow part 6 from 3 objects to 12 and say nothing about part
// 8. testdata/linearized-shared.pdf was built for that question (pages 2..n
// share a dictionary page 1 never names). So part 8's position here is n=1:
// corroborated, not established.
//
// ⚠ AND QPDF'S OWN DOCUMENTATION CORROBORATES THE WEAKER HALF ONLY. Its
// linearization chapter never enumerates the parts, never gives their order and
// cites no clause, but it does say that deciding which part each object belongs
// to "tells us the exact order in which objects are written". That supports
// "part membership determines file order" — the half this function rests on —
// and says nothing about the order being ascending by part number.
//
// ⚠ WITHIN a part the order is the object number ascending, and that is THIS
// PACKAGE'S CHOICE rather than something the annex requires or qpdf agrees
// with. It is fixed so that writing the same document twice gives the same
// bytes. Nothing depends on it for CORRECTNESS, since a reader follows the hint
// tables' offsets — but inside part 6 it can still affect what a sequential
// viewer can paint first, which is a streaming property and not a correctness
// one. The tests assert the part boundaries and never the order inside a part.
func (p *Partition) Order() ([]int, error) {
	// F.3.1's object-bearing parts. Parts 1, 2, 3, 5, 10 and 11 are not
	// objects of the document — they are the header, the parameter dictionary,
	// the two cross-reference sections and the two hint streams — so they are
	// the writer's business and not the partition's.
	parts := []Part{PartCatalog, PartFirstPage, PartOtherPrivate, PartShared, PartOther}

	// ⛔⛔ AN OBJECT IN A PART THIS FUNCTION DOES NOT KNOW IS A REFUSAL, NOT A
	// SILENT LOSS, and the first version dropped it. Part is an open int and
	// Of is an exported map, so `Of: {1: PartCatalog, 2: Part(5), 3:
	// PartOther}` returned [1 3] — three objects in, two out, no error.
	// Measured by review. A zero Part went the same way, and so would a future
	// part 3, 5, 10 or 11 if one were ever assigned.
	//
	// ⛔ A layout that loses an object writes a file missing it, and the file
	// still opens: the cross-reference table simply never mentions it, so a
	// reader following a reference to it fails on a document that looked
	// written. That is the failure Partition refuses rather than guesses at,
	// and this is the same refusal one step along.
	known := map[Part]bool{}
	for _, part := range parts {
		known[part] = true
	}
	for num, part := range p.Of {
		if !known[part] {
			return nil, fmt.Errorf("%w: object %d is in %v, which this layout does not place",
				ErrCannotLayOut, num, part)
		}
	}

	out := make([]int, 0, len(p.Of))
	for _, part := range parts {
		first := len(out)
		for num, got := range p.Of {
			if got == part {
				out = append(out, num)
			}
		}
		// ⛔ Sorting the SEGMENT and not the whole slice: a sort of everything
		// at the end would put object 2 of part 9 before object 7 of part 4
		// and destroy the only thing this function is for. Mutation-confirmed
		// — `sort.Ints(out)` here reddens all seven fixtures.
		sort.Ints(out[first:])
	}
	return out, nil
}

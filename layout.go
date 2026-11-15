// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import "sort"

// Order returns the object numbers of a partition in the order ISO 32000-1
// Annex F.3.1 lays them out: part 4, then 6, then 7, then 8, then 9.
//
// It is step 2 of writing a linearized file (go-pdfkit/reader#40) and, like
// [Document.Partition] before it, it is deliberately separable from the
// writing: the order is a pure function of the partition, and it has a foreign
// judge. A layout tangled into the emission could only be checked by writing a
// whole file and asking qpdf about it, which is a judgement on everything at
// once.
//
// ⭐⭐ THE ORDER OF THE PARTS IS MEASURED, NOT READ. Annex F.3.1 is not
// available to this package's author, so the rule here was established by
// reading where qpdf 12.4.2 actually puts each part's objects, over seven
// shapes:
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
// Every part is contiguous and no two overlap, on all seven. So "emit the
// parts in ascending part number" is a measured property of real output rather
// than an inference.
//
// ⛔ AND PART 8 APPEARS IN EXACTLY ONE OF THEM, which is why the list is worth
// printing in full. The shared-font shapes have every page reference the shared
// dictionary INCLUDING THE FIRST, and F.3.9 places in part 8 only what the
// first page does NOT reference — so those documents grow part 6 instead, from
// 3 objects to 12, and say nothing about where part 8 goes.
// testdata/linearized-shared.pdf was built for that question: pages 2..n share
// a dictionary page 1 never names. Without it this function's central claim
// would rest on six files that cannot see it.
//
// ⚠ WITHIN a part the order is the object number ascending, and that is THIS
// PACKAGE'S CHOICE rather than something the annex requires or qpdf agrees
// with. A linearized file is read by following offsets, so nothing downstream
// depends on it; it is fixed only so that writing the same document twice
// gives the same bytes. The tests assert the part boundaries, never the order
// inside a part, because asserting qpdf's intra-part order would be pinning a
// decision this package did not make.
func (p *Partition) Order() []int {
	// F.3.1's order. Parts 1, 2, 3, 5, 10 and 11 are not objects of the
	// document — they are the header, the parameter dictionary, the two
	// cross-reference sections and the two hint streams — so they are the
	// writer's business and not the partition's.
	parts := []Part{PartCatalog, PartFirstPage, PartOtherPrivate, PartShared, PartOther}

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
	return out
}

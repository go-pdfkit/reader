// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import "fmt"

// checkHintLengths compares the two LENGTHS the hint tables state against the
// file itself: Table F.4's item 2, a page's length in bytes, and Table F.6's
// item 1, a shared group's.
//
// ⛔⛔ THIS IS "THE TABLES ARE WRONG", WHICH DECODING CANNOT SEE. #60 closed
// "the tables are unreadable" — every width in range, every entry inside the
// stream, the page offset table ending at or before /S — and said in writing
// that it did not close this. A table can satisfy every rule of F.4's LAYOUT
// and still send a reader to the wrong byte, and testdata/linearized-gs.pdf is
// committed as the file that does: `min_group_length: 65536` is 0x10000, a
// placeholder nobody filled in, and qpdf reports five length mismatches in it.
//
// ⛔ THE ORACLE IS QPDF'S OWN `computed` FIGURES, not this package's idea of a
// length. qpdf prints both numbers for each mismatch, so ours can be compared
// field by field — which is how #53 and #55 settled the headers, and the only
// way to know that a check refusing a file is right rather than merely strict.
// A length check that disagrees with a conforming file is worse than no check:
// it rejects readable PDFs, which is #52's scar (a work budget that refused a
// 150 000-page file) and #59's (a bound that refused an ordinary document
// sharing three fonts).
// ⛔ IT RECORDS WHAT IT MEASURED on the Linearization, because returning nil
// alone cannot distinguish "every length agreed" from "no length could be
// measured" — see LengthsChecked.
func checkHintLengths(b []byte, d *Document, lin *Linearization, t *hintTables) error {
	if err := checkPageLengths(b, d, lin, t); err != nil {
		return err
	}
	return checkGroupLengths(b, d, lin, t)
}

// ⛔⛔ THERE IS NO SEAM HERE, AND THE REASONING THAT WANTED ONE WAS FALSE. A
// first version made this a replaceable var, on the grounds that no file could
// reach the propagation: the one fixture with wrong lengths does not decode (its
// entries overrun /S by 26 bits), every file that decodes is a file qpdf accepts,
// and building a counter-example "would mean re-encoding a Flate-compressed hint
// stream".
//
// ⭐ That is true of qpdf's output and FALSE OF OURS. writeHintStream emits the
// data RAW with no /Filter, and hintByteWidth pins item 2 at 32 bits — so the
// page-length column of a file this package wrote sits at a computable byte, and
// adding 1 to it produces exactly the file the propagation needs, with every
// offset, /Length and /L still valid. Six review rounds of #66 were spent
// replacing stated claims with files; reaching for a seam while the materials
// for a real witness were in our own writer was the same mistake one layer up.

// checkPageLengths compares Table F.4 item 2 for every page.
//
// ⛔⛔ THE SPAN IS CONSECUTIVE OBJECT NUMBERS FROM THE PAGE OBJECT, which is
// qpdf's definition and not an obvious one: Lin::lengthNextN takes the page
// dictionary's number and the next nobjects-1 NUMBERS, and sums each one's own
// length. It never consults which objects the page actually reaches. Writing
// this package's linearizer taught that the hard way — a page whose content
// stream was numbered below its page object was measured against the page tree,
// and the fix was to the LAYOUT (#66).
//
// ⛔ So a checker that summed "the objects this page reaches" would disagree
// with qpdf on exactly the files qpdf complains about, and would be measuring a
// different quantity under the same name.
func checkPageLengths(b []byte, d *Document, lin *Linearization, t *hintTables) error {
	refs := d.pageRefs()
	for i, e := range t.pages {
		if i >= len(refs) {
			return fmt.Errorf("%w: the page offset table holds %d entr(ies) and the document "+
				"has %d page(s)", ErrHintTablesInvalid, len(t.pages), len(refs))
		}
		total, ok, err := spanLength(b, d, lin, refs[i].Num, int(e.NObjects))
		if err != nil {
			return err
		}
		if !ok {
			// ⚠ Not measurable the way qpdf measures it — see spanLength.
			lin.LengthsSkipped++
			continue
		}
		lin.LengthsChecked++
		if int(e.Length) != total {
			return fmt.Errorf("%w: page %d's entry says %d bytes and its %d object(s) from "+
				"object %d occupy %d", ErrHintTablesInvalid, i+1, e.Length, e.NObjects,
				refs[i].Num, total)
		}
	}
	return nil
}

// checkGroupLengths compares Table F.6 item 1 for every shared group.
//
// ⛔⛔ THE GROUPS DESCRIBE TWO DIFFERENT RUNS OF OBJECTS, and reading them as
// one is the mistake this function exists to avoid. Table F.5's item 3 is the
// number of entries for the FIRST PAGE and item 4 is the number in all,
// including item 3 — so the first NSharedFirstPage groups describe objects of
// part 6, counted from /O, and the rest describe part 8, counted from item 1.
//
// ⭐ MEASURED on qpdf's own output rather than inferred from the annex: for a
// two-page file it reports nshared_first_page 3, nshared_total 3 and part 8
// EMPTY, with group lengths 96, 113 and 31 — and /O is 7, with objects 7, 8 and
// 9 at bytes 677, 773 and 886 and the first page ending at 917. 773-677 = 96,
// 886-773 = 113, 917-886 = 31. The three groups are the first page's own
// objects, which is why "the shared table describes part 8" is false.
func checkGroupLengths(b []byte, d *Document, lin *Linearization, t *hintTables) error {
	if t.shared == nil {
		return nil
	}

	// Where each run starts: the first page's objects from /O, part 8's from
	// Table F.5 item 1.
	num := lin.FirstPageObject
	for i, g := range t.sharedGroups {
		if i == int(t.shared.NSharedFirstPage) {
			num = int(t.shared.FirstSharedObj)
		}
		count := int(g.NObjects)
		if count <= 0 {
			return fmt.Errorf("%w: shared group %d covers %d object(s)",
				ErrHintTablesInvalid, i, count)
		}
		total, ok, err := spanLength(b, d, lin, num, count)
		if err != nil {
			return err
		}
		if !ok {
			lin.LengthsSkipped++
		} else {
			lin.LengthsChecked++
			if int(g.Length) != total {
				return fmt.Errorf("%w: shared group %d says %d bytes and its %d object(s) "+
					"from object %d occupy %d", ErrHintTablesInvalid, i, g.Length, count,
					num, total)
			}
		}
		num += count
	}
	return nil
}

// spanLength sums the lengths of `count` objects numbered from `first`, and
// reports whether the span could be measured at all.
//
// ⛔⛔ FALSE RATHER THAN AN ERROR WHEN AN OBJECT IS COMPRESSED. An object inside
// an object stream has no offset of its own: qpdf substitutes its CONTAINER's
// offset, so several objects of one stream all measure from the same byte and
// their "lengths" overlap. That is a quirk of its accounting rather than a fact
// about the file, and reproducing it would make this checker refuse or accept
// files for reasons neither the annex nor the document supports.
//
// ⚠ So such a span is NOT CHECKED, and that is a stated hole rather than a
// silent one: a length check that cannot measure a quantity must not pretend to
// have agreed with it. Nothing here refuses the file for it, because an object
// stream is legal in a linearized file and F.4 says nothing against it.
func spanLength(b []byte, d *Document, lin *Linearization, first, count int) (int, bool, error) {
	total := 0
	for k := 0; k < count; k++ {
		e, ok := d.xref[first+k]
		if !ok {
			return 0, false, fmt.Errorf("%w: a hint table measures object %d, which the "+
				"cross-reference table does not list", ErrHintTablesInvalid, first+k)
		}
		if e.kind != 'n' {
			return 0, false, nil
		}
		n, err := objectExtent(b, int(e.offset), first+k)
		if err != nil {
			return 0, false, err
		}
		total += n
	}
	return total, true, nil
}

// objectExtent returns how many bytes the object at `at` occupies, counting the
// line ending after `endobj`.
//
// ⛔ THE SAME END-OF-OBJECT RULE /E AND /H ALREADY USE, which is what makes the
// three agree: Table F.1 counts the line ending after `endobj`, the parse stops
// at it, and eolAfter adds it back. qpdf calls the same quantity
// `end_after_space`.
func objectExtent(b []byte, at, num int) (int, error) {
	if at < 0 || at >= len(b) {
		return 0, fmt.Errorf("%w: object %d is at byte %d and the file is %d bytes",
			ErrHintTablesInvalid, num, at, len(b))
	}
	_, _, n, err := ParseIndirectObject(b[at:], nil)
	if err != nil {
		return 0, fmt.Errorf("%w: object %d, at byte %d, does not parse: %v",
			ErrHintTablesInvalid, num, at, err)
	}
	return n + eolAfter(b, at+n), nil
}

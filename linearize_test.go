// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// digits normalises the byte offsets and object numbers inside a qpdf
// complaint, so the same complaint about two different files compares equal.
var digits = regexp.MustCompile(`\d+`)

// ⛔⛔⛔ THE FIRST TIME A FOREIGN JUDGE SEES A FILE THIS PACKAGE WROTE. Every
// earlier step was judged against qpdf's output; this asks qpdf about ours.
//
// ⚠ IT IS NOT EXPECTED TO PASS YET, and what it records is the point. The hint
// tables are a placeholder, so `qpdf --check` must complain about them — and
// must NOT complain about anything else. That separation is the whole reason to
// write the layout before the tables: if qpdf reported a hint-table problem
// while a wrong /T sat underneath, neither this code nor its author could tell
// which was which.
//
// ⭐ So this test asserts the SHAPE of qpdf's verdict: it recognises the file,
// it reports the linearization as checked, and every complaint it makes is
// about hint tables or page offsets. A new complaint of any other kind is a
// defect in the layout and fails here.
func TestWhatQpdfSaysAboutAFileWeWrote(t *testing.T) {
	for _, pages := range []int{1, 2, 5} {
		t.Run(fmt.Sprintf("%d pages", pages), func(t *testing.T) {
			qpdf := qpdfOrSkip(t)

			plain := buildPages(t, pages)
			d, err := Open(plain)
			if err != nil {
				t.Fatal(err)
			}
			out, err := d.Linearize()
			if err != nil {
				t.Fatalf("Linearize refused a %d-page document this package just built: %v",
					pages, err)
			}

			dir := t.TempDir()
			path := filepath.Join(dir, "ours.pdf")
			if err := os.WriteFile(path, out, 0o644); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(qpdf, "--check", path)
			cmd.Stdin = nil
			raw, _ := cmd.CombinedOutput()
			got := string(raw)

			// ⛔ The exit status is discarded as everywhere else here: qpdf
			// exits 3 for warnings and 0 for files it has refused in other
			// ways. The verdict is in the text.
			if !strings.Contains(got, "PDF Version:") {
				t.Fatalf("qpdf did not report on our file at all, so what it does not say "+
					"proves nothing:\n%s", got)
			}
			t.Logf("qpdf --check on our own %d-page file:\n%s", pages, got)

			// ⛔⛔⛔ THE COMPLAINTS ARE DIFFED AGAINST THE INPUT'S, not matched
			// against a list of words I chose. The first version of this test
			// carried two keyword lists — "what counts as a complaint" and
			// "what counts as a hint-table complaint" — and both were guesses
			// about qpdf's wording.
			//
			// ⛔ MEASURED, and it is why the list had to go: `qpdf --check` on
			// the INPUT already says "object 3 0 ... kid 0 (from 0) Resources
			// is missing or invalid; repairing", because buildPages gives its
			// pages no /Resources. That complaint is not about anything this
			// writer did, and a keyword list would have had me chasing it.
			//
			// ⭐ So the judge is the DIFFERENCE: every complaint qpdf makes
			// about our file that it does not also make about the file we were
			// given. That needs no list and cannot go stale with qpdf's
			// wording.
			// ⛔⛔ PART 5 BEFORE PART 6, ASSERTED HERE BECAUSE QPDF DOES NOT
			// POLICE IT. Review found the first version emitting the hint
			// stream AFTER the first page and `qpdf --check` passing it
			// anyway: the judge below is "no complaints qpdf does not also
			// make about the input", and qpdf never complains about which
			// part the hint stream occupies. A divergence from F.3.1's
			// numbering AND from qpdf's own output sailed through.
			//
			// ⭐ So the fix needs its own witness. /H must come before the
			// first page's own object, or the tables that tell a streaming
			// reader what to fetch arrive after the bytes they were meant to
			// save it from fetching.
			lin, err := Linearized(out)
			if err != nil {
				t.Fatalf("this package cannot read the file it just wrote: %v", err)
			}
			written, err := Open(out)
			if err != nil {
				t.Fatal(err)
			}
			pageEntry, ok := written.xref[lin.FirstPageObject]
			if !ok {
				t.Fatalf("/O names object %d and our own xref has no entry for it",
					lin.FirstPageObject)
			}
			if int64(lin.HintOffset) >= pageEntry.offset {
				t.Errorf("the hint stream is at %d and the first page's object at %d, so part 5 "+
					"follows part 6 — the tables arrive after the page they describe",
					lin.HintOffset, pageEntry.offset)
			}
			if lin.HintOffset <= 0 {
				t.Errorf("/H is %d, so the hint stream was never placed", lin.HintOffset)
			}

			// ⛔ The other side of the bracket — part 5 must not land in the
			// MIDDLE of part 4 either — is asserted by
			// TestOrderPutsPartFourBeforePartSixContiguously, on the
			// assumption itself rather than on a consequence of it. A /Root
			// offset below /H was tried here first and is too weak: see that
			// test.

			base := qpdfComplaints(t, qpdf, dir, "in.pdf", plain)
			ours := qpdfComplaints(t, qpdf, dir, "ours.pdf", out)
			for l := range ours {
				if base[l] {
					continue
				}
				if isAboutHintTables(l) {
					continue
				}
				t.Errorf("qpdf complains about OUR file and not about the input, and it is not "+
					"about the hint tables — so the layout or the parameters are wrong: %q", l)
			}
		})
	}
}

// qpdfComplaints runs `qpdf --check` and returns its WARNING and ERROR lines
// with the file's own path removed, so two runs on different paths compare.
func qpdfComplaints(t *testing.T, qpdf, dir, name string, b []byte) map[string]bool {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(qpdf, "--check", path)
	cmd.Stdin = nil
	raw, _ := cmd.CombinedOutput()

	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "WARNING") && !strings.HasPrefix(l, "ERROR") {
			continue
		}
		// ⛔ The path is stripped because it holds the test's temporary
		// directory, which differs between the two runs and would make every
		// line unique.
		l = strings.ReplaceAll(l, path, "<file>")
		// ⛔⛔ AND SO ARE THE NUMBERS, which the first version did not do and
		// which defeated the whole diff. The /Resources complaint is in BOTH
		// files and reads "at offset 95" for the input and "at offset 659" for
		// ours — the same complaint about the same object, never matching
		// because the object moved. Comparing complaints across two files
		// means comparing what they SAY, not where they point.
		out[digits.ReplaceAllString(l, "<n>")] = true
	}
	return out
}

// isAboutHintTables says whether a complaint is about F.4, which this writer
// does not produce yet.
//
// ⚠ THIS IS STILL A LIST OF WORDS AND STILL A GUESS, and it is narrower than
// the two it replaced: it only decides which of OUR OWN new complaints are the
// expected ones. A wrong guess here makes the test too strict, never too
// lenient — the opposite risk from the version that used a list to decide what
// counted as a complaint at all.
func isAboutHintTables(line string) bool {
	for _, p := range []string{"hint table", "hint stream", "linearization hint",
		"shared object", "page offset", "page length",
		// ⛔ "first page object offset" had to be added, and the reason is the
		// one this function's own note predicts: the list is a guess and a
		// wrong guess here makes the test too strict. That string is Table
		// F.3's item 2, `first_page_offset` — squarely a hint-table field —
		// and it does not contain "page offset" because the word "object"
		// sits in the middle.
		"first page object offset",
		// ⛔ And the per-page object counts, Table F.4 item 1.
		"object count mismatch for page"} {
		if strings.Contains(line, p) {
			return true
		}
	}
	return false
}

// ⛔⛔ ORDER IS PART-MAJOR, which the emission SLICES ON and which nothing
// else states. It writes order[:catalogueCount] as part 4 and
// order[catalogueCount:firstPageCount] as part 6, with the hint stream between
// them, and those are the two parts only if every part-4 object precedes every
// part-6 object in `order`.
//
// ⛔ A WEAKER WITNESS WAS TRIED FIRST AND REVIEW REFUTED IT. The qpdf test
// asserted /Root's offset was below /H — "part 5 is not inside part 4". But
// Partition assigns exactly ONE object to part 4 (partition.go: a single
// `of[catNum] = PartCatalog`), so catalogueCount is 1 on every shape,
// order[:catalogueCount] is always order[:1], and the check reduces to "the
// catalogue is emitted first". A layout that interleaved parts 4 and 6 while
// leaving the catalogue first would survive it.
//
// ⭐ So this asserts the PREFIX ITSELF, which no corpus coincidence can satisfy:
// part 4's objects, then part 6's, with nothing else among them. Measured today
// the prefix is [4 6 6 6] at one, two and five pages.
func TestOrderPutsPartFourBeforePartSixContiguously(t *testing.T) {
	for _, pages := range []int{1, 2, 5} {
		t.Run(fmt.Sprintf("%d pages", pages), func(t *testing.T) {
			d, err := Open(buildPages(t, pages))
			if err != nil {
				t.Fatal(err)
			}
			p, err := d.Partition()
			if err != nil {
				t.Fatal(err)
			}
			order, err := p.Order()
			if err != nil {
				t.Fatal(err)
			}

			catalogueCount, firstPageCount := 0, 0
			for _, old := range order {
				part := p.Of[old]
				if part != PartCatalog && part != PartFirstPage {
					break
				}
				if part == PartCatalog {
					catalogueCount++
				}
				firstPageCount++
			}
			if catalogueCount == 0 || firstPageCount == catalogueCount {
				t.Fatalf("the prefix is %d part-4 and %d part-6 objects, so this test would "+
					"pass on an empty assertion", catalogueCount, firstPageCount-catalogueCount)
			}
			for i, old := range order[:firstPageCount] {
				want := PartFirstPage
				if i < catalogueCount {
					want = PartCatalog
				}
				if got := p.Of[old]; got != want {
					t.Errorf("order[%d] is object %d in %v, and the emission will write it as "+
						"%v — parts 4 and 6 are interleaved, so slicing on the counts is wrong",
						i, old, got, want)
				}
			}

			// ⛔⛔ AND THE BOUNDARY IN THE EMITTED FILE, because everything
			// above re-derives the counts in this test's own body and so
			// exercises Order, NOT linearizeFrom's slicing. Review measured
			// the hole: with catalogueCount forced to 0, /H moves from 579 to
			// 532, ahead of the catalogue at 647, and CheckLinearized returns
			// nil on all three shapes — qpdf accepts it too. Removing the weak
			// /Root-before-/H check left NO witness at all, which is worse than
			// the weak one it replaced.
			//
			// ⭐ This asserts the boundary rather than a consequence of it:
			// renumbering follows order position, so the front section is
			// objects 1..firstPageCount, and part 4 is exactly 1..catalogueCount.
			// A count wrong in EITHER direction moves an object across /H.
			out, err := d.Linearize()
			if err != nil {
				t.Fatal(err)
			}
			lin, err := Linearized(out)
			if err != nil {
				t.Fatalf("this package cannot read the file it just wrote: %v", err)
			}
			written, err := Open(out)
			if err != nil {
				t.Fatal(err)
			}
			for n := 1; n <= firstPageCount; n++ {
				entry, ok := written.xref[n]
				if !ok {
					t.Fatalf("object %d is in the front section and our own xref has no "+
						"entry for it", n)
				}
				below := entry.offset < int64(lin.HintOffset)
				if wantBelow := n <= catalogueCount; below != wantBelow {
					side, want := "after", "before"
					if below {
						side, want = "before", "after"
					}
					t.Errorf("object %d is in %v at offset %d, which is %s the hint stream "+
						"at %d — it must come %s it",
						n, p.Of[order[n-1]], entry.offset, side, lin.HintOffset, want)
				}
			}
		})
	}
}

// ⛔⛔ THE EMISSION IS STABLE ACROSS PASSES, which the writer relies on and
// which no guard inside it can state. The first version compared pass 1's
// measurements against pass 0's and refused a mismatch; that branch is
// unreachable, because everything ahead of the objects is fixed width, and the
// coverage gate refused it.
//
// ⛔⛔ AND REPEATING Linearize IS NOT THE PROPERTY. Four calls redo the same two
// passes and would agree whether or not the fixpoint converged, so that half of
// this test witnesses DETERMINISM only — worth having, and not what the removed
// guard claimed.
//
// ⭐ THE CONVERGENCE WITNESS IS THAT Linearized ACCEPTS THE FILE. /L is written
// from the PREVIOUS pass's measurement, and F.3.3 requires it to "be exactly
// equal to the actual length of the PDF file" — so if anything ahead of the
// objects ever becomes variable-width (a trailer key, a subsection header, a
// span that stops being reserved) the length moves under a /L already written.
// This package's own reader refuses exactly that, and measuring the mutation
// proved it: one pass instead of two gives "/L says 0 bytes and the file is
// 1112" from Linearized itself, on all three shapes.
//
// ⛔ Which is why there is no /L comparison HERE. The first version of this test
// added one, and it was unreachable: Linearized had already returned the error,
// so the branch could never run. An unreachable assertion in a test is the same
// fault as an unreachable guard in the code — a claim nothing can falsify — and
// it is the fault this test exists to correct.
func TestTheEmissionIsStableAcrossPasses(t *testing.T) {
	for _, pages := range []int{1, 2, 5} {
		t.Run(fmt.Sprintf("%d pages", pages), func(t *testing.T) {
			d, err := Open(buildPages(t, pages))
			if err != nil {
				t.Fatal(err)
			}
			first, err := d.Linearize()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Linearized(first); err != nil {
				t.Fatalf("this package cannot read the file it just wrote: %v", err)
			}
			for pass := 2; pass <= 4; pass++ {
				again, err := d.Linearize()
				if err != nil {
					t.Fatalf("pass %d refused what pass 1 wrote: %v", pass, err)
				}
				if !bytes.Equal(first, again) {
					t.Fatalf("pass %d produced %d bytes and pass 1 produced %d, so the layout "+
						"is not stable", pass, len(again), len(first))
				}
			}
		})
	}
}

// infoAndIDFixture writes a one-page document that carries both a document
// information dictionary and an /ID, which buildPages does not — and whose
// absence is why the first version of the writer dropped both in silence.
func infoAndIDFixture(t *testing.T) []byte {
	t.Helper()
	w := NewWriter("1.7")
	pagesRef := w.Reserve()
	content := w.Add(&Stream{Dict: Dict{}, Raw: []byte("0 g")})
	page := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef, "Contents": content})
	w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": Array{page},
		"Count":    Integer(1),
		"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
	root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})
	info := w.Add(Dict{"Producer": String("go-pdfkit/reader"), "Title": String("a title")})
	id := Array{String("0123456789abcdef"), String("0123456789abcdef")}

	plain, err := w.Finish(Dict{"Root": root, "Info": info, "ID": id})
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

// ⛔⛔ /Info AND /ID SURVIVE THE COPY, which no fixture could show: neither
// buildPages nor any committed file carries either, which is exactly why the
// first version dropped them in silence and no test noticed. Review found it by
// reading what the trailer does NOT write.
//
// ⛔ A document information dictionary is metadata a reader shows a person, and
// /ID is how a reader tells whether two files are the same document. Losing
// either is not a crash; it is a quieter kind of wrong.
func TestTheTrailerCarriesInfoAndID(t *testing.T) {
	plain := infoAndIDFixture(t)
	d, err := Open(plain)
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Linearize()
	if err != nil {
		t.Fatalf("a document with /Info and /ID was refused: %v", err)
	}

	got, err := Open(out)
	if err != nil {
		t.Fatalf("our own file does not open: %v", err)
	}
	ref, isRef := got.trailer["Info"].(Ref)
	if !isRef {
		t.Fatalf("/Info came out as %v, not an indirect reference", got.trailer["Info"])
	}
	// ⛔ AND IT RESOLVES, which the reference alone does not show: a renumbered
	// /Info pointing at an object the layout dropped would still be a Ref.
	infoDict, err := got.Get(ref)
	if err != nil {
		t.Fatalf("/Info names object %d and it does not read: %v", ref.Num, err)
	}
	dict, ok := ToDict(infoDict)
	if !ok {
		t.Fatalf("/Info resolves to %v, not a dictionary", infoDict)
	}
	if s, _ := dict["Title"].(String); string(s) != "a title" {
		t.Errorf("/Info's /Title came out as %q", dict["Title"])
	}
	arr, isArr := got.trailer["ID"].(Array)
	if !isArr || len(arr) != 2 {
		t.Fatalf("/ID came out as %v", got.trailer["ID"])
	}
	if s, _ := arr[0].(String); string(s) != "0123456789abcdef" {
		t.Errorf("/ID's first string changed to %q — a reader uses it to tell two files "+
			"apart, so a linearized copy must keep it", s)
	}
	// ⛔⛔ AND THE SECOND STRING MUST MOVE. The first version asserted only the
	// line above and passed while BOTH strings were copied, which made the copy
	// claim to be the same REVISION as its input. qpdf --linearize regenerates
	// this element; see documentID for the measurement and for why Ghostscript
	// is not the model here.
	if s, _ := arr[1].(String); string(s) == "0123456789abcdef" {
		t.Errorf("/ID's second string is unchanged, so the copy claims to be the same "+
			"revision as its input: %q", s)
	}
	// ⛔ A derived identifier, not a random one: Linearize must be
	// deterministic, which TestTheEmissionIsStableAcrossPasses relies on.
	again, err := d.Linearize()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, again) {
		t.Error("two linearizations of one document differ, so /ID's second string is random")
	}

}

// ⛔⛔ QPDF JUDGES THE /Info + /ID SHAPE TOO, which until now nothing did. Every
// other qpdf measurement in this file runs on buildPages, and buildPages carries
// neither key — so the two trailer entries added for them had no foreign oracle
// at all. A rewritten /ID or a mispadded span is exactly what qpdf names.
//
// ⛔ A separate test and not a branch inside the one above, because the judge
// must not be silently skipped: qpdfOrSkip fails when REQUIRE_QPDF is set, and
// an inline `if qpdf is installed` would have reported success for an agreement
// it never checked.
func TestQpdfJudgesTheInfoAndIDFile(t *testing.T) {
	qpdf := qpdfOrSkip(t)

	plain := infoAndIDFixture(t)
	d, err := Open(plain)
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Linearize()
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	base := qpdfComplaints(t, qpdf, dir, "in.pdf", plain)
	written := qpdfComplaints(t, qpdf, dir, "ours.pdf", out)
	for l := range written {
		if !base[l] && !isAboutHintTables(l) {
			t.Errorf("qpdf says this about our /Info+/ID file and not about the input, "+
				"and it is not about the hint tables: %q", l)
		}
	}
}

// ⛔ EVERY SHAPE OF /ID THIS WRITER WILL NOT REWRITE, because it copies
// documents it did not produce. /ID is required to be two strings; a file that
// breaks that is not this operation's business to refuse, and rewriting half of
// a structure we cannot read is worse than carrying it.
func TestDocumentIDLeavesWhatItCannotRead(t *testing.T) {
	d := &Document{buf: []byte("%PDF-1.7\n")}
	for _, tc := range []struct {
		name string
		in   Object
	}{
		{"not an array", Name("nope")},
		{"one element", Array{String("only one")}},
		{"empty", Array{}},
		{"first element not a string", Array{Integer(1), String("second")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// ⛔ reflect.DeepEqual and not fmt.Sprint: comparing through
			// String() would call every decorative Kind equal.
			if got := d.documentID(tc.in); !reflect.DeepEqual(got, tc.in) {
				t.Errorf("documentID rewrote %v into %v", tc.in, got)
			}
		})
	}

	t.Run("two strings", func(t *testing.T) {
		in := Array{String("permanent-------"), String("revision--------")}
		got, ok := d.documentID(in).(Array)
		if !ok || len(got) != 2 {
			t.Fatalf("documentID returned %v", got)
		}
		if s, _ := got[0].(String); string(s) != "permanent-------" {
			t.Errorf("the permanent identifier changed to %q", s)
		}
		if s, _ := got[1].(String); string(s) == "revision--------" {
			t.Error("the revision identifier was not replaced")
		}
		// ⛔ AND THE INPUT IS NOT MUTATED. documentID copies before writing;
		// the caller's Array is the document's own trailer value, and the two
		// passes would otherwise see different inputs.
		if s, _ := in[1].(String); string(s) != "revision--------" {
			t.Errorf("documentID mutated its caller's array: %q", s)
		}
	})
}

// ⛔⛔ AN /ID TOO LONG FOR THE OLD CONSTANT, which is the regression review
// measured: frontTrailerSpan was derived from /Size, /Prev and the catalogue's
// number alone, so 64-byte /ID strings made a conforming file REFUSED with "the
// first-page trailer needs 212 bytes and 192 are reserved". The span is now the
// base plus the bytes /Info and /ID actually take.
//
// ⛔ 64 is not a round number chosen for comfort: /ID strings have no bound in
// 14.4, so this test fixes the case that broke and the span is computed rather
// than guessed.
func TestALongIDIsNotRefused(t *testing.T) {
	// ⛔⛔ 76 IS THE BOUNDARY AND IT IS WHY THIS LIST IS NOT ROUND NUMBERS. With
	// the span fix reverted, 64 and 74 pass and 76 is the first refusal — "needs
	// 194 bytes and 192 are reserved" — so a list of 16/32/64 pins nothing and
	// 256 alone pins it far from where it breaks.
	//
	// ⛔ 76 AND NOT 74, WHICH IS REVIEW'S NUMBER FOR REVIEW'S FIXTURE. Measuring
	// it here moved it: their table (135 at 16 bytes, 183 at 64, 193 at 74, 375
	// at 256) was taken on a document whose object numbering differs from this
	// one's, which reads 372 at 256 rather than 375. A boundary is a property of
	// the fixture, not of the format, so taking theirs on faith would have put a
	// passing case where the breaking one belongs.
	//
	// ⚠ And the length is NOT monotonic in n: 76 needs 194, 77 needs 193, 78
	// needs 194. /ID[1] is a hash of the input, so its escaped literal and the
	// object offsets after it move by a byte or two with n. That is harmless
	// here — every value is pass-invariant — but it is why this reads as a
	// measured list and not a formula.
	for _, n := range []int{16, 32, 64, 74, 76, 256} {
		t.Run(fmt.Sprintf("%d-byte /ID", n), func(t *testing.T) {
			w := NewWriter("1.7")
			pagesRef := w.Reserve()
			content := w.Add(&Stream{Dict: Dict{}, Raw: []byte("0 g")})
			page := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef, "Contents": content})
			w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": Array{page},
				"Count":    Integer(1),
				"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
			root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})
			info := w.Add(Dict{"Producer": String("go-pdfkit/reader")})
			long := String(bytes.Repeat([]byte("a"), n))

			plain, err := w.Finish(Dict{"Root": root, "Info": info,
				"ID": Array{long, long}})
			if err != nil {
				t.Fatal(err)
			}
			d, err := Open(plain)
			if err != nil {
				t.Fatal(err)
			}
			out, err := d.Linearize()
			if err != nil {
				t.Fatalf("a %d-byte /ID was refused: %v", n, err)
			}
			// ⛔ AND THE FILE IS STILL COHERENT, not merely produced: a span
			// that padded wrongly would leave our own reader unable to find
			// the cross-reference section.
			if _, err := Linearized(out); err != nil {
				t.Fatalf("the file written with a %d-byte /ID does not read: %v", n, err)
			}
			got, err := Open(out)
			if err != nil {
				t.Fatalf("the file written with a %d-byte /ID does not open: %v", n, err)
			}
			// ⛔⛔ AND THE LONG /ID IS ACTUALLY IN THERE, asserted because the
			// mutation looked wrong: with the span fix reverted only the
			// 256-byte case reddened, while review had measured a refusal at
			// 64. A fixture that never carried the long /ID would look exactly
			// like that, so it had to be ruled out rather than reasoned about.
			//
			// ⭐ It is carried, and the numbers reconcile: the 212 was measured
			// while /ID[1] was still a COPY of the 64-byte string, and
			// documentID now replaces it with a 16-byte hash, so the same input
			// fits the old 192 and the first refusal moves out to 76 bytes.
			//
			// ⛔ TWO OF MY OWN FIGURES WERE WRONG ON THE WAY HERE, both from
			// arithmetic instead of measurement: 180 for the 64-byte trailer
			// (computed with /Prev 0, while the file carries a three-digit
			// /Prev) and 74 for the boundary (review's number, for review's
			// fixture). The list above is measured.
			arr, ok := got.trailer["ID"].(Array)
			if !ok || len(arr) != 2 {
				t.Fatalf("the output's /ID is %v, so this test did not exercise a long /ID",
					got.trailer["ID"])
			}
			if s, _ := arr[0].(String); len(s) != n {
				t.Fatalf("the output's /ID[0] is %d bytes and the input's was %d, so the "+
					"span was never asked to hold one", len(s), n)
			}
		})
	}
}

// ⛔⛔ AND THE NEGATIVE CONTROL #40 ASKED FOR: an ordinary file from the same
// writer must be refused as not linearized. "A test suite in which every input
// passes measures nothing" — and without this, a Linearize that returned its
// input unchanged would satisfy the test above on a file qpdf had already
// linearized.
func TestAnOrdinaryFileIsNotReportedLinearized(t *testing.T) {
	qpdf := qpdfOrSkip(t)

	plain := buildPages(t, 3)
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.pdf")
	if err := os.WriteFile(path, plain, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(qpdf, "--check", path)
	cmd.Stdin = nil
	raw, _ := cmd.CombinedOutput()
	got := string(raw)
	if !strings.Contains(got, "PDF Version:") {
		t.Fatalf("qpdf did not report on the plain file:\n%s", got)
	}
	if strings.Contains(got, "File is linearized") {
		t.Errorf("qpdf calls an ordinary file from this writer linearized, so the positive "+
			"above proves nothing:\n%s", got)
	}

	// And this package agrees it is not linearized.
	if _, err := Linearized(plain); err == nil {
		t.Error("Linearized accepted an ordinary file")
	}
}

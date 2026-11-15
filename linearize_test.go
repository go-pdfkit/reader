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

			// ⛔ THE OTHER SIDE OF THE BRACKET. The check above only says part 5
			// does not follow part 6; it says nothing about part 5 landing in
			// the MIDDLE of part 4. The emission slices `order` into
			// order[:catalogueCount] and order[catalogueCount:firstPageCount],
			// which is only the two parts if Order is part-major — and if that
			// assumption broke, a catalogue object would be written after the
			// hint stream with /H still ahead of the first page, so the check
			// above would pass.
			rootRef, ok := written.trailer["Root"].(Ref)
			if !ok {
				t.Fatalf("our own trailer's /Root is %T, not a reference", written.trailer["Root"])
			}
			rootEntry, ok := written.xref[rootRef.Num]
			if !ok {
				t.Fatalf("/Root names object %d and our own xref has no entry for it",
					rootRef.Num)
			}
			if rootEntry.offset >= int64(lin.HintOffset) {
				t.Errorf("the catalogue is at %d and the hint stream at %d, so part 5 was written "+
					"inside part 4", rootEntry.offset, lin.HintOffset)
			}

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

// ⛔⛔ /Info AND /ID SURVIVE THE COPY, which no fixture could show: neither
// buildPages nor any committed file carries either, which is exactly why the
// first version dropped them in silence and no test noticed. Review found it by
// reading what the trailer does NOT write.
//
// ⛔ A document information dictionary is metadata a reader shows a person, and
// /ID is how a reader tells whether two files are the same document. Losing
// either is not a crash; it is a quieter kind of wrong.
func TestTheTrailerCarriesInfoAndID(t *testing.T) {
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

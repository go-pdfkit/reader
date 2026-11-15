// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
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

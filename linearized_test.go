// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ⛔ THE POSITIVES ARE MADE BY QPDF, NOT BY ME. A checker tested only against
// files its own author wrote agrees with its author's reading of Annex F, and
// that is the one thing it cannot be allowed to do: the whole point is to
//判断 bytes somebody else produced. qpdf is the linearizer BentoPDF itself
// uses, so its output is both an independent implementation and the exact
// thing this has to agree about.
//
// Every test here skips when qpdf is absent rather than passing, because a
// skipped test that looks like a pass is how a checker comes to be believed
// without ever having been checked.

// TestLinearizedReadsTheCommittedFixture is the one positive that needs
// nothing installed.
//
// ⛔ IT EXISTS BECAUSE CI CAUGHT ME OUT. Every other positive here is built by
// running qpdf, and the runner has no qpdf — so they all skipped, the exact
// "a skipped test that looks like a pass" this file's own header warns about,
// and the coverage gate reported Linearized at 96.0% while it was 100% on my
// machine. testdata/linearized.pdf is a two-page document this package wrote
// and qpdf linearized, committed at 1 367 bytes: still an independent
// implementation's output, now also a deterministic one.
func TestLinearizedReadsTheCommittedFixture(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "linearized.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	lin, err := Linearized(b)
	if err != nil {
		t.Fatalf("the committed fixture was refused: %v", err)
	}
	if lin.FileLength != len(b) {
		t.Errorf("/L %d, file %d", lin.FileLength, len(b))
	}
	if lin.PageCount != 2 {
		t.Errorf("/N %d, want 2", lin.PageCount)
	}
	if lin.Version <= 0 || lin.HintOffset <= 0 || lin.HintLength <= 0 ||
		lin.MainXrefOffset <= 0 || lin.FirstPageEnd <= 0 {
		t.Errorf("an entry came back unset: %+v", lin)
	}
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := d.PageRef(1)
	if !ok {
		t.Fatal("no first page")
	}
	if ref.Num != lin.FirstPageObject {
		t.Errorf("/O is %d, the first page is object %d", lin.FirstPageObject, ref.Num)
	}
}

// ⛔⛔ THE PAIR THAT MAKES CheckLinearized WORTH HAVING. Each case below is a
// file that [Linearized] ACCEPTS and [CheckLinearized] refuses: the dictionary
// is well formed, within the window, and /L still equals the byte count,
// because every edit replaces an integer with one of THE SAME WIDTH. Nothing
// in the front of the file is wrong — what is wrong is that it describes a
// different document.
//
// That is the shape a first linearizer emits, and it is why a writer must not
// be developed against the cheap check alone: it would pass while linearizing
// nothing.
func TestAParameterTheDocumentContradictsIsCaughtOnlyByTheDeepCheck(t *testing.T) {
	for _, c := range []struct {
		name, from, to, says string
	}{
		{"a page count the document contradicts", "/N 2", "/N 3", "/N says 3 page(s)"},
		{"a document with no pages at all", "/N 2", "/N 0", "a linearized document has at least one"},
		{"a first page that is not the first page", "/O 7", "/O 6", "/O says object 6"},
		{"a hint stream that starts one byte late", "/H [ 550 127 ]", "/H [ 551 127 ]", "/H points at byte 551"},
		{"a hint stream at no offset at all", "/H [ 550 127 ]", "/H [ 000 127 ]", "/H starts at 0"},
		{"a hint stream running off the end", "/H [ 550 127 ]", "/H [ 550 999 ]", "/H is 999 bytes from 550"},
		{"a hint offset pointing at something that is not a stream",
			"/H [ 550 127 ]", "/H [ 501 127 ]", "not a stream"},
		{"a first page that ends nowhere", "/E 902", "/E 000", "/E is 0"},
		{"a main cross-reference that is not there", "/T 1169", "/T 1160", "/T is 1160"},
		{"a main cross-reference at no offset at all", "/T 1169", "/T 0000", "/T is 0"},
		// ⛔ One byte from the end: the free-entry test has to notice that the
		// buffer is shorter than an entry rather than read past it.
		{"a main cross-reference one byte from the end", "/T 1169", "/T 1366", "/T is 1366"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := editedFixture(t, c.from, c.to)

			// The cheap check still passes, which is the whole point.
			if _, err := Linearized(b); err != nil {
				t.Fatalf("Linearized refused it, so this case proves nothing "+
					"about the deep check: %v", err)
			}
			_, err := CheckLinearized(b)
			if err == nil {
				t.Fatal("CheckLinearized accepted a file that describes another document")
			}
			if !errors.Is(err, ErrLinearizationStale) {
				t.Errorf("error is %v, want it to wrap ErrLinearizationStale", err)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("it does not name the claim that failed: %v", err)
			}
		})
	}
}

// editedFixture rewrites one entry of the committed fixture's parameter
// dictionary, keeping the byte count identical.
//
// ⛔ THE WIDTH IS ASSERTED, NOT ASSUMED. A replacement one byte longer changes
// the file's length, so /L would no longer match and [Linearized] would refuse
// the file for THAT reason — the deep check would never run, and the test
// would pass while measuring nothing.
func editedFixture(t *testing.T, from, to string) []byte {
	t.Helper()
	if len(from) != len(to) {
		t.Fatalf("the edit %q -> %q changes the file's length, which breaks /L "+
			"and makes this test measure the wrong refusal", from, to)
	}
	b, err := os.ReadFile(filepath.Join("testdata", "linearized.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Count(s, from) != 1 {
		t.Fatalf("%q appears %d times in the fixture, so this edit is not the one "+
			"it is named for", from, strings.Count(s, from))
	}
	return []byte(strings.Replace(s, from, to, 1))
}

// ⛔⛔ TABLE F.1 GIVES /T TWO MEANINGS AND THE FIRST FIXTURE ONLY HAS ONE OF
// THEM. testdata/linearized.pdf uses a traditional cross-reference table, so
// every test above exercises the free-entry arm and the cross-reference STREAM
// arm had never run — the coverage gate is what said so, and until it did, the
// claim that both shapes are handled was a comment and nothing more.
//
// testdata/linearized-objstm.pdf is the same document through
// `qpdf --linearize --object-streams=generate`: still an independent
// implementation's output, and its /T is the offset of an /XRef stream object.
func TestCheckLinearizedAcceptsACrossReferenceStreamFile(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "linearized-objstm.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	lin, err := CheckLinearized(b)
	if err != nil {
		t.Fatalf("a file whose /T names a cross-reference stream was refused: %v", err)
	}
	if lin.PageCount != 2 {
		t.Errorf("/N %d, want 2", lin.PageCount)
	}
}

// ⛔ A FRONT THAT STILL SAYS "LINEARIZED" OVER A BODY THAT IS GONE. The first
// 1024 bytes are untouched and /L still equals the file length, so the cheap
// check passes; everything after the parameter dictionary is blanked, so there
// is no cross-reference table to open the document with.
//
// This is what a truncated download padded back to length looks like, and it
// is the one case where the deep check's answer is about the DOCUMENT rather
// than about a parameter.
func TestAFrontThatSaysLinearizedOverABodyThatIsGone(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "linearized.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	end := bytes.Index(b, []byte("endobj"))
	if end < 0 {
		t.Fatal("the fixture has no first object to keep")
	}
	end += len("endobj")
	gutted := make([]byte, 0, len(b))
	gutted = append(gutted, b[:end]...)
	gutted = append(gutted, bytes.Repeat([]byte(" "), len(b)-end)...)
	if len(gutted) != len(b) {
		t.Fatalf("the blanking changed the length, so /L would be the refusal: %d vs %d",
			len(gutted), len(b))
	}
	if _, err := Linearized(gutted); err != nil {
		t.Fatalf("the cheap check refused it, so this proves nothing about the deep one: %v", err)
	}
	_, err = CheckLinearized(gutted)
	if !errors.Is(err, ErrLinearizationStale) {
		t.Fatalf("error is %v, want it to wrap ErrLinearizationStale", err)
	}
	if !strings.Contains(err.Error(), "does not open") {
		t.Errorf("it does not say the document would not open: %v", err)
	}
}

// And the fixture itself passes the deep check, so the refusals above are
// about the edits and not about the checker disliking qpdf's output.
func TestCheckLinearizedAcceptsTheCommittedFixture(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "linearized.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	lin, err := CheckLinearized(b)
	if err != nil {
		t.Fatalf("qpdf's own linearized file was refused: %v", err)
	}
	if lin.PageCount != 2 {
		t.Errorf("/N %d, want 2", lin.PageCount)
	}
}

// ⛔ THE NEGATIVE CONTROL, and it is the first thing this file needed. An
// ordinary document written by this package is NOT linearized, and both checks
// must say so — a suite in which every input passes measures nothing.
func TestAnOrdinaryFileIsRefusedByBothChecks(t *testing.T) {
	b := buildPages(t, 2)
	if _, err := Linearized(b); !errors.Is(err, ErrNotLinearized) {
		t.Errorf("Linearized said %v, want ErrNotLinearized", err)
	}
	if _, err := CheckLinearized(b); !errors.Is(err, ErrNotLinearized) {
		t.Errorf("CheckLinearized said %v, want ErrNotLinearized", err)
	}
}

// qpdf's output passes the deep check at several page counts, not only at the
// one the committed fixture happens to have.
func TestCheckLinearizedAgreesWithQpdfAcrossPageCounts(t *testing.T) {
	for _, pages := range []int{1, 2, 5} {
		t.Run(strconv.Itoa(pages)+" pages", func(t *testing.T) {
			plain, linear := linearizedFixture(t, pages)
			if _, err := CheckLinearized(plain); !errors.Is(err, ErrNotLinearized) {
				t.Errorf("the plain file was not refused: %v", err)
			}
			lin, err := CheckLinearized(linear)
			if err != nil {
				t.Fatalf("qpdf linearized it and this refused it: %v", err)
			}
			if lin.PageCount != pages {
				t.Errorf("/N %d, want %d", lin.PageCount, pages)
			}
		})
	}
}

func qpdfOrSkip(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("qpdf")
	if err != nil {
		t.Skip("qpdf is not installed, so there is no independent linearizer to agree with")
	}
	return path
}

// linearizedFixture writes a small document, has qpdf linearize it, and
// returns the bytes of both.
func linearizedFixture(t *testing.T, pages int) (plain, linear []byte) {
	t.Helper()
	qpdf := qpdfOrSkip(t)
	plain = buildPages(t, pages)

	dir := t.TempDir()
	in := filepath.Join(dir, "in.pdf")
	out := filepath.Join(dir, "out.pdf")
	if err := os.WriteFile(in, plain, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(qpdf, "--linearize", in, out)
	cmd.Stdin = nil
	if b, err := cmd.CombinedOutput(); err != nil {
		// qpdf exits 3 for warnings and still writes the file.
		if _, statErr := os.Stat(out); statErr != nil {
			t.Fatalf("qpdf --linearize: %v: %s", err, b)
		}
	}
	linear, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return plain, linear
}

// buildPages writes a document of n pages, each with a little content, using
// this package's own writer.
func buildPages(t *testing.T, n int) []byte {
	t.Helper()
	w := NewWriter("1.7")
	pagesRef := w.Reserve()
	kids := make(Array, 0, n)
	for i := 1; i <= n; i++ {
		content := w.Add(&Stream{Dict: Dict{}, Raw: []byte("0 g 72 700 100 20 re f")})
		kids = append(kids, w.Add(Dict{
			"Type": Name("Page"), "Parent": pagesRef, "Contents": content,
		}))
	}
	w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": kids,
		"Count":    Integer(n),
		"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
	root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})
	b, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLinearizedAgreesWithQpdf(t *testing.T) {
	for _, pages := range []int{1, 2, 7} {
		plain, linear := linearizedFixture(t, pages)

		lin, err := Linearized(linear)
		if err != nil {
			t.Fatalf("%d pages: qpdf linearized it and we say %v", pages, err)
		}
		// Table F.1, each entry against something known independently.
		if lin.FileLength != len(linear) {
			t.Errorf("%d pages: /L %d, file %d", pages, lin.FileLength, len(linear))
		}
		if lin.PageCount != pages {
			t.Errorf("%d pages: /N says %d", pages, lin.PageCount)
		}
		if lin.Version <= 0 {
			t.Errorf("%d pages: /Linearized is %v", pages, lin.Version)
		}
		if lin.HintOffset <= 0 || lin.HintLength <= 0 {
			t.Errorf("%d pages: /H is [%d %d]", pages, lin.HintOffset, lin.HintLength)
		}
		if lin.HintOffset+lin.HintLength > len(linear) {
			t.Errorf("%d pages: /H runs to %d, past the end of a %d-byte file",
				pages, lin.HintOffset+lin.HintLength, len(linear))
		}
		if lin.MainXrefOffset <= 0 || lin.MainXrefOffset >= len(linear) {
			t.Errorf("%d pages: /T is %d in a %d-byte file", pages, lin.MainXrefOffset, len(linear))
		}
		if lin.FirstPageEnd <= 0 || lin.FirstPageEnd > len(linear) {
			t.Errorf("%d pages: /E is %d in a %d-byte file", pages, lin.FirstPageEnd, len(linear))
		}

		// ⛔ /O names the FIRST PAGE's page object, and this is the claim most
		// worth checking against the document rather than against itself: an
		// off-by-one here would send a reader to the wrong object and nothing
		// in the dictionary would contradict it.
		d, err := Open(linear)
		if err != nil {
			t.Fatalf("%d pages: our own reader cannot open qpdf's output: %v", pages, err)
		}
		if got := d.PageCount(); got != pages {
			t.Errorf("%d pages: the document has %d", pages, got)
		}
		ref, ok := d.PageRef(1)
		if !ok {
			t.Fatalf("%d pages: no first page", pages)
		}
		if ref.Num != lin.FirstPageObject {
			t.Errorf("%d pages: /O is %d, the first page is object %d", pages, lin.FirstPageObject, ref.Num)
		}

		// And the plain file is NOT linearized, which is the control: without
		// it, a checker that said yes to everything would pass every line
		// above.
		if _, err := Linearized(plain); !errors.Is(err, ErrNotLinearized) {
			t.Errorf("%d pages: the un-linearized original was accepted (%v)", pages, err)
		}
	}
}

func TestLinearizedRejectsAFileChangedAfterwards(t *testing.T) {
	// ⛔ THE CASE THE FORMAT ITSELF WARNS ABOUT. Table F.1 says a /L mismatch
	// "indicates that the file is not linearized and shall be treated as
	// ordinary PDF", and F.3.6 says any incremental update invalidates the
	// hint tables. Appending anything at all is enough, and that is the
	// ordinary fate of these files — so this must be reported, not ignored.
	// ⛔ The COMMITTED fixture, not a freshly qpdf'd one: this is the only
	// test of the staleness path, and on a runner without qpdf a generated
	// fixture would skip it — which is how `Linearized` sat at 96.0% in CI
	// while reading 100% here.
	linear, err := os.ReadFile(filepath.Join("testdata", "linearized.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Linearized(linear); err != nil {
		t.Fatalf("the fixture is not linearized to begin with: %v", err)
	}
	appended := append(append([]byte{}, linear...), []byte("\n% an incremental update would go here\n")...)
	_, err = Linearized(appended)
	if !errors.Is(err, ErrLinearizationStale) {
		t.Errorf("a file with bytes appended: got %v, want ErrLinearizationStale", err)
	}
	if err != nil && !strings.Contains(err.Error(), "/L says") {
		t.Errorf("the error should name the mismatch: %v", err)
	}
}

// linFile wraps a parameter dictionary body into the smallest file that can
// carry it, padded so that /L is right unless the case under test says
// otherwise. The point is to vary ONE rule of Table F.1 at a time.
func linFile(body string) []byte {
	const header = "%PDF-1.7\n%\xe2\xe3\xcf\xd3\n"
	obj := "1 0 obj\n<< " + body + " >>\nendobj\n"
	return []byte(header + obj)
}

// linWithLength builds the same file with /L set to its own true length.
//
// The field is written to a fixed width so that filling in the number cannot
// change the length it is supposed to state — the obvious version, which
// measures and then substitutes a shorter number, makes /L wrong by exactly
// the digits it saved.
func linWithLength(t *testing.T, rest string) []byte {
	t.Helper()
	return linWithLengthVersion(t, "1", rest)
}

func linWithLengthVersion(t *testing.T, version, rest string) []byte {
	t.Helper()
	const width = 6
	body := func(n string) string { return "/Linearized " + version + " /L " + n + " " + rest }
	n := len(linFile(body(strings.Repeat("0", width))))
	s := strconv.Itoa(n)
	if len(s) > width {
		t.Fatalf("the fixture is %d bytes, too long for a %d-wide /L", n, width)
	}
	return linFile(body(strings.Repeat("0", width-len(s)) + s))
}

// TestLinearizedRejectsAMalformedDictionary walks Table F.1 rule by rule.
//
// ⛔ Each case is a file that is CORRECT except for the one thing named, so a
// failure says what it says. A single blob with several faults would pass this
// test whichever fault the code happened to notice first.
func TestLinearizedRejectsAMalformedDictionary(t *testing.T) {
	good := "/H [ 100 20 ] /O 3 /E 200 /N 1 /T 300"
	for _, tc := range []struct {
		why  string
		body string
	}{
		{"/Linearized is a name, not a number", "/Linearized /yes /L 9 " + good},
		{"/Linearized is an indirect reference", "/Linearized 2 0 R /L 9 " + good},
		{"/L is missing", "/Linearized 1 /H [ 100 20 ] /O 3 /E 200 /N 1 /T 300"},
		{"/L is a real, not an integer", "/Linearized 1 /L 9.5 " + good},
		{"/L is an indirect reference", "/Linearized 1 /L 2 0 R " + good},
		{"/O is missing", "/Linearized 1 /L 9 /H [ 100 20 ] /E 200 /N 1 /T 300"},
		{"/E is missing", "/Linearized 1 /L 9 /H [ 100 20 ] /O 3 /N 1 /T 300"},
		{"/N is missing", "/Linearized 1 /L 9 /H [ 100 20 ] /O 3 /E 200 /T 300"},
		{"/T is missing", "/Linearized 1 /L 9 /H [ 100 20 ] /O 3 /E 200 /N 1"},
		{"/T is a string", "/Linearized 1 /L 9 /H [ 100 20 ] /O 3 /E 200 /N 1 /T (300)"},
		{"/P is not an integer", "/Linearized 1 /L 9 " + good + " /P /first"},
		{"/H is missing", "/Linearized 1 /L 9 /O 3 /E 200 /N 1 /T 300"},
		{"/H is not an array", "/Linearized 1 /L 9 /H 100 /O 3 /E 200 /N 1 /T 300"},
		{"/H has one entry", "/Linearized 1 /L 9 /H [ 100 ] /O 3 /E 200 /N 1 /T 300"},
		{"/H has three entries", "/Linearized 1 /L 9 /H [ 1 2 3 ] /O 3 /E 200 /N 1 /T 300"},
		{"/H holds a name", "/Linearized 1 /L 9 /H [ 100 /x ] /O 3 /E 200 /N 1 /T 300"},
	} {
		_, err := Linearized(linFile(tc.body))
		if !errors.Is(err, ErrLinearizationMalformed) {
			t.Errorf("%s: got %v, want ErrLinearizationMalformed", tc.why, err)
		}
	}

	// Four entries in /H is the other legal shape, and must be ACCEPTED — the
	// control that the arity check is about arity and not about length four.
	b := linWithLength(t, "/H [ 100 20 400 30 ] /O 3 /E 200 /N 1 /T 300")
	lin, err := Linearized(b)
	if err != nil {
		t.Fatalf("a four-entry /H was refused: %v", err)
	}
	if !lin.HasOverflowHint || lin.OverflowHintOffset != 400 || lin.OverflowHintLength != 30 {
		t.Errorf("the overflow hint stream was not read: %+v", lin)
	}
	// ⛔ `/Linearized 1.0` is what Annex F's own Example 2 writes, while qpdf
	// writes `/Linearized 1`. Both are "a number" per Table F.1 and both have
	// to be accepted; this case exists because the first version of these
	// tests used only the integer, and the real branch sat uncovered.
	b = linWithLengthVersion(t, "1.0", "/H [ 100 20 ] /O 3 /E 200 /N 1 /T 300")
	lin, err = Linearized(b)
	if err != nil {
		t.Fatalf("/Linearized 1.0 was refused: %v", err)
	}
	if lin.Version != 1 {
		t.Errorf("/Linearized 1.0 read as %v, want 1", lin.Version)
	}

	// And /P is optional but read when present.
	b = linWithLength(t, "/H [ 100 20 ] /O 3 /E 200 /N 1 /T 300 /P 7")
	if lin, err := Linearized(b); err != nil {
		t.Errorf("/P 7 was refused: %v", err)
	} else if lin.FirstPageNumber != 7 {
		t.Errorf("/P read as %d, want 7", lin.FirstPageNumber)
	}
}

func TestLinearizedRejectsADictionaryPastTheWindow(t *testing.T) {
	// ⛔ F.3.3 puts the dictionary "entirely contained within the first 1024
	// bytes", and the reason is the point of the whole format: a reader must
	// not have to fetch more than that to decide whether the file is
	// linearized. A dictionary padded past the window is well formed and
	// still unusable.
	pad := strings.Repeat(" ", 1100)
	b := linFile("/Linearized 1 /L 9 /H [ 100 20 ] /O 3 /E 200 /N 1 /T 300" + pad)
	_, err := Linearized(b)
	if !errors.Is(err, ErrLinearizationMalformed) {
		t.Fatalf("got %v, want ErrLinearizationMalformed", err)
	}
	if !strings.Contains(err.Error(), "window") {
		t.Errorf("the error should say the window is the cause: %v", err)
	}
}

func TestLinearizedRejectsWhatIsNotLinearized(t *testing.T) {
	for _, tc := range []struct {
		why  string
		in   []byte
		want error
	}{
		{"empty", nil, ErrNotLinearized},
		{"not a PDF at all", []byte("hello"), ErrNotLinearized},
		{"a header and nothing else", []byte("%PDF-1.7\n"), ErrNotLinearized},
		{"a first object that does not parse", []byte("%PDF-1.7\n1 0 obj\n<< /L"), ErrNotLinearized},
		{"a first object that is not a dictionary", []byte("%PDF-1.7\n1 0 obj\n42\nendobj\n"), ErrNotLinearized},
		{"a dictionary without /Linearized", []byte("%PDF-1.7\n1 0 obj\n<< /L 10 >>\nendobj\n"), ErrNotLinearized},
	} {
		if _, err := Linearized(tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.why, err, tc.want)
		}
	}
}

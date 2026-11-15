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
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ⛔⛔ THE PARTITION HAS A FOREIGN JUDGE, WHICH IS WHY IT IS A STEP OF ITS OWN.
// `qpdf --show-linearization` prints its own "Computed Linearization Part
// Assignments" — the reference implementation's answer to exactly the question
// Partition asks — so step 1 of go-pdfkit/reader#40 can be measured before a
// single byte of a linearized file is written.
//
// ⛔ Without this the partition would be judged only by the file it produces,
// and a wrong placement does not make a reader fail: it makes the reader fetch
// the wrong bytes and carry on. That is the failure this issue was opened to
// prevent, and the only cheap defence is a second opinion that was not written
// to agree with mine.
func TestPartitionAgreesWithQpdfAcrossPageCountsAndShapes(t *testing.T) {
	qpdf := qpdfOrSkip(t)

	for _, shape := range []struct {
		name  string
		extra []string
	}{
		{"as qpdf writes it", nil},
		{"with object streams", []string{"--object-streams=generate"}},
		{"with streams uncompressed", []string{"--compress-streams=n"}},
	} {
		for _, pages := range []int{1, 2, 5, 25} {
			t.Run(shape.name+", "+strconv.Itoa(pages)+" pages", func(t *testing.T) {
				qpdfExtra = shape.extra
				defer func() { qpdfExtra = nil }()
				_, linear := linearizedFixture(t, pages)

				d, err := Open(linear)
				if err != nil {
					t.Fatalf("this package cannot open qpdf's linearized output: %v", err)
				}
				got, err := d.Partition()
				if err != nil {
					t.Fatalf("Partition refused a file qpdf linearized: %v", err)
				}

				path := filepath.Join(t.TempDir(), "linear.pdf")
				if err := os.WriteFile(path, linear, 0o644); err != nil {
					t.Fatal(err)
				}
				want := qpdfParts(t, d, qpdf, path)

				comparePartitions(t, got.Of, want)
			})
		}
	}
}

// comparePartitions reports every object the two judges place differently,
// and every object only one of them places at all.
//
// ⛔ THE TWO DIRECTIONS ARE REPORTED SEPARATELY, because they are different
// defects. An object this package places and qpdf does not is a layout that
// would write something the reference implementation leaves out; an object
// qpdf places and this does not is an object a layout would DROP, which is
// the one that loses a page.
func comparePartitions(t *testing.T, got, want map[int]Part) {
	t.Helper()

	var differ, onlyGot, onlyWant []int
	for num, w := range want {
		g, ok := got[num]
		if !ok {
			onlyWant = append(onlyWant, num)
			continue
		}
		if g != w {
			differ = append(differ, num)
		}
	}
	for num := range got {
		if _, ok := want[num]; !ok {
			onlyGot = append(onlyGot, num)
		}
	}
	sort.Ints(differ)
	sort.Ints(onlyGot)
	sort.Ints(onlyWant)

	for _, num := range differ {
		t.Errorf("object %d: this package says %v, qpdf says %v", num, got[num], want[num])
	}
	for _, num := range onlyWant {
		t.Errorf("object %d: qpdf places it in %v and this package places it nowhere, "+
			"so a layout built on this would DROP it", num, want[num])
	}
	for _, num := range onlyGot {
		t.Errorf("object %d: this package places it in %v and qpdf places it nowhere",
			num, got[num])
	}

	// ⛔ AND BOTH PARTITIONS IN FULL WHEN THEY DISAGREE. A mismatch named one
	// object at a time cannot show the SHAPE of the disagreement, and the
	// shape is what identifies the cause: the first run of this test reported
	// "qpdf says 1, we say 3" on four files and the useful fact — that the
	// difference was the same two numbers apart in every one of them — was
	// invisible until both sides were printed side by side.
	if t.Failed() {
		t.Logf("this package:\n%s", formatPartition(got))
		t.Logf("qpdf:\n%s", formatPartition(want))
	}
}

// formatPartition prints a partition grouped by part, in object order.
func formatPartition(of map[int]Part) string {
	byPart := map[Part][]int{}
	for num, p := range of {
		byPart[p] = append(byPart[p], num)
	}
	var b strings.Builder
	for _, p := range []Part{PartCatalog, PartFirstPage, PartOtherPrivate, PartShared, PartOther} {
		nums := byPart[p]
		if len(nums) == 0 {
			continue
		}
		sort.Ints(nums)
		fmt.Fprintf(&b, "  %v: %v\n", p, nums)
	}
	return b.String()
}

// qpdfParts runs `qpdf --show-linearization` and reads its computed part
// assignments, translated into the objects a DOCUMENT has.
//
// ⛔⛔ THE TWO JUDGES COUNT DIFFERENT THINGS, WHICH COST FOUR FAILING SHAPES TO
// FIND AND IS NOT A DEFECT IN EITHER. qpdf names the objects it will WRITE;
// Partition names the objects the document HAS. In a file with object streams
// those differ: for the two-page `--object-streams=generate` fixture, qpdf puts
// object 3 in part 9 and object 3 is an /ObjStm whose single member is object
// 5 — the page tree node, which is what F.3.10 actually places ("The page
// tree"). Partition answered 5. Both are right about their own question.
//
// ⛔ SO THE JUDGE IS TRANSLATED, NOT THE SUBJECT. Expanding qpdf's containers
// into their members compares like with like; "correcting" Partition to name
// containers instead would have made it answer a question Annex F does not
// ask, and would have been wrong the moment a layout chose different
// containers. An object stream is a packing decision that comes AFTER the
// partition.
//
// ⛔ IT ASSERTS IT FOUND THE SECTION, because an absent section and an empty
// one are indistinguishable in a map, and a comparison against nothing reads
// as agreement. This is the shape "a scan that could not read reported zero"
// is named for.
func qpdfParts(t *testing.T, d *Document, qpdf, path string) map[int]Part {
	t.Helper()

	cmd := exec.Command(qpdf, "--show-linearization", path)
	cmd.Stdin = nil
	// The status is not the verdict here: qpdf exits 3 when it warns about
	// hint tables and still prints the assignments this test reads.
	out, _ := cmd.CombinedOutput()
	text := string(out)

	const header = "Computed Linearization Part Assignments"
	at := strings.Index(text, header)
	if at < 0 {
		// ⛔⛔ THE VERSION IS IN THE MESSAGE BECAUSE THAT IS WHAT THE ANSWER WAS.
		// This fired on CI and not locally, and the cause was the judge's
		// version: qpdf prints this section only from 12 onwards, and ubuntu
		// ships 11.9.0. Diagnosing it meant reading a CI log for a version
		// the failure could have named itself.
		ver, _ := exec.Command(qpdf, "--version").Output()
		t.Fatalf("qpdf printed no %q section, so there is nothing to compare against — "+
			"this section needs qpdf 12 or newer, and this one says %q:\n%s",
			header, strings.TrimSpace(string(ver)), text)
	}

	// qpdf names the parts in its own words; the numbers are F.3.1's and are
	// what the two sides agree on.
	parts := map[string]Part{
		"part 4": PartCatalog,
		"part 6": PartFirstPage,
		"part 7": PartOtherPrivate,
		"part 8": PartShared,
		"part 9": PartOther,
	}

	of := map[int]Part{}
	seen := map[Part]bool{}
	var current Part
	var have bool

	for _, line := range strings.Split(text[at+len(header):], "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "part ") {
			head := trimmed
			if i := strings.Index(head, " ("); i >= 0 {
				head = head[:i]
			}
			p, ok := parts[head]
			if !ok {
				t.Fatalf("qpdf named a part this test does not know: %q", trimmed)
			}
			current, have = p, true
			seen[p] = true
			// "part 8 (other pages, shared): none" carries its emptiness on
			// the same line.
			if strings.HasSuffix(trimmed, "none") {
				have = false
			}
			continue
		}
		if !have {
			continue
		}
		for _, num := range parseObjectList(t, trimmed) {
			of[num] = current
		}
	}

	// ⛔ Parts 4, 6, 7 and 9 are named by qpdf for every file in this corpus;
	// part 8 is "none" throughout, which is a fact about the corpus and not a
	// parse failure. Asserting the other four were SEEN is what stops a
	// changed output format from reading as a clean comparison.
	for _, p := range []Part{PartCatalog, PartFirstPage, PartOtherPrivate, PartOther} {
		if !seen[p] {
			t.Fatalf("qpdf's output named no %v, so its format has changed and this "+
				"comparison is not measuring what it claims:\n%s", p, text)
		}
	}
	if len(of) == 0 {
		t.Fatalf("qpdf named parts but no objects, so the comparison would be vacuous:\n%s", text)
	}
	return expandObjectStreams(t, d, of)
}

// expandObjectStreams replaces each /ObjStm qpdf named with the objects it
// holds, so that both sides of the comparison speak about document objects.
//
// ⛔ An object stream whose members cannot be read is left in place rather
// than dropped: dropping it would shrink the judge's answer and make the
// comparison easier to pass, which is the wrong direction for a mistake.
func expandObjectStreams(t *testing.T, d *Document, of map[int]Part) map[int]Part {
	t.Helper()
	out := map[int]Part{}
	expanded := 0
	for num, p := range of {
		o, err := d.Get(Ref{Num: num})
		if err != nil {
			out[num] = p
			continue
		}
		s, ok := o.(*Stream)
		if !ok {
			out[num] = p
			continue
		}
		if name, _ := ToName(s.Dict.Get("Type")); name != "ObjStm" {
			out[num] = p
			continue
		}
		members, err := d.objectStream(num)
		if err != nil || len(members) == 0 {
			out[num] = p
			continue
		}
		for member := range members {
			out[member] = p
		}
		expanded++
	}
	if expanded > 0 {
		t.Logf("expanded %d object stream(s) qpdf named into their members, so that both "+
			"sides compare document objects", expanded)
	}
	return out
}

// parseObjectList reads qpdf's " 5, 4, 6" and " 9-11" forms.
func parseObjectList(t *testing.T, s string) []int {
	t.Helper()
	var out []int
	for _, field := range strings.Split(s, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(field, "-")
		first, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			t.Fatalf("qpdf listed an object this test cannot read: %q in %q", field, s)
		}
		if !isRange {
			out = append(out, first)
			continue
		}
		last, err := strconv.Atoi(strings.TrimSpace(hi))
		if err != nil {
			t.Fatalf("qpdf listed a range this test cannot read: %q in %q", field, s)
		}
		if last < first {
			t.Fatalf("qpdf listed a backwards range: %q", field)
		}
		for n := first; n <= last; n++ {
			out = append(out, n)
		}
	}
	return out
}

// ⛔ AND THE PARSER IS TESTED ON ITS OWN, because it is the instrument: a
// parser that silently read nothing would make every comparison above pass.
// The forms are the ones qpdf actually printed for this corpus.
func TestTheQpdfPartParserReadsBothOfItsForms(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want []int
	}{
		{"a comma list", " 5, 4, 6", []int{5, 4, 6}},
		{"a range", " 9-11", []int{9, 10, 11}},
		{"one object", " 8", []int{8}},
		{"a range and a list", " 1-2, 7", []int{1, 2, 7}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := parseObjectList(t, c.in)
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("parseObjectList(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

// ⛔⛔ EVERY WAY buildHintTables REFUSES, reached directly because the emission
// cannot produce any of them: it gives every page its own object, measures from
// a buffer that only grows, and writes the hint stream before the first page so
// its length is always under that page's offset.
//
// ⛔ The refusals stay anyway. A later caller — the one that will supply shared
// references for #61 — has none of those guarantees, and a propagation no test
// reaches is a claim nothing can falsify.
func TestEveryWayTheHintTablesRefuse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pages []pageExtent
		first int
		hint  int
		want  string
	}{
		{
			name:  "no pages at all",
			pages: nil,
			want:  "a hint table for no pages",
		},
		{
			name:  "a page with no objects",
			pages: []pageExtent{{objects: 0, start: 10, end: 20, length: 10}},
			want:  "least its own page object",
		},
		{
			name:  "an extent that runs backwards",
			pages: []pageExtent{{objects: 1, start: 90, end: 20, length: 10}},
			want:  "ends at 20 and starts at 90",
		},
		{
			name:  "a negative length",
			pages: []pageExtent{{objects: 1, start: 10, end: 20, length: -1}},
			want:  "sum to -1 bytes",
		},
		{
			name: "a content stream before the page",
			pages: []pageExtent{{objects: 1, start: 50, end: 90, length: 40,
				hasContent: true, contentStart: 10, contentEnd: 60}},
			want: "not inside it",
		},
		{
			name: "a content stream past the page",
			pages: []pageExtent{{objects: 1, start: 50, end: 90, length: 40,
				hasContent: true, contentStart: 60, contentEnd: 200}},
			want: "not inside it",
		},
		{
			name: "a content stream that runs backwards",
			pages: []pageExtent{{objects: 1, start: 50, end: 90, length: 40,
				hasContent: true, contentStart: 80, contentEnd: 60}},
			want: "not inside it",
		},
		{
			// ⛔ Item 2 is stored as if the hint stream were not in the file, so
			// a first page below the stream's length would store a negative.
			name:  "a first page above the hint stream's length",
			pages: []pageExtent{{objects: 1, start: 10, end: 20, length: 10}},
			first: 5,
			hint:  100,
			want:  "item 2 would go negative",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := buildHintTables(tc.pages, tc.first, tc.hint)
			if !errors.Is(err, errHintTable) {
				t.Fatalf("it was accepted: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refused, but not for %q: %v", tc.want, err)
			}
		})
	}
}

// ⛔⛔⛔ THE CASE THE WHOLE JUDGED CORPUS COULD NOT EXPRESS, which is why qpdf
// was green about a hint table it would have refused. buildPages gives every
// page its own content stream, so no object is reached by two pages, part 6
// holds nothing a later page needs, and the shared object hint table has
// nothing to say. Every shape the suite builds is that shape.
//
// ⛔ Measured on a document whose two pages share ONE content stream:
//
//	WARNING: page 1: shared object 3: in computed list but not hint table
//
// from `qpdf --check`, while our own CheckLinearized accepted the file. So
// nothing in this package would have caught it, and the foreign judge was silent
// only because no fixture could reach the case.
//
// ⚠ The writer refuses such a document rather than describing it, and that is
// #61's to fix. This test is the witness that it refuses, and the fixture is
// committed so the corpus can express the case from now on.
func TestADocumentWhoseLaterPagesShareAnObjectIsRefused(t *testing.T) {
	plain := sharedContentsFixture(t)
	d, err := Open(plain)
	if err != nil {
		t.Fatal(err)
	}

	// ⛔ THE POSITIVE HALF FIRST: the partition must SEE the sharing, or the
	// refusal below would be passing for the wrong reason.
	p, err := d.Partition()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Shared) < 2 {
		t.Fatalf("the partition reports %d page(s) of shared objects", len(p.Shared))
	}
	if len(p.Shared[0]) != 0 {
		t.Errorf("page 1 is credited with shared objects %v, and Table F.4 item 3 "+
			"requires 0 there", p.Shared[0])
	}
	if len(p.Shared[1]) == 0 {
		t.Fatal("page 2 reaches page 1's content stream and the partition reports " +
			"no shared object, so this fixture witnesses nothing")
	}

	_, err = d.Linearize()
	if !errors.Is(err, ErrCannotLinearize) {
		t.Fatalf("a document this writer cannot describe was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "shares object") {
		t.Errorf("refused, but not for the shared object: %v", err)
	}
}

// ⛔ AND THE NEGATIVE CONTROL: the documents the suite does judge must have
// nothing shared, or the refusal above would reject them too and the whole
// corpus would be unreachable.
//
// ⭐ "A test suite in which every input passes measures nothing" — and one in
// which every input is refused measures nothing either. This is the line
// between the two.
func TestTheJudgedCorpusSharesNothingBetweenPages(t *testing.T) {
	for _, pages := range []int{1, 2, 5, 25} {
		t.Run(fmt.Sprintf("%d pages", pages), func(t *testing.T) {
			d, err := Open(buildPages(t, pages))
			if err != nil {
				t.Fatal(err)
			}
			p, err := d.Partition()
			if err != nil {
				t.Fatal(err)
			}
			for i, s := range p.Shared {
				if len(s) != 0 {
					t.Errorf("page %d shares %v, so buildPages is no longer the "+
						"independent-pages fixture this corpus assumes", i+1, s)
				}
			}
		})
	}
}

// sharedContentsFixture writes a two-page document whose pages share ONE content
// stream, so that page 2 reaches an object living in part 6.
func sharedContentsFixture(t *testing.T) []byte {
	t.Helper()
	w := NewWriter("1.7")
	pagesRef := w.Reserve()
	shared := w.Add(&Stream{Dict: Dict{}, Raw: []byte("BT /F1 12 Tf 72 720 Td (both) Tj ET")})
	one := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef,
		"Contents": shared, "Resources": Dict{}})
	two := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef,
		"Contents": shared, "Resources": Dict{}})
	w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": Array{one, two},
		"Count":    Integer(2),
		"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
	root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})

	plain, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

// ⛔⛔ A VALUE TOO WIDE FOR ITS 32-BIT FIELD IS REFUSED, NOT TRUNCATED. No
// fixture can reach this — the suite does not build a 4 GiB document — and that
// is precisely the reason: an untestable truncation in the one structure whose
// job is to be trusted would send a reader to a wrapped offset.
//
// ⛔ This package has named that class three times (#42, #53, #58) and then did
// it anyway, which is why the refusal is tested by hand here.
func TestTheHintTablesRefuseAValueTooWideForItsField(t *testing.T) {
	// ⛔⛔ NOT A CONSTANT, AND NOT ON A 32-BIT BUILD. `int(math.MaxUint32) + 1`
	// is a constant expression, so it does not COMPILE where int is 32 bits —
	// caught by `go vet` on linux/386, which is why this repository vets three
	// architectures. A runtime conversion compiles everywhere, and on a 32-bit
	// build the value cannot be represented at all, so there is nothing to test.
	if math.MaxInt <= math.MaxUint32 {
		t.Skip("int is 32 bits here, so a value above MaxUint32 cannot be built — " +
			"the refusal is for 64-bit builds, where it is reachable")
	}
	var limit int64 = math.MaxUint32
	tooBig := int(limit) + 1

	for _, tc := range []struct {
		name  string
		pages []pageExtent
		first int
		want  string
	}{
		{
			name:  "a page longer than 4 GiB",
			pages: []pageExtent{{objects: 1, start: 0, end: tooBig, length: tooBig}},
			first: 10,
			want:  "length in bytes",
		},
		{
			name:  "a page of more than 4 Gi objects",
			pages: []pageExtent{{objects: tooBig, start: 0, end: 10, length: 10}},
			first: 10,
			want:  "object count",
		},
		{
			name: "a content stream past 4 GiB",
			pages: []pageExtent{{objects: 1, start: 0, end: tooBig + 2, length: 10,
				hasContent: true, contentStart: 1, contentEnd: tooBig + 1}},
			first: 10,
			want:  "content stream",
		},
		{
			name:  "a first page past 4 GiB",
			pages: []pageExtent{{objects: 1, start: 0, end: 10, length: 10}},
			first: tooBig + 1,
			want:  "item 2 would be",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := buildHintTables(tc.pages, tc.first, 1)
			if !errors.Is(err, errHintTable) {
				t.Fatalf("it was truncated instead of refused: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refused, but not for %q: %v", tc.want, err)
			}
		})
	}
}

// ⛔⛔ widthFor TERMINATES ON A 32-BIT DELTA, which it did not: `1<<n` takes the
// delta's type, so at n == 32 a uint32 shift gives 0, the comparison stays true
// and the loop spins forever. Measured on 2^31 before the bound was added.
//
// ⛔ No caller reaches it today, because the three byte-measured widths are
// pinned and the derived ones are counts. It is the function a derived byte
// width would go through — exactly when a 32-bit delta turns up.
func TestWidthForTerminatesAtThirtyTwoBits(t *testing.T) {
	of := func(e pageEntry) uint32 { return e.Length }
	for _, delta := range []uint32{1 << 30, 1 << 31, math.MaxUint32} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			done := make(chan int, 1)
			go func() {
				done <- widthFor([]pageEntry{{Length: 0}, {Length: delta}}, of, 0)
			}()
			select {
			case got := <-done:
				if got < 1 || got > 32 {
					t.Errorf("widthFor = %d, and F.4.1 bounds every width to 0..32", got)
				}
			case <-timeAfterSeconds(5):
				t.Fatalf("widthFor did not return for a delta of %d", delta)
			}
		})
	}
}

// ⛔ THE TWO ENCODER ERRORS, THROUGH THEIR SEAMS. buildHintTables constructs
// inputs neither encoder can reject, so these are the propagations and not the
// encoders — the encoders have their own tests, judged against qpdf's bytes.
func TestTheHintTablesPropagateAnEncoderRefusal(t *testing.T) {
	pages := []pageExtent{{objects: 2, start: 10, end: 50, length: 40}}
	boom := fmt.Errorf("%w: a refusal planted by the test", errHintTable)

	t.Run("the page offset table", func(t *testing.T) {
		saved := writePageOffset
		defer func() { writePageOffset = saved }()
		writePageOffset = func(*pageOffsetHeader, []pageEntry) ([]byte, error) {
			return nil, boom
		}
		if _, _, err := buildHintTables(pages, 100, 10); !errors.Is(err, boom) {
			t.Fatalf("the page offset table's refusal did not reach the caller: %v", err)
		}
	})

	t.Run("the shared table", func(t *testing.T) {
		saved := writeShared
		defer func() { writeShared = saved }()
		writeShared = func(*sharedHeader, []sharedGroup) ([]byte, error) {
			return nil, boom
		}
		if _, _, err := buildHintTables(pages, 100, 10); !errors.Is(err, boom) {
			t.Fatalf("the shared table's refusal did not reach the caller: %v", err)
		}
	})
}

// ⛔ AND THE EMISSION PROPAGATES ITS OWN, through the buildHint seam, at BOTH
// call sites: once before part 6 to learn the stream's length, once after the
// file exists to build the tables the next pass writes. A test of one would
// leave the other free to swallow the error.
func TestTheEmissionPropagatesAHintTableRefusal(t *testing.T) {
	boom := fmt.Errorf("%w: a refusal planted by the test", errHintTable)

	for _, tc := range []struct {
		name  string
		after int // let this many calls succeed before failing
	}{
		{"the call that sizes the stream", 0},
		{"the call that builds the next pass's tables", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := buildHint
			defer func() { buildHint = saved }()
			calls := 0
			buildHint = func(p []pageExtent, first, hint int) ([]byte, int, error) {
				if calls >= tc.after {
					calls++
					return nil, 0, boom
				}
				calls++
				return saved(p, first, hint)
			}

			d, err := Open(buildPages(t, 1))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Linearize(); !errors.Is(err, boom) {
				t.Fatalf("the refusal did not reach Linearize: %v", err)
			}
			if calls != tc.after+1 {
				t.Errorf("the seam ran %d time(s) and the case wanted %d",
					calls, tc.after+1)
			}
		})
	}
}

// ⛔⛔ A MINIMUM AND A WIDTH THAT COME FROM A LATER PAGE, which every fixture in
// this package fails to exercise: buildPages makes pages of equal size, so the
// first page is already the smallest and the largest and `v < least` never
// fires. A table built from uniform pages cannot tell leastOf from "return the
// first entry".
//
// ⭐ The expectations are computed here by hand rather than from the code: with
// lengths 40, 10 and 70 the least is 10 and the widest delta is 60, which needs
// six bits because 2^5 is 32 and 2^6 is 64.
func TestTheLeastAndTheWidthComeFromWhicheverPageHoldsThem(t *testing.T) {
	entries := []pageEntry{{Length: 40}, {Length: 10}, {Length: 70}}
	length := func(e pageEntry) uint32 { return e.Length }

	if got := leastOf(entries, length); got != 10 {
		t.Errorf("leastOf = %d, want 10 — it did not look past the first entry", got)
	}
	if got := widthFor(entries, length, 10); got != 6 {
		t.Errorf("widthFor = %d, want 6 bits for a delta of 60", got)
	}

	// ⛔ AND NEVER ZERO, for a column that does not vary: F.4.1 allows a width
	// of 0 and bitw.write(0, 0) is a path nothing here has exercised.
	flat := []pageEntry{{Length: 7}, {Length: 7}}
	if got := widthFor(flat, length, 7); got != 1 {
		t.Errorf("widthFor on a constant column = %d, want 1", got)
	}
}

// ⛔⛔ A PAGE WHOSE /Contents IS AN ARRAY, which is a legal shape no fixture in
// this package had: Table F.4's items 6 and 7 name the content stream, and a
// page may hold several. The extent has to span all of them.
//
// ⛔ And this is not only about coverage. A writer that handled only the single
// Ref would report hasContent false for such a page and send a streaming reader
// to fetch nothing — silently, because qpdf checks neither item.
func TestAPageWithSeveralContentStreams(t *testing.T) {
	w := NewWriter("1.7")
	pagesRef := w.Reserve()
	first := w.Add(&Stream{Dict: Dict{}, Raw: []byte("0 g")})
	second := w.Add(&Stream{Dict: Dict{}, Raw: []byte("1 0 0 RG 10 w")})
	page := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef,
		"Contents": Array{first, second}, "Resources": Dict{}})
	w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": Array{page},
		"Count":    Integer(1),
		"MediaBox": Array{Integer(0), Integer(0), Integer(612), Integer(792)}})
	root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})

	plain, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(plain)
	if err != nil {
		t.Fatal(err)
	}

	p, err := d.Partition()
	if err != nil {
		t.Fatal(err)
	}
	if got := d.contentNumbers(p, 0); len(got) != 2 {
		t.Fatalf("contentNumbers found %v, want both content streams", got)
	}

	out, err := d.Linearize()
	if err != nil {
		t.Fatalf("a page with two content streams was refused: %v", err)
	}
	if _, err := CheckLinearized(out); err != nil {
		t.Fatalf("our own checker refuses it: %v", err)
	}
}

// ⛔ EVERY WAY contentNumbers FINDS NOTHING, which the emission cannot reach
// because Partition always supplies as many page objects as page sets.
func TestContentNumbersFindsNothingToReport(t *testing.T) {
	t.Run("a page index with no page object", func(t *testing.T) {
		d := bareDoc(nil, nil, nil)
		p := &Partition{Pages: [][]int{{1}}, PageObjects: nil}
		if got := d.contentNumbers(p, 0); got != nil {
			t.Errorf("it reported %v for a page it cannot identify", got)
		}
	})

	t.Run("a page object that does not read", func(t *testing.T) {
		d := bareDoc(nil, nil, nil)
		d.buf = []byte("%PDF-1.7\n")
		d.xref[1] = xrefEntry{kind: 'n', offset: 100000}
		p := &Partition{Pages: [][]int{{1}}, PageObjects: []int{1}}
		if got := d.contentNumbers(p, 0); got != nil {
			t.Errorf("it reported %v for a page that does not read", got)
		}
	})

	t.Run("a page object that is not a dictionary", func(t *testing.T) {
		d := bareDoc(nil, nil, map[int]Object{1: Integer(7)})
		p := &Partition{Pages: [][]int{{1}}, PageObjects: []int{1}}
		if got := d.contentNumbers(p, 0); got != nil {
			t.Errorf("it reported %v for a page that is not a dictionary", got)
		}
	})

	t.Run("a /Contents the page's own set does not hold", func(t *testing.T) {
		// ⛔ THE FILTER IS NOT A FORMALITY: an extent taken from an offset
		// outside the page would make items 6 and 7 describe bytes that are
		// not the page's.
		d := bareDoc(nil, nil, map[int]Object{
			1: Dict{"Type": Name("Page"), "Contents": Ref{Num: 99}},
		})
		p := &Partition{Pages: [][]int{{1}}, PageObjects: []int{1}}
		if got := d.contentNumbers(p, 0); got != nil {
			t.Errorf("it reported %v for a stream the page does not own", got)
		}
	})

	t.Run("an /Contents array of things that are not references", func(t *testing.T) {
		d := bareDoc(nil, nil, map[int]Object{
			1: Dict{"Type": Name("Page"), "Contents": Array{Integer(1), Name("x")}},
		})
		p := &Partition{Pages: [][]int{{1}}, PageObjects: []int{1}}
		if got := d.contentNumbers(p, 0); got != nil {
			t.Errorf("it reported %v for an array holding no references", got)
		}
	})
}

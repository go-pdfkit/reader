// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ⛔⛔⛔ THE PREMISE OF THE WHOLE FIXPOINT, PINNED BY A FOREIGN PRODUCER. The
// reserved-span technique only works because the span does not move when the
// numbers change width. qpdf's two classic fixtures say it does not: the same
// 201 bytes from the start of the parameter object to the `xref` keyword, while
// the object itself is 82 bytes in one and 83 in the other.
//
// ⛔ If a qpdf upgrade began padding inside the dictionary instead, or stopped
// padding at all, this test is where that shows — and it would mean the
// mechanism in linwrite.go was modelled on behaviour that had changed.
func TestQpdfReservesAConstantSpanForTheParameters(t *testing.T) {
	objStart := regexp.MustCompile(`(?m)^\d+ 0 obj\n`)

	type measured struct{ span, object int }
	got := map[string]measured{}
	for _, name := range []string{"linearized.pdf", "linearized-shared.pdf"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		loc := objStart.FindIndex(b)
		if loc == nil {
			t.Fatalf("%s has no leading indirect object", name)
		}
		start := loc[0]

		// ⛔ THE `\nxref\n` SECTION AND NOT A BARE `xref`. A file with an xref
		// STREAM has no such section at all, and searching for the four
		// letters anywhere measures a span that does not exist —
		// testdata/linearized-objstm.pdf did exactly that to the probe this
		// test grew from, reporting a 1499-byte gap.
		//
		// ⛔⛔ AND THE REASON I FIRST WROTE HERE WAS FABRICATED. It said the
		// probe had matched "the letters inside an object stream's compressed
		// data", which sounded right because the gap printed as binary.
		// Measured: that file holds ZERO occurrences of "\nxref\n" and exactly
		// one "xref", at byte 1597 of 1612, inside the trailing `startxref`.
		// The gap was binary because object streams lay between, not because
		// the match was in them. The observation was real and the mechanism I
		// inferred from it was invented.
		rel := bytes.Index(b[start:], []byte("\nxref\n"))
		if rel < 0 {
			t.Fatalf("%s has no xref keyword after its first object", name)
		}
		end := bytes.Index(b[start:], []byte("endobj"))
		if end < 0 {
			t.Fatalf("%s has no endobj in its first object", name)
		}
		got[name] = measured{span: rel + 1, object: end + len("endobj")}
	}

	a, b := got["linearized.pdf"], got["linearized-shared.pdf"]
	if a.span != b.span {
		t.Errorf("qpdf reserved %d bytes in one file and %d in the other, so the span is not "+
			"constant and the mechanism in linwrite.go is modelled on nothing: %+v",
			a.span, b.span, got)
	}
	// ⛔ AND THE OBJECTS MUST DIFFER, or the constant span proves nothing: two
	// identical objects would of course be followed by identical padding, and
	// the test would pass against a producer that padded inside the dictionary.
	if a.object == b.object {
		t.Errorf("both parameter objects are %d bytes, so this pair cannot show that the span "+
			"absorbs a width change: %+v", a.object, got)
	}
	// ⛔⛔ AND THE SPAN IS 201 EXACTLY, which qpdf's source says it must be:
	// libqpdf/QPDFWriter.cc's writeLinearized() holds `static int const pad =
	// 200` and writes `pad` less the object's bytes as spaces, then a newline.
	// Pinning the number and not just its constancy is what makes this test a
	// witness to the MECHANISM rather than to a coincidence of two documents.
	if a.span != 201 {
		t.Errorf("qpdf reserved %d bytes and its source reserves 200 spaces plus a newline; "+
			"either the constant moved or this measurement is wrong", a.span)
	}
	t.Logf("qpdf: span %d, objects %d and %d, padding %d and %d",
		a.span, a.object, b.object, a.span-a.object, b.span-b.object)
}

// ⛔⛔ AND THE TWO PRODUCERS DISAGREE ABOUT THE SPAN, which is the third thing
// the pair above needs and did not assert. Two files both saying 201 invite the
// next reader to take 201 as normative — and then paramSpan's 256 looks
// arbitrary rather than chosen. Ghostscript reserves 131 for the same part, so
// the span is each producer's decision and this package is entitled to its own.
func TestTheTwoProducersReserveDifferentSpans(t *testing.T) {
	objStart := regexp.MustCompile(`(?m)^\d+ 0 obj\n`)

	span := func(name string) int {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		loc := objStart.FindIndex(b)
		if loc == nil {
			t.Fatalf("%s has no leading indirect object", name)
		}
		rel := bytes.Index(b[loc[0]:], []byte("\nxref\n"))
		if rel < 0 {
			t.Fatalf("%s has no xref section after its first object", name)
		}
		return rel + 1
	}

	qpdf, gs := span("linearized.pdf"), span("linearized-gs.pdf")
	if qpdf == gs {
		t.Fatalf("both producers reserve %d bytes, so the span looks like a property of the "+
			"FORMAT rather than a producer's choice — and this package's 256 would need a "+
			"different justification", qpdf)
	}
	t.Logf("qpdf reserves %d, Ghostscript %d: the span is a producer's choice", qpdf, gs)
}

// ⛔⛔ AND THE SPAN THIS PACKAGE WRITES DOES NOT MOVE WITH THE NUMBERS, which is
// the one property every offset after part 2 depends on.
func TestTheParameterObjectIsAlwaysTheReservedWidth(t *testing.T) {
	for _, c := range []struct {
		name string
		p    linParams
	}{
		{"one-digit values", linParams{L: 1, H0: 2, H1: 3, O: 4, E: 5, N: 6, T: 7}},
		{"a realistic file", linParams{L: 1367, H0: 550, H1: 127, O: 7, E: 902, N: 2, T: 1169}},
		{"values near the top of int64", linParams{
			L: 9223372036854775807, H0: 9223372036854775807, H1: 9223372036854775807,
			O: 9223372036854775807, E: 9223372036854775807, N: 9223372036854775807,
			T: 9223372036854775807}},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := c.p.object(4)
			if err != nil {
				t.Fatalf("it was refused: %v", err)
			}
			if len(out) != paramSpan {
				t.Fatalf("the object is %d bytes and the span is %d — every offset after part "+
					"2 would be wrong by the difference", len(out), paramSpan)
			}
			// ⛔ The padding is SPACES, which is whitespace to a PDF lexer. A
			// NUL is not, and a parser would stop at it.
			if i := bytes.IndexByte(out, 0); i >= 0 {
				t.Errorf("the padding holds a NUL at byte %d, which is not whitespace in ISO "+
					"32000-1's lexical rules", i)
			}
			if !strings.HasSuffix(string(bytes.TrimRight(out, " ")), "endobj\n") {
				t.Errorf("the object does not end in endobj before its padding: %q", out)
			}
		})
	}

	// ⛔⛔ THE WIDEST CASE ABOVE IS THE ONE THAT MATTERS, and this asserts the
	// reason 256 was chosen rather than copying qpdf's 201: seven int64 values
	// at nineteen digits do not fit 201. If they did, this package would be
	// carrying a larger span for no reason; if 256 did not fit them, the
	// refusal would be reachable from a real file.
	widest := linParams{
		L: 9223372036854775807, H0: 9223372036854775807, H1: 9223372036854775807,
		O: 9223372036854775807, E: 9223372036854775807, N: 9223372036854775807,
		T: 9223372036854775807}
	out, err := widest.object(999999)
	if err != nil {
		t.Fatalf("the widest possible parameters do not fit the span, so a real file could "+
			"reach the refusal: %v", err)
	}
	used := len(bytes.TrimRight(out, " "))
	if used <= 201 {
		t.Errorf("the widest parameters need %d bytes, which fits qpdf's 201 — then 256 is "+
			"unjustified and the comment explaining it is wrong", used)
	}
	// ⛔ THE EXACT FIGURE, because the comment in linwrite.go now cites it: 202,
	// which clears qpdf's 201 by ONE byte. A comment that cites a number has to
	// have the number asserted somewhere or it is the next stale claim.
	if used != 202 {
		t.Errorf("the widest parameter object is %d bytes and linwrite.go's note says 202; "+
			"one of the two is now wrong", used)
	}
	t.Logf("the widest possible parameter object is %d bytes of %d reserved", used, paramSpan)
}

// ⛔⛔ AND THE DICTIONARY IS READ BACK, which the tests above do not do: they
// assert its LENGTH and its padding and never that it is a dictionary at all.
// A formatting error — a missing space before `>>`, the wrong bracket spacing
// in /H, a key this package's own lexer will not take — produces an object of
// exactly the right width that no reader accepts, and every assertion above
// would still pass.
//
// ⛔ Linearized() is the judge here and it is MY OWN reader, so this is a round
// trip and not a foreign witness. It is worth having anyway: it is the reader
// every other test in this package trusts, and a dictionary it rejects could
// not possibly be right. The foreign judge for the writer is qpdf --check, and
// that arrives with the emission, which this PR does not add.
func TestTheParameterObjectIsADictionaryThisPackageCanRead(t *testing.T) {
	p := linParams{L: 1367, H0: 550, H1: 127, O: 7, E: 902, N: 2, T: 1169}
	obj, err := p.object(4)
	if err != nil {
		t.Fatal(err)
	}

	// A file is a header, the parameter object, and enough of a tail that
	// Linearized can find /L's claim about the length. /L must equal the
	// file's real length, so the tail is padded to make it so.
	head := "%PDF-1.7\n"
	file := append([]byte(head), obj...)
	if int64(len(file)) > p.L {
		t.Fatalf("the header and parameter object are %d bytes and /L claims %d, so this "+
			"fixture cannot be built", len(file), p.L)
	}
	file = append(file, bytes.Repeat([]byte{'\n'}, int(p.L)-len(file))...)

	lin, err := Linearized(file)
	if err != nil {
		t.Fatalf("this package cannot read the dictionary it just wrote: %v", err)
	}

	// ⛔ EVERY VALUE, because a reader that silently dropped one would leave
	// the others agreeing and the test green.
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"/L", lin.FileLength, int(p.L)},
		{"/H's offset", lin.HintOffset, int(p.H0)},
		{"/H's length", lin.HintLength, int(p.H1)},
		{"/O", lin.FirstPageObject, int(p.O)},
		{"/E", lin.FirstPageEnd, int(p.E)},
		{"/N", lin.PageCount, int(p.N)},
		{"/T", lin.MainXrefOffset, int(p.T)},
	} {
		if c.got != c.want {
			t.Errorf("%s read back as %d, written as %d", c.name, c.got, c.want)
		}
	}
}

// ⛔⛔ AND A DICTIONARY THAT WILL NOT FIT IS REFUSED, NOT TRUNCATED. One byte
// too long does not give a broken dictionary: it gives a file where every
// offset after part 2 is one byte further along than /E, /T and the first-page
// cross-reference section say. The file opens and a reader following those
// offsets lands inside the wrong object.
func TestParametersTooWideForTheSpanAreRefused(t *testing.T) {
	// ⛔⛔ THE SPAN IS SHRUNK, because the refusal is otherwise UNREACHABLE and
	// an unreachable refusal is an untested claim. Measured: the widest object
	// this function can produce is 202 bytes of the 256 reserved, so no int64
	// value and no int object number can overflow it — an earlier version of
	// this test tried an eighteen-digit object number and was accepted at 256.
	// paramSpan is a var for exactly this, the seam intMax and maxDecodedSize
	// already use here.
	defer func(n int) { paramSpan = n }(paramSpan)
	paramSpan = 64

	p := linParams{L: 1367, H0: 550, H1: 127, O: 7, E: 902, N: 2, T: 1169}
	out, err := p.object(4)
	if !errors.Is(err, ErrLinearizationTooWide) {
		t.Fatalf("a realistic dictionary was accepted into a %d-byte span at %d bytes — want "+
			"ErrLinearizationTooWide", paramSpan, len(out))
	}
	if out != nil {
		t.Errorf("it was refused AND returned %d bytes; a caller ignoring the error would "+
			"write a file whose every later offset is wrong", len(out))
	}
	if !strings.Contains(err.Error(), "are reserved") {
		t.Errorf("it was refused without saying what the span is: %v", err)
	}
}

// ⛔⛔ A NEGATIVE VALUE IS REFUSED, AND THE FIRST VERSION ACCEPTED ONE. Every
// one of Table F.1's values is a length, an offset or a count, so none can be
// below zero — but the fields are int64 and nothing said so.
//
// ⛔ WHAT THAT BROKE WAS A CLAIM, not behaviour. Review measured all seven at
// math.MinInt64: 209 bytes with a six-digit object number and 213 with a
// ten-digit one, against the 202 this package calls its widest. Both fit 256,
// so no file was ever wrong — but "202 is the widest this function can produce"
// was true only of non-negative values, and the test pinning 202 used a
// non-negative case, so it could not see its own gap. The refusal is what makes
// the sentence true.
func TestNegativeParametersAreRefused(t *testing.T) {
	ok := linParams{L: 1367, H0: 550, H1: 127, O: 7, E: 902, N: 2, T: 1169}

	for _, c := range []struct {
		name string
		mut  func(*linParams)
		want string
	}{
		{"/L", func(p *linParams) { p.L = -1 }, "/L is -1"},
		{"/H's offset", func(p *linParams) { p.H0 = -1 }, "/H's offset is -1"},
		{"/H's length", func(p *linParams) { p.H1 = -1 }, "/H's length is -1"},
		{"/O", func(p *linParams) { p.O = -1 }, "/O is -1"},
		{"/E", func(p *linParams) { p.E = -1 }, "/E is -1"},
		{"/N", func(p *linParams) { p.N = -1 }, "/N is -1"},
		{"/T", func(p *linParams) { p.T = -1 }, "/T is -1"},
		{"the widest negative, which review measured at 209 bytes",
			func(p *linParams) { *p = linParams{L: -9223372036854775808} }, "/L is -9223372036854775808"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := ok
			c.mut(&p)
			out, err := p.object(4)
			if !errors.Is(err, ErrLinearizationTooWide) {
				t.Fatalf("it was accepted: %v, %d bytes", err, len(out))
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("it was refused, but not naming %s: %v", c.want, err)
			}
			if out != nil {
				t.Errorf("it was refused AND returned %d bytes", len(out))
			}
		})
	}

	// ⛔ A negative OBJECT NUMBER too, which is the eighth way in and is not a
	// Table F.1 value.
	if _, err := ok.object(-1); !errors.Is(err, ErrLinearizationTooWide) {
		t.Errorf("object number -1 was accepted: %v", err)
	}

	// ⛔ THE CONTROL: zero is not negative, and /H's offset legitimately could
	// be nothing in a malformed-but-writable file; refusing it would be a
	// ceiling in the wrong direction.
	zero := linParams{}
	if _, err := zero.object(0); err != nil {
		t.Errorf("all-zero parameters were refused, and zero is not negative: %v", err)
	}
}

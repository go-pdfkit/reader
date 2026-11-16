// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔⛔⛔ #61's PLANNED WITNESS DOES NOT EXIST, AND MEASURING IS HOW I FOUND OUT.
// The issue says "the Ghostscript fixture must be refused naming a length, not
// only the overrun, which means the overrun refusal cannot be the thing that
// stops the check running". It is not an ordering problem: that fixture's tables
// DO NOT DECODE AT ALL.
//
//	reader: the hint table cannot be read: the page offset table's entries end
//	at bit 330 and /S puts the shared object table at byte 38 (bit 304), so 26
//	bit(s) of the entries are really the next table
//
// ⛔ So there is no well-defined table to take a length from, by any ordering.
// qpdf prints `computed = 252` for that file because ITS decoder reads the
// entries through the overrun; ours refuses them, which #60 added deliberately.
// Reaching #61's witness would mean loosening a landed check to get at a second
// defect in the same file — trading a certain refusal for a less certain one.
//
// ⭐ THE ORACLE IS STILL QPDF, from the other side. On the three fixtures qpdf
// ACCEPTS, qpdf's silence means its own computed length equals the length the
// table states. So if our computation equals the table's value on those files,
// it equals qpdf's computation — the same agreement #61 asks for, obtained from
// the files that decode rather than the one that does not.
func TestOurLengthsAgreeWithEveryFileQpdfAccepts(t *testing.T) {
	for _, tc := range []struct {
		name string
		// splits says this fixture exercises BOTH runs of shared groups — the
		// first page's and part 8's — so the counter switch is witnessed.
		splits bool
	}{
		{name: "linearized.pdf"},
		// ⛔⛔ THE TWO-RUN SPLIT'S WITNESS, measured rather than assumed:
		// `qpdf --show-linearization testdata/linearized-shared.pdf` reports
		// nshared_first_page 2, nshared_total 3, first_shared_obj 5, part 8
		// POPULATED, with group lengths 108, 99 and 601. So group 2 is where
		// the counter must leave /O and start at object 5, and this is the only
		// fixture that says so — linearized-objstm.pdf is 3 == 3 and exercises
		// the first run alone.
		{name: "linearized-shared.pdf", splits: true},
		{name: "linearized-objstm.pdf"},
	} {
		name := tc.name
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			// ⛔ Through CheckLinearized, so this also proves the check is
			// WIRED and not merely callable: a file qpdf accepts must survive
			// every check this package makes, the new one included.
			lin, err := CheckLinearized(b)
			if err != nil {
				t.Fatalf("a file qpdf accepts was refused: %v", err)
			}
			d, err := Open(b)
			if err != nil {
				t.Fatal(err)
			}
			tables, err := d.readHintTables(lin)
			if err != nil {
				t.Fatal(err)
			}

			// ⛔⛔ AND THE COMPARISONS ARE COUNTED. Every page of an
			// object-stream file may be unmeasurable — spanLength returns
			// false for a compressed object — and a test that silently
			// compared NOTHING would pass exactly as loudly as one that
			// compared everything. That is the fault this suite keeps
			// catching, so the count is asserted and printed.
			compared, skipped := 0, 0
			refs := d.pageRefs()
			for i, e := range tables.pages {
				got, ok, err := spanLength(b, d, lin, refs[i].Num, int(e.NObjects))
				if err != nil {
					t.Fatalf("page %d: %v", i+1, err)
				}
				if !ok {
					skipped++
					continue
				}
				compared++
				if got != int(e.Length) {
					t.Errorf("page %d: the table says %d bytes, we compute %d — and qpdf "+
						"accepts the file, so %d is also qpdf's figure",
						i+1, e.Length, got, e.Length)
				}
			}

			if tc.splits != (tables.shared.NSharedFirstPage < tables.shared.NSharedTotal) {
				t.Fatalf("this fixture reports %d first-page and %d total shared entr(ies), "+
					"so it does not exercise the runs this case claims",
					tables.shared.NSharedFirstPage, tables.shared.NSharedTotal)
			}

			num := lin.FirstPageObject
			switched := false
			for i, g := range tables.sharedGroups {
				if i == int(tables.shared.NSharedFirstPage) {
					num = int(tables.shared.FirstSharedObj)
					switched = true
				}
				got, ok, err := spanLength(b, d, lin, num, int(g.NObjects))
				if err != nil {
					t.Fatalf("group %d: %v", i, err)
				}
				if ok {
					compared++
					if got != int(g.Length) {
						t.Errorf("group %d: the table says %d bytes, we compute %d",
							i, g.Length, got)
					}
				} else {
					skipped++
				}
				num += int(g.NObjects)
			}

			if tc.splits && !switched {
				t.Error("the counter never left /O, so part 8's run was not measured")
			}
			t.Logf("%d length(s) compared, %d not measurable, split fired: %v",
				compared, skipped, switched)
			if compared == 0 {
				t.Fatal("nothing was compared, so this file witnesses no agreement " +
					"with qpdf at all")
			}
		})
	}
}

// ⛔⛔ AND THE COMPARISON BITES, witnessed by altering the TABLE rather than the
// file. checkHintLengths is handed the decoded tables, so a table that states a
// length the file does not have is exactly the input it exists to refuse — and
// building one in memory needs no re-encoded hint stream, which a byte-level
// mutation of a Flate-compressed stream would.
//
// ⭐ Both numbers must be in the message: a reader has to be able to tell which
// of the two is wrong, the table or their file. qpdf prints both for the same
// reason.
func TestAStatedLengthTheFileDoesNotHaveIsRefused(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "linearized.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	lin, err := CheckLinearized(b)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("a page length", func(t *testing.T) {
		tables, err := d.readHintTables(lin)
		if err != nil {
			t.Fatal(err)
		}
		was := tables.pages[0].Length
		tables.pages[0].Length = was + 7

		err = checkHintLengths(b, d, lin, tables)
		if !errors.Is(err, ErrHintTablesInvalid) {
			t.Fatalf("a page length seven bytes out was accepted: %v", err)
		}
		for _, want := range []string{"page 1", fmtInt(int(was) + 7), fmtInt(int(was))} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
	})

	t.Run("a shared group's length", func(t *testing.T) {
		tables, err := d.readHintTables(lin)
		if err != nil {
			t.Fatal(err)
		}
		if len(tables.sharedGroups) == 0 {
			t.Skip("this fixture holds no shared groups")
		}
		was := tables.sharedGroups[0].Length
		tables.sharedGroups[0].Length = was + 3

		err = checkHintLengths(b, d, lin, tables)
		if !errors.Is(err, ErrHintTablesInvalid) {
			t.Fatalf("a group length three bytes out was accepted: %v", err)
		}
		if !strings.Contains(err.Error(), "shared group") {
			t.Errorf("refused, but not for the group: %v", err)
		}
	})
}

// ⛔⛔ THE COMPRESSED-OBJECT HOLE, WITNESSED, because no fixture reaches it: all
// three accepted files measured every span, 0 not measurable, INCLUDING
// linearized-objstm.pdf — whose object streams hold objects that no page span
// happens to name. So the loop can reach this state and only the corpus cannot,
// which is the reading that says "write the witness" rather than "delete the
// branch".
//
// ⚠ AND IT MUST NOT BE AN ERROR. An object inside an object stream has no offset
// of its own; qpdf substitutes its container's, so several objects measure from
// one byte and their lengths overlap. That is its accounting, not a fact about
// the file, and an object stream is legal in a linearized file — so the span is
// left unchecked and the file is not refused for it.
func TestASpanInsideAnObjectStreamIsNotMeasured(t *testing.T) {
	d := bareDoc(nil, nil, nil)
	d.buf = []byte("%PDF-1.7\n1 0 obj\n<< >>\nendobj\n")
	// Object 1 has an offset of its own; object 2 lives in a stream.
	d.xref[1] = xrefEntry{kind: 'n', offset: 9}
	// ⛔⛔ 'o', NOT 'c'. xref.go defines exactly three kinds — 'n' at a byte
	// offset, 'o' inside an object stream, 'f' free — and 'c' is not one of
	// them: nothing in this package writes it. The branch WAS reached, because
	// 'c' is not 'n', so the test passed while witnessing a state no file can
	// hold, and a later change treating 'o' specially would have left it green.
	d.xref[2] = xrefEntry{kind: 'o', strmNum: 1, strmIdx: 0}

	lin := &Linearization{FirstPageObject: 1}

	got, ok, err := spanLength(d.buf, d, lin, 1, 2)
	if err != nil {
		t.Fatalf("a compressed object was reported as an error: %v", err)
	}
	if ok {
		t.Errorf("the span was reported measurable, as %d bytes", got)
	}

	// ⛔ AND THE OTHER HALF: an object the cross-reference table does not list
	// at all IS an error, because a hint table naming an object the file does
	// not have is a different claim from one naming an object we cannot
	// measure.
	//
	// ⛔⛔ THE SPAN HAS TO START AT THE ABSENT OBJECT. The first version of this
	// assertion asked for objects 1..9, and object 2 is COMPRESSED — so the
	// loop returned "not measurable" on its second step and never reached the
	// absent one. It reported nil and the assertion read as a defect in the
	// code it was testing, when the case simply could not arrive.
	if _, _, err := spanLength(d.buf, d, lin, 5, 2); !errors.Is(err, ErrHintTablesInvalid) {
		t.Errorf("a hint table measuring an absent object was accepted: %v", err)
	}
}

// ⛔⛔ THE COUNTS DISTINGUISH "ALL AGREED" FROM "NONE MEASURABLE", which a nil
// error cannot. A file whose page objects are all inside object streams is not
// refused — object streams are legal in a linearized document — so without these
// a caller depending on "the lengths were checked" gets the same answer from a
// verified file and an unverifiable one.
//
// ⚠ Review found this, and it also found why linearized-objstm.pdf shows 0
// skipped: qpdf left objects 8-10 uncompressed, and the compressed ones (11-12)
// are named by no span. So that fixture does NOT witness the skip, which is why
// the skipping case below is built by hand.
func TestTheLengthCheckSaysHowMuchItMeasured(t *testing.T) {
	t.Run("a file every length of which is measurable", func(t *testing.T) {
		b, err := os.ReadFile(filepath.Join("testdata", "linearized.pdf"))
		if err != nil {
			t.Fatal(err)
		}
		lin, err := CheckLinearized(b)
		if err != nil {
			t.Fatal(err)
		}
		if lin.LengthsChecked == 0 {
			t.Error("it reports nothing checked for a file qpdf accepts")
		}
		if lin.LengthsSkipped != 0 {
			t.Errorf("it skipped %d length(s) in a file with no compressed span",
				lin.LengthsSkipped)
		}
	})

	t.Run("a page whose span is compressed", func(t *testing.T) {
		d := bareDoc(nil, []Ref{{Num: 1}}, map[int]Object{1: Dict{"Type": Name("Page")}})
		d.buf = []byte("%PDF-1.7\n1 0 obj\n<< >>\nendobj\n")
		d.xref[1] = xrefEntry{kind: 'n', offset: 9}
		d.xref[2] = xrefEntry{kind: 'o', strmNum: 1, strmIdx: 0}

		lin := &Linearization{FirstPageObject: 1}
		tables := &hintTables{pages: []pageEntry{{NObjects: 2, Length: 123456}}}

		if err := checkHintLengths(d.buf, d, lin, tables); err != nil {
			t.Fatalf("a compressed span was refused: %v", err)
		}
		// ⛔ THE POINT: nil, and nothing checked. A caller reading only the
		// error would believe 123456 had been confirmed.
		if lin.LengthsChecked != 0 || lin.LengthsSkipped != 1 {
			t.Errorf("it reports %d checked and %d skipped, want 0 and 1",
				lin.LengthsChecked, lin.LengthsSkipped)
		}
	})

	t.Run("a shared group whose span is compressed", func(t *testing.T) {
		// ⛔ THE GROUP PATH SKIPS TOO, and no fixture reaches it either: both
		// runs of groups in every accepted file measure cleanly. The page path
		// above and this one are separate loops, so a witness for one is not a
		// witness for the other.
		d := bareDoc(nil, nil, nil)
		d.buf = []byte("%PDF-1.7\n1 0 obj\n<< >>\nendobj\n")
		d.xref[1] = xrefEntry{kind: 'n', offset: 9}
		d.xref[2] = xrefEntry{kind: 'o', strmNum: 1, strmIdx: 0}

		lin := &Linearization{FirstPageObject: 1}
		tables := &hintTables{
			shared:       &sharedHeader{NSharedFirstPage: 1, NSharedTotal: 1},
			sharedGroups: []sharedGroup{{NObjects: 2, Length: 99999}},
		}

		if err := checkHintLengths(d.buf, d, lin, tables); err != nil {
			t.Fatalf("a compressed group span was refused: %v", err)
		}
		if lin.LengthsChecked != 0 || lin.LengthsSkipped != 1 {
			t.Errorf("it reports %d checked and %d skipped, want 0 and 1",
				lin.LengthsChecked, lin.LengthsSkipped)
		}
	})
}

// ⛔ EVERY WAY THE LENGTH CHECK REFUSES, reached directly because the files that
// decode are the files qpdf accepts — so none of these arrives through
// CheckLinearized, and a propagation no test reaches is an untested claim.
func TestEveryWayTheLengthCheckRefuses(t *testing.T) {
	good, err := os.ReadFile(filepath.Join("testdata", "linearized.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := CheckLinearized(good)
	if err != nil {
		t.Fatal(err)
	}
	src, err := Open(good)
	if err != nil {
		t.Fatal(err)
	}
	real, err := src.readHintTables(ref)
	if err != nil {
		t.Fatal(err)
	}

	// A document with one readable object and one compressed, for the spans
	// that must not be measured.
	//
	// ⛔ IT HAS A PAGE. The first version passed no pages, so `refs` was empty
	// and "more page entries than pages" fired before the compressed span was
	// ever reached — the case could not arrive, and the failure read as a
	// defect in the code rather than in the fixture.
	tiny := bareDoc(nil, []Ref{{Num: 1}},
		map[int]Object{1: Dict{"Type": Name("Page")}})
	tiny.buf = []byte("%PDF-1.7\n1 0 obj\n<< >>\nendobj\n")
	tiny.xref[1] = xrefEntry{kind: 'n', offset: 9}
	tiny.xref[2] = xrefEntry{kind: 'o', strmNum: 1, strmIdx: 0}

	// And one whose single object has an offset the file cannot hold.
	lost := bareDoc(nil, []Ref{{Num: 1}},
		map[int]Object{1: Dict{"Type": Name("Page")}})
	lost.buf = []byte("%PDF-1.7\n")
	lost.xref[1] = xrefEntry{kind: 'n', offset: 100000}

	for _, tc := range []struct {
		name   string
		doc    *Document
		b      []byte
		lin    *Linearization
		tables *hintTables
		want   string
	}{
		{
			name: "more page entries than the document has pages",
			doc:  tiny,
			b:    tiny.buf,
			lin:  &Linearization{FirstPageObject: 1},
			tables: &hintTables{
				// ⛔ THE FIRST ENTRY MUST BE RIGHT, or its own mismatch fires
				// first and this case never reaches the count check — which is
				// what the first version did: it asserted the count message and
				// got a length message, for a page whose length it had set to
				// 1 while the object occupies 21 bytes.
				pages: []pageEntry{{NObjects: 1, Length: 21}, {NObjects: 1, Length: 21}},
			},
			want: "the document has",
		},
		{
			name: "a page measuring an object the file does not list",
			doc:  src,
			b:    good,
			lin:  ref,
			tables: &hintTables{
				pages: []pageEntry{{NObjects: 9999, Length: 1}},
			},
			want: "does not list",
		},
		{
			name: "a page whose objects are compressed",
			doc:  tiny,
			b:    tiny.buf,
			lin:  &Linearization{FirstPageObject: 1},
			tables: &hintTables{
				// ⛔ ACCEPTED, not refused: the span cannot be measured the way
				// qpdf measures it, and an object stream is legal here.
				pages: []pageEntry{{NObjects: 2, Length: 123456}},
			},
			want: "",
		},
		{
			name: "a page whose object sits past the end of the file",
			doc:  lost,
			b:    lost.buf,
			lin:  &Linearization{FirstPageObject: 1},
			tables: &hintTables{
				pages: []pageEntry{{NObjects: 1, Length: 1}},
			},
			want: "and the file is",
		},
		{
			name: "a shared group covering no objects",
			doc:  src,
			b:    good,
			lin:  ref,
			tables: &hintTables{
				shared:       &sharedHeader{NSharedFirstPage: 0, NSharedTotal: 1},
				sharedGroups: []sharedGroup{{NObjects: 0, Length: 1}},
			},
			want: "covers 0 object",
		},
		{
			name: "a shared group measuring an object the file does not list",
			doc:  src,
			b:    good,
			lin:  ref,
			tables: &hintTables{
				shared:       &sharedHeader{NSharedFirstPage: 1, NSharedTotal: 1},
				sharedGroups: []sharedGroup{{NObjects: 9999, Length: 1}},
			},
			want: "does not list",
		},
		{
			name: "no shared table at all",
			doc:  src,
			b:    good,
			lin:  ref,
			// ⛔ ACCEPTED here: readHintTables always returns one, so this is
			// the shape only a caller can build, and the page lengths are
			// still worth checking without it.
			tables: &hintTables{pages: real.pages},
			want:   "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkHintLengths(tc.b, tc.doc, tc.lin, tc.tables)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("this shape is meant to be accepted and was refused: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrHintTablesInvalid) {
				t.Fatalf("it was accepted: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refused, but not for %q: %v", tc.want, err)
			}
		})
	}
}

// ⛔⛔ CheckLinearized REFUSES A REAL FILE WITH A WRONG LENGTH, built by flipping
// one byte of one this package wrote — not through a replaced function.
//
// ⭐ IT IS POSSIBLE BECAUSE OF TWO CHOICES OUR WRITER MADE. writeHintStream
// emits the hint data RAW, with no /Filter, and hintByteWidth pins Table F.4
// item 2 at 32 bits — so the page-length column is at a byte this test can
// COMPUTE, and adding 1 to it leaves every offset, /Length and /L untouched
// while making the table disagree with the file by exactly one byte.
//
// ⛔ A first version used a seam instead, on the stated grounds that a
// counter-example "would mean re-encoding a Flate-compressed hint stream". That
// is true of qpdf's output and false of ours — the materials were in our own
// writer. Six rounds of #66 went into replacing claims with files; this is the
// same lesson one layer up.
func TestCheckLinearizedRefusesAFileWhoseLengthIsWrong(t *testing.T) {
	d, err := Open(buildPages(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	good, err := d.Linearize()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckLinearized(good); err != nil {
		t.Fatalf("our own file is refused before anything is flipped: %v", err)
	}

	lin, err := Linearized(good)
	if err != nil {
		t.Fatal(err)
	}
	written, err := Open(good)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := written.readHintTables(lin)
	if err != nil {
		t.Fatal(err)
	}

	// ⛔ THE POSITION IS DERIVED, NOT COPIED. Table F.3's header is 36 bytes;
	// group (a) is one item-1 delta per page at NBitsDeltaNObjects bits, padded
	// to a byte; group (b) follows with item 2 at 32 bits per page. The low
	// byte of the first page's item 2 is three bytes into that field.
	dataAt := hintDataOffset(t, good, lin)
	bits := len(tables.pages) * tables.pageOffset.NBitsDeltaNObjects
	groupB := 36 + (bits+7)/8
	at := dataAt + groupB + 3

	was := tables.pages[0].Length
	bad := make([]byte, len(good))
	copy(bad, good)
	bad[at]++

	// ⛔ The flip must land on the field this test claims it did, or the
	// refusal below could come from anywhere in the stream.
	reread, err := Linearized(bad)
	if err != nil {
		t.Fatalf("the flipped file's parameters no longer read: %v", err)
	}
	rd, err := Open(bad)
	if err != nil {
		t.Fatal(err)
	}
	after, err := rd.readHintTables(reread)
	if err != nil {
		t.Fatalf("the flipped file's tables no longer decode, so the flip hit the wrong "+
			"byte: %v", err)
	}
	if after.pages[0].Length != was+1 {
		t.Fatalf("the flip changed page 1's length from %d to %d, want %d — it did not "+
			"land on Table F.4 item 2", was, after.pages[0].Length, was+1)
	}

	_, err = CheckLinearized(bad)
	if !errors.Is(err, ErrHintTablesInvalid) {
		t.Fatalf("a file whose page length is one byte out was accepted: %v", err)
	}
	for _, want := range []string{fmtInt(int(was) + 1), fmtInt(int(was))} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// hintDataOffset returns where the primary hint stream's DATA begins, by finding
// the `stream` keyword inside the object /H points at.
func hintDataOffset(t *testing.T, b []byte, lin *Linearization) int {
	t.Helper()
	at := lin.HintOffset
	if at <= 0 || at >= len(b) {
		t.Fatalf("/H is %d and the file is %d bytes", at, len(b))
	}
	end := at + lin.HintLength
	if end > len(b) {
		end = len(b)
	}
	i := strings.Index(string(b[at:end]), "stream")
	if i < 0 {
		t.Fatal("the object /H names holds no `stream` keyword")
	}
	j := at + i + len("stream")
	return j + eolAfter(b, j)
}

// ⛔ AND THE TWO WAYS AN OBJECT'S EXTENT CANNOT BE TAKEN, which no file reaches
// because the cross-reference table that named the offset was already read.
func TestAnObjectWhoseExtentCannotBeTaken(t *testing.T) {
	b := []byte("%PDF-1.7\n1 0 obj\n<< >>\nendobj\n")

	t.Run("an offset past the end of the file", func(t *testing.T) {
		if _, err := objectExtent(b, len(b)+100, 1); !errors.Is(err, ErrHintTablesInvalid) {
			t.Errorf("an offset past the file was measured: %v", err)
		}
	})

	t.Run("a negative offset", func(t *testing.T) {
		if _, err := objectExtent(b, -1, 1); !errors.Is(err, ErrHintTablesInvalid) {
			t.Errorf("a negative offset was measured: %v", err)
		}
	})

	t.Run("bytes that are not an object", func(t *testing.T) {
		if _, err := objectExtent([]byte("not an object at all"), 0, 1); !errors.Is(err,
			ErrHintTablesInvalid) {
			t.Errorf("unparseable bytes were measured: %v", err)
		}
	})
}

func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

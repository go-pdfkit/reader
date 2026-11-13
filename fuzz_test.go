package reader_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-pdfkit/reader"
)

// seedDir is where an adversarial corpus can be put: point PDF_SEEDS at
// mozilla's pdf.js test suite, every file of which is there because it broke a
// reader once, and the fuzzer starts from a far better population than
// anything a generator would invent. Without it the committed seeds and the
// crashers under testdata still run — the corpus makes the search better, it
// is not what makes the test valid.
var seedDir = os.Getenv("PDF_SEEDS")

// addSeeds feeds the fuzzer files from the corpus, capped: a seed the fuzzer
// cannot mutate quickly is a seed it will not explore.
func addSeeds(f *testing.F, max int, cap int) {
	f.Add([]byte("%PDF-1.7\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>"))
	f.Add([]byte("%PDF-1.4\nstartxref\n0\n%%EOF"))
	if seedDir == "" {
		return
	}
	ents, err := os.ReadDir(seedDir)
	if err != nil {
		return
	}
	type ent struct {
		path string
		size int64
	}
	var list []ent
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".pdf" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Size() > int64(cap) {
			continue
		}
		list = append(list, ent{filepath.Join(seedDir, e.Name()), info.Size()})
	}
	n := 0
	for _, e := range list {
		b, err := os.ReadFile(e.path)
		if err != nil {
			continue
		}
		f.Add(b)
		n++
		if n >= max {
			break
		}
	}
}

// budget fails the run if one input takes longer than a reader has any right
// to take. The fuzzer's own -timeout kills the whole process after ten
// minutes, which tells you nothing about which input was at fault; this names
// it. What is being hunted is a small file that costs a large amount of time,
// so the check has to be per input.
func budget(t *testing.T, limit time.Duration, size int, what string, f func()) {
	t.Helper()
	start := time.Now()
	f()
	if d := time.Since(start); d > limit {
		t.Fatalf("%s: %d bytes took %s, over the %s budget", what, size, d, limit)
	}
}

// FuzzOpenWithinABudget is the public-API twin of the in-package FuzzOpen: it
// asserts not only that no byte string makes the reader panic, but that none
// takes longer than a couple of seconds. Time is a return code in disguise —
// the 219-byte file that took 21 seconds to open panicked at nothing and
// allocated nothing, so only a clock could have caught it.
//
// Two fuzz targets of the same name in the two test packages cannot both be
// selected, and `go test -fuzz FuzzOpen` refuses the ambiguity outright, so
// neither could be run until this one was renamed.
func FuzzOpenWithinABudget(f *testing.F) {
	addSeeds(f, 400, 40*1024)
	f.Fuzz(func(t *testing.T, b []byte) {
		budget(t, 2*time.Second, len(b), "Open", func() {
			d, err := reader.Open(b)
			if err != nil {
				return
			}
			n := d.PageCount()
			if n > 4 {
				n = 4
			}
			for i := 1; i <= n; i++ {
				if _, err := d.Page(i); err != nil {
					continue
				}
				if _, err := d.PageContent(i); err != nil {
					continue
				}
				_, _ = d.PageOperations(i)
			}
		})
	})
}

func FuzzParseObject(f *testing.F) {
	for _, s := range []string{
		"<< /A 1 /B [1 2 3] >>", "(hello \\( world)", "<AABBCC>", "12 0 R",
		"[[[[[[[[[[]]]]]]]]]]", "<</A<</B<</C<</D 1>>>>>>>>", "3.14", "true",
		"/Name#20with#20spaces", "null", "-.0000000000000000001",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		budget(t, time.Second, len(b), "ParseObject", func() {
			o, _, err := reader.ParseObject(b)
			if err == nil {
				_ = reader.FormatObject(o)
			}
		})
	})
}

func FuzzOperations(f *testing.F) {
	for _, s := range []string{
		"BT /F1 12 Tf (hi) Tj ET", "q 1 0 0 1 0 0 cm Q",
		"BI /W 2 /H 2 /BPC 8 /CS /G ID \x00\x01\x02\x03 EI",
		"[ (a) -250 (b) ] TJ", "1 2 3 4 5 6 c",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		budget(t, time.Second, len(b), "Operations", func() {
			_, _ = reader.Operations(b)
		})
	})
}

// FuzzDecode drives the filter chain directly. The first byte picks the
// filter, so that one corpus explores all of them rather than one each.
func FuzzDecode(f *testing.F) {
	names := []reader.Name{"FlateDecode", "LZWDecode", "ASCII85Decode", "ASCIIHexDecode", "RunLengthDecode"}
	for i := range names {
		f.Add(byte(i), byte(0), []byte("x\x9c\x03\x00\x00\x00\x00\x01"))
		f.Add(byte(i), byte(2), []byte("~>"))
	}
	f.Fuzz(func(t *testing.T, which byte, colors byte, data []byte) {
		name := names[int(which)%len(names)]
		d := reader.Dict{"Filter": name}
		if colors&1 != 0 {
			// A predictor is where a decoder starts trusting numbers the file
			// chose: rows, columns, colours and bits per component all come
			// from the document, and their product is a buffer size.
			d["DecodeParms"] = reader.Dict{
				"Predictor":        reader.Integer(2 + int(colors)%14),
				"Colors":           reader.Integer(1 + int(colors>>1)%8),
				"Columns":          reader.Integer(1 + int(colors>>2)%64),
				"BitsPerComponent": reader.Integer([]int{1, 2, 4, 8, 16}[int(colors>>3)%5]),
			}
		}
		budget(t, 2*time.Second, len(data), "Decode", func() {
			_, _, _ = reader.Decode(d, data, func(reader.Ref) (reader.Object, error) {
				return nil, nil
			})
		})
	})
}

// FuzzCheckLinearized drives the deep linearization check.
//
// ⛔ IT IS A PARSER OF NUMBERS AN ATTACKER CHOSE. /H, /O, /E, /T and /N come
// out of the first object of the file and are used as OFFSETS and LENGTHS into
// it. The cheap [reader.Linearized] only type-checks them; this one slices the
// file at them, which is where a bad bound stops being a wrong answer and
// starts being a read past the end.
//
// The seeds are the two committed fixtures — byte substitutions in them keep
// the length, so /L still matches and the mutator reaches the deep checks
// rather than stopping at the front — plus fronts whose integers are absurd on
// purpose.
func FuzzCheckLinearized(f *testing.F) {
	for _, name := range []string{"linearized.pdf", "linearized-objstm.pdf"} {
		if b, err := os.ReadFile(filepath.Join("testdata", name)); err == nil {
			f.Add(b)
		}
	}
	// ⛔ MaxInt32 in both halves of /H. Written as an addition, the bound check
	// overflows to a negative number on a 32-bit build and admits the file;
	// this seed is the regression for that, and it is the shape a hostile file
	// takes rather than one a generator would invent.
	//
	// ⛔⛔ THE SEED'S /L MUST BE THE SEED'S OWN LENGTH, which review found it
	// was not: every one of them said `/L 1` for a file hundreds of bytes
	// long, so [reader.Linearized] refused them on the length and the deep
	// checks — the ones these seeds were written for — never ran at all. The
	// placeholder is TEN DIGITS so that filling it in cannot change the
	// length it is measuring.
	for _, s := range []string{
		"<< /Linearized 1 /L %010d /H [ 2147483647 2147483647 ] /O 1 /E 1 /N 1 /T 1 >>",
		"<< /Linearized 1 /L %010d /H [ -1 -1 ] /O -1 /E -1 /N -1 /T -1 >>",
		"<< /Linearized 1 /L %010d /H [ 0 0 ] /O 0 /E 0 /N 0 /T 0 >>",
		"<< /Linearized 1 /L %010d /H [ 40 20 ] /O 1 /E 60 /N 1 /T 50 >>",
	} {
		body := "%PDF-1.7\n1 0 obj\n" + s + "\nendobj\n"
		f.Add([]byte(fmt.Sprintf(body, len(fmt.Sprintf(body, 0)))))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		budget(t, 2*time.Second, len(b), "CheckLinearized", func() {
			lin, err := reader.CheckLinearized(b)
			if err != nil {
				return
			}
			// ⛔ A TAUTOLOGY WAS REMOVED FROM HERE. It asserted that anything
			// CheckLinearized accepts, Linearized accepts too — which cannot
			// fail, because CheckLinearized calls Linearized first and returns
			// its error. Review named it; an invariant that cannot fail is a
			// line that looks like a gate and is not one.
			//
			// ⛔ WHAT FOLLOWS IS NOT A RESTATEMENT OF THE CHECKS. The checks
			// bound each value as they read it; this re-derives the same
			// property at the API boundary, from len(b) alone, AFTER the
			// function has committed to an answer. That difference is what
			// catches an arithmetic fault rather than a missing branch: with
			// the old `HintOffset+HintLength > len(b)` form, a length near
			// MaxInt passes the check and fails here.
			if lin.HintOffset < 0 || lin.HintOffset > len(b) ||
				lin.HintLength < 0 || lin.HintLength > len(b)-lin.HintOffset {
				t.Fatalf("/H [%d %d] accepted for a file of %d bytes",
					lin.HintOffset, lin.HintLength, len(b))
			}
			if lin.MainXrefOffset < 0 || lin.MainXrefOffset > len(b) {
				t.Fatalf("/T %d accepted for a file of %d bytes", lin.MainXrefOffset, len(b))
			}
			if lin.FirstPageEnd < 0 || lin.FirstPageEnd > len(b) {
				t.Fatalf("/E %d accepted for a file of %d bytes", lin.FirstPageEnd, len(b))
			}
		})
	})
}

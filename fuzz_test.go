package reader_test

import (
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

func FuzzOpen(f *testing.F) {
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

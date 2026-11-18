package reader

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestApplyPredictorNone(t *testing.T) {
	data := []byte("unchanged")
	for _, parm := range []Dict{nil, {}, {"Predictor": Integer(1)}} {
		got, err := applyPredictor(data, parm, nil)
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("%v: got %q, %v", parm, got, err)
		}
	}
}

func TestApplyPredictorRejects(t *testing.T) {
	cases := []Dict{
		{"Predictor": Integer(5)},                        // 3 to 9 are not defined
		{"Predictor": Integer(2), "Colors": Integer(0)},  // no components
		{"Predictor": Integer(2), "Columns": Integer(0)}, // no columns
		{"Predictor": Integer(2), "BitsPerComponent": Integer(0)},
		{"Predictor": Integer(2), "BitsPerComponent": Integer(4)}, // sub-byte TIFF
	}
	for _, parm := range cases {
		if _, err := applyPredictor([]byte("xx"), parm, nil); err == nil {
			t.Errorf("%v: want an error", parm)
		}
	}
}

func TestApplyPredictorResolvesParameters(t *testing.T) {
	boom := func(Ref) (Object, error) { return nil, errors.New("boom") }
	// An unresolvable /Predictor falls back to its default, which is "none".
	got, err := applyPredictor([]byte("abc"), Dict{"Predictor": Ref{1, 0}}, boom)
	if err != nil || string(got) != "abc" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestTIFFPredictor8(t *testing.T) {
	// Three RGB pixels, differenced horizontally.
	enc := []byte{10, 20, 30, 5, 5, 5, 1, 2, 3}
	want := []byte{10, 20, 30, 15, 25, 35, 16, 27, 38}
	parm := Dict{"Predictor": Integer(2), "Colors": Integer(3), "Columns": Integer(3)}
	got, err := applyPredictor(enc, parm, nil)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got % d, want % d (%v)", got, want, err)
	}
	// The input must not be modified in place.
	if enc[3] != 5 {
		t.Error("the encoded slice was overwritten")
	}
}

func TestTIFFPredictor16(t *testing.T) {
	// Two 16-bit greyscale samples: 0x0102 then a delta of 0x0001.
	enc := []byte{0x01, 0x02, 0x00, 0x01}
	want := []byte{0x01, 0x02, 0x01, 0x03}
	parm := Dict{"Predictor": Integer(2), "Colors": Integer(1),
		"BitsPerComponent": Integer(16), "Columns": Integer(2)}
	got, err := applyPredictor(enc, parm, nil)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x (%v)", got, want, err)
	}
}

// pngEncode applies one PNG row filter, so the decoder can be checked against
// data it did not itself produce.
func pngEncode(rows [][]byte, filters []byte, bpp int) []byte {
	var out []byte
	prev := make([]byte, len(rows[0]))
	for i, row := range rows {
		ft := filters[i]
		enc := make([]byte, len(row))
		for k := range row {
			var left, upLeft byte
			if k >= bpp {
				left, upLeft = row[k-bpp], prev[k-bpp]
			}
			switch ft {
			case 0:
				enc[k] = row[k]
			case 1:
				enc[k] = row[k] - left
			case 2:
				enc[k] = row[k] - prev[k]
			case 3:
				enc[k] = row[k] - byte((int(left)+int(prev[k]))/2)
			case 4:
				enc[k] = row[k] - paeth(left, prev[k], upLeft)
			}
		}
		out = append(out, ft)
		out = append(out, enc...)
		prev = row
	}
	return out
}

func TestPNGPredictorAllRowFilters(t *testing.T) {
	rows := [][]byte{
		{10, 20, 30, 40, 50, 60},
		{11, 22, 33, 44, 55, 66},
		{200, 100, 50, 25, 12, 6},
		{1, 2, 3, 4, 5, 6},
		{255, 254, 253, 252, 251, 250},
	}
	filters := []byte{0, 1, 2, 3, 4}
	parm := Dict{"Predictor": Integer(12), "Colors": Integer(3), "Columns": Integer(2)}
	enc := pngEncode(rows, filters, 3)
	got, err := applyPredictor(enc, parm, nil)
	if err != nil {
		t.Fatal(err)
	}
	var want []byte
	for _, r := range rows {
		want = append(want, r...)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got % d\nwant % d", got, want)
	}
}

func TestPNGPredictorUnknownRowFilter(t *testing.T) {
	parm := Dict{"Predictor": Integer(12), "Columns": Integer(2)}
	if _, err := applyPredictor([]byte{9, 1, 2}, parm, nil); err == nil {
		t.Error("want an error for row filter 9")
	}
}

func TestPNGPredictorTruncatedRow(t *testing.T) {
	// A file cut off mid-row keeps the row, zero-filled, rather than losing it.
	parm := Dict{"Predictor": Integer(12), "Columns": Integer(4)}
	got, err := applyPredictor([]byte{0, 1, 2}, parm, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{1, 2, 0, 0}) {
		t.Errorf("got % d", got)
	}
}

// ⛔⛔⛔ A ROW THE STREAM CANNOT BACK IS REFUSED, AND THE RATIO IS WHAT DECIDES.
// /Columns, /Colors and /BitsPerComponent are numbers the FILE chose and their
// product is a buffer size: pngPredictor allocates two rows of it and then
// walks every byte of each. Found by fuzzing the writer (go-pdfkit/reader#69),
// filed as #70 — 102 bytes carrying /Columns 977777777 /Predictor 10 ask for a
// row of about 932 MiB.
//
// ⛔ maxDecodedSize COULD NOT SEE IT. That cap is 1 GiB and the row is 932 MiB,
// just under: an absolute limit cannot express "a hundred bytes must not become
// a gigabyte". Only a comparison with the bytes present can.
//
// ⛔⛔ AND THE FIRST BOUND I WROTE WAS `rowLen > len(data)`, WHICH THE SUITE
// REFUTED IN ONE RUN: TestPNGPredictorTruncatedRow above decodes /Columns 4
// from three bytes, so a legitimately truncated single row already has a row
// longer than the whole stream. Salvage is deliberate here. The bound is
// therefore a FLOOR plus a ratio: under maxUnbackedRow, inventing the zeros a
// damaged row is missing costs nothing worth defending.
//
// ⚠ NO GOMEMLIMIT HARNESS, for the reason the page-count bomb test gives: with
// the bound in place the refusal happens BEFORE either row is allocated, so
// there is nothing to contain. The test that would need one is the test without
// the fix, and that one is the defect.
func TestAPredictorRowTheStreamCannotBackIsRefused(t *testing.T) {
	// ⛔ THE FLOOR IS SHRUNK rather than the fixture grown: the var exists for
	// this, like maxDecodedSize, and a real 1 MiB row in a test would be the
	// allocation this refuses to make.
	defer func(old int) { maxUnbackedRow = old }(maxUnbackedRow)
	maxUnbackedRow = 32

	t.Run("a row past the floor that the stream cannot back", func(t *testing.T) {
		parm := Dict{"Predictor": Integer(12), "Columns": Integer(4096)}
		_, err := applyPredictor([]byte{0, 1, 2}, parm, nil)
		if err == nil {
			t.Fatal("a 4096-byte row was built from three bytes")
		}
		if !strings.Contains(err.Error(), "not backed by the data it claims") {
			t.Errorf("refused, but not for being unbacked: %v", err)
		}
	})

	// ⛔⛔ THE CONTROL, AND IT IS THE WHOLE POINT: the SAME /Columns with the
	// data to back it is accepted. Without this, "refuses a big row" and
	// "refuses a row bigger than its data" are the same test, and the bound
	// could be a size cap wearing a ratio's name.
	t.Run("the same row with the bytes to back it", func(t *testing.T) {
		parm := Dict{"Predictor": Integer(12), "Columns": Integer(4096)}
		data := make([]byte, 1+4096)
		if _, err := applyPredictor(data, parm, nil); err != nil {
			t.Fatalf("a 4096-byte row backed by 4097 bytes was refused: %v", err)
		}
	})

	// ⛔ AND BELOW THE FLOOR, SALVAGE IS UNTOUCHED — the case that refuted the
	// first version of this bound, asserted here as a boundary rather than left
	// to the test above to notice.
	t.Run("a short row the stream cannot back is still salvaged", func(t *testing.T) {
		parm := Dict{"Predictor": Integer(12), "Columns": Integer(4)}
		got, err := applyPredictor([]byte{0, 1, 2}, parm, nil)
		if err != nil {
			t.Fatalf("a four-byte row from three bytes is salvage, not a bomb: %v", err)
		}
		if !bytes.Equal(got, []byte{1, 2, 0, 0}) {
			t.Errorf("got % d", got)
		}
	})
}

func TestPaeth(t *testing.T) {
	cases := []struct{ a, b, c, want byte }{
		{10, 20, 30, 10}, // a is closest
		{20, 10, 30, 10}, // b is closest
		{10, 30, 5, 30},  // b again, by the tie rule
		{200, 5, 3, 200},
	}
	for _, k := range cases {
		if got := paeth(k.a, k.b, k.c); got != k.want {
			t.Errorf("paeth(%d,%d,%d) = %d, want %d", k.a, k.b, k.c, got, k.want)
		}
	}
	// The third branch: c wins when neither a nor b is nearest.
	if got := paeth(1, 200, 100); got != 100 {
		t.Errorf("paeth(1,200,100) = %d, want 100", got)
	}
	if got := abs(-3); got != 3 {
		t.Errorf("abs(-3) = %d", got)
	}
}

func TestRowGeometry(t *testing.T) {
	rowLen, bpp := rowGeometry(3, 8, 4)
	if rowLen != 12 || bpp != 3 {
		t.Errorf("rowGeometry(3,8,4) = %d, %d", rowLen, bpp)
	}
	// Sub-byte components round up, both per row and per pixel.
	rowLen, bpp = rowGeometry(1, 1, 9)
	if rowLen != 2 || bpp != 1 {
		t.Errorf("rowGeometry(1,1,9) = %d, %d", rowLen, bpp)
	}
}

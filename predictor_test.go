package reader

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
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

// ⛔ rowGeometry's cases, kept against rowBytes, which replaced it. The
// geometry has not changed — a row is still the components rounded up, and the
// sample distance is that row with one column — only the arithmetic it is
// computed in has.
func TestRowBytes(t *testing.T) {
	for _, tc := range []struct {
		colors, bpc, columns int
		want                 int64
	}{
		{3, 8, 4, 12},
		{3, 8, 1, 3}, // the sample distance is a one-column row
		{1, 1, 9, 2}, // sub-byte components round up per row
		{1, 1, 1, 1}, // and per pixel
	} {
		got, _, err := rowBytes(tc.colors, tc.bpc, tc.columns)
		if err != nil {
			t.Fatalf("rowBytes(%d,%d,%d): %v", tc.colors, tc.bpc, tc.columns, err)
		}
		if got != tc.want {
			t.Errorf("rowBytes(%d,%d,%d) = %d, want %d",
				tc.colors, tc.bpc, tc.columns, got, tc.want)
		}
	}
}

// ⛔⛔⛔ THE PRODUCT CANNOT WRAP, AND THIS IS THE TEST THAT SAYS SO ON EVERY
// ARCHITECTURE. The bound added for #70 was BYPASSED by overflow: rowGeometry
// multiplied three file-chosen numbers in plain int, a large /Columns wrapped
// to a NEGATIVE row length, `rowLen > maxUnbackedRow` was false, and
// make([]byte, negative) panicked — the guard skipped by the very input it was
// written for. Found by external review.
//
// ⛔⛔ AND THE 32-BIT CASES ARE THE COMMITTED REPRODUCERS' OWN NUMBERS. There
// are TWO of them, each with its own case below, and /Colors 1
// /BitsPerComponent 8 is what both leave to the defaults:
//
//	3b1b3cdb9d35987f  /Columns 977777777  bits 7 822 222 216
//	                  int32: bits -767 712 376   row  -95 964 046
//	ccf3735d005f4673  /Columns 911111111  bits 7 288 888 888
//	                  int32: bits -1 301 045 704  row -162 630 712
//
// Both wrap NEGATIVE where int is 32 bits, so both PANICKED on linux/386 and
// linux/arm while CI, which vets those targets without running them, stayed
// green.
//
// ⛔⛔⛔ AND THIS COMMENT USED TO NAME THE WRONG ONE OF THE TWO: it printed
// 977777777's arithmetic and concluded "so ccf3735d005f4673 panicked", which is
// the seed carrying 911111111. The claim survived — both panicked — so nothing
// failed and nothing went red; only the pairing was false, which is the kind of
// error a passing suite cannot see. What let it hide: the second seed had NO
// CASE OF ITS OWN here, so a committed reproducer could not contradict a
// sentence that attributed its numbers elsewhere. Both are asserted now, and
// the figures above come from a program, not from arithmetic done by hand.
//
// ⭐ These assertions are architecture-independent BY CONSTRUCTION: rowBytes
// returns int64, so the value below is the true product on a 32-bit build too,
// and a wrap anywhere in the chain fails the comparison rather than hiding in
// the sign bit.
func TestARowTooLargeToMeasureIsRefusedRatherThanWrapped(t *testing.T) {
	// ⭐ ONE SUBTEST PER COMMITTED REPRODUCER, each naming its own seed. A single
	// case standing for both is what made the mislabelling above possible.
	for _, tc := range []struct {
		seed    string
		columns int
	}{
		{"3b1b3cdb9d35987f", 977777777},
		{"ccf3735d005f4673", 911111111},
	} {
		t.Run("testdata/fuzz/FuzzLinearize/"+tc.seed+", which wraps a 32-bit int", func(t *testing.T) {
			// ⭐⭐ THE PAIRING CHECKS ITSELF. Writing "seed X carries /Columns N"
			// in a table is the same gesture that mislabelled the comment above:
			// a name recalled beside a number measured. So the seed is READ and
			// asked whether it really holds this number — the assertion that
			// would have gone red the moment the two were swapped.
			path := filepath.Join("testdata", "fuzz", "FuzzLinearize", tc.seed)
			seed, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("the seed this case is named for could not be read: %v", err)
			}
			if want := strconv.Itoa(tc.columns); !bytes.Contains(seed, []byte(want)) {
				t.Fatalf("%s does not contain %s, so this case is named after the "+
					"wrong seed", path, want)
			}

			got, _, err := rowBytes(1, 8, tc.columns)
			if err != nil {
				t.Fatalf("a measurable row was refused: %v", err)
			}
			if got != int64(tc.columns) {
				t.Errorf("rowBytes(1,8,%d) = %d, want %d — a wrap", tc.columns, got, tc.columns)
			}
		})
	}

	// ⛔ EACH MULTIPLY HAS ITS OWN WITNESS, because each is a separate chance to
	// wrap and a guard with no case of its own is an untested claim.
	//
	// ⛔⛔ AND THEIR VALUES ARE BUILT AT RUNTIME, BEHIND A SKIP. Written as
	// constants they do not COMPILE where int is 32 bits — `go vet` on
	// linux/386 said so, which is the same trap
	// TestTheHintTablesRefuseAValueTooWideForItsField already documents. The
	// skip costs nothing here: a product past int64 cannot be expressed on a
	// 32-bit build at all, and the wrap that build really suffers is the case
	// ABOVE, whose numbers fit in an int32 and which therefore runs everywhere.
	t.Run("each multiply refuses rather than wrapping", func(t *testing.T) {
		if math.MaxInt <= math.MaxUint32 {
			t.Skip("int is 32 bits here, so a product past int64 cannot be built")
		}
		// ⛔ THROUGH VARIABLES, because `int(int64(math.MaxInt64))` is still a
		// CONSTANT expression and the compiler folds it before the skip above
		// can run: `constant 9223372036854775807 overflows int` on linux/386
		// and linux/arm, which is the same refusal one step later.
		var huge, wide int64 = math.MaxInt64, 1 << 60
		maxInt, bigColumns := int(huge), int(wide)
		for _, tc := range []struct {
			name                 string
			colors, bpc, columns int
		}{
			{"/Columns overflows the row", 1, 8, bigColumns},
			{"/Colors times /BitsPerComponent overflows first", maxInt, 2, 1},
			{"the row fits but rounding up does not", maxInt, 1, 1},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, _, err := rowBytes(tc.colors, tc.bpc, tc.columns)
				if err == nil {
					t.Fatal("an unmeasurable row was measured")
				}
				if !strings.Contains(err.Error(), "too large to measure") {
					t.Errorf("refused, but not for being unmeasurable: %v", err)
				}
			})
		}
	})

	t.Run("and the whole path refuses rather than panicking", func(t *testing.T) {
		// ⛔ THROUGH applyPredictor, because the panic was in make() and only
		// the whole path shows that nothing reaches it.
		//
		// ⭐ Integer is int64-wide, so this /Columns is expressible on a 32-bit
		// build and this case runs EVERYWHERE — unlike the rowBytes cases
		// above, whose arguments are plain ints. intParm is what narrows it,
		// and that narrowing is exactly what the guard has to survive.
		var wide int64 = 1 << 60
		parm := Dict{"Predictor": Integer(12), "Columns": Integer(wide)}
		if _, err := applyPredictor([]byte{0, 1, 2}, parm, nil); err == nil {
			t.Fatal("a 2^60-column row was decoded")
		}
	})
}

// FuzzApplyPredictor lets the fuzzer choose the four numbers the FILE chooses.
//
// ⛔⛔ IT EXISTS BECAUSE NO COMMITTED TARGET COULD EXPRESS THIS PR'S INPUT
// CLASS, and the two seeds above are the only subjects the bound ever had.
// FuzzDecode builds its own /Columns as `1 + int(colors>>2)%64`
// (fuzz_test.go), so its rows are at most about a kilobyte, and
// FuzzDecodeRecovering pins /Columns 4 (recover_test.go). Both reach
// applyPredictor; NEITHER can reach a row worth refusing. A bound defended by
// two static files is a bound tested at two points of a four-dimensional
// space — which is how the overflow survived the suite that was written for it.
//
// ⭐ The budget is the guard's own promise, not a copy of the loop: past the
// refusal a row is at most max(maxUnbackedRow, len(data)), a PNG row consumes
// its filter byte before its data, and a partial last row adds at most one
// more row — so the output cannot exceed len(data) + that maximum. Nothing
// here recomputes how many rows the decoder walks; this fails if the output
// grows without bound, which is the whole claim.
//
// ⚠ Deliberately NOT bounded like a bomb test (GOMEMLIMIT, ulimit -v): the
// refusal under test is what keeps the allocation small, so capping the
// allocation from outside would hide exactly the failure being hunted. A guard
// that stops working takes the worker down, and the fuzzer records the input.
func FuzzApplyPredictor(f *testing.F) {
	// The two committed reproducers' parameters, and ordinary shapes beside
	// them so a refusal that swallowed every input would show up as a failure
	// here rather than as a clean run.
	f.Add(10, 1, 8, 977777777, []byte("b100808"))
	f.Add(12, 1, 8, 911111111, []byte("b10traa802"))
	f.Add(12, 1, 8, 4, []byte{0, 1, 2, 3, 4})
	f.Add(2, 3, 8, 2, []byte{1, 2, 3, 4, 5, 6})
	f.Add(2, 1, 16, 2, []byte{0, 1, 0, 2})
	f.Add(1, 1, 8, 1, []byte{9})
	f.Add(10, 0, 0, 0, []byte{})

	f.Fuzz(func(t *testing.T, pred, colors, bpc, columns int, data []byte) {
		parm := Dict{
			"Predictor":        Integer(int64(pred)),
			"Colors":           Integer(int64(colors)),
			"BitsPerComponent": Integer(int64(bpc)),
			"Columns":          Integer(int64(columns)),
		}
		got, err := applyPredictor(data, parm, nil)
		if err != nil {
			if got != nil {
				t.Fatalf("refused and returned %d bytes anyway", len(got))
			}
			return
		}
		room := int64(maxUnbackedRow)
		if n := int64(len(data)); n > room {
			room = n
		}
		if budget := int64(len(data)) + room; int64(len(got)) > budget {
			t.Fatalf("/Predictor %d /Colors %d /BitsPerComponent %d /Columns %d "+
				"turned %d bytes into %d, past a ceiling of %d",
				pred, colors, bpc, columns, len(data), len(got), budget)
		}
	})
}

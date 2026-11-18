package reader

import (
	"errors"
	"strings"
	"testing"
)

// The tests here write a fax the way the specification does, as a string of
// '0' and '1', and read the result back as a string of 'W' and 'B'. Neither
// end of that is the decoder's own arithmetic, so a test that passes says the
// decoder agrees with the specification rather than with itself.

// faxBits assembles a fax from names: "W4" and "B2" are runs, "V0" and "VR2"
// and "P" and "H" are modes, "EOL" is an end-of-line code, and a run of digits
// is written out as it stands.
func faxBits(t *testing.T, parts ...string) []byte {
	t.Helper()
	var sb strings.Builder
	for _, p := range parts {
		switch {
		case p == "EOL":
			sb.WriteString("000000000001")
		case p == "P":
			sb.WriteString(codeBits(t, ccittModeCodes, modePass))
		case p == "H":
			sb.WriteString(codeBits(t, ccittModeCodes, modeH))
		case p == "EXT":
			sb.WriteString(codeBits(t, ccittModeCodes, modeExt))
		case strings.HasPrefix(p, "V"):
			sb.WriteString(codeBits(t, ccittModeCodes, modeNamed(t, p)))
		case p[0] == 'W' || p[0] == 'B':
			table := ccittWhiteCodes
			if p[0] == 'B' {
				table = ccittBlackCodes
			}
			sb.WriteString(codeBits(t, table, atoi(t, p[1:])))
		default:
			for _, c := range p {
				if c != '0' && c != '1' {
					t.Fatalf("faxBits: %q is not a name or a run of bits", p)
				}
			}
			sb.WriteString(p)
		}
	}
	// Pad the last byte with zeros, which is what a fax does and what the
	// decoder must not read as another row.
	bits := sb.String()
	out := make([]byte, (len(bits)+7)/8)
	for i, c := range bits {
		if c == '1' {
			out[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	return out
}

func codeBits(t *testing.T, table []ccittCode, value int) string {
	t.Helper()
	for _, c := range table {
		if c.value == value {
			return c.bits
		}
	}
	t.Fatalf("codeBits: no code for %d", value)
	return ""
}

func modeNamed(t *testing.T, s string) int {
	t.Helper()
	switch s {
	case "V0":
		return modeV0
	case "VR1":
		return modeVR1
	case "VR2":
		return modeVR2
	case "VR3":
		return modeVR3
	case "VL1":
		return modeVL1
	case "VL2":
		return modeVL2
	case "VL3":
		return modeVL3
	}
	t.Fatalf("modeNamed: %q", s)
	return 0
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("atoi: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// faxRows reads decoded samples back as 'W' and 'B' per pixel, one string a
// row. With /BlackIs1 false — the default — a 0 bit is black, which is the
// convention this asserts.
func faxRows(t *testing.T, data []byte, columns int, blackIs1 bool) []string {
	t.Helper()
	stride := (columns + 7) / 8
	var out []string
	for i := 0; i+stride <= len(data); i += stride {
		var sb strings.Builder
		for x := 0; x < columns; x++ {
			set := data[i+x/8]&(1<<(7-uint(x%8))) != 0
			if set == blackIs1 {
				sb.WriteByte('B')
			} else {
				sb.WriteByte('W')
			}
		}
		out = append(out, sb.String())
	}
	return out
}

func decodeFax(t *testing.T, data []byte, p ccittParams) []string {
	t.Helper()
	out, err := ccittDecode(data, p)
	if err != nil {
		t.Fatalf("ccittDecode: %v", err)
	}
	return faxRows(t, out, p.columns, p.blackIs1)
}

func g4(columns int) ccittParams {
	return ccittParams{k: -1, columns: columns, endOfBlock: true}
}

func TestAGroup4RowInHorizontalMode(t *testing.T) {
	// Horizontal mode reads two runs and pays no attention to the row above,
	// so it is the one mode that can start a picture from nothing.
	data := faxBits(t, "H", "W3", "B5")
	got := decodeFax(t, data, g4(8))
	want := []string{"WWWBBBBB"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAGroup4RowCopiesTheOneAboveIt(t *testing.T) {
	// V0 says the colour changes exactly where it changed on the row above,
	// which is how a fax of a form spends most of its bits.
	data := faxBits(t, "H", "W3", "B5", "V0", "V0")
	got := decodeFax(t, data, g4(8))
	if len(got) != 2 || got[0] != "WWWBBBBB" || got[1] != "WWWBBBBB" {
		t.Errorf("got %q, want two rows of WWWBBBBB", got)
	}
}

func TestTheVerticalModesMoveTheChangeSideways(t *testing.T) {
	// Each vertical mode puts the change at b1 plus its own offset. Against a
	// first row that changes at pixel 3, VR1 changes at 4 and VL1 at 2.
	for _, tc := range []struct {
		mode string
		want string
	}{
		{"V0", "WWWBBBBB"},
		{"VR1", "WWWWBBBB"},
		{"VR2", "WWWWWBBB"},
		{"VR3", "WWWWWWBB"},
		{"VL1", "WWBBBBBB"},
		{"VL2", "WBBBBBBB"},
		{"VL3", "BBBBBBBB"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			data := faxBits(t, "H", "W3", "B5", tc.mode, "V0")
			got := decodeFax(t, data, g4(8))
			if len(got) != 2 {
				t.Fatalf("got %d rows, want 2: %q", len(got), got)
			}
			if got[1] != tc.want {
				t.Errorf("%s gave %q, want %q", tc.mode, got[1], tc.want)
			}
		})
	}
}

func TestGroup4PassMode(t *testing.T) {
	// Pass mode says the white run under way reaches at least as far as b2:
	// the black run on the row above is passed over rather than followed.
	// Against a first row of three white and five black, b2 is the end of the
	// row, so one pass mode fills the whole second row white and finishes it —
	// which is why nothing follows it here.
	data := faxBits(t, "H", "W3", "B5", "P")
	got := decodeFax(t, data, g4(8))
	if len(got) != 2 {
		t.Fatalf("got %d rows: %q", len(got), got)
	}
	if got[1] != "WWWWWWWW" {
		t.Errorf("pass mode gave %q, want the black above passed over", got[1])
	}
}

func TestAGroup3OneDimensionalRow(t *testing.T) {
	// Group 3 with K = 0 is nothing but runs, white first, and a row may be
	// followed by an end-of-line code or by nothing at all.
	for _, tc := range []struct {
		name  string
		parts []string
	}{
		{"with end-of-line codes", []string{"W2", "B6", "EOL", "W4", "B4"}},
		{"without", []string{"W2", "B6", "W4", "B4"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeFax(t, faxBits(t, tc.parts...), ccittParams{
				k: 0, columns: 8, endOfBlock: true})
			if len(got) != 2 || got[0] != "WWBBBBBB" || got[1] != "WWWWBBBB" {
				t.Errorf("got %q", got)
			}
		})
	}
}

func TestAGroup3MixedRowSaysWhichKindItIs(t *testing.T) {
	// With K > 0 a bit after the end-of-line says whether the row that follows
	// is one-dimensional (1) or two-dimensional (0).
	data := faxBits(t, "EOL", "1", "W3", "B5", "EOL", "0", "V0", "V0")
	got := decodeFax(t, data, ccittParams{k: 1, columns: 8, endOfBlock: true})
	if len(got) != 2 || got[0] != "WWWBBBBB" || got[1] != "WWWBBBBB" {
		t.Errorf("got %q, want two rows of WWWBBBBB", got)
	}
}

func TestBlackIs1TurnsTheSamplesOver(t *testing.T) {
	data := faxBits(t, "H", "W3", "B5")
	p := g4(8)
	p.blackIs1 = true
	got := decodeFax(t, data, p)
	if len(got) != 1 || got[0] != "WWWBBBBB" {
		t.Errorf("got %q: the picture must not change, only the bits that say it", got)
	}
	// And the bits themselves are the other way round.
	out, err := ccittDecode(data, p)
	if err != nil {
		t.Fatal(err)
	}
	if out[0] != 0b00011111 {
		t.Errorf("first byte is %08b, want 00011111", out[0])
	}
}

func TestEncodedByteAlignStartsEveryRowOnAByte(t *testing.T) {
	// "H W3 B5" is 3 + 4 + 4 = 11 bits, so the second row would begin
	// mid-byte. With the flag set the decoder skips to the boundary, and the
	// bits in between are ignored rather than read as a mode.
	first := "001" + codeBits(t, ccittWhiteCodes, 3) + codeBits(t, ccittBlackCodes, 5)
	pad := strings.Repeat("0", 8-len(first)%8)
	data := faxBits(t, first+pad, codeBits(t, ccittModeCodes, modeV0),
		codeBits(t, ccittModeCodes, modeV0))
	p := g4(8)
	p.encodedByteAlign = true
	got := decodeFax(t, data, p)
	if len(got) != 2 || got[0] != "WWWBBBBB" || got[1] != "WWWBBBBB" {
		t.Errorf("got %q", got)
	}
}

func TestRowsGivenIsHonouredExactly(t *testing.T) {
	// A file that says how many rows it has gets that many: short data is
	// padded with white, so the samples match the /Height the dictionary
	// promises rather than being a row short.
	data := faxBits(t, "H", "W3", "B5")
	p := g4(8)
	p.rows = 3
	got := decodeFax(t, data, p)
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3: %q", len(got), got)
	}
	if got[1] != "WWWWWWWW" || got[2] != "WWWWWWWW" {
		t.Errorf("the padding rows are %q and %q, want white", got[1], got[2])
	}
}

func TestAMakeUpCodeAddsToTheRunBeforeIt(t *testing.T) {
	// A run longer than 63 is a make-up code and then a terminating one. 64
	// plus 0 is the shortest way to say sixty-four.
	data := faxBits(t, "H", "W64", "W0", "B1")
	got := decodeFax(t, data, g4(65))
	if len(got) != 1 {
		t.Fatalf("got %d rows: %q", len(got), got)
	}
	if got[0] != strings.Repeat("W", 64)+"B" {
		t.Errorf("got %q", got[0])
	}
}

func TestATruncatedFaxComesBackAsFarAsItGot(t *testing.T) {
	// A damaged scan is worth showing: the rows already decoded are real, and
	// refusing them turns a form into a blank page.
	data := faxBits(t, "H", "W3", "B5", "V0", "V0", "H", "W3")
	got := decodeFax(t, data, g4(8))
	if len(got) < 2 {
		t.Fatalf("got %d rows, want at least the two that decoded: %q", len(got), got)
	}
	if got[0] != "WWWBBBBB" {
		t.Errorf("the first row came back as %q", got[0])
	}
}

func TestAFaxThatSaysNothingSensibleIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		p    ccittParams
	}{
		{"no columns", nil, ccittParams{k: -1, columns: 0}},
		{"negative rows", nil, ccittParams{k: -1, columns: 8, rows: -1}},
		// ⛔ THE WIDTH IS INSIDE maxCCITTColumns ON PURPOSE. This case is for
		// the PAIR check — columns times rows past the image budget — and
		// 2^20 columns is now refused for its width before that check is
		// reached, which would leave the pair check with no subject. 2^16 by
		// 2^13 is 2^29 pixels: a legal width, an illegal area.
		{"more pixels than anyone can want",
			nil, ccittParams{k: -1, columns: 1 << 16, rows: 1 << 13}},
		{"an extension mode", faxBits(t, "EXT", "000"), g4(8)},
		{"a run longer than the row", faxBits(t, "H", "W63", "B63"), g4(8)},
		{"a mode this reader does not know", faxBits(t, "00000001"), g4(8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ccittDecode(tc.data, tc.p); err == nil {
				t.Error("no error")
			}
		})
	}
}

// ⛔⛔ /Rows IS OPTIONAL, AND WITHOUT IT NOTHING BOUNDED /Columns BEFORE THE
// ALLOCATION. The case above it — 2^20 by 2^20 — is refused by the guard that
// reads `p.rows > 0 && p.columns > maxCCITTPixels/p.rows`, and that conjunction
// is the hole: /Rows DEFAULTS TO ZERO, so the ordinary file skips the check
// entirely. `make([]byte, p.columns)` then runs first, and the in-loop budget
// that would have caught it is tested only once a row has already been
// allocated.
//
// ⭐ The limit is lowered rather than the columns raised, so this test proves
// the ordering with a megabyte instead of a fatal allocation. Found by the
// audit #73 asked for, which is the same class: a count the file chose, used
// as a size, with the only guard on a different parameter.
func TestColumnsAreBoundedEvenWhenRowsIsAbsent(t *testing.T) {
	defer func(old int) { maxCCITTColumns = old }(maxCCITTColumns)
	maxCCITTColumns = 1 << 10

	t.Run("no rows named, and the columns are past the width limit", func(t *testing.T) {
		_, err := ccittDecode(nil, ccittParams{k: -1, columns: 1 << 20})
		if err == nil {
			t.Fatal("a row of 2^20 bytes was allocated although the limit is 2^10 columns")
		}
		// ⛔ THE MESSAGE, NOT MERELY "AN ERROR". This whole test exists because
		// a refusal arriving from somewhere else reads exactly like the one
		// being tested — which is what the repair below is about.
		if !strings.Contains(err.Error(), "names 1048576 columns") {
			t.Errorf("refused for the wrong reason: %v", err)
		}
	})

	// ⛔ THE CONTROL, because a refusal that fires on everything is not a bound:
	// a fax within the budget must still be read, with /Rows just as absent.
	t.Run("no rows named, and the columns fit", func(t *testing.T) {
		if _, err := ccittDecode(make([]byte, 64), ccittParams{k: -1, columns: 8}); err != nil {
			t.Errorf("an 8-column fax inside the budget was refused: %v", err)
		}
	})
}

func TestAFaxWithNoRowsAtAllDecodesToNothing(t *testing.T) {
	// All-zero data is fill, not a picture. Zeros decode as perfectly good
	// modes — pass mode is 0001 — so a decoder that does not stop here invents
	// rows for as long as it is asked to.
	out, err := ccittDecode(make([]byte, 64), g4(8))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("%d bytes out of nothing but fill", len(out))
	}
}

func TestTheBlockMarkerEndsTheData(t *testing.T) {
	// Two end-of-line codes in a row are Group 4's way of saying it has
	// finished; six are Group 3's. What follows them is not a row.
	g4Data := faxBits(t, "H", "W3", "B5", "EOL", "EOL", "H", "W1", "B7")
	if got := decodeFax(t, g4Data, g4(8)); len(got) != 1 {
		t.Errorf("Group 4 gave %d rows past its block marker: %q", len(got), got)
	}
	g3 := []string{"W3", "B5"}
	for i := 0; i < 6; i++ {
		g3 = append(g3, "EOL")
	}
	g3 = append(g3, "W1", "B7")
	got := decodeFax(t, faxBits(t, g3...), ccittParams{k: 0, columns: 8, endOfBlock: true})
	if len(got) != 1 {
		t.Errorf("Group 3 gave %d rows past its block marker: %q", len(got), got)
	}
	if endOfBlockEOLs(-1) != 2 || endOfBlockEOLs(0) != 6 || endOfBlockEOLs(4) != 6 {
		t.Error("endOfBlockEOLs disagrees with the two cases above")
	}
}

func TestEndOfBlockCanBeTurnedOff(t *testing.T) {
	// A file may say its data has no block marker, in which case the codes
	// that would be one are read as whatever they are.
	p := g4(8)
	p.endOfBlock = false
	p.rows = 1
	got := decodeFax(t, faxBits(t, "H", "W3", "B5"), p)
	if len(got) != 1 || got[0] != "WWWBBBBB" {
		t.Errorf("got %q", got)
	}
}

func TestTheDecodeParametersComeFromTheDictionary(t *testing.T) {
	got := ccittParamsOf(Dict{
		"K":                Integer(-1),
		"Columns":          Integer(120),
		"Rows":             Integer(30),
		"BlackIs1":         Bool(true),
		"EncodedByteAlign": Bool(true),
		"EndOfBlock":       Bool(false),
	}, nil)
	want := ccittParams{k: -1, columns: 120, rows: 30, blackIs1: true,
		encodedByteAlign: true, endOfBlock: false}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	// No dictionary at all means the defaults the specification gives.
	def := ccittParamsOf(nil, nil)
	if def != (ccittParams{k: 0, columns: 1728, rows: 0, blackIs1: false,
		encodedByteAlign: false, endOfBlock: true}) {
		t.Errorf("defaults are %+v", def)
	}
	// A parameter of the wrong kind is not a parameter.
	if boolParm(Dict{"BlackIs1": Integer(1)}, "BlackIs1", false, nil) {
		t.Error("an integer was read as a boolean")
	}
	if !boolParm(Dict{"BlackIs1": Ref{Num: 9}}, "BlackIs1", true, failingResolver) {
		t.Error("a reference that will not resolve did not fall back to the default")
	}
}

// failingResolver refuses every reference, which is what a damaged file's
// cross-reference table amounts to.
func failingResolver(Ref) (Object, error) {
	return nil, errors.New("reader: no such object")
}

func TestAFaxReachesTheCallerThroughTheFilterChain(t *testing.T) {
	// The point of decoding here rather than in a renderer: /CCITTFaxDecode is
	// no longer handed back encoded, so every caller gets samples.
	data := faxBits(t, "H", "W3", "B5")
	out, img, err := Decode(Dict{
		"Filter":      Name("CCITTFaxDecode"),
		"DecodeParms": Dict{"K": Integer(-1), "Columns": Integer(8)},
	}, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if img != "" {
		t.Errorf("the chain stopped at /%s", img)
	}
	if len(out) != 1 || out[0] != 0b11100000 {
		t.Errorf("got %08b", out)
	}
	// The abbreviated name an inline image uses reaches the same code.
	out2, _, err := Decode(Dict{
		"Filter":      Name("CCF"),
		"DecodeParms": Dict{"K": Integer(-1), "Columns": Integer(8)},
	}, data, nil)
	if err != nil || len(out2) != 1 || out2[0] != out[0] {
		t.Errorf("/CCF gave %08b, %v", out2, err)
	}
}

// ⭐ THE GRID THE FUZZ TARGET USED TO CARRY, AS A TEST, WHICH IS WHAT IT WAS.
// FuzzCCITTDecode ran three K values against three widths on every input — a
// deterministic cross-product, not fuzzing, and the only thing reaching four
// branches of the decoder. Freeing the fuzzer to choose those parameters took
// the grid away with it and the blocks went uncovered, so the grid lives here
// where it is honest about being a table, and the fuzzer keeps the space.
func TestCCITTDecodeOverAGridOfParameters(t *testing.T) {
	for _, data := range [][]byte{
		faxBits(t, "H", "W3", "B5"),
		{0, 0, 0, 0},
		{0xff, 0xff},
	} {
		for _, k := range []int{-1, 0, 1} {
			for _, cols := range []int{1, 8, 1728} {
				out, err := ccittDecode(data, ccittParams{k: k, columns: cols,
					rows: 4, endOfBlock: true})
				if err == nil && len(out) != 4*((cols+7)/8) {
					t.Errorf("K=%d columns=%d gave %d bytes for 4 rows",
						k, cols, len(out))
				}
			}
		}
	}
}

// ⛔⛔ THE FUZZER CHOOSES k, rows AND columns, AND IT DID NOT USED TO. This
// target pinned `rows: 4` and `columns ∈ {1, 8, 1728}`, so it could not express
// the one shape that mattered: /Rows ABSENT with a large /Columns, which is
// exactly where the guard above was missing. That is the same gap FuzzDecode
// had on #72 — a target whose own parameters exclude the defect class — and it
// is why the bound shipped unfuzzed. Found in review.
func FuzzCCITTDecode(f *testing.F) {
	f.Add(faxBits(f2t(f), "H", "W3", "B5"), -1, 8, 4)
	f.Add([]byte{0, 0, 0, 0}, 0, 1728, 4)
	f.Add([]byte{0xff, 0xff}, 1, 1, 0)
	// The shape the old target could not reach: no rows named, a huge width.
	f.Add([]byte{0, 0}, -1, 1<<24, 0)
	f.Add([]byte{0, 0}, -1, 1, 0)

	f.Fuzz(func(t *testing.T, data []byte, k, cols, rows int) {
		out, err := ccittDecode(data, ccittParams{k: k, columns: cols,
			rows: rows, endOfBlock: true})
		if err != nil {
			return
		}
		// ⭐ The size invariant needs a stated height: with /Rows absent the
		// decoder reads until the data runs out, so there is no row count to
		// check against. The bound that governs THAT case is the in-loop pixel
		// budget, asserted by its own test.
		if rows > 0 && len(out) != rows*((cols+7)/8) {
			t.Fatalf("K=%d columns=%d rows=%d gave %d bytes",
				k, cols, rows, len(out))
		}
		// ⛔ And whatever was decoded must fit the width that was allowed:
		// a row past the limit should have been refused, not produced.
		if cols > maxCCITTColumns {
			t.Fatalf("K=%d columns=%d was decoded although the limit is %d",
				k, cols, maxCCITTColumns)
		}
	})
}

// f2t lets the fuzz seed use the helpers, which take a *testing.T.
func f2t(f *testing.F) *testing.T { return &testing.T{} }

func TestTheChangingElementsAreFoundPartWayThroughARow(t *testing.T) {
	// b1 and b2 are found by walking the row above from where the pen is. The
	// interesting case is a mode that is not the first of its row, because
	// then the walk starts inside the row rather than before it — and the pen
	// may be either colour by that point.
	//
	// The first row here changes four times, so the second row's four vertical
	// modes each start their walk from a different place and from alternating
	// colours; the third row's pass mode is preceded by a vertical one, so it
	// too begins part way along.
	data := faxBits(t,
		"H", "W4", "B4", "H", "W4", "B4", // WWWWBBBBWWWWBBBB
		"V0", "V0", "V0", "V0", // copied exactly
		"V0", "P", "V0", // white to 4, then pass over the black above
	)
	got := decodeFax(t, data, g4(16))
	if len(got) != 3 {
		t.Fatalf("got %d rows: %q", len(got), got)
	}
	if got[0] != "WWWWBBBBWWWWBBBB" {
		t.Errorf("first row %q", got[0])
	}
	if got[1] != "WWWWBBBBWWWWBBBB" {
		t.Errorf("second row %q, want the first copied", got[1])
	}
	if got[2] != "WWWWBBBBBBBBBBBB" {
		t.Errorf("third row %q, want the black above passed over", got[2])
	}
}

func TestAFaxIsBoundedEvenWhenItSaysNothingAboutItsHeight(t *testing.T) {
	// A fax that does not say how many rows it has is decoded until the data
	// runs out, so the bound is the only thing standing between a file and the
	// heap. The bound is lowered here rather than a hundred megabytes of fax
	// written, which is what maxDecodedSize does for Flate.
	was := maxCCITTPixels
	maxCCITTPixels = 16
	defer func() { maxCCITTPixels = was }()

	// Four rows of eight pixels is thirty-two, which is past sixteen.
	data := faxBits(t, "H", "W3", "B5", "V0", "V0", "V0", "V0", "V0", "V0")
	if _, err := ccittDecode(data, g4(8)); err == nil {
		t.Error("no error from a fax past the bound")
	}
	// A run may be built up from make-up codes without ever terminating, and
	// is bounded the same way.
	//
	// ⛔⛔ THE COLUMN COUNT HERE IS 100, NOT 1728, AND THE REASON IS THE POINT.
	// It was g4(1728) while the budget was 100, so once /Columns gained a bound
	// of its own this case was refused for ITS columns and never reached the
	// run at all — and the assertion is `err == nil`, which a different error
	// satisfies in silence. The test went on passing while its subject went
	// untested; `covblocks` caught it as one uncovered block, the coverage
	// percentage did not (it still read 100.0%, rounded — see #49).
	// A bound must be tested through the path it bounds.
	maxCCITTPixels = 100
	long := []string{"H"}
	for i := 0; i < 8; i++ {
		long = append(long, "W64")
	}
	err := func() error {
		_, err := ccittDecode(faxBits(t, long...), g4(100))
		return err
	}()
	if err == nil {
		t.Fatal("no error from a run past the bound")
	}
	// ⛔ AND THE MESSAGE IS ASSERTED, WHICH IS THE OTHER HALF OF THE LESSON.
	// "some error" is what let this case go on passing while its subject went
	// untested; naming the refusal is what makes it a witness for the RUN
	// bound rather than for whichever check happens to fire first.
	if !strings.Contains(err.Error(), "named a run of") {
		t.Errorf("refused for the wrong reason, so the run bound is still untested: %v", err)
	}
}

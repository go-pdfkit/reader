package reader

import (
	"bytes"
	"compress/lzw"
	"testing"
)

// TestLZWSpecExample decodes the worked example from the PDF specification,
// which is the one external anchor for this decoder: the encoded bytes and the
// data they stand for are both printed there.
func TestLZWSpecExample(t *testing.T) {
	encoded := []byte{0x80, 0x0B, 0x60, 0x50, 0x22, 0x0C, 0x0C, 0x85, 0x01}
	want := []byte{45, 45, 45, 45, 45, 65, 45, 45, 45, 66}
	got, err := lzwDecode(encoded, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got % x, want % x", got, want)
	}
}

// TestLZWAgainstStdlibWriter checks the decoder against an encoder nobody here
// wrote: compress/lzw in MSB order is the same algorithm without PDF's early
// code-width change, so it is a genuine oracle for the EarlyChange 0 case.
func TestLZWAgainstStdlibWriter(t *testing.T) {
	want := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 200)
	var buf bytes.Buffer
	w := lzw.NewWriter(&buf, lzw.MSB, 8)
	if _, err := w.Write(want); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := lzwDecode(buf.Bytes(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip differs: got %d bytes, want %d", len(got), len(want))
	}
	// And the whole point of the parameter: reading the same bytes with the
	// early change on must NOT give the same answer.
	if other, err := lzwDecode(buf.Bytes(), true); err == nil && bytes.Equal(other, want) {
		t.Error("EarlyChange made no difference; the width schedule is not being applied")
	}
}

// lzwEncode is a test-only encoder for both width conventions, so the early
// change can be exercised past the 511-, 1023- and 2047-entry boundaries that
// a short example never reaches.
func lzwEncode(data []byte, early bool) []byte {
	var out []byte
	bit, width, next := 0, 9, lzwFirst
	emit := func(code int) {
		for k := width - 1; k >= 0; k-- {
			if bit/8 >= len(out) {
				out = append(out, 0)
			}
			if code>>k&1 == 1 {
				out[bit/8] |= 1 << (7 - bit%8)
			}
			bit++
		}
	}
	widen := func() {
		// The decoder is one entry behind: it learns an entry only when it
		// reads the code that follows the one that created it.
		count := next - 1
		if early {
			count++
		}
		switch {
		case count >= 2048:
			width = 12
		case count >= 1024:
			width = 11
		case count >= 512:
			width = 10
		default:
			width = 9
		}
	}
	table := map[string]int{}
	codeOf := func(w []byte) int {
		if len(w) == 1 {
			return int(w[0])
		}
		return table[string(w)]
	}
	emit(lzwClear)
	var w []byte
	for _, c := range data {
		wc := append(append([]byte{}, w...), c)
		if len(wc) == 1 || codeOf(wc) != 0 {
			w = wc
			continue
		}
		emit(codeOf(w))
		if next < lzwMax {
			table[string(wc)] = next
			next++
		}
		widen()
		w = []byte{c}
	}
	if len(w) > 0 {
		emit(codeOf(w))
	}
	emit(lzwEOD)
	return out
}

func TestLZWEarlyChangeAcrossWidths(t *testing.T) {
	// Enough distinct sequences to push the table past 2048 entries, so all
	// three width steps are crossed.
	var data []byte
	for i := 0; i < 6000; i++ {
		data = append(data, byte(i), byte(i>>8), byte(i*7), byte(i>>3))
	}
	for _, early := range []bool{true, false} {
		got, err := lzwDecode(lzwEncode(data, early), early)
		if err != nil {
			t.Fatalf("early=%v: %v", early, err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("early=%v: round trip differs (%d bytes vs %d)", early, len(got), len(data))
		}
	}
}

func TestLZWClearAndTruncation(t *testing.T) {
	// A clear code resets the table mid-stream.
	data := bytes.Repeat([]byte("abcabcabc"), 100)
	enc := lzwEncode(data, true)
	if got, err := lzwDecode(enc, true); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("round trip: %v", err)
	}
	// A stream cut short keeps whatever it managed to say rather than failing.
	short, err := lzwDecode(enc[:len(enc)/2], true)
	if err != nil {
		t.Fatalf("truncated stream: %v", err)
	}
	if len(short) == 0 || len(short) >= len(data) {
		t.Errorf("truncated stream decoded %d bytes of %d", len(short), len(data))
	}
	// No bits at all is an empty result, not an error.
	if got, err := lzwDecode(nil, true); err != nil || len(got) != 0 {
		t.Errorf("empty: %v %q", err, got)
	}
}

func TestLZWInvalidCode(t *testing.T) {
	// These bits are the 9-bit codes 256 (clear) then 258, which cannot be in a
	// table that has just been reset.
	if _, err := lzwDecode([]byte{0x80, 0x40, 0x80}, true); err == nil {
		t.Error("want an error for a code outside the table")
	}
}

package reader

import (
	"bytes"
	"testing"
)

func TestInlineImageEndByArithmetic(t *testing.T) {
	// Three grey pixels at eight bits each: exactly three bytes, and the EI
	// that follows confirms it even though the data spells EI itself.
	d := Dict{"W": Integer(3), "H": Integer(1), "BPC": Integer(8), "CS": Name("G")}
	b := []byte("EIx EI more")
	end, err := inlineImageEnd(b, 0, d)
	if err != nil || end != 3 {
		t.Fatalf("end = %d, %v", end, err)
	}
}

func TestInlineImageEndByDeclaredLength(t *testing.T) {
	// No arithmetic is possible because the colour space is a resource name,
	// but /L says how long the data is.
	d := Dict{"CS": Name("Cs1"), "L": Integer(4)}
	b := []byte("ab\x00d EI rest")
	end, err := inlineImageEnd(b, 0, d)
	if err != nil || end != 4 {
		t.Fatalf("end = %d, %v", end, err)
	}
	// A length that runs past the buffer, or that no EI follows, is ignored.
	if _, err := inlineImageEnd([]byte("abcd"), 0, Dict{"CS": Name("Cs1"), "L": Integer(99)}); err == nil {
		t.Error("want an error")
	}
	if end, err := inlineImageEnd([]byte("abcd EI"), 0, Dict{"CS": Name("Cs1"), "L": Integer(2)}); err != nil || end != 4 {
		t.Errorf("a lying /L: end = %d, %v", end, err)
	}
	// A negative length is not a length.
	if end, err := inlineImageEnd([]byte("abcd EI"), 0, Dict{"CS": Name("Cs1"), "L": Integer(-2)}); err != nil || end != 4 {
		t.Errorf("a negative /L: end = %d, %v", end, err)
	}
}

func TestInlineImageEndWithoutSpaceBeforeEI(t *testing.T) {
	// The keyword run straight into the data: only the second pass finds it.
	d := Dict{"CS": Name("Cs1")}
	end, err := inlineImageEnd([]byte("abcdEI rest"), 0, d)
	if err != nil || end != 4 {
		t.Fatalf("end = %d, %v", end, err)
	}
}

func TestInlineImageEndValidatesFilteredData(t *testing.T) {
	// Compressed bytes that happen to spell EI must not end the image; only
	// the candidate whose data really inflates does.
	payload := deflate(bytes.Repeat([]byte("sample"), 8))
	var b bytes.Buffer
	b.Write(payload)
	b.WriteString(" EI rest")
	d := Dict{"F": Name("Fl"), "CS": Name("Cs1")}
	end, err := inlineImageEnd(b.Bytes(), 0, d)
	if err != nil {
		t.Fatal(err)
	}
	if end != len(payload) {
		t.Errorf("end = %d, want %d", end, len(payload))
	}
	// A decoy EI inside the data is stepped over: the first candidate does
	// not decode to the number of samples the image says it has, the second
	// does. Run-length data is used because its bytes can be chosen exactly.
	decoy := []byte{2, ' ', 'E', 'I', 128, ' ', 'E', 'I', ' ', 'r'}
	rl := Dict{"F": Name("RL"), "W": Integer(3), "H": Integer(1), "BPC": Integer(8), "CS": Name("G")}
	end, err = inlineImageEnd(decoy, 0, rl)
	if err != nil || end != 5 {
		t.Errorf("decoy: end = %d, %v", end, err)
	}
	// Data that never decodes has no end at all.
	if _, err := inlineImageEnd([]byte("junk EI junk EI"), 0, Dict{"F": Name("Fl"), "W": Integer(300), "H": Integer(1), "BPC": Integer(8), "CS": Name("G")}); err == nil {
		t.Error("data that never inflates should have no end")
	}
}
func TestInlineDataDecodes(t *testing.T) {
	// No filter: anything goes, the length having been settled elsewhere.
	if !inlineDataDecodes(Dict{}, []byte("x"), 0, false) {
		t.Error("unfiltered data was refused")
	}
	// A JPEG is checked by its markers, which nothing here can decode.
	jpeg := []byte{0xFF, 0xD8, 0x01, 0x02, 0xFF, 0xD9}
	if !inlineDataDecodes(Dict{"Filter": Name("DCTDecode")}, jpeg, 0, false) {
		t.Error("a well-formed JPEG was refused")
	}
	if inlineDataDecodes(Dict{"Filter": Name("DCTDecode")}, []byte{0xFF, 0xD8, 0x00}, 0, false) {
		t.Error("a truncated JPEG was accepted")
	}
	// Another image filter cannot be checked at all, so it is believed.
	if !inlineDataDecodes(Dict{"Filter": Name("JPXDecode")}, []byte("x"), 0, false) {
		t.Error("a JPEG 2000 image was refused")
	}
	// A byte filter that fails is a refusal.
	if inlineDataDecodes(Dict{"Filter": Name("FlateDecode")}, []byte("not deflate"), 0, false) {
		t.Error("data that does not inflate was accepted")
	}
	// And one that succeeds but yields the wrong number of samples.
	good := deflate([]byte("1234"))
	if inlineDataDecodes(Dict{"Filter": Name("FlateDecode")}, good, 99, true) {
		t.Error("the wrong sample count was accepted")
	}
	if !inlineDataDecodes(Dict{"Filter": Name("FlateDecode")}, good, 4, true) {
		t.Error("the right sample count was refused")
	}
}

func TestInlineComponents(t *testing.T) {
	cases := []struct {
		d    Dict
		n    int
		know bool
	}{
		{Dict{"IM": Bool(true)}, 1, true},
		{Dict{"IM": Bool(false), "CS": Name("RGB")}, 3, true},
		{Dict{"CS": Name("G")}, 1, true},
		{Dict{"CS": Name("DeviceGray")}, 1, true},
		{Dict{"CS": Name("CalRGB")}, 3, true},
		{Dict{"CS": Name("CMYK")}, 4, true},
		{Dict{"CS": Name("DeviceCMYK")}, 4, true},
		{Dict{"CS": Name("I")}, 1, true},
		{Dict{"CS": Array{Name("Indexed")}}, 1, true},
		{Dict{"CS": Name("Cs1")}, 0, false},
		{Dict{}, 0, false},
	}
	for _, c := range cases {
		n, ok := inlineComponents(c.d)
		if n != c.n || ok != c.know {
			t.Errorf("%v: got %d, %v", c.d, n, ok)
		}
	}
}

func TestInlineSampleBytes(t *testing.T) {
	cases := []struct {
		d    Dict
		n    int
		know bool
	}{
		{Dict{"W": Integer(3), "H": Integer(2), "CS": Name("RGB")}, 18, true},
		{Dict{"W": Integer(9), "H": Integer(1), "IM": Bool(true)}, 2, true},
		{Dict{"W": Integer(4), "H": Integer(1), "BPC": Integer(4), "CS": Name("G")}, 2, true},
		{Dict{"H": Integer(1), "CS": Name("G")}, 0, false},
		{Dict{"W": Integer(1), "CS": Name("G")}, 0, false},
		{Dict{"W": Integer(0), "H": Integer(1), "CS": Name("G")}, 0, false},
		{Dict{"W": Integer(1), "H": Integer(1), "BPC": Integer(99), "CS": Name("G")}, 0, false},
		{Dict{"W": Integer(1), "H": Integer(1), "CS": Name("Cs1")}, 0, false},
	}
	for _, c := range cases {
		n, ok := inlineSampleBytes(c.d)
		if n != c.n || ok != c.know {
			t.Errorf("%v: got %d, %v", c.d, n, ok)
		}
	}
}

func TestInlineImageDataLengthIgnoresFilteredImages(t *testing.T) {
	base := Dict{"W": Integer(1), "H": Integer(1), "CS": Name("G")}
	if _, ok := inlineImageDataLength(base); !ok {
		t.Error("an unfiltered image has a computable length")
	}
	for _, key := range []Name{"F", "Filter"} {
		d := Dict{"W": Integer(1), "H": Integer(1), "CS": Name("G"), key: Name("Fl")}
		if _, ok := inlineImageDataLength(d); ok {
			t.Errorf("/%s should make the length uncomputable", key)
		}
	}
}

func TestEIFollows(t *testing.T) {
	if !eiFollows([]byte("  EI "), 0) {
		t.Error("EI after white-space was not seen")
	}
	if !eiFollows([]byte("EI"), 0) {
		t.Error("EI at the very end was not seen")
	}
	if eiFollows([]byte("  EIx"), 0) {
		t.Error("EIx is not the keyword")
	}
	if eiFollows([]byte("  xx"), 0) {
		t.Error("xx is not the keyword")
	}
}

func TestNextEI(t *testing.T) {
	b := []byte("aaEI bb EI")
	// With the white-space requirement the first candidate is the second EI.
	end, resume := nextEI(b, 0, 0, true)
	if end != 7 || resume != 10 {
		t.Errorf("end = %d, resume = %d", end, resume)
	}
	// Without it, the first EI counts.
	end, resume = nextEI(b, 0, 0, false)
	if end != 2 || resume != 4 {
		t.Errorf("end = %d, resume = %d", end, resume)
	}
	if end, _ := nextEI([]byte("nothing"), 0, 0, false); end != -1 {
		t.Errorf("end = %d, want -1", end)
	}
}

func TestNextEISkipsALongerKeyword(t *testing.T) {
	// EIx is a word of its own, not the keyword.
	if end, _ := nextEI([]byte("aa EIx bb EI"), 0, 0, true); end != 9 {
		t.Errorf("end = %d, want 9", end)
	}
}

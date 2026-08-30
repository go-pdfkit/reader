package reader

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"errors"
	"testing"
)

// deflateBytes returns data as a zlib stream, the way a producer writes one.
func deflateBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// rawDeflateBytes returns data as bare deflate, with no zlib wrapper — which
// some producers emit and every reader is expected to cope with.
func rawDeflateBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImageFilter(t *testing.T) {
	for _, n := range []Name{"DCTDecode", "DCT", "JPXDecode", "JBIG2Decode"} {
		if !ImageFilter(n) {
			t.Errorf("ImageFilter(%s) = false", n)
		}
	}
	// A fax is not one of them any more: it decodes to bilevel samples, which
	// is a byte stream, so the filter chain finishes it rather than handing it
	// back encoded. See ccitt.go.
	for _, n := range []Name{"FlateDecode", "CCITTFaxDecode", "CCF"} {
		if ImageFilter(n) {
			t.Errorf("ImageFilter(%s) = true", n)
		}
	}
}

func TestDecodeNoFilter(t *testing.T) {
	got, img, err := Decode(Dict{}, []byte("plain"), nil)
	if err != nil || img != "" || string(got) != "plain" {
		t.Fatalf("got %q, %q, %v", got, img, err)
	}
}

func TestDecodeSingleAndChained(t *testing.T) {
	want := []byte("hello, streams")
	// ASCIIHex over Flate: the chain is applied left to right.
	inner := deflateBytes(t, want)
	var hex bytes.Buffer
	for _, b := range inner {
		hex.WriteString(string("0123456789abcdef"[b>>4]))
		hex.WriteString(string("0123456789abcdef"[b&15]))
	}
	hex.WriteByte('>')
	d := Dict{"Filter": Array{Name("ASCIIHexDecode"), Name("FlateDecode")}}
	got, img, err := Decode(d, hex.Bytes(), nil)
	if err != nil || img != "" || !bytes.Equal(got, want) {
		t.Fatalf("chained: got %q, %q, %v", got, img, err)
	}

	d = Dict{"Filter": Name("FlateDecode")}
	if got, _, err := Decode(d, inner, nil); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("single: got %q, %v", got, err)
	}
}

func TestDecodeStreamHelper(t *testing.T) {
	s := &Stream{Dict: Dict{"Filter": Name("ASCIIHexDecode")}, Raw: []byte("414243>")}
	got, _, err := DecodeStream(s, nil)
	if err != nil || string(got) != "ABC" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDecodeStopsAtImageFilter(t *testing.T) {
	d := Dict{"Filter": Array{Name("ASCIIHexDecode"), Name("DCTDecode")}}
	got, img, err := Decode(d, []byte("4142>"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if img != "DCTDecode" || string(got) != "AB" {
		t.Errorf("got %q, image filter %q", got, img)
	}
}

func TestDecodeFilterChainErrors(t *testing.T) {
	boom := func(Ref) (Object, error) { return nil, errors.New("boom") }
	cases := []struct {
		name string
		d    Dict
		r    Resolver
	}{
		{"unsupported filter", Dict{"Filter": Name("Nope")}, nil},
		{"filter is a number", Dict{"Filter": Integer(1)}, nil},
		{"filter array holds a number", Dict{"Filter": Array{Integer(1)}}, nil},
		{"filter reference fails", Dict{"Filter": Ref{1, 0}}, boom},
		{"filter array element fails", Dict{"Filter": Array{Ref{1, 0}}}, boom},
		{"decode parms is a number", Dict{"Filter": Name("ASCIIHexDecode"), "DecodeParms": Integer(1)}, nil},
		{"decode parms reference fails", Dict{"Filter": Name("ASCIIHexDecode"), "DecodeParms": Ref{1, 0}}, boom},
		{"decode parms element fails", Dict{"Filter": Name("ASCIIHexDecode"), "DecodeParms": Array{Ref{1, 0}}}, boom},
		{"broken data", Dict{"Filter": Name("ASCIIHexDecode")}, nil},
	}
	for _, c := range cases {
		raw := []byte("zz")
		if _, _, err := Decode(c.d, raw, c.r); err == nil {
			t.Errorf("%s: want an error", c.name)
		}
	}
}

func TestDecodeParmsShapes(t *testing.T) {
	// A single parameter dictionary applies to the first filter; an array
	// aligns positionally, and a longer array is ignored past the chain.
	raw := deflateBytes(t, []byte("abc"))
	for _, d := range []Dict{
		{"Filter": Name("FlateDecode"), "DecodeParms": Dict{"Predictor": Integer(1)}},
		{"Filter": Name("FlateDecode"), "DecodeParms": Array{Dict{"Predictor": Integer(1)}, Null{}}},
		{"Filter": Name("FlateDecode"), "DecodeParms": Array{Null{}}},
		{"Filter": Name("FlateDecode"), "DecodeParms": Null{}},
		{"DecodeParms": Dict{"Predictor": Integer(1)}},
	} {
		in := raw
		if d.Get("Filter").Kind() == KindNull {
			in = []byte("abc")
		}
		got, _, err := Decode(d, in, nil)
		if err != nil || string(got) != "abc" {
			t.Errorf("%v: got %q, %v", d, got, err)
		}
	}
}

func TestFlateDecode(t *testing.T) {
	want := []byte("some data worth compressing, twice over")
	if got, err := flateDecode(deflateBytes(t, want)); err != nil || !bytes.Equal(got, want) {
		t.Errorf("zlib: %q, %v", got, err)
	}
	if got, err := flateDecode(rawDeflateBytes(t, want)); err != nil || !bytes.Equal(got, want) {
		t.Errorf("raw deflate: %q, %v", got, err)
	}
	// Leading white-space before the header.
	padded := append([]byte("\n \r"), deflateBytes(t, want)...)
	if got, err := flateDecode(padded); err != nil || !bytes.Equal(got, want) {
		t.Errorf("padded: %q, %v", got, err)
	}
	// A stream cut short yields the bytes it did carry, together with the
	// error that ended it: a prefix is never passed off as a whole stream.
	full := deflateBytes(t, bytes.Repeat(want, 50))
	short, err := flateDecode(full[:len(full)/2])
	if err == nil {
		t.Error("truncated: want the error that ended it")
	}
	if len(short) == 0 {
		t.Error("truncated: no data recovered")
	}
	// Nothing usable at all is an error.
	if _, err := flateDecode([]byte("not compressed at all, really")); err == nil {
		t.Error("garbage: want an error")
	}
}

func TestFlateDecodeSizeCap(t *testing.T) {
	defer func(old int64) { maxDecodedSize = old }(maxDecodedSize)
	maxDecodedSize = 16
	if _, err := flateDecode(deflateBytes(t, bytes.Repeat([]byte("x"), 1000))); err == nil {
		t.Error("want an error once the cap is passed")
	}
}

func TestASCIIHexDecode(t *testing.T) {
	cases := []struct{ src, want string }{
		{"414243>", "ABC"},
		{"41 42\n43>", "ABC"},
		{"4>", "@"},
		{"414243", "ABC"},
		{">", ""},
	}
	for _, c := range cases {
		got, err := asciiHexDecode([]byte(c.src))
		if err != nil || string(got) != c.want {
			t.Errorf("%q: got %q, %v", c.src, got, err)
		}
	}
	if _, err := asciiHexDecode([]byte("4G>")); err == nil {
		t.Error("want an error for a bad digit")
	}
}

func TestASCII85Decode(t *testing.T) {
	cases := []struct{ src, want string }{
		{"87cURD]i,\"Ebo80~>", "Hello World!"},
		{"<~87cURD]i,\"Ebo80~>", "Hello World!"},
		{"87cURD]i,\"Ebo80", "Hello World!"},
		{"z~>", "\x00\x00\x00\x00"},
		{"87cU~>", "Hel"},
		{"87 cU\n~>", "Hel"},
		{"~>", ""},
	}
	for _, c := range cases {
		got, err := ascii85Decode([]byte(c.src))
		if err != nil || string(got) != c.want {
			t.Errorf("%q: got %q, %v", c.src, got, err)
		}
	}
	for _, src := range []string{"8~>", "8\x01~>", "uuuuu~>"} {
		if _, err := ascii85Decode([]byte(src)); err == nil {
			t.Errorf("%q: want an error", src)
		}
	}
}

func TestRunLengthDecode(t *testing.T) {
	cases := []struct {
		src  []byte
		want string
	}{
		{[]byte{2, 'a', 'b', 'c', 128}, "abc"},
		{[]byte{254, 'x', 128}, "xxx"},
		{[]byte{0, 'q'}, "q"},
		{[]byte{128}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		got, err := runLengthDecode(c.src)
		if err != nil || string(got) != c.want {
			t.Errorf("% x: got %q, %v", c.src, got, err)
		}
	}
	for _, src := range [][]byte{{5, 'a'}, {200}} {
		if _, err := runLengthDecode(src); err == nil {
			t.Errorf("% x: want an error", src)
		}
	}
}

func TestIntParm(t *testing.T) {
	boom := func(Ref) (Object, error) { return nil, errors.New("boom") }
	if got := intParm(nil, "K", 7, nil); got != 7 {
		t.Errorf("nil dictionary: %d", got)
	}
	if got := intParm(Dict{}, "K", 7, nil); got != 7 {
		t.Errorf("missing key: %d", got)
	}
	if got := intParm(Dict{"K": Name("x")}, "K", 7, nil); got != 7 {
		t.Errorf("wrong type: %d", got)
	}
	if got := intParm(Dict{"K": Ref{1, 0}}, "K", 7, boom); got != 7 {
		t.Errorf("resolver error: %d", got)
	}
	if got := intParm(Dict{"K": Integer(3)}, "K", 7, nil); got != 3 {
		t.Errorf("present: %d", got)
	}
}

func TestLZWFilterEndToEnd(t *testing.T) {
	want := bytes.Repeat([]byte("lzw through the filter chain "), 40)
	enc := lzwEncode(want, true)
	d := Dict{"Filter": Name("LZWDecode")}
	if got, _, err := Decode(d, enc, nil); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("default EarlyChange: %v", err)
	}
	d = Dict{"Filter": Name("LZW"), "DecodeParms": Dict{"EarlyChange": Integer(0)}}
	if got, _, err := Decode(d, lzwEncode(want, false), nil); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("EarlyChange 0: %v", err)
	}
	// A stream whose codes are nonsense is an error, not silent rubbish.
	if _, _, err := Decode(Dict{"Filter": Name("LZWDecode")}, []byte{0x80, 0x40, 0x80}, nil); err == nil {
		t.Error("want an error")
	}
}

func TestFilterAbbreviations(t *testing.T) {
	if got, _, err := Decode(Dict{"Filter": Name("AHx")}, []byte("41>"), nil); err != nil || string(got) != "A" {
		t.Errorf("AHx: %q %v", got, err)
	}
	if got, _, err := Decode(Dict{"Filter": Name("A85")}, []byte("87cU~>"), nil); err != nil || string(got) != "Hel" {
		t.Errorf("A85: %q %v", got, err)
	}
	if got, _, err := Decode(Dict{"Filter": Name("RL")}, []byte{0, 'q', 128}, nil); err != nil || string(got) != "q" {
		t.Errorf("RL: %q %v", got, err)
	}
	if got, _, err := Decode(Dict{"Filter": Name("Fl")}, deflateBytes(t, []byte("z")), nil); err != nil || string(got) != "z" {
		t.Errorf("Fl: %q %v", got, err)
	}
}

func TestDecodeFlateFailure(t *testing.T) {
	// The filter chain must pass a decoder's failure on rather than swallow it.
	if _, _, err := Decode(Dict{"Filter": Name("FlateDecode")}, []byte("not deflate data"), nil); err == nil {
		t.Error("want an error")
	}
}

// A bare deflate stream whose first block is STORED begins with a NUL, and NUL
// is one of the six bytes the specification calls white-space. Skipping it eats
// a real byte and the stream dies one byte in.
//
// The stream is built by hand rather than by a deflater, because which block
// type a deflater picks is its own business and changes between releases: Go
// 1.26 compressed this test's data, Go 1.27 stored it, and that is how this was
// found.
func TestFlateDecodeStoredBlockStartingWithNUL(t *testing.T) {
	want := []byte("abc")

	var raw []byte
	// Non-final stored block: BFINAL=0, BTYPE=00, then LEN and ^LEN, little-endian.
	raw = append(raw, 0x00, byte(len(want)), 0x00, ^byte(len(want)), 0xff)
	raw = append(raw, want...)
	// Final empty stored block.
	raw = append(raw, 0x01, 0x00, 0x00, 0xff, 0xff)

	if raw[0] != 0x00 {
		t.Fatalf("test is not exercising what it claims: first byte %#x, want NUL", raw[0])
	}
	if !isSpace(raw[0]) {
		t.Fatal("test is not exercising what it claims: NUL is not treated as white-space")
	}

	got, err := flateDecode(raw)
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("stored block: got %q, %v; want %q", got, err, want)
	}
}

// A bare deflate stream preceded by the stream's EOL: here the white-space skip
// is the thing that makes it readable, which is why it is there.
func TestFlateDecodeRawAfterLeadingEOL(t *testing.T) {
	want := []byte("some data worth compressing, twice over")
	padded := append([]byte("\r\n"), rawDeflateBytes(t, want)...)
	got, err := flateDecode(padded)
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("raw deflate after EOL: got %q, %v", got, err)
	}
}

package reader

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// A filter chain that runs to the end is a clean decode, and says so.
func TestDecodeRecoveringClean(t *testing.T) {
	raw := deflateBytes(t, []byte("plain content"))
	dec := DecodeRecovering(Dict{"Filter": Name("FlateDecode")}, raw, nil)
	if dec.Recovered || dec.Cause != nil || dec.Image != "" {
		t.Fatalf("got %+v", dec)
	}
	if string(dec.Data) != "plain content" {
		t.Errorf("got %q", dec.Data)
	}
}

// A chain that ends in an image filter is not a failure: the caller is handed
// the still-encoded bytes and the filter's name.
func TestDecodeRecoveringImageFilter(t *testing.T) {
	dec := DecodeRecovering(Dict{"Filter": Name("DCTDecode")}, []byte("jpeg-ish"), nil)
	if dec.Recovered || dec.Image != "DCTDecode" || string(dec.Data) != "jpeg-ish" {
		t.Fatalf("got %+v", dec)
	}
}

// A filter nobody implements ends the chain, and the bytes as they stand come
// back flagged rather than not at all — as Undecoded, since nothing decoded
// them.
func TestDecodeRecoveringUnknownFilter(t *testing.T) {
	dec := DecodeRecovering(Dict{"Filter": Name("BrotliDecode")}, []byte("brotli bytes"), nil)
	if !dec.Recovered || dec.Cause == nil {
		t.Fatalf("got %+v", dec)
	}
	if dec.Filter != "BrotliDecode" || string(dec.Undecoded) != "brotli bytes" {
		t.Errorf("got %+v", dec)
	}
	if len(dec.Data) != 0 {
		t.Errorf("undecoded bytes arrived as Data: %q", dec.Data)
	}
	// The strict reading refuses the same stream outright.
	if _, _, err := Decode(Dict{"Filter": Name("BrotliDecode")}, []byte("brotli bytes"), nil); err == nil {
		t.Error("Decode: want an error")
	}
}

// The salvage is as far down the chain as the filters got: an unknown filter
// after a Flate one yields the inflated bytes, never the compressed ones.
func TestDecodeRecoveringSalvagesDownTheChain(t *testing.T) {
	raw := deflateBytes(t, []byte("inflated already"))
	d := Dict{"Filter": Array{Name("FlateDecode"), Name("Nope")}}
	dec := DecodeRecovering(d, raw, nil)
	if !dec.Recovered || dec.Filter != "Nope" {
		t.Fatalf("got %+v", dec)
	}
	// The bytes are inflated, but they are still whatever /Nope encodes, so
	// they are Undecoded and not content.
	if string(dec.Undecoded) != "inflated already" {
		t.Errorf("got %q, want the inflated bytes", dec.Undecoded)
	}
	if len(dec.Data) != 0 {
		t.Errorf("bytes still in a filter's encoding arrived as Data: %q", dec.Data)
	}
}

// A /Filter entry that cannot be read at all blames no filter in particular.
func TestDecodeRecoveringUnreadableFilterEntry(t *testing.T) {
	dec := DecodeRecovering(Dict{"Filter": Integer(7)}, []byte("as it lies"), nil)
	if !dec.Recovered || dec.Cause == nil || dec.Filter != "" {
		t.Fatalf("got %+v", dec)
	}
	if string(dec.Undecoded) != "as it lies" || len(dec.Data) != 0 {
		t.Errorf("got %+v", dec)
	}
}

// A damaged Flate stream yields the prefix it did inflate, with the predictor
// still undone over it, because the predictors undo one row at a time.
func TestDecodeRecoveringDamagedFlateKeepsPredictor(t *testing.T) {
	const columns = 4
	var rows, want []byte
	acc := make([]byte, columns)
	for i := 0; i < 300; i++ {
		v := byte(i*37 + 11)
		rows = append(rows, 2) // the PNG "up" filter
		for c := 0; c < columns; c++ {
			rows = append(rows, v+byte(c))
			acc[c] += v + byte(c)
		}
		want = append(want, acc...)
	}
	full := deflateBytes(t, rows)
	d := Dict{
		"Filter":      Name("FlateDecode"),
		"DecodeParms": Dict{"Predictor": Integer(12), "Columns": Integer(columns)},
	}
	dec := DecodeRecovering(d, full[:len(full)/2], nil)
	if !dec.Recovered || dec.Cause == nil {
		t.Fatalf("got %+v", dec)
	}
	if len(dec.Data) < 4*columns {
		t.Fatalf("recovered only %d bytes", len(dec.Data))
	}
	if !bytes.HasPrefix(want, dec.Data) {
		t.Errorf("predictor not undone over the prefix: got %v, want a prefix of %v", dec.Data[:8], want[:8])
	}
}

// A predictor that cannot be applied to the salvaged prefix leaves it as it is
// rather than throwing it away.
func TestDecodeRecoveringDamagedFlateUnusablePredictor(t *testing.T) {
	full := deflateBytes(t, bytes.Repeat([]byte("row"), 200))
	d := Dict{
		"Filter":      Name("FlateDecode"),
		"DecodeParms": Dict{"Predictor": Integer(5)}, // no such predictor
	}
	dec := DecodeRecovering(d, full[:len(full)/2], nil)
	if !dec.Recovered || len(dec.Data) == 0 {
		t.Fatalf("got %+v", dec)
	}
	if !bytes.HasPrefix(dec.Data, []byte("rowrow")) {
		t.Errorf("got %q, want the un-predicted prefix", dec.Data[:min(12, len(dec.Data))])
	}
}

// Every filter keeps what it managed to produce, with the reason it stopped.
func TestFiltersKeepTheirPrefix(t *testing.T) {
	for _, c := range []struct {
		filter Name
		raw    string
		want   string
	}{
		{"ASCIIHexDecode", "4142zz", "AB"},
		{"ASCII85Decode", "87cURDZ\x01", "Hell"},
		{"RunLengthDecode", "\x02abc\x7f", "abc"},
	} {
		dec := DecodeRecovering(Dict{"Filter": c.filter}, []byte(c.raw), nil)
		if !dec.Recovered || dec.Cause == nil {
			t.Errorf("/%s: got %+v", c.filter, dec)
			continue
		}
		if string(dec.Data) != c.want {
			t.Errorf("/%s: got %q, want %q", c.filter, dec.Data, c.want)
		}
	}
}

// LZW keeps its prefix too, where the old reading returned nothing at all.
func TestLZWKeepsItsPrefix(t *testing.T) {
	// A clear code, then "A", then a code that names no table entry.
	raw := []byte{0x80, 0x20, 0x50, 0x1f, 0xf0}
	out, err := lzwDecode(raw, true)
	if err == nil {
		t.Fatal("want the error that ended it")
	}
	if len(out) == 0 {
		t.Error("no data recovered")
	}
}

// The stream helpers agree with the dictionary ones.
func TestDecodeStreamRecovering(t *testing.T) {
	s := &Stream{Dict: Dict{"Filter": Name("Nope")}, Raw: []byte("raw")}
	if dec := DecodeStreamRecovering(s, nil); !dec.Recovered || string(dec.Undecoded) != "raw" {
		t.Errorf("package: got %+v", dec)
	}
	d, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	if dec := d.DecodeStreamRecovering(s); !dec.Recovered || string(dec.Undecoded) != "raw" {
		t.Errorf("document: got %+v", dec)
	}
}

// An object stream whose data stops short still defines the objects its prefix
// holds, which is the difference between some pages and none.
func TestObjectStreamSalvagesItsPrefix(t *testing.T) {
	bodies := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 1 1] >>",
		"<< /Type /Page /Parent 2 0 R >>",
	}
	var index, payload strings.Builder
	for i, body := range bodies {
		fmt.Fprintf(&index, "%d %d ", i+1, payload.Len())
		payload.WriteString(body)
		payload.WriteString(" ")
	}
	first := index.Len()
	packed := deflateBytes(t, []byte(index.String()+payload.String()))

	b := newBuilder()
	// The last four bytes of a zlib stream are its checksum: a file cut there
	// carries every byte of the data and still refuses to inflate cleanly.
	b.streamObj(4, fmt.Sprintf("/Type /ObjStm /N %d /First %d /Filter /FlateDecode", len(bodies), first), packed[:len(packed)-4])
	raw := b.table("/Root 1 0 R")
	d := &Document{
		buf:     raw,
		xref:    map[int]xrefEntry{4: {kind: 'n', offset: int64(bytes.Index(raw, []byte("4 0 obj")))}},
		cache:   map[int]Object{},
		loading: map[int]bool{},
		objStms: map[int]map[int]Object{},
		trailer: Dict{"Root": Ref{Num: 1}},
	}
	for i := range bodies {
		d.xref[i+1] = xrefEntry{kind: 'o', strmNum: 4}
	}
	if got := d.PageCount(); got != 1 {
		t.Fatalf("page count %d, want 1", got)
	}
	if _, err := d.Catalog(); err != nil {
		t.Errorf("catalogue lost: %v", err)
	}
}

// The guarantee the split exists for: bytes no filter decoded never arrive in
// Data, whatever the filter and whatever the shape of the failure. A stencil
// path reading Data cannot paint a compressed stream as a one-bit image, and a
// content scanner reading Data cannot tokenise one.
func TestUndecodedBytesNeverArriveAsData(t *testing.T) {
	for _, c := range []struct {
		what string
		d    Dict
		raw  []byte
	}{
		{"a filter nobody implements", Dict{"Filter": Name("BrotliDecode")}, []byte("\x1b\x2e\x00")},
		{"Flate that is not Flate", Dict{"Filter": Name("FlateDecode")}, []byte("not compressed at all")},
		{"a fax with no columns", Dict{"Filter": Name("CCITTFaxDecode"),
			"DecodeParms": Dict{"Columns": Integer(0)}}, []byte("\x00\xff\x00\xff")},
		{"a fax past the pixel limit", Dict{"Filter": Name("CCITTFaxDecode"),
			"DecodeParms": Dict{"Columns": Integer(1 << 20), "Rows": Integer(1 << 20)}}, []byte("\x26\xa0")},
		{"an unreadable /Filter", Dict{"Filter": Real(2.5)}, []byte("who knows")},
		{"a crypt filter that cannot be applied", Dict{"Filter": Name("Crypt"),
			"DecodeParms": Dict{"Name": Name("StdCF")}}, []byte("ciphertext")},
	} {
		dec := DecodeRecovering(c.d, c.raw, nil)
		if !dec.Recovered || dec.Cause == nil {
			t.Errorf("%s: not reported as recovered: %+v", c.what, dec)
			continue
		}
		if len(dec.Data) != 0 {
			t.Errorf("%s: %d undecoded bytes arrived as Data", c.what, len(dec.Data))
		}
		if !bytes.Equal(dec.Undecoded, c.raw) {
			t.Errorf("%s: Undecoded = %q, want the bytes as they lie", c.what, dec.Undecoded)
		}
		// And the strict reading gives nothing at all.
		if got, _, err := Decode(c.d, c.raw, nil); err == nil || got != nil {
			t.Errorf("%s: Decode returned %q, %v", c.what, got, err)
		}
	}
}

// FuzzDecodeRecovering asserts the contract the salvage rests on: it never
// panics and never reports a clean decode it did not make.
func FuzzDecodeRecovering(f *testing.F) {
	f.Add([]byte("FlateDecode"), []byte("not deflate"))
	f.Add([]byte("ASCIIHexDecode"), []byte("41 42 4"))
	f.Add([]byte("ASCII85Decode"), []byte("<~87cURD]~>"))
	f.Add([]byte("RunLengthDecode"), []byte("\x02abc"))
	f.Add([]byte("LZWDecode"), []byte("\x80\x20\x50\x1f\xf0"))
	f.Add([]byte("Nope"), []byte("whatever"))
	f.Fuzz(func(t *testing.T, filter, raw []byte) {
		d := Dict{
			"Filter":      Name(filter),
			"DecodeParms": Dict{"Predictor": Integer(12), "Columns": Integer(4)},
		}
		dec := DecodeRecovering(d, raw, nil)
		if dec.Recovered != (dec.Cause != nil) {
			t.Fatalf("Recovered and Cause disagree: %+v", dec)
		}
		if len(dec.Undecoded) > 0 && len(dec.Data) > 0 {
			t.Fatalf("Data and Undecoded both set: %+v", dec)
		}
		if len(dec.Undecoded) > 0 && !dec.Recovered {
			t.Fatalf("Undecoded set on a clean decode: %+v", dec)
		}
		data, img, err := Decode(d, raw, nil)
		if (err != nil) != dec.Recovered {
			t.Fatalf("Decode and DecodeRecovering disagree: %v vs %+v", err, dec)
		}
		if err == nil && (img != dec.Image || !bytes.Equal(data, dec.Data)) {
			t.Fatalf("clean decodes differ: %q/%s vs %+v", data, img, dec)
		}
	})
}

// FuzzOpen asserts that no byte string makes the reader panic or hand back a
// document it cannot walk.
func FuzzOpen(f *testing.F) {
	f.Add(onePage())
	f.Add([]byte("%PDF-1.7\n1 0 obj\n<< /Type /Page >>\nendobj\ntrailer\n<< >>\n"))
	f.Add([]byte("%PDF-1.4\nstartxref\n9\n%%EOF\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := Open(b)
		if err != nil {
			return
		}
		for i := 1; i <= d.PageCount() && i <= 8; i++ {
			if _, err := d.PageContentDecoded(i); err != nil {
				continue
			}
			if _, err := d.PageOperations(i); err != nil {
				continue
			}
		}
	})
}

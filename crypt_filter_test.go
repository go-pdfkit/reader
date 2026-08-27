package reader

import (
	"fmt"
	"testing"
)

// /Crypt is not a transformation: /Identity, and /Crypt with no /Name at all,
// leave the bytes exactly as they are.
func TestCryptFilterIsIdentity(t *testing.T) {
	for _, d := range []Dict{
		{"Filter": Name("Crypt")},
		{"Filter": Name("Crypt"), "DecodeParms": Dict{"Name": Name("Identity")}},
		{"Filter": Array{Name("Crypt")}, "DecodeParms": Array{Dict{"Name": Name("Identity")}}},
	} {
		dec := DecodeRecovering(d, []byte("<?xpacket begin=?>"), nil)
		if dec.Recovered || string(dec.Data) != "<?xpacket begin=?>" {
			t.Errorf("%v: got %+v", d, dec)
		}
	}
}

// A /Crypt filter chained ahead of a real one is stepped over.
func TestCryptFilterAheadOfFlate(t *testing.T) {
	raw := deflateBytes(t, []byte("metadata"))
	d := Dict{
		"Filter":      Array{Name("Crypt"), Name("FlateDecode")},
		"DecodeParms": Array{Dict{"Name": Name("Identity")}, Null{}},
	}
	dec := DecodeRecovering(d, raw, nil)
	if dec.Recovered || string(dec.Data) != "metadata" {
		t.Fatalf("got %+v", dec)
	}
}

// A crypt filter this reader cannot account for is reported, because the bytes
// would otherwise be ciphertext passed off as data.
func TestCryptFilterRefusesANamedFilter(t *testing.T) {
	d := Dict{"Filter": Name("Crypt"), "DecodeParms": Dict{"Name": Name("StdCF")}}
	dec := DecodeRecovering(d, []byte("cipher"), nil)
	if !dec.Recovered || dec.Cause == nil || dec.Filter != "Crypt" {
		t.Fatalf("got %+v", dec)
	}
}

// A /Name that cannot be resolved is reported too.
func TestCryptFilterUnresolvableName(t *testing.T) {
	fail := func(Ref) (Object, error) { return nil, fmt.Errorf("no") }
	d := Dict{"Filter": Name("Crypt"), "DecodeParms": Dict{"Name": Ref{Num: 9}}}
	dec := DecodeRecovering(d, []byte("cipher"), fail)
	if !dec.Recovered || dec.Cause == nil {
		t.Fatalf("got %+v", dec)
	}
}

func TestFirstFilterAndDecodeParms(t *testing.T) {
	for _, c := range []struct {
		d    Dict
		name Name
		ok   bool
	}{
		{Dict{"Filter": Name("Fl")}, "Fl", true},
		{Dict{"Filter": Array{Name("Crypt"), Name("Fl")}}, "Crypt", true},
		{Dict{"Filter": Array{Integer(1)}}, "", false},
		{Dict{"Filter": Array{}}, "", false},
		{Dict{"Filter": Integer(3)}, "", false},
		{Dict{}, "", false},
	} {
		if got, ok := firstFilter(c.d); got != c.name || ok != c.ok {
			t.Errorf("firstFilter(%v) = %q, %v", c.d, got, ok)
		}
	}
	for _, c := range []struct {
		d    Dict
		want Name
	}{
		{Dict{"DecodeParms": Dict{"Name": Name("A")}}, "A"},
		{Dict{"DecodeParms": Array{Dict{"Name": Name("B")}}}, "B"},
		{Dict{"DecodeParms": Array{Integer(0)}}, ""},
		{Dict{"DecodeParms": Array{}}, ""},
		{Dict{"DecodeParms": Integer(0)}, ""},
		{Dict{}, ""},
	} {
		got, _ := ToName(firstDecodeParms(c.d).Get("Name"))
		if got != c.want {
			t.Errorf("firstDecodeParms(%v)/Name = %q, want %q", c.d, got, c.want)
		}
	}
}

// A stream whose chain begins with /Crypt /Identity was never encrypted, so
// decrypting it would turn readable bytes into noise.
func TestStreamIsPlain(t *testing.T) {
	for _, c := range []struct {
		d    Dict
		want bool
	}{
		{Dict{"Filter": Name("Crypt")}, true},
		{Dict{"Filter": Name("Crypt"), "DecodeParms": Dict{"Name": Name("Identity")}}, true},
		{Dict{"Filter": Name("Crypt"), "DecodeParms": Dict{"Name": Name("StdCF")}}, false},
		{Dict{"Filter": Name("FlateDecode")}, false},
		{Dict{}, false},
	} {
		if got := streamIsPlain(c.d); got != c.want {
			t.Errorf("streamIsPlain(%v) = %v", c.d, got)
		}
	}
}

// The decryptor leaves such a stream alone, and still decrypts its neighbours.
func TestDecryptorSkipsAPlainStream(t *testing.T) {
	dec := &decryptor{revision: 4, streams: cryptRC4, strings: cryptRC4, key: []byte("0123456789abcdef")}
	plain := &Stream{Dict: Dict{"Filter": Name("Crypt")}, Raw: []byte("<?xpacket?>")}
	if got := dec.decryptObject(7, 0, plain).(*Stream); string(got.Raw) != "<?xpacket?>" {
		t.Errorf("a plain stream was decrypted: %q", got.Raw)
	}
	other := &Stream{Dict: Dict{"Filter": Name("FlateDecode")}, Raw: []byte("<?xpacket?>")}
	if got := dec.decryptObject(7, 0, other).(*Stream); string(got.Raw) == "<?xpacket?>" {
		t.Error("an encrypted stream was left alone")
	}
}

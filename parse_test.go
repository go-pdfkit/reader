package reader

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseObjectBasics(t *testing.T) {
	cases := []struct {
		src  string
		want Object
	}{
		{"null", Null{}},
		{"true", Bool(true)},
		{"false", Bool(false)},
		{"42", Integer(42)},
		{"3.5", Real(3.5)},
		{"(hi)", String("hi")},
		{"<41>", String("A")},
		{"/Type", Name("Type")},
		{"[]", Array{}},
		{"[1 (a) /N]", Array{Integer(1), String("a"), Name("N")}},
		{"[[1] 2]", Array{Array{Integer(1)}, Integer(2)}},
		{"<<>>", Dict{}},
		{"<< /A 1 /B << /C 2 >> >>", Dict{"A": Integer(1), "B": Dict{"C": Integer(2)}}},
		{"12 0 R", Ref{12, 0}},
		{"[12 0 R 3]", Array{Ref{12, 0}, Integer(3)}},
		// Not a reference: no R follows.
		{"[1 2 3]", Array{Integer(1), Integer(2), Integer(3)}},
		// Not a reference: a negative object number cannot start one.
		{"-1 0 R", Integer(-1)},
	}
	for _, c := range cases {
		got, _, err := ParseObject([]byte(c.src))
		if err != nil {
			t.Errorf("%q: %v", c.src, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %#v, want %#v", c.src, got, c.want)
		}
	}
}

func TestParseObjectConsumed(t *testing.T) {
	_, n, err := ParseObject([]byte("42 trailing"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("consumed %d bytes, want 2", n)
	}
}

func TestParseObjectErrors(t *testing.T) {
	for _, src := range []string{
		"",           // nothing at all
		"]",          // a token no object starts with
		"endobj",     // an unexpected keyword
		"[1",         // unterminated array
		"<< /A 1",    // unterminated dictionary
		"<< 1 2 >>",  // a key that is not a name
		"<< /A ] >>", // a bad value
		"<< /A",      // a value that is missing entirely
		"<< /A#2 1 >>",
		"[/A#2]",
		"(",        // a lexer error where an object should start
		"[1 -2 R]", // a stray keyword where a value belongs
	} {
		if _, _, err := ParseObject([]byte(src)); err == nil {
			t.Errorf("%q: want an error", src)
		}
	}
}

func TestTokKindDescribe(t *testing.T) {
	for k, want := range map[tokKind]string{
		tokInteger:    "number",
		tokReal:       "number",
		tokString:     "string",
		tokArrayOpen:  "bracket",
		tokArrayClose: "bracket",
		tokDictOpen:   "dictionary delimiter",
		tokDictClose:  "dictionary delimiter",
		tokBraceOpen:  "brace",
		tokBraceClose: "brace",
		tokKeyword:    "keyword",
		tokEOF:        "token",
	} {
		if got := k.describe(); got != want {
			t.Errorf("describe(%v) = %q, want %q", k, got, want)
		}
	}
}

func TestParseIndirectObject(t *testing.T) {
	ref, obj, n, err := ParseIndirectObject([]byte("7 0 obj\n<< /A 1 >>\nendobj\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ref != (Ref{7, 0}) {
		t.Errorf("ref = %v", ref)
	}
	if !reflect.DeepEqual(obj, Dict{"A": Integer(1)}) {
		t.Errorf("obj = %#v", obj)
	}
	if n != len("7 0 obj\n<< /A 1 >>\nendobj") {
		t.Errorf("consumed %d bytes", n)
	}
}

func TestParseIndirectObjectWithoutEndobj(t *testing.T) {
	_, obj, _, err := ParseIndirectObject([]byte("7 0 obj 1"), nil)
	if err != nil || obj != Object(Integer(1)) {
		t.Fatalf("obj = %v, err = %v", obj, err)
	}
}

func TestParseIndirectObjectErrors(t *testing.T) {
	for _, src := range []string{
		"x 0 obj 1 endobj",            // no object number
		"-1 0 obj 1 endobj",           // a negative object number
		"7 x obj 1 endobj",            // no generation number
		"7 0 xxx 1 endobj",            // the obj keyword is missing
		"7 0 obj ] endobj",            // the body does not parse
		"7 0 obj 1 stream\n",          // a stream keyword after a non-dictionary
		"7 0 obj\n<< >>\nstream\nabc", // no endstream
		"7 1.2.3 obj 1 endobj",        // a generation number that is not an integer
		"1.2.3 0 obj 1 endobj",        // an object number that is not an integer
	} {
		if _, _, _, err := ParseIndirectObject([]byte(src), nil); err == nil {
			t.Errorf("%q: want an error", src)
		}
	}
}

func TestParseStream(t *testing.T) {
	src := "7 0 obj\n<< /Length 5 >>\nstream\nHELLO\nendstream\nendobj\n"
	_, obj, _, err := ParseIndirectObject([]byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := ToStream(obj)
	if !ok {
		t.Fatalf("got %T, want a stream", obj)
	}
	if string(s.Raw) != "HELLO" {
		t.Errorf("Raw = %q", s.Raw)
	}
}

func TestParseStreamLengthWrong(t *testing.T) {
	// /Length lies, so the reader falls back to finding endstream itself.
	for _, src := range []string{
		"7 0 obj\n<< /Length 2 >>\nstream\nHELLO\nendstream\nendobj\n",
		"7 0 obj\n<< /Length 900 >>\nstream\nHELLO\nendstream\nendobj\n",
		"7 0 obj\n<< /Length (x) >>\nstream\nHELLO\nendstream\nendobj\n",
		"7 0 obj\n<< /Length -3 >>\nstream\nHELLO\nendstream\nendobj\n",
		"7 0 obj\n<< >>\nstream\nHELLO\nendstream\nendobj\n",
	} {
		_, obj, _, err := ParseIndirectObject([]byte(src), nil)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		s, _ := ToStream(obj)
		if string(s.Raw) != "HELLO" {
			t.Errorf("%q: Raw = %q, want %q", src, s.Raw, "HELLO")
		}
	}
}

func TestParseStreamIndirectLength(t *testing.T) {
	src := "7 0 obj\n<< /Length 9 0 R >>\nstream\nHELLO\nendstream\nendobj\n"
	resolve := func(r Ref) (Object, error) {
		if r == (Ref{9, 0}) {
			return Integer(5), nil
		}
		return nil, errors.New("no such object")
	}
	_, obj, _, err := ParseIndirectObject([]byte(src), resolve)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := ToStream(obj)
	if string(s.Raw) != "HELLO" {
		t.Errorf("Raw = %q", s.Raw)
	}

	// A resolver that fails leaves the scan to do the work.
	failing := func(Ref) (Object, error) { return nil, errors.New("boom") }
	_, obj, _, err = ParseIndirectObject([]byte(src), failing)
	if err != nil {
		t.Fatal(err)
	}
	s, _ = ToStream(obj)
	if string(s.Raw) != "HELLO" {
		t.Errorf("Raw = %q", s.Raw)
	}
}

func TestParseStreamLineEndings(t *testing.T) {
	cases := []struct{ src, want string }{
		{"7 0 obj\n<< >>\nstream\r\nAB\r\nendstream", "AB"},
		{"7 0 obj\n<< >>\nstream\rAB\nendstream", "AB"},
		{"7 0 obj\n<< >>\nstream \nAB\nendstream", "AB"},
		{"7 0 obj\n<< >>\nstream\nendstream", ""},
	}
	for _, c := range cases {
		_, obj, _, err := ParseIndirectObject([]byte(c.src), nil)
		if err != nil {
			t.Fatalf("%q: %v", c.src, err)
		}
		s, _ := ToStream(obj)
		if string(s.Raw) != c.want {
			t.Errorf("%q: Raw = %q, want %q", c.src, s.Raw, c.want)
		}
	}
}

func TestEndstreamAt(t *testing.T) {
	b := []byte("data  endstream tail")
	if got := endstreamAt(b, 4); got != len("data  endstream") {
		t.Errorf("endstreamAt = %d", got)
	}
	if got := endstreamAt(b, 0); got != -1 {
		t.Errorf("endstreamAt(0) = %d, want -1", got)
	}
}

func TestParseIndirectObjectLexErrorAtKeyword(t *testing.T) {
	// The lexer fails where the obj keyword should be.
	if _, _, _, err := ParseIndirectObject([]byte("7 0 (unterminated"), nil); err == nil {
		t.Error("want an error")
	}
}

func TestParseIndirectObjectWithNoValue(t *testing.T) {
	// "7 0 obj endobj" appears in real files; its value is null.
	ref, obj, _, err := ParseIndirectObject([]byte("7 0 obj\nendobj\n"), nil)
	if err != nil || ref != (Ref{7, 0}) || obj.Kind() != KindNull {
		t.Fatalf("ref %v, obj %v, err %v", ref, obj, err)
	}
}

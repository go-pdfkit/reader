package reader

import (
	"strings"
	"testing"
)

// lexAll runs the lexer to exhaustion and returns the tokens, or the first
// error.
func lexAll(t *testing.T, src string) ([]token, error) {
	t.Helper()
	l := &lexer{buf: []byte(src)}
	var toks []token
	for {
		tk, err := l.next()
		if err != nil {
			return toks, err
		}
		if tk.kind == tokEOF {
			return toks, nil
		}
		toks = append(toks, tk)
	}
}

func TestSyntaxErrorMessage(t *testing.T) {
	e := &SyntaxError{Offset: 42, Msg: "bad thing"}
	if got := e.Error(); got != "reader: bad thing at offset 42" {
		t.Errorf("Error() = %q", got)
	}
}

func TestLexDelimiters(t *testing.T) {
	toks, err := lexAll(t, "[ ] { } << >>")
	if err != nil {
		t.Fatal(err)
	}
	want := []tokKind{tokArrayOpen, tokArrayClose, tokBraceOpen, tokBraceClose, tokDictOpen, tokDictClose}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d", len(toks), len(want))
	}
	for i, w := range want {
		if toks[i].kind != w {
			t.Errorf("token %d = %v, want %v", i, toks[i].kind, w)
		}
	}
}

func TestLexStrayDelimiters(t *testing.T) {
	for _, src := range []string{">", ")"} {
		if _, err := lexAll(t, src); err == nil {
			t.Errorf("%q: want an error", src)
		}
	}
}

func TestLexCommentsAndWhitespace(t *testing.T) {
	toks, err := lexAll(t, "% a comment\n\x00 \t 1 % trailing")
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 1 || toks[0].kind != tokInteger || toks[0].i != 1 {
		t.Fatalf("got %+v", toks)
	}
}

func TestLexNumbers(t *testing.T) {
	cases := []struct {
		src  string
		kind tokKind
		i    int64
		f    float64
	}{
		{"0", tokInteger, 0, 0},
		{"42", tokInteger, 42, 42},
		{"-17", tokInteger, -17, -17},
		{"+9", tokInteger, 9, 9},
		{"34.5", tokReal, 34, 34.5},
		{"-3.62", tokReal, -3, -3.62},
		{"4.", tokReal, 4, 4},
		{"-.002", tokReal, 0, -0.002},
		{".5", tokReal, 0, 0.5},
		// Producers do emit a doubled sign; only the first one counts.
		{"--5", tokReal, -5, -5},
		// Too large for an int64, so it becomes a real.
		{"99999999999999999999", tokReal, 0, 1e20},
	}
	for _, c := range cases {
		toks, err := lexAll(t, c.src)
		if err != nil {
			t.Fatalf("%q: %v", c.src, err)
		}
		if len(toks) != 1 || toks[0].kind != c.kind || toks[0].f != c.f {
			t.Fatalf("%q: got %+v, want kind %v value %v", c.src, toks, c.kind, c.f)
		}
		if c.kind == tokInteger && toks[0].i != c.i {
			t.Errorf("%q: i = %d, want %d", c.src, toks[0].i, c.i)
		}
	}
}

func TestLexMalformedNumbers(t *testing.T) {
	for _, src := range []string{"1.2.3", "-", "+", ".", "1e5", "12x"} {
		if _, err := lexAll(t, src); err == nil {
			t.Errorf("%q: want an error", src)
		}
	}
	// Out of float64's range: strconv reports it and the lexer passes it on.
	if _, err := lexAll(t, "1"+strings.Repeat("0", 400)+".0"); err == nil {
		t.Error("overflowing real: want an error")
	}
}

func TestLexLiteralStrings(t *testing.T) {
	cases := []struct{ src, want string }{
		{"()", ""},
		{"(abc)", "abc"},
		{"(a(b)c)", "a(b)c"},
		{`(\n\r\t\b\f)`, "\n\r\t\b\f"},
		{`(\(\)\\)`, `()\`},
		{`(\053)`, "+"},
		{`(\53)`, "+"},
		{`(\5)`, "\005"},
		{`(\q)`, "q"},
		{"(a\\\nb)", "ab"},
		{"(a\\\r\nb)", "ab"},
		{"(a\\\rb)", "ab"},
		{"(a\rb)", "a\nb"},
		{"(a\r\nb)", "a\nb"},
		{"(a\nb)", "a\nb"},
	}
	for _, c := range cases {
		toks, err := lexAll(t, c.src)
		if err != nil {
			t.Fatalf("%q: %v", c.src, err)
		}
		if len(toks) != 1 || toks[0].kind != tokString || string(toks[0].text) != c.want {
			t.Errorf("%q: got %q, want %q", c.src, toks[0].text, c.want)
		}
	}
	for _, src := range []string{"(abc", `(abc\`} {
		if _, err := lexAll(t, src); err == nil {
			t.Errorf("%q: want an error", src)
		}
	}
}

func TestLexHexStrings(t *testing.T) {
	cases := []struct{ src, want string }{
		{"<>", ""},
		{"<41>", "A"},
		{"<4 1>", "A"},
		{"<4>", "@"},
		{"<abcDEF>", "\xab\xcd\xef"},
	}
	for _, c := range cases {
		toks, err := lexAll(t, c.src)
		if err != nil {
			t.Fatalf("%q: %v", c.src, err)
		}
		if string(toks[0].text) != c.want {
			t.Errorf("%q: got %q, want %q", c.src, toks[0].text, c.want)
		}
	}
	// A stray non-hex byte is skipped, not fatal: one file in the corpus
	// sample carries one, and losing the object is the worse outcome.
	if toks, err := lexAll(t, "<4G1>"); err != nil || string(toks[0].text) != "A" {
		t.Errorf("stray byte: %+v, %v", toks, err)
	}
	for _, src := range []string{"<41"} {
		if _, err := lexAll(t, src); err == nil {
			t.Errorf("%q: want an error", src)
		}
	}
}

func TestLexNames(t *testing.T) {
	cases := []struct{ src, want string }{
		{"/Name", "Name"},
		{"/", ""},
		{"/A#20B", "A B"},
		{"/#41", "A"},
		{"/Name1 /Two", "Name1"},
	}
	for _, c := range cases {
		toks, err := lexAll(t, c.src)
		if err != nil {
			t.Fatalf("%q: %v", c.src, err)
		}
		if toks[0].kind != tokName || string(toks[0].text) != c.want {
			t.Errorf("%q: got %q, want %q", c.src, toks[0].text, c.want)
		}
	}
	for _, src := range []string{"/A#2", "/A#GG"} {
		if _, err := lexAll(t, src); err == nil {
			t.Errorf("%q: want an error", src)
		}
	}
}

func TestLexKeywords(t *testing.T) {
	toks, err := lexAll(t, "obj endobj R true")
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 4 {
		t.Fatalf("got %d tokens", len(toks))
	}
	for i, w := range []string{"obj", "endobj", "R", "true"} {
		if toks[i].kind != tokKeyword || string(toks[i].text) != w {
			t.Errorf("token %d = %q", i, toks[i].text)
		}
	}
}

func TestHexVal(t *testing.T) {
	for c, want := range map[byte]int{'0': 0, '9': 9, 'a': 10, 'f': 15, 'A': 10, 'F': 15, 'g': -1, ' ': -1} {
		if got := hexVal(c); got != want {
			t.Errorf("hexVal(%q) = %d, want %d", c, got, want)
		}
	}
}

func TestIsDelim(t *testing.T) {
	for _, c := range []byte("()<>[]{}/%") {
		if !isDelim(c) {
			t.Errorf("isDelim(%q) = false", c)
		}
	}
	if isDelim('a') {
		t.Error("isDelim('a') = true")
	}
	if !isRegular('a') || isRegular(' ') || isRegular('/') {
		t.Error("isRegular")
	}
}

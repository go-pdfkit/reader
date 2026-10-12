package reader

import (
	"testing"
	"unicode/utf8"
)

// ⛔ THE DEFECT. `pdfops info` printed a document's creator as
// "\xfe\xffL\x00a\x00T\x00e\x00X" for as long as the verb had existed,
// because ToString hands back BYTES and the bytes of a text string are not
// text.
func TestAUTF16StringIsNotReadAByteAtATime(t *testing.T) {
	// How xdvipdfmx actually wrote it, BOM and all.
	raw := "\xfe\xff\x00L\x00a\x00T\x00e\x00X\x00 \x00v\x00i\x00a\x00 \x00p\x00a\x00n\x00d\x00o\x00c"
	got, ok := TextString(String(raw))
	if !ok {
		t.Fatal("a string object was refused")
	}
	if got != "LaTeX via pandoc" {
		t.Errorf("read %q", got)
	}
	// And the bytes are still available for whoever wants them.
	if b, _ := ToString(String(raw)); string(b) != raw {
		t.Error("ToString no longer hands back the bytes")
	}
}

func TestAUTF16StringOutsideLatin1(t *testing.T) {
	// "Résumé" in UTF-16BE: the accented letters are what a producer reaches
	// for UTF-16 to say in the first place.
	raw := "\xfe\xff\x00R\x00\xe9\x00s\x00u\x00m\x00\xe9"
	if got, _ := TextString(String(raw)); got != "Résumé" {
		t.Errorf("read %q, want %q", got, "Résumé")
	}
}

// A character outside the basic plane arrives as a surrogate pair, and a pair
// decoded one unit at a time is two replacement characters.
func TestASurrogatePair(t *testing.T) {
	// U+1F4C4 PAGE FACING UP is D83D DCC4.
	raw := "\xfe\xff\xd8\x3d\xdc\xc4"
	if got, _ := TextString(String(raw)); got != "\U0001F4C4" {
		t.Errorf("read %q (%d runes)", got, utf8.RuneCountInString(got))
	}
}

// Half a code unit is not a character, and a truncated string should look
// truncated rather than lose its last letter silently.
func TestAUTF16StringWithAnOddNumberOfBytes(t *testing.T) {
	raw := "\xfe\xff\x00a\x00"
	got, _ := TextString(String(raw))
	if want := "a" + string(utf8.RuneError); got != want {
		t.Errorf("read %q, want %q", got, want)
	}
}

// A BOM and nothing after it is an empty string, not a failure.
func TestNothingButABOM(t *testing.T) {
	if got, ok := TextString(String("\xfe\xff")); !ok || got != "" {
		t.Errorf("read %q, %v", got, ok)
	}
}

// PDF 2.0 added UTF-8 with its own mark.
func TestAUTF8StringWithItsMark(t *testing.T) {
	if got, _ := TextString(String("\xef\xbb\xbfRésumé")); got != "Résumé" {
		t.Errorf("read %q", got)
	}
	// And bytes that are not UTF-8 after a mark that says they are: each one
	// becomes a replacement character rather than making the whole string
	// unprintable.
	got, _ := TextString(String("\xef\xbb\xbfa\xffb"))
	if want := "a" + string(utf8.RuneError) + "b"; got != want {
		t.Errorf("read %q, want %q", got, want)
	}
}

// ⛔ PDFDocEncoding is NOT Latin 1, and 0x18 to 0x1F is the first place a
// Latin-1 decoder goes wrong: Latin 1 has control characters there and PDF
// has the spacing accents.
func TestTheAccentsWhereLatin1HasControlCharacters(t *testing.T) {
	got, _ := TextString(String("\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f"))
	if want := "˘ˇˆ˙˝˛˚˜"; got != want {
		t.Errorf("read %q, want %q", got, want)
	}
}

// The typographic marks at 0x80 to 0x9E, where Latin 1 has nothing at all.
func TestTheTypographicMarks(t *testing.T) {
	cases := map[byte]rune{
		0x80: '•', 0x83: '…', 0x84: '—', 0x85: '–', 0x8D: '“', 0x8E: '”',
		0x90: '’', 0x92: '™', 0x93: 'ﬁ', 0x96: 'Œ', 0x9E: 'ž',
	}
	for b, want := range cases {
		got, _ := TextString(String([]byte{b}))
		if got != string(want) {
			t.Errorf("0x%02X read %q, want %q", b, got, want)
		}
	}
	// 0xA0 is the Euro, not a non-breaking space.
	if got, _ := TextString(String("\xa0")); got != "€" {
		t.Errorf("0xA0 read %q, want €", got)
	}
}

// Three bytes the standard leaves undefined. A byte that means nothing is said
// to mean nothing, rather than quietly becoming a soft hyphen.
func TestTheBytesTheStandardLeavesUndefined(t *testing.T) {
	for _, b := range []byte{0x7F, 0x9F, 0xAD} {
		got, _ := TextString(String([]byte{b}))
		if got != string(utf8.RuneError) {
			t.Errorf("0x%02X read %q, want the replacement character", b, got)
		}
	}
}

// The rest of PDFDocEncoding is Latin 1, where the byte is the code point.
func TestTheRestIsLatin1(t *testing.T) {
	if got, _ := TextString(String("R\xe9sum\xe9")); got != "Résumé" {
		t.Errorf("read %q, want %q", got, "Résumé")
	}
	if got, _ := TextString(String("plain ASCII")); got != "plain ASCII" {
		t.Errorf("read %q", got)
	}
}

// ⛔ A string that merely LOOKS like UTF-16LE is PDFDocEncoding with a zero
// byte in it. Guessing at it turns a readable title into mojibake, which is
// the opposite failure to reading UTF-16BE a byte at a time and just as
// silent.
func TestAStringThatLooksLikeUTF16ButHasNoMark(t *testing.T) {
	// The whole string, not "it does not say La": accepting FF FE as a mark
	// and then decoding big-endian anyway gives mojibake too, so only the
	// exact reading tells the two apart.
	got, _ := TextString(String("\xff\xfeL\x00a\x00"))
	if want := "ÿþL\x00a\x00"; got != want {
		t.Errorf("read %q, want %q — it guessed at a little-endian mark", got, want)
	}
}

// Something that is not a string at all is refused rather than rendered.
func TestWhatIsNotAStringAtAll(t *testing.T) {
	for _, o := range []Object{Name("Title"), Integer(4), nil, Dict{}} {
		if got, ok := TextString(o); ok {
			t.Errorf("%T was read as a text string: %q", o, got)
		}
	}
}

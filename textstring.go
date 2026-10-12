package reader

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// TextString decodes a PDF text string: what a document says in a human
// language, as opposed to the bytes of a name, a date or a content stream. A
// title, an author, a bookmark's label, a form field's value.
//
// ⛔ [ToString] is NOT this. It hands back the bytes, and the bytes of a text
// string are not text: `pdfops info` printed a document's creator as
// "\xfe\xffL\x00a\x00T\x00e\x00X" — a UTF-16 string read a byte at a time —
// and that output had been shipping for as long as the verb had.
//
// A text string is one of three things (ISO 32000-1 §7.9.2.2 and §7.9.2.2.1 of
// ISO 32000-2):
//
//	FE FF …         UTF-16BE, which is how a document says anything outside
//	                Latin 1 and most of what any modern producer writes
//	EF BB BF …      UTF-8, which PDF 2.0 added
//	anything else   PDFDocEncoding
//
// ⛔ Only those two byte orders of mark, and no guessing beyond them. A string
// that merely looks like UTF-16LE is PDFDocEncoding that happens to contain a
// zero byte, and decoding it as UTF-16 turns a readable title into mojibake —
// which is the opposite failure to the one above and just as silent.
func TextString(o Object) (string, bool) {
	b, ok := ToString(o)
	if !ok {
		return "", false
	}
	switch {
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return fromUTF16BE(b[2:]), true
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return fromUTF8(b[3:]), true
	}
	return fromPDFDoc(b), true
}

// fromUTF16BE decodes big-endian UTF-16, surrogate pairs included.
func fromUTF16BE(b []byte) string {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
	}
	out := string(utf16.Decode(units))
	if len(b)%2 == 1 {
		// An odd number of bytes: the last one is half a code unit, and half a
		// code unit is not a character. It is said rather than dropped, so
		// that a truncated string looks truncated.
		out += string(utf8.RuneError)
	}
	return out
}

// fromUTF8 keeps valid UTF-8 and replaces what is not, so that the result is
// always a string Go can print, compare and put in JSON.
func fromUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		out.WriteRune(r)
		b = b[size:]
	}
	return out.String()
}

// fromPDFDoc decodes PDFDocEncoding, which is Latin 1 with the holes in it
// filled: the spacing accents at 0x18 to 0x1F where Latin 1 has control
// characters, the typographic marks at 0x80 to 0x9E, and the Euro at 0xA0.
func fromPDFDoc(b []byte) string {
	var out strings.Builder
	out.Grow(len(b))
	for _, c := range b {
		out.WriteRune(pdfDocRune(c))
	}
	return out.String()
}

// pdfDocRune is one byte of PDFDocEncoding.
func pdfDocRune(c byte) rune {
	switch {
	case c >= 0x18 && c <= 0x1F:
		return pdfDocAccents[c-0x18]
	case c >= 0x80 && c <= 0x9E:
		return pdfDocMarks[c-0x80]
	case c == 0xA0:
		return '€'
	case c == 0x7F || c == 0x9F || c == 0xAD:
		// Undefined by the standard. A byte that means nothing is said to
		// mean nothing rather than quietly becoming a soft hyphen or a
		// control character.
		return utf8.RuneError
	}
	// The rest is Latin 1, where the byte IS the code point.
	return rune(c)
}

// pdfDocAccents is 0x18 to 0x1F: breve, caron, circumflex, dot accent, double
// acute, ogonek, ring, tilde. Latin 1 has control characters here, which is
// the first place a Latin-1 decoder goes wrong on a PDF.
var pdfDocAccents = [8]rune{
	'˘', 'ˇ', 'ˆ', '˙', '˝', '˛', '˚', '˜',
}

// pdfDocMarks is 0x80 to 0x9E, the typographic marks: Annex D.2 of ISO
// 32000-1, checked against it entry by entry rather than remembered.
var pdfDocMarks = [31]rune{
	'•', '†', '‡', '…', '—', '–', 'ƒ', '⁄',
	'‹', '›', '−', '‰', '„', '“', '”', '‘',
	'’', '‚', '™', 'ﬁ', 'ﬂ', 'Ł', 'Œ', 'Š',
	'Ÿ', 'Ž', 'ı', 'ł', 'œ', 'š', 'ž',
}

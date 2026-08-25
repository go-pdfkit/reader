package reader

import (
	"fmt"
	"strconv"
)

// A SyntaxError reports malformed PDF syntax and where it was found, so a
// repair pass can restart from a known offset instead of giving up.
type SyntaxError struct {
	Offset int
	Msg    string
}

// Error implements the error interface.
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("reader: %s at offset %d", e.Msg, e.Offset)
}

// isSpace reports the six bytes the specification calls white-space,
// NUL included.
func isSpace(c byte) bool {
	return c == 0 || c == 9 || c == 10 || c == 12 || c == 13 || c == 32
}

// isDelim reports the eight delimiter bytes.
func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// isRegular reports a byte that may appear inside a name, a number or a
// keyword: anything that is neither white-space nor a delimiter.
func isRegular(c byte) bool { return !isSpace(c) && !isDelim(c) }

// hexVal decodes one hexadecimal digit, or reports -1.
func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

type tokKind uint8

const (
	tokEOF tokKind = iota
	tokInteger
	tokReal
	tokString
	tokName
	tokArrayOpen
	tokArrayClose
	tokDictOpen
	tokDictClose
	tokBraceOpen
	tokBraceClose
	tokKeyword
)

// A token is one lexical unit. text carries the bytes of a string, name or
// keyword; i and f carry the value of a number.
type token struct {
	kind tokKind
	text []byte
	i    int64
	f    float64
	pos  int
}

// A lexer walks the PDF object syntax over a byte slice. It never copies the
// input except where an escape sequence forces it.
type lexer struct {
	buf []byte
	pos int
}

// skipSpace consumes white-space and comments.
func (l *lexer) skipSpace() {
	for l.pos < len(l.buf) {
		c := l.buf[l.pos]
		if isSpace(c) {
			l.pos++
			continue
		}
		if c == '%' {
			for l.pos < len(l.buf) && l.buf[l.pos] != '\n' && l.buf[l.pos] != '\r' {
				l.pos++
			}
			continue
		}
		return
	}
}

// next returns the token at the current position and advances past it.
func (l *lexer) next() (token, error) {
	l.skipSpace()
	start := l.pos
	if l.pos >= len(l.buf) {
		return token{kind: tokEOF, pos: start}, nil
	}
	switch c := l.buf[l.pos]; {
	case c == '[':
		l.pos++
		return token{kind: tokArrayOpen, pos: start}, nil
	case c == ']':
		l.pos++
		return token{kind: tokArrayClose, pos: start}, nil
	case c == '{':
		l.pos++
		return token{kind: tokBraceOpen, pos: start}, nil
	case c == '}':
		l.pos++
		return token{kind: tokBraceClose, pos: start}, nil
	case c == '<':
		if l.pos+1 < len(l.buf) && l.buf[l.pos+1] == '<' {
			l.pos += 2
			return token{kind: tokDictOpen, pos: start}, nil
		}
		return l.hexString(start)
	case c == '>':
		if l.pos+1 < len(l.buf) && l.buf[l.pos+1] == '>' {
			l.pos += 2
			return token{kind: tokDictClose, pos: start}, nil
		}
		l.pos++
		return token{}, &SyntaxError{start, "stray '>'"}
	case c == '(':
		return l.literalString(start)
	case c == ')':
		l.pos++
		return token{}, &SyntaxError{start, "stray ')'"}
	case c == '/':
		return l.name(start)
	case c == '+' || c == '-' || c == '.' || (c >= '0' && c <= '9'):
		return l.number(start)
	default:
		return l.keyword(start), nil
	}
}

// keyword reads a run of regular characters: an operator, obj, endobj, R,
// true, false, null.
func (l *lexer) keyword(start int) token {
	p := l.pos
	for p < len(l.buf) && isRegular(l.buf[p]) {
		p++
	}
	l.pos = p
	return token{kind: tokKeyword, text: l.buf[start:p], pos: start}
}

// number reads an integer or a real.
func (l *lexer) number(start int) (token, error) {
	p := l.pos
	for p < len(l.buf) && isRegular(l.buf[p]) {
		p++
	}
	s := l.buf[l.pos:p]
	l.pos = p
	if i, err := strconv.ParseInt(string(s), 10, 64); err == nil {
		return token{kind: tokInteger, i: i, f: float64(i), pos: start}, nil
	}
	f, err := parseReal(s)
	if err != nil {
		return token{}, &SyntaxError{start, "malformed number " + strconv.Quote(string(s))}
	}
	return token{kind: tokReal, i: int64(f), f: f, pos: start}, nil
}

// parseReal accepts what producers actually write, which is a superset of what
// the grammar allows: "4.", "-.002", and the doubled sign of "--5" (only the
// first sign counts). An exponent is not PDF syntax and is rejected.
func parseReal(s []byte) (float64, error) {
	clean := make([]byte, 0, len(s)+1)
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		clean = append(clean, s[i])
		i++
	}
	for i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits, dot := 0, false
	for ; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits++
			clean = append(clean, c)
		case c == '.' && !dot:
			dot = true
			clean = append(clean, c)
		default:
			return 0, errNotANumber
		}
	}
	if digits == 0 {
		return 0, errNotANumber
	}
	return strconv.ParseFloat(string(clean), 64)
}

// errNotANumber is internal: [lexer.number] turns it into a SyntaxError that
// carries the offset.
var errNotANumber = fmt.Errorf("reader: not a number")

// literalString reads a (parenthesised) string, resolving escapes. Nested
// parentheses nest; an end-of-line inside the string, however written, becomes
// a single line feed.
func (l *lexer) literalString(start int) (token, error) {
	l.pos++
	depth := 1
	out := []byte{}
	for l.pos < len(l.buf) {
		c := l.buf[l.pos]
		l.pos++
		switch c {
		case '\\':
			if l.pos >= len(l.buf) {
				return token{}, &SyntaxError{start, "unterminated string"}
			}
			e := l.buf[l.pos]
			l.pos++
			switch {
			case e == 'n':
				out = append(out, '\n')
			case e == 'r':
				out = append(out, '\r')
			case e == 't':
				out = append(out, '\t')
			case e == 'b':
				out = append(out, '\b')
			case e == 'f':
				out = append(out, '\f')
			case e == '\r':
				// A backslash before an end-of-line continues the line.
				if l.pos < len(l.buf) && l.buf[l.pos] == '\n' {
					l.pos++
				}
			case e == '\n':
			case e >= '0' && e <= '7':
				v := int(e - '0')
				for k := 0; k < 2 && l.pos < len(l.buf) && l.buf[l.pos] >= '0' && l.buf[l.pos] <= '7'; k++ {
					v = v*8 + int(l.buf[l.pos]-'0')
					l.pos++
				}
				out = append(out, byte(v))
			default:
				// \( \) \\ and, per the specification, any other escaped
				// character stands for itself.
				out = append(out, e)
			}
		case '(':
			depth++
			out = append(out, c)
		case ')':
			depth--
			if depth == 0 {
				return token{kind: tokString, text: out, pos: start}, nil
			}
			out = append(out, c)
		case '\r':
			if l.pos < len(l.buf) && l.buf[l.pos] == '\n' {
				l.pos++
			}
			out = append(out, '\n')
		default:
			out = append(out, c)
		}
	}
	return token{}, &SyntaxError{start, "unterminated string"}
}

// hexString reads a <hex> string. White-space is ignored and a final odd digit
// is padded with zero, as the specification requires.
func (l *lexer) hexString(start int) (token, error) {
	l.pos++
	out := []byte{}
	hi := -1
	for l.pos < len(l.buf) {
		c := l.buf[l.pos]
		l.pos++
		if c == '>' {
			if hi >= 0 {
				out = append(out, byte(hi<<4))
			}
			return token{kind: tokString, text: out, pos: start}, nil
		}
		if isSpace(c) {
			continue
		}
		v := hexVal(c)
		if v < 0 {
			// Producers do emit a stray byte inside <…>; skipping it is what
			// every other reader does, and losing the whole object is worse.
			continue
		}
		if hi < 0 {
			hi = v
			continue
		}
		out = append(out, byte(hi<<4|v))
		hi = -1
	}
	return token{}, &SyntaxError{start, "unterminated hex string"}
}

// name reads a /Name, resolving #xx escapes.
func (l *lexer) name(start int) (token, error) {
	l.pos++
	out := []byte{}
	for l.pos < len(l.buf) && isRegular(l.buf[l.pos]) {
		c := l.buf[l.pos]
		l.pos++
		if c != '#' {
			out = append(out, c)
			continue
		}
		if l.pos+1 >= len(l.buf) {
			return token{}, &SyntaxError{start, "truncated #xx escape in name"}
		}
		h1, h2 := hexVal(l.buf[l.pos]), hexVal(l.buf[l.pos+1])
		if h1 < 0 || h2 < 0 {
			return token{}, &SyntaxError{start, "invalid #xx escape in name"}
		}
		out = append(out, byte(h1<<4|h2))
		l.pos += 2
	}
	return token{kind: tokName, text: out, pos: start}, nil
}

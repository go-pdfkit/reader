package reader

import (
	"bytes"
	"strconv"
)

// A parser turns the token stream into objects. resolve is consulted only for
// a stream's /Length, which files are allowed to store indirectly.
type parser struct {
	lex     lexer
	resolve Resolver
}

// ParseObject parses a single direct object from the start of b and reports how
// many bytes it consumed.
func ParseObject(b []byte) (Object, int, error) {
	p := &parser{lex: lexer{buf: b}}
	o, err := p.parseObject()
	if err != nil {
		return nil, 0, err
	}
	return o, p.lex.pos, nil
}

// ParseIndirectObject parses "N G obj … endobj" from the start of b, returning
// the reference it defines, the object itself, and how many bytes it consumed.
// A stream's /Length may be an indirect reference; resolve supplies it, and a
// nil or unhelpful resolve falls back to scanning for the endstream keyword.
func ParseIndirectObject(b []byte, resolve Resolver) (Ref, Object, int, error) {
	p := &parser{lex: lexer{buf: b}, resolve: resolve}
	num, err := p.expectUint("object number")
	if err != nil {
		return Ref{}, nil, 0, err
	}
	gen, err := p.expectUint("generation number")
	if err != nil {
		return Ref{}, nil, 0, err
	}
	if err := p.expectKeyword("obj"); err != nil {
		return Ref{}, nil, 0, err
	}
	ref := Ref{Num: num, Gen: gen}
	// An indirect object may have no value at all — "7 0 obj endobj" —
	// which the corpus does contain. Its value is null.
	if p.accept("endobj") {
		return ref, Null{}, p.lex.pos, nil
	}
	obj, err := p.parseObject()
	if err != nil {
		return ref, nil, 0, err
	}
	obj, err = p.maybeStream(obj)
	if err != nil {
		return ref, nil, 0, err
	}
	p.accept("endobj")
	return ref, obj, p.lex.pos, nil
}

// expectUint reads a non-negative integer token.
func (p *parser) expectUint(what string) (int, error) {
	t, err := p.lex.next()
	if err != nil {
		return 0, err
	}
	if t.kind != tokInteger || t.i < 0 {
		return 0, &SyntaxError{t.pos, what + " expected"}
	}
	return int(t.i), nil
}

// expectKeyword requires the given keyword next.
func (p *parser) expectKeyword(kw string) error {
	t, err := p.lex.next()
	if err != nil {
		return err
	}
	if t.kind != tokKeyword || string(t.text) != kw {
		return &SyntaxError{t.pos, strconv.Quote(kw) + " expected"}
	}
	return nil
}

// accept consumes the keyword if it is next, and reports whether it was.
func (p *parser) accept(kw string) bool {
	save := p.lex.pos
	t, err := p.lex.next()
	if err == nil && t.kind == tokKeyword && string(t.text) == kw {
		return true
	}
	p.lex.pos = save
	return false
}

// maybeStream turns a dictionary followed by the stream keyword into a Stream.
func (p *parser) maybeStream(obj Object) (Object, error) {
	save := p.lex.pos
	t, err := p.lex.next()
	if err != nil || t.kind != tokKeyword || string(t.text) != "stream" {
		p.lex.pos = save
		return obj, nil
	}
	d, ok := obj.(Dict)
	if !ok {
		return nil, &SyntaxError{t.pos, "stream keyword after a " + obj.Kind().String()}
	}
	return p.readStream(d, t.pos)
}

// readStream extracts the bytes between stream and endstream. The declared
// /Length is trusted only when endstream really does follow it — producers get
// it wrong often enough that the scan below is the common path, not the
// exception.
func (p *parser) readStream(d Dict, kwPos int) (Object, error) {
	b := p.lex.buf
	i := p.lex.pos
	for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	if i < len(b) && b[i] == '\r' {
		i++
	}
	if i < len(b) && b[i] == '\n' {
		i++
	}
	start := i
	if n, ok := p.streamLength(d); ok && start+n <= len(b) {
		if end := endstreamAt(b, start+n); end >= 0 {
			p.lex.pos = end
			return &Stream{Dict: d, Raw: b[start : start+n]}, nil
		}
	}
	j := bytes.Index(b[start:], []byte("endstream"))
	if j < 0 {
		return nil, &SyntaxError{kwPos, "unterminated stream"}
	}
	end := start + j
	p.lex.pos = end + len("endstream")
	// The end-of-line that precedes endstream belongs to the file, not to the
	// stream's data.
	if end > start && b[end-1] == '\n' {
		end--
	}
	if end > start && b[end-1] == '\r' {
		end--
	}
	return &Stream{Dict: d, Raw: b[start:end]}, nil
}

// streamLength reads /Length, following an indirect reference when it can.
func (p *parser) streamLength(d Dict) (int, bool) {
	o, err := Resolve(d.Get("Length"), p.resolve)
	if err != nil {
		return 0, false
	}
	n, ok := ToInt(o)
	if !ok || n < 0 {
		return 0, false
	}
	return int(n), true
}

// endstreamAt reports the offset just past an endstream keyword that follows
// white-space at i, or -1 when something else is there.
func endstreamAt(b []byte, i int) int {
	for i < len(b) && isSpace(b[i]) {
		i++
	}
	if bytes.HasPrefix(b[i:], []byte("endstream")) {
		return i + len("endstream")
	}
	return -1
}

// parseObject reads one object.
func (p *parser) parseObject() (Object, error) {
	t, err := p.lex.next()
	if err != nil {
		return nil, err
	}
	return p.parseFrom(t)
}

// parseFrom reads the object that starts with an already-read token.
func (p *parser) parseFrom(t token) (Object, error) {
	switch t.kind {
	case tokEOF:
		return nil, &SyntaxError{t.pos, "unexpected end of input"}
	case tokInteger:
		return p.maybeRef(t)
	case tokReal:
		return Real(t.f), nil
	case tokString:
		return String(t.text), nil
	case tokName:
		return Name(t.text), nil
	case tokArrayOpen:
		return p.parseArray()
	case tokDictOpen:
		return p.parseDict()
	case tokKeyword:
		switch string(t.text) {
		case "true":
			return Bool(true), nil
		case "false":
			return Bool(false), nil
		case "null":
			return Null{}, nil
		}
		return nil, &SyntaxError{t.pos, "unexpected keyword " + strconv.Quote(string(t.text))}
	}
	return nil, &SyntaxError{t.pos, "unexpected token"}
}

// maybeRef decides between the integer just read and the "N G R" that starts
// the same way. Only two tokens of lookahead separate them.
func (p *parser) maybeRef(t token) (Object, error) {
	save := p.lex.pos
	if t.i >= 0 {
		if t2, err := p.lex.next(); err == nil && t2.kind == tokInteger && t2.i >= 0 {
			if t3, err := p.lex.next(); err == nil && t3.kind == tokKeyword && string(t3.text) == "R" {
				return Ref{Num: int(t.i), Gen: int(t2.i)}, nil
			}
		}
	}
	p.lex.pos = save
	return Integer(t.i), nil
}

// parseArray reads the body of an array, the opening bracket already consumed.
func (p *parser) parseArray() (Object, error) {
	arr := Array{}
	for {
		t, err := p.lex.next()
		if err != nil {
			return nil, err
		}
		switch t.kind {
		case tokArrayClose:
			return arr, nil
		case tokEOF:
			return nil, &SyntaxError{t.pos, "unterminated array"}
		}
		o, err := p.parseFrom(t)
		if err != nil {
			return nil, err
		}
		arr = append(arr, o)
	}
}

// parseDict reads the body of a dictionary, "<<" already consumed.
func (p *parser) parseDict() (Object, error) {
	d := Dict{}
	for {
		t, err := p.lex.next()
		if err != nil {
			return nil, err
		}
		switch t.kind {
		case tokDictClose:
			return d, nil
		case tokEOF:
			return nil, &SyntaxError{t.pos, "unterminated dictionary"}
		case tokName:
		default:
			return nil, &SyntaxError{t.pos, "dictionary key is a " + t.kind.describe() + ", not a name"}
		}
		v, err := p.parseObject()
		if err != nil {
			return nil, err
		}
		d[Name(t.text)] = v
	}
}

// describe names a token kind for an error message.
func (k tokKind) describe() string {
	switch k {
	case tokInteger, tokReal:
		return "number"
	case tokString:
		return "string"
	case tokArrayOpen, tokArrayClose:
		return "bracket"
	case tokDictOpen, tokDictClose:
		return "dictionary delimiter"
	case tokBraceOpen, tokBraceClose:
		return "brace"
	case tokKeyword:
		return "keyword"
	}
	return "token"
}

package reader

import (
	"bytes"
	"fmt"
)

// An Operation is one step of a content stream: an operator and the operands
// that precede it. An inline image is reported as the operator "BI" with its
// dictionary and data attached, since the three keywords it spans are one
// thing.
type Operation struct {
	Operator string
	Operands []Object
	Image    *InlineImage
}

// An InlineImage is the dictionary and the still-encoded bytes of a BI … ID …
// EI sequence. Its dictionary uses the abbreviated keys inline images are
// written with; [InlineImage.Expanded] gives the ordinary spelling.
type InlineImage struct {
	Dict Dict
	Raw  []byte
}

// inlineKeys maps the abbreviations an inline image dictionary uses to the
// names their long form would have.
var inlineKeys = map[Name]Name{
	"BPC": "BitsPerComponent",
	"CS":  "ColorSpace",
	"D":   "Decode",
	"DP":  "DecodeParms",
	"F":   "Filter",
	"H":   "Height",
	"IM":  "ImageMask",
	"I":   "Interpolate",
	"W":   "Width",
	"L":   "Length",
}

// inlineColourSpaces maps the abbreviated colour space names.
var inlineColourSpaces = map[Name]Name{
	"G":    "DeviceGray",
	"RGB":  "DeviceRGB",
	"CMYK": "DeviceCMYK",
	"I":    "Indexed",
}

// Expanded returns the image's dictionary with the abbreviated keys and colour
// space names written out, so it can be read like any image XObject.
func (im *InlineImage) Expanded() Dict {
	out := Dict{}
	for k, v := range im.Dict {
		if long, ok := inlineKeys[k]; ok {
			k = long
		}
		if k == "ColorSpace" {
			if n, ok := ToName(v); ok {
				if long, ok := inlineColourSpaces[n]; ok {
					v = long
				}
			}
		}
		out[k] = v
	}
	return out
}

// A ContentScanner walks a content stream one operation at a time. A stream
// with rubbish in it yields the operations around the rubbish rather than
// nothing at all, which is what a renderer needs; [ContentScanner.Err] reports
// the first thing that did not parse.
type ContentScanner struct {
	lex     lexer
	pending []Object
	err     error
}

// NewContentScanner reads operations from a decoded content stream.
func NewContentScanner(data []byte) *ContentScanner {
	return &ContentScanner{lex: lexer{buf: data}}
}

// Err reports the first malformed token the scan stepped over, if any.
func (s *ContentScanner) Err() error { return s.err }

// Next returns the next operation, and false once the stream is exhausted.
func (s *ContentScanner) Next() (Operation, bool) {
	for {
		t, err := s.lex.next()
		if err != nil {
			s.note(err)
			// Step over the byte that would not parse and carry on.
			if s.lex.pos < len(s.lex.buf) {
				s.lex.pos++
				continue
			}
			return Operation{}, false
		}
		if t.kind == tokEOF {
			return Operation{}, false
		}
		if t.kind != tokKeyword {
			s.operand(t)
			continue
		}
		switch string(t.text) {
		case "true":
			s.pending = append(s.pending, Bool(true))
			continue
		case "false":
			s.pending = append(s.pending, Bool(false))
			continue
		case "null":
			s.pending = append(s.pending, Null{})
			continue
		case "BI":
			img, err := s.inlineImage()
			if err != nil {
				s.note(err)
				return Operation{}, false
			}
			return s.emit("BI", img), true
		}
		return s.emit(string(t.text), nil), true
	}
}

// operand parses one operand, an already-read token starting it.
func (s *ContentScanner) operand(t token) {
	p := &parser{lex: s.lex}
	o, err := p.parseFrom(t)
	s.lex = p.lex
	if err != nil {
		s.note(err)
		if s.lex.pos < len(s.lex.buf) {
			s.lex.pos++
		}
		return
	}
	s.pending = append(s.pending, o)
}

// emit finishes an operation and clears the operands waiting for it.
func (s *ContentScanner) emit(op string, img *InlineImage) Operation {
	out := Operation{Operator: op, Operands: s.pending, Image: img}
	s.pending = nil
	return out
}

// note keeps the first error the scan met.
func (s *ContentScanner) note(err error) {
	if s.err == nil {
		s.err = err
	}
}

// inlineImage reads the dictionary after BI and the data after ID.
func (s *ContentScanner) inlineImage() (*InlineImage, error) {
	d := Dict{}
	for {
		t, err := s.lex.next()
		if err != nil {
			return nil, err
		}
		if t.kind == tokEOF {
			return nil, &SyntaxError{t.pos, "an inline image has no ID"}
		}
		if t.kind == tokKeyword && string(t.text) == "ID" {
			break
		}
		if t.kind != tokName {
			return nil, &SyntaxError{t.pos, "an inline image key is not a name"}
		}
		p := &parser{lex: s.lex}
		v, err := p.parseObject()
		s.lex = p.lex
		if err != nil {
			return nil, err
		}
		d[Name(t.text)] = v
	}
	// Exactly one white-space byte separates ID from the data.
	if s.lex.pos < len(s.lex.buf) && isSpace(s.lex.buf[s.lex.pos]) {
		s.lex.pos++
	}
	start := s.lex.pos
	end, err := inlineImageEnd(s.lex.buf, start, d)
	if err != nil {
		return nil, err
	}
	raw := s.lex.buf[start:end]
	s.lex.pos = end
	// Step over the EI that follows.
	for s.lex.pos < len(s.lex.buf) && isSpace(s.lex.buf[s.lex.pos]) {
		s.lex.pos++
	}
	if bytes.HasPrefix(s.lex.buf[s.lex.pos:], []byte("EI")) {
		s.lex.pos += 2
	}
	return &InlineImage{Dict: d, Raw: raw}, nil
}

// Operations tokenises a whole content stream. The error it returns describes
// what did not parse; the operations it returns are still worth having.
func Operations(data []byte) ([]Operation, error) {
	s := NewContentScanner(data)
	var out []Operation
	for {
		op, ok := s.Next()
		if !ok {
			return out, s.Err()
		}
		out = append(out, op)
	}
}

// PageContent returns the decoded content stream of the i'th page, counting
// from one. A page whose /Contents is an array has its streams joined, as the
// specification requires, with a newline between them.
func (d *Document) PageContent(i int) ([]byte, error) {
	page, err := d.Page(i)
	if err != nil {
		return nil, err
	}
	return d.contentOf(page)
}

// contentOf decodes a page's /Contents, whether one stream or several.
func (d *Document) contentOf(page Dict) ([]byte, error) {
	o, err := d.Resolve(page.Get("Contents"))
	if err != nil {
		return nil, err
	}
	switch v := o.(type) {
	case *Stream:
		return d.decodedContent(v)
	case Array:
		var out []byte
		for _, e := range v {
			eo, err := d.Resolve(e)
			if err != nil {
				return nil, err
			}
			s, ok := ToStream(eo)
			if !ok {
				continue
			}
			part, err := d.decodedContent(s)
			if err != nil {
				return nil, err
			}
			if len(out) > 0 {
				out = append(out, '\n')
			}
			out = append(out, part...)
		}
		return out, nil
	}
	return nil, nil
}

// decodedContent decodes one content stream, refusing an image filter, which
// has no business being there.
func (d *Document) decodedContent(s *Stream) ([]byte, error) {
	data, img, err := d.DecodeStream(s)
	if err != nil {
		return nil, err
	}
	if img != "" {
		return nil, fmt.Errorf("reader: a content stream is filtered as an image (/%s)", img)
	}
	return data, nil
}

// PageOperations tokenises the i'th page's content stream, counting from one.
func (d *Document) PageOperations(i int) ([]Operation, error) {
	data, err := d.PageContent(i)
	if err != nil {
		return nil, err
	}
	ops, _ := Operations(data)
	return ops, nil
}

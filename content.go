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
//
// A dictionary may say the same thing twice and disagree with itself: /W 20
// beside /Width 10, /CS /RGB beside /ColorSpace /3chanRGB. The specification
// permits both spellings in an inline image and says nothing about which wins,
// and the public implementations disagree — pdf.js asks for the abbreviation
// and falls back to the written-out name, MuPDF asks the other way round.
//
// The abbreviation wins here. Two reasons: it is the spelling Table 93 gives
// for an inline image, the written-out name being the tolerated alias; and
// safedocs' own fixture for this case marks the abbreviation as the line to
// remove "to see the effect", which is a statement about what the reference
// viewer does.
//
// What is not defensible is the answer changing between runs. Expanding in one
// pass over the map made the winner depend on Go's map iteration order, which
// is deliberately random: the same file drew differently each time it was
// opened, with no way for a caller to notice. Two passes settle it — the
// written-out names first, then the abbreviations over them — and no order
// within either pass can matter, because the names in each are distinct.
func (im *InlineImage) Expanded() Dict {
	out := Dict{}
	for k, v := range im.Dict {
		if _, abbreviated := inlineKeys[k]; abbreviated {
			continue
		}
		out[k] = expandColourSpace(k, v)
	}
	for k, v := range im.Dict {
		long, abbreviated := inlineKeys[k]
		if !abbreviated {
			continue
		}
		out[long] = expandColourSpace(long, v)
	}
	return out
}

// expandColourSpace writes out an abbreviated colour space name, which is the
// one value that is abbreviated as well as its key.
func expandColourSpace(k Name, v Object) Object {
	if k != "ColorSpace" {
		return v
	}
	n, ok := ToName(v)
	if !ok {
		return v
	}
	if long, ok := inlineColourSpaces[n]; ok {
		return long
	}
	return v
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
//
// A content stream whose filters cannot be applied contributes what could be
// salvaged from it rather than failing the page, which is what a renderer
// needs; the error is for a page that is not there at all.
// [Document.PageContentDecoded] says whether any salvaging happened.
func (d *Document) PageContent(i int) ([]byte, error) {
	dec, err := d.PageContentDecoded(i)
	return dec.Data, err
}

// PageContentDecoded is [Document.PageContent] with the outcome of the decode
// attached: [Decoded.Recovered] reports that at least one of the page's content
// streams could not be decoded cleanly, and [Decoded.Cause] says why.
func (d *Document) PageContentDecoded(i int) (Decoded, error) {
	page, err := d.Page(i)
	if err != nil {
		return Decoded{}, err
	}
	return d.contentOf(page)
}

// contentOf decodes a page's /Contents, whether one stream or several.
func (d *Document) contentOf(page Dict) (Decoded, error) {
	o, err := d.Resolve(page.Get("Contents"))
	if err != nil {
		return Decoded{}, err
	}
	switch v := o.(type) {
	case *Stream:
		return d.decodedContent(v), nil
	case Array:
		var out Decoded
		for _, e := range v {
			eo, err := d.Resolve(e)
			if err != nil {
				return Decoded{}, err
			}
			s, ok := ToStream(eo)
			if !ok {
				continue
			}
			part := d.decodedContent(s)
			if part.Recovered && !out.Recovered {
				out.Recovered, out.Cause, out.Filter = true, part.Cause, part.Filter
				out.Undecoded = part.Undecoded
			}
			if len(part.Data) == 0 {
				continue
			}
			if len(out.Data) > 0 {
				out.Data = append(out.Data, '\n')
			}
			out.Data = append(out.Data, part.Data...)
		}
		return out, nil
	}
	return Decoded{}, nil
}

// decodedContent decodes one content stream. An image filter has no business
// being there, so the bytes it holds are not content: they are reported as
// salvage, not handed over as if they could be tokenised.
func (d *Document) decodedContent(s *Stream) Decoded {
	dec := d.DecodeStreamRecovering(s)
	if dec.Image != "" {
		return Decoded{
			Undecoded: dec.Data,
			Recovered: true,
			Filter:    dec.Image,
			Cause:     fmt.Errorf("reader: a content stream is filtered as an image (/%s)", dec.Image),
		}
	}
	return dec
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

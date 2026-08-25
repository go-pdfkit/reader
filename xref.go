package reader

import (
	"bytes"
	"fmt"
)

// An xrefEntry says where object N lives.
type xrefEntry struct {
	kind    byte  // 'n' at a byte offset, 'o' inside an object stream, 'f' free
	offset  int64 // kind 'n'
	gen     int   // kind 'n'
	strmNum int   // kind 'o': the object stream's own number
	strmIdx int   // kind 'o': the index within it
}

// maxXrefSections bounds a /Prev chain, which a file is free to make a cycle.
const maxXrefSections = 1024

// startxrefWindow is how far back from the end the startxref keyword is looked
// for. The specification allows only a short tail; real files sometimes append
// junk, so the window is generous.
const startxrefWindow = 4096

// findStartxref reads the offset the file's last startxref names.
func (d *Document) findStartxref() (int64, error) {
	from := len(d.buf) - startxrefWindow
	if from < 0 {
		from = 0
	}
	i := bytes.LastIndex(d.buf[from:], []byte("startxref"))
	if i < 0 {
		return 0, fmt.Errorf("reader: no startxref in the last %d bytes", startxrefWindow)
	}
	l := &lexer{buf: d.buf, pos: from + i + len("startxref")}
	t, err := l.next()
	if err != nil {
		return 0, err
	}
	if t.kind != tokInteger || t.i < 0 || t.i >= int64(len(d.buf)) {
		return 0, &SyntaxError{t.pos, "startxref does not name an offset inside the file"}
	}
	return t.i, nil
}

// loadXref follows the chain of cross-reference sections from the last one
// back through /Prev, taking the newest definition of every object and of
// every trailer entry.
func (d *Document) loadXref() error {
	off, err := d.findStartxref()
	if err != nil {
		return err
	}
	seen := map[int64]bool{}
	for n := 0; off > 0 && n < maxXrefSections; n++ {
		if seen[off] {
			return fmt.Errorf("reader: cross-reference sections form a cycle at offset %d", off)
		}
		seen[off] = true
		tr, err := d.readXrefSection(off)
		if err != nil {
			return err
		}
		d.mergeTrailer(tr)
		// A hybrid file keeps a second, stream-shaped section for the objects
		// its table deliberately hides from old readers.
		if x, ok := ToInt(tr.Get("XRefStm")); ok && x > 0 && !seen[x] {
			seen[x] = true
			if hy, err := d.readXrefSection(x); err == nil {
				d.mergeTrailer(hy)
			}
		}
		p, ok := ToInt(tr.Get("Prev"))
		if !ok || p <= 0 {
			break
		}
		off = p
	}
	if len(d.xref) == 0 {
		return fmt.Errorf("reader: the cross-reference table is empty")
	}
	return nil
}

// mergeTrailer keeps the first value seen for each key, sections being read
// newest first.
func (d *Document) mergeTrailer(tr Dict) {
	if d.trailer == nil {
		d.trailer = Dict{}
	}
	for k, v := range tr {
		if _, ok := d.trailer[k]; !ok {
			d.trailer[k] = v
		}
	}
}

// setEntry records an entry unless a newer section already defined the object.
func (d *Document) setEntry(num int, e xrefEntry) {
	if num < 0 {
		return
	}
	if _, ok := d.xref[num]; !ok {
		d.xref[num] = e
	}
}

// readXrefSection reads whichever of the two forms is at off and returns its
// trailer dictionary.
func (d *Document) readXrefSection(off int64) (Dict, error) {
	if off < 0 || off >= int64(len(d.buf)) {
		return nil, fmt.Errorf("reader: cross-reference offset %d is outside the file", off)
	}
	l := &lexer{buf: d.buf, pos: int(off)}
	t, err := l.next()
	if err != nil {
		return nil, err
	}
	if t.kind == tokKeyword && string(t.text) == "xref" {
		return d.readXrefTable(l)
	}
	_, obj, _, err := ParseIndirectObject(d.buf[off:], nil)
	if err != nil {
		return nil, err
	}
	s, ok := ToStream(obj)
	if !ok {
		return nil, &SyntaxError{int(off), "neither an xref table nor an xref stream"}
	}
	return d.readXrefStream(s)
}

// readXrefTable reads the classic subsection form, the xref keyword already
// consumed, up to and including its trailer dictionary.
func (d *Document) readXrefTable(l *lexer) (Dict, error) {
	p := &parser{lex: *l}
	for {
		save := p.lex.pos
		t, err := p.lex.next()
		if err != nil {
			return nil, err
		}
		if t.kind == tokKeyword && string(t.text) == "trailer" {
			o, err := p.parseObject()
			if err != nil {
				return nil, err
			}
			tr, ok := ToDict(o)
			if !ok {
				return nil, &SyntaxError{save, "the trailer is not a dictionary"}
			}
			return tr, nil
		}
		if t.kind != tokInteger {
			return nil, &SyntaxError{t.pos, "a cross-reference subsection header was expected"}
		}
		start := int(t.i)
		count, err := p.expectUint("a subsection entry count")
		if err != nil {
			return nil, err
		}
		for i := 0; i < count; i++ {
			if err := d.readXrefTableEntry(p, start+i); err != nil {
				return nil, err
			}
		}
	}
}

// readXrefTableEntry reads one "offset generation n|f" line.
func (d *Document) readXrefTableEntry(p *parser, num int) error {
	t1, err := p.lex.next()
	if err != nil {
		return err
	}
	if t1.kind != tokInteger {
		return &SyntaxError{t1.pos, "a cross-reference entry offset was expected"}
	}
	t2, err := p.lex.next()
	if err != nil {
		return err
	}
	if t2.kind != tokInteger {
		return &SyntaxError{t2.pos, "a cross-reference entry generation was expected"}
	}
	t3, err := p.lex.next()
	if err != nil {
		return err
	}
	if t3.kind != tokKeyword || len(t3.text) != 1 || (t3.text[0] != 'n' && t3.text[0] != 'f') {
		return &SyntaxError{t3.pos, "a cross-reference entry must end in n or f"}
	}
	if t3.text[0] == 'n' {
		d.setEntry(num, xrefEntry{kind: 'n', offset: t1.i, gen: int(t2.i)})
		return nil
	}
	d.setEntry(num, xrefEntry{kind: 'f'})
	return nil
}

// readXrefStream reads the /Type /XRef stream form. Its own data is never
// encrypted, so it is decoded without the document's decryptor.
func (d *Document) readXrefStream(s *Stream) (Dict, error) {
	data, img, err := Decode(s.Dict, s.Raw, nil)
	if err != nil {
		return nil, err
	}
	if img != "" {
		return nil, fmt.Errorf("reader: the cross-reference stream is filtered as an image")
	}
	widths, err := xrefWidths(s.Dict)
	if err != nil {
		return nil, err
	}
	index, err := xrefIndex(s.Dict)
	if err != nil {
		return nil, err
	}
	row := widths[0] + widths[1] + widths[2]
	if row == 0 {
		return nil, fmt.Errorf("reader: the cross-reference stream's /W entries are all zero")
	}
	pos := 0
	for i := 0; i+1 < len(index); i += 2 {
		for k := 0; k < index[i+1]; k++ {
			if pos+row > len(data) {
				// A section that claims more rows than it carries still
				// contributes the rows it does carry.
				return s.Dict, nil
			}
			f1 := xrefField(data[pos:], widths[0], 1)
			f2 := xrefField(data[pos+widths[0]:], widths[1], 0)
			f3 := xrefField(data[pos+widths[0]+widths[1]:], widths[2], 0)
			pos += row
			d.storeXrefStreamEntry(index[i]+k, f1, f2, f3)
		}
	}
	return s.Dict, nil
}

// storeXrefStreamEntry turns one decoded row into an entry.
func (d *Document) storeXrefStreamEntry(num int, f1, f2, f3 int64) {
	switch f1 {
	case 1:
		d.setEntry(num, xrefEntry{kind: 'n', offset: f2, gen: int(f3)})
	case 2:
		d.setEntry(num, xrefEntry{kind: 'o', strmNum: int(f2), strmIdx: int(f3)})
	default:
		d.setEntry(num, xrefEntry{kind: 'f'})
	}
}

// xrefWidths reads /W, which says how many bytes each of the three fields
// occupies.
func xrefWidths(d Dict) ([3]int, error) {
	var w [3]int
	arr, ok := ToArray(d.Get("W"))
	if !ok || len(arr) < 3 {
		return w, fmt.Errorf("reader: the cross-reference stream has no usable /W")
	}
	for i := 0; i < 3; i++ {
		n, ok := ToInt(arr[i])
		if !ok || n < 0 || n > 8 {
			return w, fmt.Errorf("reader: /W[%d] is not a byte width", i)
		}
		w[i] = int(n)
	}
	return w, nil
}

// xrefIndex reads /Index, defaulting to the whole range /Size describes.
func xrefIndex(d Dict) ([]int, error) {
	arr, ok := ToArray(d.Get("Index"))
	if !ok {
		size, ok := ToInt(d.Get("Size"))
		if !ok || size < 0 {
			return nil, fmt.Errorf("reader: the cross-reference stream has neither /Index nor /Size")
		}
		return []int{0, int(size)}, nil
	}
	out := make([]int, 0, len(arr))
	for _, e := range arr {
		n, ok := ToInt(e)
		if !ok || n < 0 {
			return nil, fmt.Errorf("reader: /Index holds a value that is not a count")
		}
		out = append(out, int(n))
	}
	return out, nil
}

// xrefField reads one big-endian field of the given width, or its default when
// the width is zero.
func xrefField(b []byte, width int, def int64) int64 {
	if width == 0 {
		return def
	}
	v := int64(0)
	for i := 0; i < width; i++ {
		v = v<<8 | int64(b[i])
	}
	return v
}

package reader

import (
	"bytes"
	"fmt"
)

// maxPageTreeNodes bounds a page tree walk. /Count is a claim, not a fact, and
// /Kids can be made to point back at an ancestor.
const maxPageTreeNodes = 1 << 20

// inheritable lists the page attributes a node may take from an ancestor.
var inheritable = []Name{"Resources", "MediaBox", "CropBox", "Rotate"}

// A Document is a parsed PDF file: its cross-reference information, its
// trailer, and lazy access to every object in it.
type Document struct {
	buf      []byte
	xref     map[int]xrefEntry
	trailer  Dict
	cache    map[int]Object
	loading  map[int]bool
	objStms  map[int]map[int]Object
	pages    []Ref
	repaired bool
}

// Open parses the cross-reference information of a PDF file held in memory.
// A file whose tables are damaged is rebuilt by scanning it, so Open fails
// only when there is no usable document structure at all.
func Open(b []byte) (*Document, error) {
	d := &Document{
		buf:     b,
		xref:    map[int]xrefEntry{},
		cache:   map[int]Object{},
		loading: map[int]bool{},
		objStms: map[int]map[int]Object{},
	}
	err := d.loadXref()
	if err == nil {
		if _, cerr := d.Catalog(); cerr == nil {
			return d, nil
		}
		// Tables that parse but lead nowhere are worse than none: rebuild.
		err = fmt.Errorf("reader: the trailer does not lead to a catalog")
	}
	if rerr := d.repair(); rerr != nil {
		return nil, err
	}
	return d, nil
}

// Trailer returns the file's trailer dictionary, the newest value of every key
// across the chain of cross-reference sections.
func (d *Document) Trailer() Dict { return d.trailer }

// Repaired reports whether the document's structure was rebuilt by scanning
// the file rather than read from its cross-reference tables.
func (d *Document) Repaired() bool { return d.repaired }

// Resolver returns the function that turns this document's indirect references
// into objects, for [Resolve] and [Decode].
func (d *Document) Resolver() Resolver { return d.Get }

// Resolve follows indirect references until it reaches a direct object.
func (d *Document) Resolve(o Object) (Object, error) { return Resolve(o, d.Get) }

// GetDict resolves an entry of a dictionary to a dictionary.
func (d *Document) GetDict(from Dict, key Name) (Dict, bool) {
	o, err := d.Resolve(from.Get(key))
	if err != nil {
		return nil, false
	}
	return ToDict(o)
}

// Get returns the object an indirect reference names, or [Null] when the file
// does not define it.
func (d *Document) Get(ref Ref) (Object, error) {
	if o, ok := d.cache[ref.Num]; ok {
		return o, nil
	}
	e, ok := d.xref[ref.Num]
	if !ok || e.kind == 'f' {
		return Null{}, nil
	}
	if d.loading[ref.Num] {
		// An object stream whose own /Length or /Filter points back at an
		// object inside itself would otherwise recurse forever.
		return Null{}, nil
	}
	d.loading[ref.Num] = true
	defer delete(d.loading, ref.Num)

	var obj Object
	var err error
	if e.kind == 'o' {
		obj, err = d.getFromObjectStream(ref.Num, e)
	} else {
		obj, err = d.getAtOffset(ref.Num, e)
	}
	if err != nil {
		return nil, err
	}
	d.cache[ref.Num] = obj
	return obj, nil
}

// getAtOffset reads an object written directly in the file. An offset that
// does not hold the expected object number means the tables are wrong, which
// is common enough that it triggers a rebuild rather than an error.
func (d *Document) getAtOffset(num int, e xrefEntry) (Object, error) {
	if e.offset < 0 || e.offset >= int64(len(d.buf)) {
		return d.retryAfterRepair(num, fmt.Errorf("reader: object %d is at offset %d, outside the file", num, e.offset))
	}
	got, obj, _, err := ParseIndirectObject(d.buf[e.offset:], d.Get)
	if err != nil {
		return d.retryAfterRepair(num, err)
	}
	if got.Num != num {
		return d.retryAfterRepair(num, fmt.Errorf("reader: offset %d holds object %d, not %d", e.offset, got.Num, num))
	}
	return obj, nil
}

// retryAfterRepair rebuilds the tables once and looks the object up again.
func (d *Document) retryAfterRepair(num int, cause error) (Object, error) {
	if d.repaired {
		return Null{}, nil
	}
	if err := d.repair(); err != nil {
		return nil, cause
	}
	e, ok := d.xref[num]
	if !ok || e.kind != 'n' || e.offset >= int64(len(d.buf)) {
		return Null{}, nil
	}
	got, obj, _, err := ParseIndirectObject(d.buf[e.offset:], d.Get)
	if err != nil || got.Num != num {
		return Null{}, nil
	}
	return obj, nil
}

// getFromObjectStream reads an object held inside a /Type /ObjStm stream.
func (d *Document) getFromObjectStream(num int, e xrefEntry) (Object, error) {
	objs, err := d.objectStream(e.strmNum)
	if err != nil {
		return nil, err
	}
	if o, ok := objs[num]; ok {
		return o, nil
	}
	// The index is a hint; a stream that does not hold the object simply
	// does not define it.
	return Null{}, nil
}

// objectStream parses an object stream once and keeps its contents.
func (d *Document) objectStream(num int) (map[int]Object, error) {
	if objs, ok := d.objStms[num]; ok {
		return objs, nil
	}
	objs := map[int]Object{}
	d.objStms[num] = objs

	o, err := d.Get(Ref{Num: num})
	if err != nil {
		return objs, err
	}
	s, ok := ToStream(o)
	if !ok {
		return objs, nil
	}
	data, img, err := d.DecodeStream(s)
	if err != nil || img != "" {
		return objs, nil
	}
	n := int(intOr(s.Dict.Get("N"), 0))
	first := int(intOr(s.Dict.Get("First"), 0))
	if n <= 0 || first <= 0 || first > len(data) {
		return objs, nil
	}
	l := &lexer{buf: data[:first]}
	type pair struct{ num, off int }
	pairs := make([]pair, 0, n)
	for i := 0; i < n; i++ {
		t1, err1 := l.next()
		t2, err2 := l.next()
		if err1 != nil || err2 != nil || t1.kind != tokInteger || t2.kind != tokInteger {
			break
		}
		pairs = append(pairs, pair{int(t1.i), first + int(t2.i)})
	}
	for _, p := range pairs {
		if p.off < 0 || p.off > len(data) {
			continue
		}
		obj, _, err := ParseObject(data[p.off:])
		if err != nil {
			continue
		}
		objs[p.num] = obj
	}
	return objs, nil
}

// intOr resolves nothing; it reads a direct integer or returns a default.
func intOr(o Object, def int64) int64 {
	if n, ok := ToInt(o); ok {
		return n
	}
	return def
}

// DecodeStream applies a stream's filter chain, resolving any indirect decode
// parameters against this document.
func (d *Document) DecodeStream(s *Stream) ([]byte, Name, error) {
	return Decode(s.Dict, s.Raw, d.Get)
}

// Catalog returns the document catalogue the trailer's /Root names.
func (d *Document) Catalog() (Dict, error) {
	if d.trailer == nil {
		return nil, fmt.Errorf("reader: the file has no trailer")
	}
	o, err := d.Resolve(d.trailer.Get("Root"))
	if err != nil {
		return nil, err
	}
	cat, ok := ToDict(o)
	if !ok {
		return nil, fmt.Errorf("reader: /Root is a %s, not a dictionary", o.Kind())
	}
	if _, ok := d.GetDict(cat, "Pages"); !ok {
		return nil, fmt.Errorf("reader: the catalogue has no page tree")
	}
	return cat, nil
}

// PageCount reports how many pages the document has.
func (d *Document) PageCount() int { return len(d.pageRefs()) }

// PageRef returns the reference of the i'th page, counting from one.
func (d *Document) PageRef(i int) (Ref, bool) {
	refs := d.pageRefs()
	if i < 1 || i > len(refs) {
		return Ref{}, false
	}
	return refs[i-1], true
}

// Page returns the i'th page's dictionary, counting from one, with the
// attributes it inherits from its ancestors filled in.
func (d *Document) Page(i int) (Dict, error) {
	ref, ok := d.PageRef(i)
	if !ok {
		return nil, fmt.Errorf("reader: page %d is out of range (the document has %d)", i, d.PageCount())
	}
	o, err := d.Get(ref)
	if err != nil {
		return nil, err
	}
	page, ok := ToDict(o)
	if !ok {
		return nil, fmt.Errorf("reader: page %d is a %s, not a dictionary", i, o.Kind())
	}
	return d.withInherited(page), nil
}

// withInherited copies the page and fills in what its ancestors provide.
func (d *Document) withInherited(page Dict) Dict {
	out := Dict{}
	for k, v := range page {
		out[k] = v
	}
	node := page
	for depth := 0; depth < maxRefDepth; depth++ {
		missing := false
		for _, k := range inheritable {
			if _, ok := out[k]; !ok {
				missing = true
			}
		}
		if !missing {
			break
		}
		parent, ok := d.GetDict(node, "Parent")
		if !ok {
			break
		}
		for _, k := range inheritable {
			if _, ok := out[k]; ok {
				continue
			}
			if v, ok := parent[k]; ok {
				out[k] = v
			}
		}
		node = parent
	}
	return out
}

// pageRefs walks the page tree once, in document order.
func (d *Document) pageRefs() []Ref {
	if d.pages != nil {
		return d.pages
	}
	d.pages = []Ref{}
	cat, err := d.Catalog()
	if err != nil {
		return d.pages
	}
	root := cat["Pages"]
	seen := map[Ref]bool{}
	d.walkPages(root, seen, 0)
	if len(d.pages) == 0 {
		// A tree that yields nothing but a catalogue that names one page node
		// is still a one-page document.
		d.pages = d.scanForPages()
	}
	return d.pages
}

// walkPages appends the leaves of a page tree node in order.
func (d *Document) walkPages(node Object, seen map[Ref]bool, depth int) {
	if depth > 64 || len(d.pages) >= maxPageTreeNodes {
		return
	}
	ref, isRef := node.(Ref)
	if isRef {
		if seen[ref] {
			return
		}
		seen[ref] = true
	}
	o, err := d.Resolve(node)
	if err != nil {
		return
	}
	dict, ok := ToDict(o)
	if !ok {
		return
	}
	kids, hasKids := d.kidsOf(dict)
	if !hasKids {
		if isRef {
			d.pages = append(d.pages, ref)
		}
		return
	}
	for _, kid := range kids {
		d.walkPages(kid, seen, depth+1)
	}
}

// kidsOf reports a node's /Kids when it is an interior node of the page tree.
func (d *Document) kidsOf(node Dict) (Array, bool) {
	if t, ok := ToName(node.Get("Type")); ok && t == "Page" {
		return nil, false
	}
	o, err := d.Resolve(node.Get("Kids"))
	if err != nil {
		return nil, false
	}
	kids, ok := ToArray(o)
	return kids, ok
}

// Version reports the PDF version the header declares, "1.7" and the like.
func (d *Document) Version() string {
	i := bytes.Index(d.buf, []byte("%PDF-"))
	if i < 0 || i+8 > len(d.buf) {
		return ""
	}
	return string(bytes.TrimRight(d.buf[i+5:i+8], "\r\n \t"))
}

// scanForPages is the last resort for a page tree that resolves to nothing:
// every object in the file that calls itself a page, in object-number order.
func (d *Document) scanForPages() []Ref {
	var out []Ref
	for _, num := range d.objectsOfType("Page") {
		out = append(out, Ref{Num: num})
	}
	return out
}

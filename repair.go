package reader

import (
	"bytes"
	"fmt"
	"slices"
)

// repair rebuilds the cross-reference information by reading the file itself
// rather than what it says about itself: every "N G obj" header in order, the
// later definition of an object number winning, plus whatever trailer or
// catalogue can be found. Damaged files are the normal case for a reader, not
// an exceptional one.
func (d *Document) repair() error {
	d.repaired = true
	d.cache = map[int]Object{}
	d.objStms = map[int]map[int]Object{}
	d.pages = nil
	d.xref = map[int]xrefEntry{}

	for _, h := range scanObjectHeaders(d.buf) {
		// A later definition wins, so the entry is overwritten unconditionally
		// rather than through setEntry.
		d.xref[h.num] = xrefEntry{kind: 'n', offset: int64(h.offset), gen: h.gen}
	}
	if len(d.xref) == 0 {
		return fmt.Errorf("reader: the file holds no indirect objects")
	}
	// Objects held in object streams are invisible to a header scan; take them
	// from every object stream the scan did find.
	d.indexObjectStreams()
	d.loadRepairedTrailer()
	if d.trailer == nil {
		return fmt.Errorf("reader: no document catalogue found")
	}
	return nil
}

// loadRepairedTrailer finds a trailer that leads to a catalogue: the file's own
// trailer dictionaries first, newest first; then any object that calls itself a
// catalogue; and failing both, a catalogue built over whatever pages survive.
func (d *Document) loadRepairedTrailer() {
	previous := d.trailer
	for _, tr := range scanTrailers(d.buf) {
		d.trailer = tr
		if _, err := d.Catalog(); err == nil {
			return
		}
	}
	if previous != nil {
		d.trailer = previous
		if _, err := d.Catalog(); err == nil {
			return
		}
	}
	d.trailer = nil
	for _, num := range d.objectsOfType("Catalog") {
		d.trailer = Dict{"Root": Ref{Num: num}}
		if _, err := d.Catalog(); err == nil {
			return
		}
		d.trailer = nil
	}
	d.synthesiseCatalogue()
}

// objectsOfType lists, in object-number order, the objects whose /Type is the
// given name.
func (d *Document) objectsOfType(want Name) []int {
	// The objects that exist are walked, in order, rather than every number
	// up to the largest of them. A file may name any object number it likes:
	// a 219-byte one in the wild declares object 2147483647, and counting up
	// to that is two thousand million map lookups for three objects — which
	// is a quarter of a minute of somebody's afternoon for a file that fits
	// in a tweet.
	nums := make([]int, 0, len(d.xref))
	for num := range d.xref {
		nums = append(nums, num)
	}
	slices.Sort(nums)
	var out []int
	for _, num := range nums {
		o, err := d.Get(Ref{Num: num})
		if err != nil {
			continue
		}
		dict, ok := ToDict(o)
		if !ok {
			continue
		}
		if t, ok := ToName(dict.Get("Type")); ok && t == want {
			out = append(out, num)
		}
	}
	return out
}

// synthesiseCatalogue is the last resort for a file whose catalogue and page
// tree did not survive: build one over whatever objects still call themselves
// pages. A truncated file that kept its pages is worth opening; one that kept
// none is not a document.
func (d *Document) synthesiseCatalogue() {
	var kids Array
	for _, num := range d.objectsOfType("Page") {
		kids = append(kids, Ref{Num: num})
	}
	if len(kids) == 0 {
		return
	}
	d.trailer = Dict{"Root": Dict{
		"Type": Name("Catalog"),
		"Pages": Dict{
			"Type":  Name("Pages"),
			"Kids":  kids,
			"Count": Integer(len(kids)),
		},
	}}
}

// indexObjectStreams adds the objects held inside every object stream the scan
// found, without overwriting an object written directly in the file.
func (d *Document) indexObjectStreams() {
	var streams []int
	for num := range d.xref {
		o, err := d.Get(Ref{Num: num})
		if err != nil {
			continue
		}
		s, ok := ToStream(o)
		if !ok {
			continue
		}
		if t, ok := ToName(s.Dict.Get("Type")); ok && t == "ObjStm" {
			streams = append(streams, num)
		}
	}
	for _, num := range streams {
		// The stream object is already cached by the pass above, so this
		// cannot fail; an empty result simply contributes nothing.
		objs, _ := d.objectStream(num)
		for objNum, obj := range objs {
			if _, ok := d.xref[objNum]; ok {
				continue
			}
			d.xref[objNum] = xrefEntry{kind: 'o', strmNum: num}
			d.cache[objNum] = obj
		}
	}
}

// An objectHeader is one "N G obj" found by scanning.
type objectHeader struct {
	num, gen, offset int
}

// scanObjectHeaders finds every indirect object header in the file, in the
// order they appear.
func scanObjectHeaders(b []byte) []objectHeader {
	var out []objectHeader
	for i := 0; i+3 <= len(b); {
		j := bytes.Index(b[i:], []byte("obj"))
		if j < 0 {
			return out
		}
		at := i + j
		i = at + 3
		if at+3 < len(b) && isRegular(b[at+3]) {
			continue
		}
		h, ok := headerBefore(b, at)
		if !ok {
			continue
		}
		out = append(out, h)
	}
	return out
}

// headerBefore reads the "N G" that must precede an obj keyword at at.
func headerBefore(b []byte, at int) (objectHeader, bool) {
	p := skipSpaceBack(b, at)
	if p == at {
		return objectHeader{}, false
	}
	gen, p, ok := digitsBack(b, p)
	if !ok {
		return objectHeader{}, false
	}
	q := skipSpaceBack(b, p)
	if q == p {
		return objectHeader{}, false
	}
	num, p, ok := digitsBack(b, q)
	if !ok {
		return objectHeader{}, false
	}
	return objectHeader{num: num, gen: gen, offset: p}, true
}

// skipSpaceBack walks back over white-space and returns the new position.
func skipSpaceBack(b []byte, p int) int {
	for p > 0 && isSpace(b[p-1]) {
		p--
	}
	return p
}

// digitsBack reads a decimal number that ends at p, going backwards.
func digitsBack(b []byte, p int) (int, int, bool) {
	end := p
	for p > 0 && b[p-1] >= '0' && b[p-1] <= '9' {
		p--
	}
	if p == end || end-p > 10 {
		return 0, p, false
	}
	v := 0
	for _, c := range b[p:end] {
		v = v*10 + int(c-'0')
	}
	return v, p, true
}

// scanTrailers finds the file's trailer dictionaries, newest first.
func scanTrailers(b []byte) []Dict {
	var out []Dict
	for i := len(b); i > 0; {
		j := bytes.LastIndex(b[:i], []byte("trailer"))
		if j < 0 {
			break
		}
		i = j
		o, _, err := ParseObject(b[j+len("trailer"):])
		if err != nil {
			continue
		}
		if tr, ok := ToDict(o); ok {
			out = append(out, tr)
		}
	}
	return out
}

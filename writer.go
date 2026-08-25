package reader

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// AppendObject writes an object in PDF syntax, appending to dst. Dictionary
// keys are written in order, so the same object always produces the same
// bytes — which is what makes a rewritten file comparable with the one it came
// from.
func AppendObject(dst []byte, o Object) []byte {
	switch v := o.(type) {
	case nil:
		return append(dst, "null"...)
	case Null:
		return append(dst, "null"...)
	case Bool:
		if v {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case Integer:
		return strconv.AppendInt(dst, int64(v), 10)
	case Real:
		return appendReal(dst, float64(v))
	case String:
		return appendString(dst, v)
	case Name:
		return appendName(dst, v)
	case Ref:
		dst = strconv.AppendInt(dst, int64(v.Num), 10)
		dst = append(dst, ' ')
		dst = strconv.AppendInt(dst, int64(v.Gen), 10)
		return append(dst, " R"...)
	case Array:
		dst = append(dst, '[')
		for i, e := range v {
			if i > 0 {
				dst = append(dst, ' ')
			}
			dst = AppendObject(dst, e)
		}
		return append(dst, ']')
	case Dict:
		return appendDict(dst, v)
	case *Stream:
		return appendStream(dst, v)
	}
	return append(dst, "null"...)
}

// FormatObject renders an object in PDF syntax.
func FormatObject(o Object) []byte { return AppendObject(nil, o) }

// appendReal writes a number the way PDF spells one: no exponent, since the
// format has no notation for it, and no infinity or not-a-number, which it
// cannot represent at all.
func appendReal(dst []byte, f float64) []byte {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return append(dst, '0')
	}
	if f == 0 {
		// Negative zero is legal and meaningless, and writing it back out
		// would be read as the integer zero, so a rewrite would not be a
		// fixpoint. Producers do emit it.
		return append(dst, '0')
	}
	return strconv.AppendFloat(dst, f, 'f', -1, 64)
}

// appendString writes a literal string, escaping only what has to be escaped
// and rendering anything unprintable as an octal escape.
func appendString(dst []byte, s []byte) []byte {
	dst = append(dst, '(')
	for _, c := range s {
		switch {
		case c == '(' || c == ')' || c == '\\':
			dst = append(dst, '\\', c)
		case c == '\n':
			dst = append(dst, '\\', 'n')
		case c == '\r':
			dst = append(dst, '\\', 'r')
		case c == '\t':
			dst = append(dst, '\\', 't')
		case c < 32 || c > 126:
			dst = append(dst, '\\')
			dst = append(dst, '0'+c>>6&7, '0'+c>>3&7, '0'+c&7)
		default:
			dst = append(dst, c)
		}
	}
	return append(dst, ')')
}

// appendName writes a name, escaping every byte that may not appear in one.
func appendName(dst []byte, n Name) []byte {
	dst = append(dst, '/')
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(n); i++ {
		c := n[i]
		if !isRegular(c) || c == '#' || c < '!' || c > '~' {
			dst = append(dst, '#', hex[c>>4], hex[c&15])
			continue
		}
		dst = append(dst, c)
	}
	return dst
}

// appendDict writes a dictionary with its keys in order.
func appendDict(dst []byte, d Dict) []byte {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	dst = append(dst, "<<"...)
	for _, k := range keys {
		dst = appendName(dst, Name(k))
		dst = append(dst, ' ')
		dst = AppendObject(dst, d[Name(k)])
		dst = append(dst, ' ')
	}
	if len(keys) > 0 {
		dst = dst[:len(dst)-1]
	}
	return append(dst, ">>"...)
}

// appendStream writes a stream, its /Length rewritten to the length of the
// data actually being written.
func appendStream(dst []byte, s *Stream) []byte {
	d := Dict{}
	for k, v := range s.Dict {
		d[k] = v
	}
	d["Length"] = Integer(len(s.Raw))
	dst = appendDict(dst, d)
	dst = append(dst, "\nstream\n"...)
	dst = append(dst, s.Raw...)
	return append(dst, "\nendstream"...)
}

// A Writer builds a PDF file out of objects. Numbers are handed out by
// [Writer.Reserve], objects are written with [Writer.Put], and [Writer.Finish]
// adds the cross-reference table and the trailer.
type Writer struct {
	buf     bytes.Buffer
	offsets map[int]int
	next    int
	copied  map[*Document]map[int]Ref
	err     error
}

// NewWriter starts a file with the given version in its header, "1.7" when the
// version is empty.
func NewWriter(version string) *Writer {
	if version == "" {
		version = "1.7"
	}
	w := &Writer{
		offsets: map[int]int{},
		next:    1,
		copied:  map[*Document]map[int]Ref{},
	}
	fmt.Fprintf(&w.buf, "%%PDF-%s\n", version)
	// The four bytes above 127 tell every tool downstream that this file is
	// not text, which is what the specification asks for.
	w.buf.Write([]byte{'%', 0xE2, 0xE3, 0xCF, 0xD3, '\n'})
	return w
}

// Reserve hands out an object number that nothing has been written to yet.
func (w *Writer) Reserve() Ref {
	r := Ref{Num: w.next}
	w.next++
	return r
}

// Put writes an object under a reserved number.
func (w *Writer) Put(ref Ref, o Object) {
	if _, seen := w.offsets[ref.Num]; seen {
		w.note(fmt.Errorf("reader: object %d written twice", ref.Num))
		return
	}
	if ref.Num >= w.next {
		w.next = ref.Num + 1
	}
	w.offsets[ref.Num] = w.buf.Len()
	fmt.Fprintf(&w.buf, "%d %d obj\n", ref.Num, ref.Gen)
	w.buf.Write(AppendObject(nil, o))
	w.buf.WriteString("\nendobj\n")
}

// Add reserves a number, writes the object under it and returns the reference.
func (w *Writer) Add(o Object) Ref {
	ref := w.Reserve()
	w.Put(ref, o)
	return ref
}

// Err reports the first thing that went wrong while building the file.
func (w *Writer) Err() error { return w.err }

// note keeps the first error.
func (w *Writer) note(err error) {
	if w.err == nil {
		w.err = err
	}
}

// Finish writes the cross-reference table and the trailer and returns the
// file. /Size is filled in; the caller supplies /Root and whatever else the
// trailer needs.
func (w *Writer) Finish(trailer Dict) ([]byte, error) {
	high := 0
	for num := range w.offsets {
		if num > high {
			high = num
		}
	}
	start := w.buf.Len()
	fmt.Fprintf(&w.buf, "xref\n0 %d\n0000000000 65535 f \n", high+1)
	for num := 1; num <= high; num++ {
		off, ok := w.offsets[num]
		if !ok {
			w.buf.WriteString("0000000000 65535 f \n")
			continue
		}
		fmt.Fprintf(&w.buf, "%010d 00000 n \n", off)
	}
	out := Dict{}
	for k, v := range trailer {
		out[k] = v
	}
	out["Size"] = Integer(high + 1)
	w.buf.WriteString("trailer\n")
	w.buf.Write(AppendObject(nil, out))
	fmt.Fprintf(&w.buf, "\nstartxref\n%d\n%%%%EOF\n", start)
	if w.err != nil {
		return nil, w.err
	}
	return w.buf.Bytes(), nil
}

// Copy writes an object from a document into this file, following every
// indirect reference it reaches and renumbering as it goes, so that objects
// from several documents can live side by side. An object already copied from
// the same document keeps the number it was given.
func (w *Writer) Copy(d *Document, o Object) Object {
	seen, ok := w.copied[d]
	if !ok {
		seen = map[int]Ref{}
		w.copied[d] = seen
	}
	return w.copyObject(d, seen, o, 0)
}

// copyObject is Copy's recursion.
func (w *Writer) copyObject(d *Document, seen map[int]Ref, o Object, depth int) Object {
	if depth > maxCopyDepth {
		w.note(fmt.Errorf("reader: an object graph nested deeper than %d was truncated", maxCopyDepth))
		return Null{}
	}
	switch v := o.(type) {
	case Ref:
		if to, ok := seen[v.Num]; ok {
			return to
		}
		// The number is reserved before the object is read, so a reference
		// back to an ancestor finds it rather than recursing.
		to := w.Reserve()
		seen[v.Num] = to
		src, err := d.Get(v)
		if err != nil {
			w.note(err)
			src = Null{}
		}
		w.Put(to, w.copyObject(d, seen, src, depth+1))
		return to
	case Array:
		out := make(Array, len(v))
		for i, e := range v {
			out[i] = w.copyObject(d, seen, e, depth+1)
		}
		return out
	case Dict:
		out := Dict{}
		for k, e := range v {
			out[k] = w.copyObject(d, seen, e, depth+1)
		}
		return out
	case *Stream:
		out := &Stream{Dict: Dict{}, Raw: v.Raw}
		for k, e := range v.Dict {
			out.Dict[k] = w.copyObject(d, seen, e, depth+1)
		}
		return out
	}
	return o
}

// maxCopyDepth bounds a copy, since a direct object may nest arbitrarily even
// when no reference repeats.
const maxCopyDepth = 256

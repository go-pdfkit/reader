package reader

import (
	"bytes"
	"compress/zlib"
	"fmt"
)

// objectsPerStream bounds how many objects go into one object stream. A
// stream has to be inflated whole to read any object in it, so packing
// everything into one would make opening a file read all of it.
const objectsPerStream = 200

// packedAt says which object stream an object was packed into, and where.
type packedAt struct {
	stream int
	index  int
}

// pendingObject is an object waiting to be packed.
type pendingObject struct {
	ref Ref
	obj Object
}

// packObjects writes every waiting object into object streams. It is called
// once, from Finish.
func (w *Writer) packObjects() {
	for start := 0; start < len(w.pending); start += objectsPerStream {
		end := start + objectsPerStream
		if end > len(w.pending) {
			end = len(w.pending)
		}
		w.packGroup(w.pending[start:end])
	}
	w.pending = nil
}

// packGroup writes one object stream holding the given objects.
func (w *Writer) packGroup(group []pendingObject) {
	var head, payload bytes.Buffer
	for _, p := range group {
		fmt.Fprintf(&head, "%d %d ", p.ref.Num, payload.Len())
		payload.Write(AppendObject(nil, p.obj))
		payload.WriteByte('\n')
	}
	body := append(head.Bytes(), payload.Bytes()...)
	stream := w.Reserve()
	for i, p := range group {
		w.packed[p.ref.Num] = packedAt{stream: stream.Num, index: i}
	}
	w.writeInline(stream, &Stream{
		Dict: Dict{
			"Type":  Name("ObjStm"),
			"N":     Integer(len(group)),
			"First": Integer(head.Len()),
		},
		Raw: body,
	})
}

// flateCompress compresses a stream's data, which is what makes packing worth doing.
func flateCompress(data []byte) []byte {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	// A bytes.Buffer never refuses bytes, and Close only flushes.
	zw.Write(data)
	zw.Close()
	return buf.Bytes()
}

// finishWithXrefStream writes the cross-reference information as a stream
// rather than a table, which is the only form that can name an object inside
// an object stream.
func (w *Writer) finishWithXrefStream(trailer Dict) ([]byte, error) {
	w.packObjects()
	xref := w.Reserve()
	// The cross-reference stream is reserved after everything else — the
	// object streams included — so it always carries the highest number.
	high := xref.Num
	start := w.buf.Len()
	w.offsets[xref.Num] = start

	rows := make([]byte, 0, (high+1)*7)
	for num := 0; num <= high; num++ {
		switch {
		case num == 0:
			rows = append(rows, 0, 0, 0, 0, 0, 0xFF, 0xFF)
		case w.offsets[num] != 0 || num == xref.Num:
			rows = append(rows, xrefRow(1, int64(w.offsets[num]), 0)...)
		default:
			if at, ok := w.packed[num]; ok {
				rows = append(rows, xrefRow(2, int64(at.stream), int64(at.index))...)
				continue
			}
			rows = append(rows, 0, 0, 0, 0, 0, 0xFF, 0xFF)
		}
	}

	dict := Dict{
		"Type": Name("XRef"),
		"Size": Integer(high + 1),
		"W":    Array{Integer(1), Integer(4), Integer(2)},
	}
	for k, v := range trailer {
		dict[k] = v
	}
	w.writeInline(xref, &Stream{Dict: dict, Raw: rows})
	fmt.Fprintf(&w.buf, "startxref\n%d\n%%%%EOF\n", start)
	if w.err != nil {
		return nil, w.err
	}
	return w.buf.Bytes(), nil
}

// xrefRow renders one three-field row of a cross-reference stream.
func xrefRow(kind byte, f2, f3 int64) []byte {
	return []byte{
		kind,
		byte(f2 >> 24), byte(f2 >> 16), byte(f2 >> 8), byte(f2),
		byte(f3 >> 8), byte(f3),
	}
}

package reader

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
)

// The helpers below assemble small but genuine PDF files, so the structural
// tests exercise the same code path a real file does rather than a hand-built
// Document value.

// builder lays objects out in a buffer and remembers where each one starts.
type builder struct {
	buf     bytes.Buffer
	offsets map[int]int
	order   []int
}

func newBuilder() *builder {
	b := &builder{offsets: map[int]int{}}
	b.buf.WriteString("%PDF-1.7\n")
	return b
}

// obj writes an indirect object whose body is the given text.
func (b *builder) obj(num int, body string) {
	b.offsets[num] = b.buf.Len()
	b.order = append(b.order, num)
	fmt.Fprintf(&b.buf, "%d 0 obj\n%s\nendobj\n", num, body)
}

// streamObj writes an indirect stream object with the given dictionary entries.
func (b *builder) streamObj(num int, dict string, data []byte) {
	b.offsets[num] = b.buf.Len()
	b.order = append(b.order, num)
	fmt.Fprintf(&b.buf, "%d 0 obj\n<< %s /Length %d >>\nstream\n", num, dict, len(data))
	b.buf.Write(data)
	b.buf.WriteString("\nendstream\nendobj\n")
}

// table finishes the file with a classic cross-reference table and trailer.
func (b *builder) table(trailer string) []byte {
	high := 0
	for n := range b.offsets {
		if n > high {
			high = n
		}
	}
	start := b.buf.Len()
	fmt.Fprintf(&b.buf, "xref\n0 %d\n", high+1)
	b.buf.WriteString("0000000000 65535 f \n")
	for n := 1; n <= high; n++ {
		if off, ok := b.offsets[n]; ok {
			fmt.Fprintf(&b.buf, "%010d 00000 n \n", off)
			continue
		}
		b.buf.WriteString("0000000000 65535 f \n")
	}
	fmt.Fprintf(&b.buf, "trailer\n<< /Size %d %s >>\nstartxref\n%d\n%%%%EOF\n", high+1, trailer, start)
	return b.buf.Bytes()
}

// bytesOf returns the buffer as it stands, for files finished by hand.
func (b *builder) bytesOf() []byte { return b.buf.Bytes() }

// onePage is the smallest complete document: a catalogue, a page tree and one
// page carrying a content stream.
func onePage() []byte {
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 612 792] /Resources << >> >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>")
	b.streamObj(4, "", []byte("BT ET"))
	return b.table("/Root 1 0 R")
}

// deflate compresses data the way a producer would.
func deflate(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}

// xrefStreamFile builds a file whose cross-reference information is itself a
// stream, with the page objects packed into an object stream.
func xrefStreamFile() []byte {
	var body bytes.Buffer
	body.WriteString("%PDF-1.5\n")

	// The object stream holds objects 1, 2 and 3.
	inner := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 200 100] >>",
		"<< /Type /Page /Parent 2 0 R >>",
	}
	var head, payload bytes.Buffer
	for i, s := range inner {
		fmt.Fprintf(&head, "%d %d ", i+1, payload.Len())
		payload.WriteString(s)
		payload.WriteString(" ")
	}
	first := head.Len()
	stmData := deflate(append(head.Bytes(), payload.Bytes()...))

	objStmOff := body.Len()
	fmt.Fprintf(&body, "4 0 obj\n<< /Type /ObjStm /N 3 /First %d /Filter /FlateDecode /Length %d >>\nstream\n",
		first, len(stmData))
	body.Write(stmData)
	body.WriteString("\nendstream\nendobj\n")

	// Now the cross-reference stream itself, object 5.
	xrefOff := body.Len()
	rows := []struct{ f1, f2, f3 int }{
		{0, 0, 65535},     // 0: free
		{2, 4, 0},         // 1: in object stream 4, index 0
		{2, 4, 1},         // 2
		{2, 4, 2},         // 3
		{1, objStmOff, 0}, // 4: the object stream itself
		{1, xrefOff, 0},   // 5: this stream
	}
	var w bytes.Buffer
	for _, r := range rows {
		w.WriteByte(byte(r.f1))
		w.WriteByte(byte(r.f2 >> 24))
		w.WriteByte(byte(r.f2 >> 16))
		w.WriteByte(byte(r.f2 >> 8))
		w.WriteByte(byte(r.f2))
		w.WriteByte(byte(r.f3 >> 8))
		w.WriteByte(byte(r.f3))
	}
	xd := deflate(w.Bytes())
	fmt.Fprintf(&body, "5 0 obj\n<< /Type /XRef /Size 6 /W [1 4 2] /Root 1 0 R /Filter /FlateDecode /Length %d >>\nstream\n",
		len(xd))
	body.Write(xd)
	body.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(&body, "startxref\n%d\n%%%%EOF\n", xrefOff)
	return body.Bytes()
}

// withoutStartxref removes the startxref line, leaving the objects intact.
func withoutStartxref(b []byte) []byte {
	i := bytes.LastIndex(b, []byte("startxref"))
	if i < 0 {
		return b
	}
	return b[:i]
}

// replaceAll is strings.ReplaceAll over bytes, for damaging a file on purpose.
func replaceAll(b []byte, old, new string) []byte {
	return []byte(strings.ReplaceAll(string(b), old, new))
}

// xrefStreamFileMissingEntries is xrefStreamFile with the rows for the objects
// held in the object stream left out, so the cross-reference stream parses but
// the catalogue it names cannot be found.
func xrefStreamFileMissingEntries() []byte {
	b := xrefStreamFile()
	// /Index [4 2] covers only the object stream and the cross-reference
	// stream, and the row count shrinks to match.
	i := bytes.Index(b, []byte("/Type /XRef /Size 6 /W [1 4 2]"))
	head := append([]byte{}, b[:i]...)
	tail := b[i:]
	tail = replaceAll(tail, "/Type /XRef /Size 6 /W [1 4 2]", "/Type /XRef /Size 6 /W [1 4 2] /Index [4 2]")
	return append(head, tail...)
}

package reader

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// rawFile wraps a body in a header and a startxref pointing at the given
// offset, for tables written by hand.
func rawFile(body string, startxref int) []byte {
	return []byte(fmt.Sprintf("%%PDF-1.4\n%s\nstartxref\n%d\n%%%%EOF\n", body, startxref))
}

func TestFindStartxrefFailures(t *testing.T) {
	cases := []string{
		"%PDF-1.4\nno keyword here\n",
		"%PDF-1.4\nstartxref\n(unterminated\n",
		"%PDF-1.4\nstartxref\n/Name\n",
		"%PDF-1.4\nstartxref\n-1\n",
		"%PDF-1.4\nstartxref\n999999\n",
	}
	for _, src := range cases {
		d := &Document{buf: []byte(src)}
		if _, err := d.findStartxref(); err == nil {
			t.Errorf("%q: want an error", src)
		}
	}
}

func TestFindStartxrefSearchWindow(t *testing.T) {
	// A startxref buried further back than the window is not found.
	body := "%PDF-1.4\nstartxref\n9\n" + strings.Repeat("padding!", startxrefWindow/8+16)
	d := &Document{buf: []byte(body)}
	if _, err := d.findStartxref(); err == nil {
		t.Error("want an error for a startxref outside the window")
	}
}

func TestReadXrefSectionFailures(t *testing.T) {
	d := &Document{buf: []byte("%PDF-1.4\n1 0 obj\n<< >>\nendobj\n"), xref: map[int]xrefEntry{}}
	if _, err := d.readXrefSection(-1); err == nil {
		t.Error("negative offset: want an error")
	}
	if _, err := d.readXrefSection(int64(len(d.buf))); err == nil {
		t.Error("offset past the end: want an error")
	}
	// An offset that holds an ordinary object, not a cross-reference stream.
	if _, err := d.readXrefSection(9); err == nil {
		t.Error("a plain object: want an error")
	}
	// An offset the lexer cannot read at all.
	d = &Document{buf: []byte("%PDF-1.4\n(unterminated"), xref: map[int]xrefEntry{}}
	if _, err := d.readXrefSection(9); err == nil {
		t.Error("a lexer error: want an error")
	}
	// An offset holding something that is not an indirect object.
	d = &Document{buf: []byte("%PDF-1.4\n]]]]"), xref: map[int]xrefEntry{}}
	if _, err := d.readXrefSection(9); err == nil {
		t.Error("junk: want an error")
	}
}

func TestReadXrefTableFailures(t *testing.T) {
	cases := []struct{ name, table string }{
		{"a subsection header that is not a number", "xref\n/Name\n"},
		{"a missing entry count", "xref\n0 /Name\n"},
		{"an entry offset that is not a number", "xref\n0 1\n/Name 0 n \n"},
		{"an entry generation that is not a number", "xref\n0 1\n0000000000 /Name n \n"},
		{"an entry that does not end in n or f", "xref\n0 1\n0000000000 00000 x \n"},
		{"an entry ending in a word", "xref\n0 1\n0000000000 00000 no \n"},
		{"a trailer that is not a dictionary", "xref\n0 1\n0000000000 65535 f \ntrailer\n42\n"},
		{"a trailer that does not parse", "xref\n0 1\n0000000000 65535 f \ntrailer\n]\n"},
		{"no trailer at all", "xref\n0 1\n0000000000 65535 f \n"},
		{"a lexer error in an entry", "xref\n0 1\n(unterminated"},
		{"a lexer error at the head", "xref\n(unterminated"},
		{"a lexer error in the count", "xref\n0 (unterminated"},
		{"a lexer error in the generation", "xref\n0 1\n0 (unterminated"},
		{"a lexer error in the keyword", "xref\n0 1\n0 0 (unterminated"},
	}
	for _, c := range cases {
		body := c.table
		b := rawFile(body, 9)
		d := &Document{buf: b, xref: map[int]xrefEntry{}}
		if _, err := d.readXrefSection(9); err == nil {
			t.Errorf("%s: want an error", c.name)
		}
	}
}

func TestReadXrefTableNegativeSubsectionIsIgnored(t *testing.T) {
	body := "xref\n-2 1\n0000000000 00000 n \ntrailer\n<< /Size 1 >>\n"
	d := &Document{buf: rawFile(body, 9), xref: map[int]xrefEntry{}}
	if _, err := d.readXrefSection(9); err != nil {
		t.Fatal(err)
	}
	if len(d.xref) != 0 {
		t.Errorf("a negative object number was recorded: %v", d.xref)
	}
}

func TestLoadXrefCycle(t *testing.T) {
	// /Prev points at the section itself.
	body := "xref\n0 1\n0000000000 65535 f \ntrailer\n<< /Size 1 /Prev 9 >>\n"
	d := &Document{buf: rawFile(body, 9), xref: map[int]xrefEntry{}}
	if err := d.loadXref(); err == nil {
		t.Error("want an error for a cycle")
	}
}

func TestLoadXrefEmptyTable(t *testing.T) {
	body := "xref\n0 0\ntrailer\n<< /Size 0 >>\n"
	d := &Document{buf: rawFile(body, 9), xref: map[int]xrefEntry{}}
	if err := d.loadXref(); err == nil {
		t.Error("want an error for an empty table")
	}
}

func TestLoadXrefPrevChain(t *testing.T) {
	// Two sections: the older defines object 1, the newer object 2 and a
	// different value for object 1, which must win.
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	old := buf.Len()
	buf.WriteString("xref\n1 1\n0000000111 00000 n \ntrailer\n<< /Size 2 >>\n")
	newer := buf.Len()
	fmt.Fprintf(&buf, "xref\n1 2\n0000000222 00000 n \n0000000333 00000 n \ntrailer\n<< /Size 3 /Root 1 0 R /Prev %d >>\n", old)
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", newer)

	d := &Document{buf: buf.Bytes(), xref: map[int]xrefEntry{}}
	if err := d.loadXref(); err != nil {
		t.Fatal(err)
	}
	if got := d.xref[1].offset; got != 222 {
		t.Errorf("object 1 came from the older section: offset %d", got)
	}
	if got := d.xref[2].offset; got != 333 {
		t.Errorf("object 2 offset = %d", got)
	}
	if _, ok := d.trailer["Root"]; !ok {
		t.Error("the trailer was not merged")
	}
}

func TestLoadXrefBrokenPrev(t *testing.T) {
	body := "xref\n1 1\n0000000111 00000 n \ntrailer\n<< /Size 2 /Prev 999999 >>\n"
	d := &Document{buf: rawFile(body, 9), xref: map[int]xrefEntry{}}
	if err := d.loadXref(); err == nil {
		t.Error("want an error for a /Prev outside the file")
	}
	// A /Prev of zero simply ends the chain.
	body = "xref\n1 1\n0000000111 00000 n \ntrailer\n<< /Size 2 /Prev 0 >>\n"
	d = &Document{buf: rawFile(body, 9), xref: map[int]xrefEntry{}}
	if err := d.loadXref(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadXrefHybrid(t *testing.T) {
	// A table that names an /XRefStm, the way a hybrid file does. The stream
	// here is unreadable, which must not stop the table being used.
	body := "xref\n1 1\n0000000111 00000 n \ntrailer\n<< /Size 2 /XRefStm 999999 >>\n"
	d := &Document{buf: rawFile(body, 9), xref: map[int]xrefEntry{}}
	if err := d.loadXref(); err != nil {
		t.Fatal(err)
	}
	if len(d.xref) != 1 {
		t.Errorf("xref = %v", d.xref)
	}
}

func TestLoadXrefHybridUsable(t *testing.T) {
	// A real hybrid: the table hides object 2, the cross-reference stream
	// defines it.
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	rows := []byte{1, 0, 0, 0, 200, 0, 0}
	stmOff := buf.Len()
	fmt.Fprintf(&buf, "9 0 obj\n<< /Type /XRef /Size 3 /W [1 4 2] /Index [2 1] /Length %d >>\nstream\n", len(rows))
	buf.Write(rows)
	buf.WriteString("\nendstream\nendobj\n")
	tblOff := buf.Len()
	fmt.Fprintf(&buf, "xref\n1 1\n0000000111 00000 n \ntrailer\n<< /Size 3 /XRefStm %d >>\n", stmOff)
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", tblOff)

	d := &Document{buf: buf.Bytes(), xref: map[int]xrefEntry{}}
	if err := d.loadXref(); err != nil {
		t.Fatal(err)
	}
	if got := d.xref[2].offset; got != 200 {
		t.Errorf("the hybrid stream was not read: %v", d.xref)
	}
}

// xrefStreamWith builds a file whose only section is a cross-reference stream
// carrying the given dictionary entries and rows.
func xrefStreamWith(dict string, rows []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.5\n")
	off := buf.Len()
	fmt.Fprintf(&buf, "1 0 obj\n<< /Type /XRef %s /Length %d >>\nstream\n", dict, len(rows))
	buf.Write(rows)
	buf.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", off)
	return buf.Bytes()
}

func TestReadXrefStreamFailures(t *testing.T) {
	cases := []struct {
		name string
		dict string
		rows []byte
	}{
		{"no /W", "/Size 2", []byte{0}},
		{"/W too short", "/Size 2 /W [1 4]", []byte{0}},
		{"/W holds a name", "/Size 2 /W [1 /x 2]", []byte{0}},
		{"/W holds a negative width", "/Size 2 /W [1 -4 2]", []byte{0}},
		{"/W holds an oversized width", "/Size 2 /W [1 40 2]", []byte{0}},
		{"all widths zero", "/Size 2 /W [0 0 0]", []byte{0}},
		{"neither /Index nor /Size", "/W [1 4 2]", []byte{0}},
		{"a negative /Size", "/Size -2 /W [1 4 2]", []byte{0}},
		{"/Index holds a name", "/Size 2 /W [1 4 2] /Index [/x 1]", []byte{0}},
		{"an image filter", "/Size 2 /W [1 4 2] /Filter /DCTDecode", []byte{0}},
		{"an unusable filter", "/Size 2 /W [1 4 2] /Filter /Nope", []byte{0}},
	}
	for _, c := range cases {
		d := &Document{buf: xrefStreamWith(c.dict, c.rows), xref: map[int]xrefEntry{}}
		if err := d.loadXref(); err == nil {
			t.Errorf("%s: want an error", c.name)
		}
	}
}

func TestReadXrefStreamShortSection(t *testing.T) {
	// /Index claims three rows, the stream carries one.
	d := &Document{
		buf:  xrefStreamWith("/Size 4 /W [1 4 2] /Index [1 3]", []byte{1, 0, 0, 0, 55, 0, 0}),
		xref: map[int]xrefEntry{},
	}
	if err := d.loadXref(); err != nil {
		t.Fatal(err)
	}
	if len(d.xref) != 1 || d.xref[1].offset != 55 {
		t.Errorf("xref = %v", d.xref)
	}
}

func TestReadXrefStreamEntryKinds(t *testing.T) {
	rows := []byte{
		0, 0, 0, 0, 0, 255, 255, // free
		1, 0, 0, 0, 70, 0, 0, // at an offset
		2, 0, 0, 0, 3, 0, 5, // in object stream 3, index 5
	}
	d := &Document{buf: xrefStreamWith("/Size 3 /W [1 4 2]", rows), xref: map[int]xrefEntry{}}
	if err := d.loadXref(); err != nil {
		t.Fatal(err)
	}
	if d.xref[0].kind != 'f' {
		t.Errorf("entry 0 = %+v", d.xref[0])
	}
	if e := d.xref[1]; e.kind != 'n' || e.offset != 70 {
		t.Errorf("entry 1 = %+v", e)
	}
	if e := d.xref[2]; e.kind != 'o' || e.strmNum != 3 || e.strmIdx != 5 {
		t.Errorf("entry 2 = %+v", e)
	}
}

func TestXrefFieldDefault(t *testing.T) {
	if got := xrefField(nil, 0, 7); got != 7 {
		t.Errorf("xrefField(width 0) = %d", got)
	}
	if got := xrefField([]byte{1, 2}, 2, 0); got != 0x0102 {
		t.Errorf("xrefField = %#x", got)
	}
}

func TestMaxXrefSections(t *testing.T) {
	// A long chain of sections, each pointing at the previous one, ends at the
	// bound rather than running away.
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offs := make([]int, 0, maxXrefSections+8)
	prev := -1
	for i := 0; i < maxXrefSections+4; i++ {
		off := buf.Len()
		offs = append(offs, off)
		buf.WriteString("xref\n1 1\n0000000111 00000 n \ntrailer\n<< /Size 2")
		if prev >= 0 {
			fmt.Fprintf(&buf, " /Prev %d", prev)
		}
		buf.WriteString(" >>\n")
		prev = off
	}
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", offs[len(offs)-1])
	d := &Document{buf: buf.Bytes(), xref: map[int]xrefEntry{}}
	if err := d.loadXref(); err != nil {
		t.Fatal(err)
	}
}

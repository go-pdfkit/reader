package reader

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

// unknownObject is an Object the writer has never heard of, for the branch
// that has to cope with one.
type unknownObject struct{}

func (unknownObject) Kind() Kind { return KindNull }

func TestAppendObject(t *testing.T) {
	cases := []struct {
		o    Object
		want string
	}{
		{nil, "null"},
		{Null{}, "null"},
		{Bool(true), "true"},
		{Bool(false), "false"},
		{Integer(-42), "-42"},
		{Real(1.5), "1.5"},
		{Real(0), "0"},
		{Real(math.Copysign(0, -1)), "0"},
		{Real(math.NaN()), "0"},
		{Real(math.Inf(1)), "0"},
		{Real(1e21), "1000000000000000000000"},
		{String("plain"), "(plain)"},
		{String("a(b)c\\"), `(a\(b\)c\\)`},
		{String("\n\r\t"), `(\n\r\t)`},
		{String{0x00, 0xFF}, "<00FF>"},
		// One awkward byte in a long readable string still comes out readable.
		{String("a readable line with one \x00 in it"), `(a readable line with one \000 in it)`},
		// Text in UTF-16 goes out as hex, which is half the size.
		{String{0xFE, 0xFF, 0x00, 0x41}, "<FEFF0041>"},
		{Name("Simple"), "/Simple"},
		{Name("With Space"), "/With#20Space"},
		{Name("h#sh"), "/h#23sh"},
		{Name(""), "/"},
		{Ref{12, 3}, "12 3 R"},
		{Array{}, "[]"},
		{Array{Integer(1), Name("N"), Array{Bool(true)}}, "[1 /N [true]]"},
		{Dict{}, "<<>>"},
		{Dict{"B": Integer(2), "A": Integer(1)}, "</A 1 /B 2>>"},
		{unknownObject{}, "null"},
	}
	for _, c := range cases {
		want := c.want
		if strings.HasPrefix(want, "</") {
			want = "<" + want
		}
		if got := string(FormatObject(c.o)); got != want {
			t.Errorf("%#v: got %s, want %s", c.o, got, want)
		}
	}
}

func TestAppendObjectDictionaryOrderIsStable(t *testing.T) {
	d := Dict{"Z": Integer(1), "A": Integer(2), "M": Integer(3)}
	first := string(FormatObject(d))
	for i := 0; i < 20; i++ {
		if got := string(FormatObject(d)); got != first {
			t.Fatalf("rendering %d differs: %s vs %s", i, got, first)
		}
	}
	if first != "<</A 2 /M 3 /Z 1>>" {
		t.Errorf("got %s", first)
	}
}

func TestAppendStreamRewritesLength(t *testing.T) {
	s := &Stream{Dict: Dict{"Length": Integer(999), "Type": Name("X")}, Raw: []byte("abcd")}
	got := string(FormatObject(s))
	want := "<</Length 4 /Type /X>>\nstream\nabcd\nendstream"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// The stream's own dictionary is not modified.
	if v, _ := ToInt(s.Dict.Get("Length")); v != 999 {
		t.Error("the source dictionary was changed")
	}
}

func TestWriterBuildsAReadableFile(t *testing.T) {
	w := NewWriter("")
	page := w.Reserve()
	contents := w.Add(&Stream{Dict: Dict{}, Raw: []byte("BT ET")})
	pages := w.Add(Dict{"Type": Name("Pages"), "Kids": Array{page}, "Count": Integer(1),
		"MediaBox": Array{Integer(0), Integer(0), Integer(300), Integer(400)}})
	w.Put(page, Dict{"Type": Name("Page"), "Parent": pages, "Contents": contents})
	root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pages})
	out, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-1.7\n")) {
		t.Errorf("header = %q", out[:12])
	}
	d, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if d.Repaired() {
		t.Error("the file it wrote had to be repaired")
	}
	if got := d.PageCount(); got != 1 {
		t.Fatalf("PageCount() = %d", got)
	}
	data, err := d.PageContent(1)
	if err != nil || string(data) != "BT ET" {
		t.Errorf("content = %q, %v", data, err)
	}
	if v, _ := ToInt(d.Trailer().Get("Size")); v != 5 {
		t.Errorf("/Size = %v", d.Trailer().Get("Size"))
	}
}

func TestWriterVersion(t *testing.T) {
	w := NewWriter("2.0")
	out, err := w.Finish(Dict{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-2.0")) {
		t.Errorf("header = %q", out[:9])
	}
}

func TestWriterRefusesToWriteAnObjectTwice(t *testing.T) {
	w := NewWriter("")
	ref := w.Add(Integer(1))
	w.Put(ref, Integer(2))
	if w.Err() == nil {
		t.Fatal("want an error")
	}
	if _, err := w.Finish(Dict{}); err == nil {
		t.Error("Finish should report it too")
	}
	// The first error is the one kept.
	first := w.Err()
	w.Put(ref, Integer(3))
	if w.Err() != first {
		t.Error("a later error replaced the first")
	}
}

func TestWriterAcceptsAnUnreservedNumber(t *testing.T) {
	w := NewWriter("")
	w.Put(Ref{Num: 10}, Integer(1))
	if got := w.Reserve(); got.Num != 11 {
		t.Errorf("Reserve() = %v, want 11", got)
	}
	out, err := w.Finish(Dict{})
	if err != nil {
		t.Fatal(err)
	}
	// The numbers in between are free entries, so the table still parses.
	d := &Document{buf: out, xref: map[int]xrefEntry{}}
	if err := d.loadXref(); err != nil {
		t.Fatal(err)
	}
	if e := d.xref[5]; e.kind != 'f' {
		t.Errorf("object 5 = %+v, want a free entry", e)
	}
}

func TestWriterCopy(t *testing.T) {
	src, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(src.Version())
	root := w.Copy(src, src.Trailer().Get("Root"))
	out, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	back, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.PageCount(); got != 1 {
		t.Fatalf("PageCount() = %d", got)
	}
	data, err := back.PageContent(1)
	if err != nil || string(data) != "BT ET" {
		t.Errorf("content = %q, %v", data, err)
	}
	page, _ := back.Page(1)
	if _, ok := ToArray(page.Get("MediaBox")); !ok {
		t.Error("the inherited MediaBox did not survive")
	}
}

func TestWriterCopyKeepsSharing(t *testing.T) {
	// Two pages pointing at one content stream must still point at one.
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 /MediaBox [0 0 9 9] >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R /Contents 5 0 R >>")
	b.obj(4, "<< /Type /Page /Parent 2 0 R /Contents 5 0 R >>")
	b.streamObj(5, "", []byte("shared"))
	src, err := Open(b.table("/Root 1 0 R"))
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter("")
	root := w.Copy(src, src.Trailer().Get("Root"))
	out, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	back, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := back.Page(1)
	two, _ := back.Page(2)
	if one.Get("Contents") != two.Get("Contents") {
		t.Errorf("the shared stream was duplicated: %v vs %v", one.Get("Contents"), two.Get("Contents"))
	}
}

func TestWriterCopyFromTwoDocuments(t *testing.T) {
	a, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter("")
	one := w.Copy(a, a.Trailer().Get("Root"))
	two := w.Copy(b, b.Trailer().Get("Root"))
	if one == two {
		t.Error("two documents were given the same object numbers")
	}
	if _, err := w.Finish(Dict{"Root": one}); err != nil {
		t.Fatal(err)
	}
}

func TestWriterCopyDirectObjects(t *testing.T) {
	d, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter("")
	in := Array{Integer(1), Dict{"S": &Stream{Dict: Dict{"A": Integer(2)}, Raw: []byte("x")}}, Name("N")}
	got := w.Copy(d, in)
	if s := string(FormatObject(got)); s != string(FormatObject(in)) {
		t.Errorf("a copy with no references changed: %s", s)
	}
}

func TestWriterCopyPropagatesALookupFailure(t *testing.T) {
	w := NewWriter("")
	if got := w.Copy(brokenDoc(), Ref{5, 0}); got.Kind() != KindRef {
		t.Errorf("got %v", got)
	}
	if w.Err() == nil {
		t.Error("the lookup failure was not reported")
	}
}

func TestWriterCopyStopsAtTheDepthLimit(t *testing.T) {
	d, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	deep := Object(Integer(1))
	for i := 0; i < maxCopyDepth+10; i++ {
		deep = Array{deep}
	}
	w := NewWriter("")
	w.Copy(d, deep)
	if w.Err() == nil {
		t.Error("the depth limit was not reported")
	}
}

func TestWriterCopyHandlesACycle(t *testing.T) {
	// A page whose /Parent points back at the tree node that holds it — every
	// real file has this, and a copy must not chase it forever.
	src, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter("")
	root := w.Copy(src, src.Trailer().Get("Root"))
	out, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	back, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	page, err := back.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	parent, ok := back.GetDict(page, "Parent")
	if !ok {
		t.Fatal("the page lost its parent")
	}
	kids, _ := ToArray(parent.Get("Kids"))
	if len(kids) != 1 {
		t.Errorf("the parent has %d kids", len(kids))
	}
}

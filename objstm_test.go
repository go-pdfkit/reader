package reader

import (
	"bytes"
	"testing"
)

func TestPackedWriterBuildsAReadableFile(t *testing.T) {
	w := NewPackedWriter("")
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
	if !bytes.HasPrefix(out, []byte("%PDF-1.7")) {
		t.Errorf("header = %q", out[:9])
	}
	// The pages are inside an object stream, and the file ends in a
	// cross-reference stream rather than a table.
	if !bytes.Contains(out, []byte("/ObjStm")) {
		t.Error("nothing was packed")
	}
	if !bytes.Contains(out, []byte("/XRef")) {
		t.Error("no cross-reference stream was written")
	}
	if bytes.Contains(out, []byte("\ntrailer\n")) {
		t.Error("a trailer was written as well")
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
}

func TestPackedWriterRaisesTheVersion(t *testing.T) {
	out, err := NewPackedWriter("1.3").Finish(Dict{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-1.5")) {
		t.Errorf("header = %q", out[:9])
	}
	// A version that already allows it is left alone.
	out, err = NewPackedWriter("2.0").Finish(Dict{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-2.0")) {
		t.Errorf("header = %q", out[:9])
	}
}

func TestPackedWriterCompressesStreamsItIsGiven(t *testing.T) {
	long := bytes.Repeat([]byte("compress me, please. "), 500)
	w := NewPackedWriter("")
	plain := w.Add(&Stream{Dict: Dict{}, Raw: long})
	// One that already says how it is encoded is left exactly as it is.
	already := w.Add(&Stream{Dict: Dict{"Filter": Name("ASCIIHexDecode")}, Raw: []byte("4142>")})
	out, err := w.Finish(Dict{"Root": plain})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > len(long)/2 {
		t.Errorf("the stream was not compressed: %d bytes for %d", len(out), len(long))
	}
	d := &Document{buf: out, xref: map[int]xrefEntry{}, cache: map[int]Object{},
		loading: map[int]bool{}, objStms: map[int]map[int]Object{}}
	if err := d.loadXref(); err != nil {
		t.Fatal(err)
	}
	o, err := d.Get(plain)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := ToStream(o)
	if !ok {
		t.Fatalf("the stream came back as a %s", o.Kind())
	}
	data, _, err := d.DecodeStream(s)
	if err != nil || !bytes.Equal(data, long) {
		t.Errorf("round trip: %d bytes, %v", len(data), err)
	}
	o, _ = d.Get(already)
	s, _ = ToStream(o)
	if f, _ := ToName(s.Dict.Get("Filter")); f != "ASCIIHexDecode" {
		t.Errorf("an encoded stream was re-encoded: %v", s.Dict)
	}
}

func TestPackedWriterSplitsIntoSeveralStreams(t *testing.T) {
	w := NewPackedWriter("")
	const n = objectsPerStream*2 + 5
	refs := make([]Ref, n)
	for i := range refs {
		refs[i] = w.Add(Dict{"Index": Integer(i)})
	}
	pagesRef := w.Reserve()
	page := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef,
		"MediaBox": Array{Integer(0), Integer(0), Integer(1), Integer(1)}})
	w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": Array{page}, "Count": Integer(1)})
	root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(out, []byte("/ObjStm")); got != 3 {
		t.Errorf("%d object streams, want three", got)
	}
	d, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	for i, ref := range refs {
		o, err := d.Get(ref)
		if err != nil {
			t.Fatalf("object %d: %v", i, err)
		}
		dict, ok := ToDict(o)
		if !ok {
			t.Fatalf("object %d came back as a %s", i, o.Kind())
		}
		if v, _ := ToInt(dict.Get("Index")); int(v) != i {
			t.Errorf("object %d holds %v", i, dict.Get("Index"))
		}
	}
}

func TestPackedWriterKeepsGapsFree(t *testing.T) {
	w := NewPackedWriter("")
	w.Put(Ref{Num: 10}, Integer(1))
	out, err := w.Finish(Dict{})
	if err != nil {
		t.Fatal(err)
	}
	d := &Document{buf: out, xref: map[int]xrefEntry{}, cache: map[int]Object{},
		loading: map[int]bool{}, objStms: map[int]map[int]Object{}}
	if err := d.loadXref(); err != nil {
		t.Fatal(err)
	}
	if e := d.xref[5]; e.kind != 'f' {
		t.Errorf("object 5 = %+v, want a free entry", e)
	}
	if e := d.xref[10]; e.kind != 'o' {
		t.Errorf("object 10 = %+v, want one held in a stream", e)
	}
}

func TestPackedWriterLeavesOtherGenerationsWhereTheyAre(t *testing.T) {
	// Only generation zero may be packed; anything else is written in place.
	w := NewPackedWriter("")
	w.Put(Ref{Num: 1, Gen: 3}, Integer(7))
	out, err := w.Finish(Dict{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("1 3 obj")) {
		t.Error("the object was packed although its generation is not zero")
	}
}

func TestPackedWriterRefusesToWriteAnObjectTwice(t *testing.T) {
	w := NewPackedWriter("")
	ref := w.Add(Integer(1))
	w.Put(ref, Integer(2))
	if w.Err() == nil {
		t.Fatal("want an error")
	}
	if _, err := w.Finish(Dict{}); err == nil {
		t.Error("Finish should report it too")
	}
}

func TestPackedAndPlainAgree(t *testing.T) {
	// The two forms must describe the same document.
	build := func(packed bool) []byte {
		var w *Writer
		if packed {
			w = NewPackedWriter("1.7")
		} else {
			w = NewWriter("1.7")
		}
		src, err := Open(onePage())
		if err != nil {
			t.Fatal(err)
		}
		root := w.Copy(src, src.Trailer().Get("Root"))
		out, err := w.Finish(Dict{"Root": root})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, packed := range []bool{false, true} {
		d, err := Open(build(packed))
		if err != nil {
			t.Fatalf("packed=%v: %v", packed, err)
		}
		if got := d.PageCount(); got != 1 {
			t.Errorf("packed=%v: PageCount() = %d", packed, got)
		}
		data, err := d.PageContent(1)
		if err != nil || string(data) != "BT ET" {
			t.Errorf("packed=%v: content = %q, %v", packed, data, err)
		}
	}
}

func TestXrefRow(t *testing.T) {
	got := xrefRow(1, 0x01020304, 0x0506)
	want := []byte{1, 1, 2, 3, 4, 5, 6}
	if !bytes.Equal(got, want) {
		t.Errorf("xrefRow = % x, want % x", got, want)
	}
}

func TestCloneDict(t *testing.T) {
	in := Dict{"A": Integer(1)}
	out := cloneDict(in)
	out["B"] = Integer(2)
	if _, ok := in["B"]; ok {
		t.Error("the original was changed")
	}
	if v, _ := ToInt(out.Get("A")); v != 1 {
		t.Errorf("the copy lost an entry: %v", out)
	}
}

func TestFlateCompress(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 1000)
	got, err := flateDecode(flateCompress(data))
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("round trip: %d bytes, %v", len(got), err)
	}
	if got := flateCompress(nil); len(got) == 0 {
		t.Error("compressing nothing produced nothing at all")
	}
}

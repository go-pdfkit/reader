package reader

import (
	"bytes"
	"testing"
)

func TestOpenOnePage(t *testing.T) {
	d, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	if d.Repaired() {
		t.Error("a well-formed file should not need repairing")
	}
	if got := d.Version(); got != "1.7" {
		t.Errorf("Version() = %q", got)
	}
	if got := d.PageCount(); got != 1 {
		t.Fatalf("PageCount() = %d", got)
	}
	ref, ok := d.PageRef(1)
	if !ok || ref != (Ref{3, 0}) {
		t.Errorf("PageRef(1) = %v, %v", ref, ok)
	}
	if _, ok := d.PageRef(0); ok {
		t.Error("PageRef(0) should not exist")
	}
	if _, ok := d.PageRef(2); ok {
		t.Error("PageRef(2) should not exist")
	}
	page, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	// MediaBox and Resources are inherited from the page tree node.
	if _, ok := ToArray(page.Get("MediaBox")); !ok {
		t.Error("MediaBox was not inherited")
	}
	if _, ok := ToDict(page.Get("Resources")); !ok {
		t.Error("Resources were not inherited")
	}
	if _, err := d.Page(2); err == nil {
		t.Error("Page(2) should fail")
	}
	if tr := d.Trailer(); tr.Get("Root").Kind() != KindRef {
		t.Errorf("trailer = %v", tr)
	}
}

func TestOpenContentStream(t *testing.T) {
	d, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	page, _ := d.Page(1)
	o, err := d.Resolve(page.Get("Contents"))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := ToStream(o)
	if !ok {
		t.Fatalf("Contents is a %s", o.Kind())
	}
	data, img, err := d.DecodeStream(s)
	if err != nil || img != "" || string(data) != "BT ET" {
		t.Errorf("content = %q, %q, %v", data, img, err)
	}
}

func TestOpenXrefStreamAndObjectStream(t *testing.T) {
	d, err := Open(xrefStreamFile())
	if err != nil {
		t.Fatal(err)
	}
	if d.Repaired() {
		t.Error("the cross-reference stream should have been usable")
	}
	if got := d.PageCount(); got != 1 {
		t.Fatalf("PageCount() = %d", got)
	}
	page, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	mb, ok := ToArray(page.Get("MediaBox"))
	if !ok || len(mb) != 4 {
		t.Fatalf("MediaBox = %v", page.Get("MediaBox"))
	}
	if v, _ := ToInt(mb[2]); v != 200 {
		t.Errorf("MediaBox width = %v", mb[2])
	}
}

func TestGetUndefinedObject(t *testing.T) {
	d, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	o, err := d.Get(Ref{Num: 999})
	if err != nil || o.Kind() != KindNull {
		t.Errorf("Get(999) = %v, %v", o, err)
	}
	// Object 0 is the head of the free list.
	if o, err := d.Get(Ref{Num: 0}); err != nil || o.Kind() != KindNull {
		t.Errorf("Get(0) = %v, %v", o, err)
	}
}

func TestGetCaches(t *testing.T) {
	d, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := d.Get(Ref{Num: 1})
	b, _ := d.Get(Ref{Num: 1})
	da, _ := ToDict(a)
	db, _ := ToDict(b)
	da["Marker"] = Integer(1)
	if _, ok := db["Marker"]; !ok {
		t.Error("the second Get did not come from the cache")
	}
}

func TestResolverAndGetDict(t *testing.T) {
	d, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	o, err := Resolve(Ref{1, 0}, d.Resolver())
	if err != nil {
		t.Fatal(err)
	}
	cat, ok := ToDict(o)
	if !ok {
		t.Fatalf("catalogue is a %s", o.Kind())
	}
	if _, ok := d.GetDict(cat, "Pages"); !ok {
		t.Error("GetDict(Pages) failed")
	}
	if _, ok := d.GetDict(cat, "Missing"); ok {
		t.Error("GetDict(Missing) should fail")
	}
}

func TestOpenRejectsNonPDF(t *testing.T) {
	for _, b := range [][]byte{
		nil,
		[]byte("not a pdf at all"),
		[]byte("%PDF-1.7\nstartxref\n0\n%%EOF\n"),
	} {
		if _, err := Open(b); err == nil {
			t.Errorf("%q: want an error", b)
		}
	}
}

func TestVersionMissing(t *testing.T) {
	d := &Document{buf: []byte("no header here")}
	if got := d.Version(); got != "" {
		t.Errorf("Version() = %q", got)
	}
	d = &Document{buf: []byte("%PDF-1")}
	if got := d.Version(); got != "" {
		t.Errorf("Version() on a truncated header = %q", got)
	}
}

func TestCatalogFailures(t *testing.T) {
	d := &Document{}
	if _, err := d.Catalog(); err == nil {
		t.Error("no trailer: want an error")
	}
	d = &Document{trailer: Dict{"Root": Integer(1)}, xref: map[int]xrefEntry{}, cache: map[int]Object{}}
	if _, err := d.Catalog(); err == nil {
		t.Error("/Root is not a dictionary: want an error")
	}
	d = &Document{trailer: Dict{"Root": Dict{}}, xref: map[int]xrefEntry{}, cache: map[int]Object{}}
	if _, err := d.Catalog(); err == nil {
		t.Error("a catalogue with no page tree: want an error")
	}
}

func TestPageTreeCycle(t *testing.T) {
	// /Kids pointing back at an ancestor must end the walk, not the process.
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [2 0 R 3 0 R] /Count 2 >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] >>")
	d, err := Open(b.table("/Root 1 0 R"))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 1 {
		t.Errorf("PageCount() = %d, want 1", got)
	}
}

func TestPageTreeWithKidsThatAreNotDictionaries(t *testing.T) {
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [42 (junk) 3 0 R] /Count 1 >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] >>")
	d, err := Open(b.table("/Root 1 0 R"))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 1 {
		t.Errorf("PageCount() = %d, want 1", got)
	}
}

func TestPageTreeEmptyFallsBackToScanning(t *testing.T) {
	// The tree resolves to nothing, but the file does hold a page object.
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.obj(3, "<< /Type /Page /MediaBox [0 0 10 10] >>")
	d, err := Open(b.table("/Root 1 0 R"))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 1 {
		t.Errorf("PageCount() = %d, want 1", got)
	}
}

func TestInheritanceStopsAtTheTop(t *testing.T) {
	// No ancestor supplies MediaBox; the walk must end rather than loop.
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R >>")
	d, err := Open(b.table("/Root 1 0 R"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Get("MediaBox").Kind() != KindNull {
		t.Errorf("MediaBox = %v, want none", page.Get("MediaBox"))
	}
}

func TestInheritanceThroughParentChain(t *testing.T) {
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 99 99] >>")
	b.obj(3, "<< /Type /Pages /Kids [4 0 R] /Count 1 /Parent 2 0 R /Rotate 90 >>")
	b.obj(4, "<< /Type /Page /Parent 3 0 R >>")
	d, err := Open(b.table("/Root 1 0 R"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := ToInt(page.Get("Rotate")); r != 90 {
		t.Errorf("Rotate = %v", page.Get("Rotate"))
	}
	mb, ok := ToArray(page.Get("MediaBox"))
	if !ok || len(mb) != 4 {
		t.Fatalf("MediaBox = %v", page.Get("MediaBox"))
	}
	if v, _ := ToInt(mb[2]); v != 99 {
		t.Errorf("MediaBox from the grandparent = %v", mb[2])
	}
}

func TestPageIsNotADictionary(t *testing.T) {
	d := &Document{
		buf:     []byte("%PDF-1.7\n"),
		xref:    map[int]xrefEntry{},
		cache:   map[int]Object{7: Integer(1)},
		loading: map[int]bool{},
		objStms: map[int]map[int]Object{},
		pages:   []Ref{{7, 0}},
	}
	if _, err := d.Page(1); err == nil {
		t.Error("want an error when a page is not a dictionary")
	}
}

func TestObjectStreamOddities(t *testing.T) {
	// An object stream whose header is unusable yields no objects, and the
	// document simply does not define them.
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.obj(3, "<< /Type /Page /MediaBox [0 0 10 10] >>")
	b.streamObj(4, "/Type /ObjStm /N 2 /First 4", []byte("xx  << >>"))
	b.streamObj(5, "/Type /ObjStm /N 0 /First 0", []byte(""))
	b.streamObj(6, "/Type /ObjStm /N 2 /First 900", []byte("1 0"))
	d, err := Open(b.table("/Root 1 0 R"))
	if err != nil {
		t.Fatal(err)
	}
	for _, num := range []int{4, 5, 6} {
		objs, err := d.objectStream(num)
		if err != nil {
			t.Errorf("objectStream(%d): %v", num, err)
		}
		if len(objs) != 0 {
			t.Errorf("objectStream(%d) yielded %d objects", num, len(objs))
		}
	}
	// A number that is not a stream at all.
	if objs, err := d.objectStream(1); err != nil || len(objs) != 0 {
		t.Errorf("objectStream(1) = %v, %v", objs, err)
	}
}

// ⛔⛔ THE BOUND ON /N ROUNDS UP, AND THIS IS WHY. #73's ceiling is "a pair is
// four bytes at least, so at most that many pairs fit in the index" — but the
// LAST pair needs no trailing separator: `0 0` is THREE bytes and lexes as two
// integers, end-of-input terminating the second. Floored, first/4 is ZERO
// there, and a stream holding exactly one object yielded NOTHING.
//
// ⭐ A bound that quietly eats a document is worse than the bomb it was added
// for, because the bomb is loud. Caught in review before it shipped; the
// refusal it belongs to is still measured by the fuzz seed
// testdata/fuzz/FuzzLinearize/86bc84b0ce34ab5c.
// ⛔ THE STRING AND THE NUMBER LIVE TOGETHER, because they drifted apart. The
// comment in document.go and the README both quote this object and state its
// length; both said "fifty" while it is fifty-one. The measurement had been
// taken correctly (`wc -c` said 51) and then overruled by an unchecked
// parenthetical about a trailing newline that was not there.
//
// ⭐ A prose figure nothing asserts is a claim, and this is the cheapest
// possible assertion: the literal the documentation quotes, measured.
func TestTheDocumentedReproducerIsTheLengthTheCommentClaims(t *testing.T) {
	const documented = `2 0 obj<</Type/ObjStm///N 10000000000000/First 10>>`
	if len(documented) != 51 {
		t.Errorf("the object document.go and the README quote is %d bytes, and both "+
			"say fifty-one", len(documented))
	}
}

func TestAnIndexTooShortToRoundUpStillYieldsItsObject(t *testing.T) {
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.obj(3, "<< /Type /Page /MediaBox [0 0 10 10] >>")
	// "0 0" is the whole index: three bytes, one pair, no trailing space.
	b.streamObj(4, "/Type /ObjStm /N 1 /First 3", []byte("0 0<< /Say 42 >>"))
	d, err := Open(b.table("/Root 1 0 R"))
	if err != nil {
		t.Fatal(err)
	}
	objs, err := d.objectStream(4)
	if err != nil {
		t.Fatalf("objectStream(4): %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("a three-byte index yielded %d object(s), want 1 — first/4 floored", len(objs))
	}
}

func TestIntOr(t *testing.T) {
	if got := intOr(Integer(4), 9); got != 4 {
		t.Errorf("intOr(4) = %d", got)
	}
	if got := intOr(Name("x"), 9); got != 9 {
		t.Errorf("intOr(name) = %d", got)
	}
}

func TestOpenTruncatedButRecoverable(t *testing.T) {
	// The cross-reference table is gone; the objects are not.
	d, err := Open(withoutStartxref(onePage()))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Repaired() {
		t.Error("the file should have been repaired")
	}
	if got := d.PageCount(); got != 1 {
		t.Errorf("PageCount() = %d", got)
	}
	if !bytes.Contains(d.buf, []byte("%PDF")) {
		t.Error("the buffer was lost")
	}
}

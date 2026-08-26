package reader

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// brokenDoc is a document whose only cross-reference entry points nowhere and
// whose file holds no objects at all, so every lookup fails outright. It is
// how the error paths of Get and its callers are reached.
func brokenDoc() *Document {
	return &Document{
		buf:     []byte("%PDF-1.7\nnothing to find here\n"),
		xref:    map[int]xrefEntry{5: {kind: 'n', offset: 999999}},
		cache:   map[int]Object{},
		loading: map[int]bool{},
		objStms: map[int]map[int]Object{},
	}
}

func TestGetFailsWhenRepairCannot(t *testing.T) {
	d := brokenDoc()
	if _, err := d.Get(Ref{5, 0}); err == nil {
		t.Fatal("want an error")
	}
	// Once the file has been repaired, a missing object is simply null.
	if o, err := d.Get(Ref{5, 0}); err != nil || o.Kind() != KindNull {
		t.Errorf("after repair: %v, %v", o, err)
	}
}

func TestBrokenDocumentPropagatesErrors(t *testing.T) {
	d := brokenDoc()
	if _, ok := d.GetDict(Dict{"K": Ref{5, 0}}, "K"); ok {
		t.Error("GetDict should fail")
	}

	d = brokenDoc()
	d.trailer = Dict{"Root": Ref{5, 0}}
	if _, err := d.Catalog(); err == nil {
		t.Error("Catalog should fail")
	}

	d = brokenDoc()
	d.pages = []Ref{{5, 0}}
	if _, err := d.Page(1); err == nil {
		t.Error("Page should fail")
	}

	d = brokenDoc()
	if kids, ok := d.kidsOf(Dict{"Kids": Ref{5, 0}}); ok {
		t.Errorf("kidsOf should fail: %v", kids)
	}

	d = brokenDoc()
	if got := d.objectsOfType("Page"); len(got) != 0 {
		t.Errorf("objectsOfType = %v", got)
	}

	d = brokenDoc()
	d.pages = nil
	d.walkPages(Ref{5, 0}, map[Ref]bool{}, 0)
	if len(d.pages) != 0 {
		t.Errorf("walkPages recorded %v", d.pages)
	}

	d = brokenDoc()
	if objs, err := d.objectStream(5); err == nil || len(objs) != 0 {
		t.Errorf("objectStream = %v, %v", objs, err)
	}

	d = brokenDoc()
	if o, err := d.getFromObjectStream(5, xrefEntry{kind: 'o', strmNum: 5}); err == nil || o != nil {
		t.Errorf("getFromObjectStream = %v, %v", o, err)
	}

	d = brokenDoc()
	d.indexObjectStreams()
}

func TestGetAtOffsetRecovers(t *testing.T) {
	// The table points object 3 at the wrong place; the rebuild finds it.
	b := onePage()
	b = replaceAll(b, "0000000009", "0000000001")
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 1 {
		t.Errorf("PageCount() = %d", got)
	}
}

func TestGetAtOffsetWrongObjectNumber(t *testing.T) {
	d := &Document{
		buf:     onePage(),
		xref:    map[int]xrefEntry{},
		cache:   map[int]Object{},
		loading: map[int]bool{},
		objStms: map[int]map[int]Object{},
	}
	// Point object 1 at the place object 3 lives.
	at := bytes.Index(d.buf, []byte("3 0 obj"))
	d.xref[1] = xrefEntry{kind: 'n', offset: int64(at)}
	o, err := d.Get(Ref{1, 0})
	if err != nil {
		t.Fatal(err)
	}
	// The rebuild puts object 1 back where it belongs.
	dict, ok := ToDict(o)
	if !ok {
		t.Fatalf("object 1 is a %s", o.Kind())
	}
	if ty, _ := ToName(dict.Get("Type")); ty != "Catalog" {
		t.Errorf("object 1 is %v", dict)
	}
}

func TestGetAtOffsetUnparsable(t *testing.T) {
	d := &Document{
		buf:     onePage(),
		xref:    map[int]xrefEntry{9: {kind: 'n', offset: 3}},
		cache:   map[int]Object{},
		loading: map[int]bool{},
		objStms: map[int]map[int]Object{},
	}
	// Offset 3 is inside the header, which does not parse as an object; the
	// rebuild then finds that object 9 does not exist.
	if o, err := d.Get(Ref{9, 0}); err != nil || o.Kind() != KindNull {
		t.Errorf("Get(9) = %v, %v", o, err)
	}
}

func TestRepairFromScratch(t *testing.T) {
	d, err := Open(withoutStartxref(onePage()))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Repaired() || d.PageCount() != 1 {
		t.Errorf("repaired %v, pages %d", d.Repaired(), d.PageCount())
	}
}

func TestRepairFindsCatalogWithoutTrailer(t *testing.T) {
	// No trailer at all: the catalogue has to be recognised by its /Type.
	b := withoutStartxref(onePage())
	b = b[:bytes.Index(b, []byte("xref"))]
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if d.PageCount() != 1 {
		t.Errorf("PageCount() = %d", d.PageCount())
	}
}

func TestRepairSynthesisesACatalogue(t *testing.T) {
	// Neither a trailer nor a catalogue survives, but a page does.
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	buf.WriteString("7 0 obj\n<< /Type /Page /MediaBox [0 0 10 10] >>\nendobj\n")
	d, err := Open(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !d.Repaired() || d.PageCount() != 1 {
		t.Fatalf("repaired %v, pages %d", d.Repaired(), d.PageCount())
	}
	page, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ToArray(page.Get("MediaBox")); !ok {
		t.Error("the page lost its MediaBox")
	}
}

func TestRepairKeepsTheNewestDefinition(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	buf.WriteString("3 0 obj\n<< /Type /Page /MediaBox [0 0 1 1] >>\nendobj\n")
	buf.WriteString("3 0 obj\n<< /Type /Page /MediaBox [0 0 2 2] >>\nendobj\n")
	d, err := Open(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	page, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	mb, _ := ToArray(page.Get("MediaBox"))
	if v, _ := ToInt(mb[2]); v != 2 {
		t.Errorf("the older definition won: %v", mb)
	}
}

func TestRepairPrefersALaterTrailer(t *testing.T) {
	// The first trailer leads nowhere; the second is the real one. Trailers
	// are read newest first, so the good one is found immediately.
	var buf bytes.Buffer
	buf.Write(withoutStartxref(onePage()))
	buf.WriteString("\ntrailer\n<< /Root 99 0 R >>\n")
	buf.WriteString("\ntrailer\n<< /Root 1 0 R >>\n")
	d, err := Open(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if d.PageCount() != 1 {
		t.Errorf("PageCount() = %d", d.PageCount())
	}
}

func TestRepairKeepsAWorkingTrailerFromTheTables(t *testing.T) {
	// The tables are unusable but their trailer is fine, and the file holds no
	// trailer keyword the scan could find twice.
	b := onePage()
	b = replaceAll(b, "xref\n0 5", "xref\n0 x")
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Repaired() || d.PageCount() != 1 {
		t.Errorf("repaired %v, pages %d", d.Repaired(), d.PageCount())
	}
}

func TestRepairWithObjectStreams(t *testing.T) {
	// The cross-reference stream is destroyed; the object stream beside it
	// still holds the catalogue and the page.
	b := xrefStreamFile()
	b = withoutStartxref(b)
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Repaired() {
		t.Error("the file should have been repaired")
	}
	if got := d.PageCount(); got != 1 {
		t.Errorf("PageCount() = %d", got)
	}
}

func TestRepairRejectsAFileWithNoObjects(t *testing.T) {
	if _, err := Open([]byte("%PDF-1.4\njust text\n")); err == nil {
		t.Error("want an error")
	}
}

func TestScanObjectHeaders(t *testing.T) {
	b := []byte("1 0 obj\nx\nobjection\n 2 0 obj\nnot a header: obj\nobj\n99999999999 0 obj\n0 obj\n")
	got := scanObjectHeaders(b)
	if len(got) != 2 {
		t.Fatalf("got %d headers: %+v", len(got), got)
	}
	if got[0].num != 1 || got[1].num != 2 {
		t.Errorf("headers = %+v", got)
	}
}

func TestScanObjectHeadersAtTheVeryEnd(t *testing.T) {
	if got := scanObjectHeaders([]byte("4 0 obj")); len(got) != 1 || got[0].num != 4 {
		t.Errorf("got %+v", got)
	}
	if got := scanObjectHeaders([]byte("obj")); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
	if got := scanObjectHeaders(nil); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestDigitsBack(t *testing.T) {
	b := []byte("12345")
	if v, p, ok := digitsBack(b, 5); !ok || v != 12345 || p != 0 {
		t.Errorf("digitsBack = %d, %d, %v", v, p, ok)
	}
	if _, _, ok := digitsBack([]byte("abc"), 3); ok {
		t.Error("no digits: want false")
	}
	long := []byte(strings.Repeat("9", 12))
	if _, _, ok := digitsBack(long, len(long)); ok {
		t.Error("too many digits: want false")
	}
}

func TestScanTrailers(t *testing.T) {
	b := []byte("trailer\n<< /A 1 >>\ntrailer\n]\ntrailer\n42\n")
	got := scanTrailers(b)
	if len(got) != 1 {
		t.Fatalf("got %d trailers: %v", len(got), got)
	}
	if _, ok := got[0]["A"]; !ok {
		t.Errorf("trailer = %v", got[0])
	}
}

func TestIndexObjectStreamsSkipsWhatIsAlreadyThere(t *testing.T) {
	// The file defines object 1 directly and again inside an object stream;
	// the direct definition must win.
	b := xrefStreamFile()
	var buf bytes.Buffer
	buf.Write(withoutStartxref(b))
	buf.WriteString("\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R /Marker 1 >>\nendobj\n")
	d, err := Open(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	cat, err := d.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cat["Marker"]; !ok {
		t.Errorf("the object stream overrode the direct definition: %v", cat)
	}
}

func TestWalkPagesDepthLimit(t *testing.T) {
	// A page tree nested deeper than the walk allows stops rather than
	// recursing without end.
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	const depth = 80
	for i := 2; i < 2+depth; i++ {
		fmt.Fprintf(&buf, "%d 0 obj\n<< /Type /Pages /Kids [%d 0 R] /Count 1 >>\nendobj\n", i, i+1)
	}
	fmt.Fprintf(&buf, "%d 0 obj\n<< /Type /Page /MediaBox [0 0 1 1] >>\nendobj\n", 2+depth)
	d, err := Open(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	// The deep leaf is out of reach through the tree, but the fallback scan
	// still finds it.
	if got := d.PageCount(); got != 1 {
		t.Errorf("PageCount() = %d", got)
	}
}

func TestGetGuardsAgainstRecursion(t *testing.T) {
	// An object stream that claims to hold itself.
	d := &Document{
		buf:     []byte("%PDF-1.4\n"),
		xref:    map[int]xrefEntry{4: {kind: 'o', strmNum: 4}},
		cache:   map[int]Object{},
		loading: map[int]bool{},
		objStms: map[int]map[int]Object{},
	}
	if o, err := d.Get(Ref{4, 0}); err != nil || o.Kind() != KindNull {
		t.Errorf("Get = %v, %v", o, err)
	}
}

func TestOpenRepairsWhenTheTrailerLeadsNowhere(t *testing.T) {
	// The tables are perfectly good; /Root just names an object that is not
	// there. Rebuilding finds the catalogue the file does hold.
	b := replaceAll(onePage(), "/Root 1 0 R", "/Root 9 0 R")
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Repaired() || d.PageCount() != 1 {
		t.Errorf("repaired %v, pages %d", d.Repaired(), d.PageCount())
	}
}

func TestRetryAfterRepairOnAnAlreadyRepairedDocument(t *testing.T) {
	d := brokenDoc()
	d.repaired = true
	if o, err := d.Get(Ref{5, 0}); err != nil || o.Kind() != KindNull {
		t.Errorf("Get = %v, %v", o, err)
	}
}

func TestRetryAfterRepairFindsAnUnparsableObject(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	buf.WriteString("3 0 obj\n<< /Type /Page /MediaBox [0 0 1 1] >>\nendobj\n")
	buf.WriteString("7 0 obj\n]\nendobj\n")
	d := &Document{
		buf:     buf.Bytes(),
		xref:    map[int]xrefEntry{7: {kind: 'n', offset: 999999}},
		cache:   map[int]Object{},
		loading: map[int]bool{},
		objStms: map[int]map[int]Object{},
	}
	// The rebuild succeeds, finds object 7's header, and still cannot read it.
	if o, err := d.Get(Ref{7, 0}); err != nil || o.Kind() != KindNull {
		t.Errorf("Get(7) = %v, %v", o, err)
	}
}

func TestRepairKeepsTheTrailerItAlreadyHad(t *testing.T) {
	// A cross-reference stream leaves no trailer keyword in the file, so a
	// rebuild has to fall back on the trailer the stream itself supplied.
	d, err := Open(xrefStreamFileMissingEntries())
	if err != nil {
		t.Fatal(err)
	}
	if !d.Repaired() {
		t.Error("the file should have been repaired")
	}
	if got := d.PageCount(); got != 1 {
		t.Errorf("PageCount() = %d", got)
	}
}

func TestRepairSkipsACatalogueThatLeadsNowhere(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 99 0 R >>\nendobj\n")
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	buf.WriteString("3 0 obj\n<< /Type /Page /MediaBox [0 0 1 1] >>\nendobj\n")
	buf.WriteString("5 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	d, err := Open(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 1 {
		t.Errorf("PageCount() = %d", got)
	}
}

func TestRepairRejectsAFileWithNoCatalogue(t *testing.T) {
	// Objects, but nothing that could be a document.
	if _, err := Open([]byte("%PDF-1.4\n1 0 obj\n<< /A 1 >>\nendobj\n")); err == nil {
		t.Error("want an error")
	}
}

func TestPageCountWithoutACatalogue(t *testing.T) {
	if got := brokenDoc().PageCount(); got != 0 {
		t.Errorf("PageCount() = %d", got)
	}
}

func TestHeaderBeforeAtTheStartOfTheBuffer(t *testing.T) {
	// "12 obj" has no room for an object number before the generation.
	if got := scanObjectHeaders([]byte("12 obj")); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestInheritanceStopsOnceEverythingIsPresent(t *testing.T) {
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 1 1] /CropBox [0 0 1 1] /Rotate 0 /Resources << >> >>")
	d, err := Open(b.table("/Root 1 0 R"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Get("Rotate").Kind() != KindInteger {
		t.Errorf("Rotate = %v", page.Get("Rotate"))
	}
}

func TestObjectStreamBadOffsetsAndBodies(t *testing.T) {
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.obj(3, "<< /Type /Page /MediaBox [0 0 1 1] >>")
	// A header pointing past the end of the payload.
	b.streamObj(4, "/Type /ObjStm /N 1 /First 6", []byte("9 9999<< >>"))
	// A header pointing at something that does not parse.
	b.streamObj(5, "/Type /ObjStm /N 1 /First 4", []byte("9 0 ]"))
	// An object stream filtered as an image.
	b.streamObj(6, "/Type /ObjStm /N 1 /First 4 /Filter /DCTDecode", []byte("9 0 1"))
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
			t.Errorf("objectStream(%d) yielded %v", num, objs)
		}
	}
}

func TestAFileThatNamesAVeryHighObjectNumber(t *testing.T) {
	// A file may name any object number it likes. This 219-byte one, from
	// the wild, declares object 2147483647 — and a reader that looks at every
	// number up to the largest one, to walk them in order, spends two
	// thousand million map lookups on three objects. That was twenty-five
	// seconds for a file that fits in a tweet, which is a denial of service
	// anybody could post.
	const bomb = "%PDF-1.7\n" +
		"1 0 obj <</Type /Catalog /Pages 2 0 R>>\nendobj\n" +
		"2 0 obj <</Type /Pages /Kids [3 0 R] /Count 1>>\nendobj\n" +
		"3 0 obj <</Type /Page /Parent 2 0 R /MediaBox [0 0 10 10]>>\nendobj\n\n" +
		"2147483647 0 obj <</Root 1 0 R>>\nendobj\n"

	done := make(chan *Document, 1)
	go func() {
		d, err := Open([]byte(bomb))
		if err != nil {
			t.Error(err)
		}
		done <- d
	}()
	select {
	case d := <-done:
		if d.PageCount() != 1 {
			t.Errorf("read %d pages, wanted one", d.PageCount())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a 219-byte file took more than five seconds")
	}
}

func TestObjectsAreListedInOrderHoweverSparseTheNumbersAre(t *testing.T) {
	// The order is what the walk is for, and it has to survive the numbers
	// being scattered rather than consecutive.
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	for _, num := range []int{900, 3, 40000, 17} {
		fmt.Fprintf(&b, "%d 0 obj <</Type /Page /MediaBox [0 0 10 10]>>\nendobj\n", num)
	}
	d, err := Open(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got := d.objectsOfType("Page")
	want := []int{3, 17, 900, 40000}
	if len(got) != len(want) {
		t.Fatalf("listed %v, wanted %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("listed %v, wanted %v", got, want)
		}
	}
}

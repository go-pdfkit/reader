package reader

import (
	"errors"
	"strings"
	"testing"
)

// A file whose catalogue lives in an encrypted object stream, and whose tables
// are gone: the rebuild has to know the key before it can read the stream, so
// establishing it has to come first. Otherwise the file is rebuilt from bytes
// nobody can read and refused for a reason that is not the reason.
func TestRepairOfAnEncryptedPackedFile(t *testing.T) {
	for _, pw := range []string{"", "hunter2"} {
		full := protectedFile(t, true, Encryption{UserPassword: pw})
		b := withoutStartxref(full)
		d, err := OpenWithPassword(b, pw)
		if err != nil {
			t.Fatalf("password %q: %v", pw, err)
		}
		if !d.Repaired() {
			t.Errorf("password %q: the file was not rebuilt", pw)
		}
		got, err := d.PageContent(1)
		if err != nil || string(got) != "BT (hello) Tj ET" {
			t.Errorf("password %q: content %q, %v", pw, got, err)
		}
	}
}

// The rebuild's own diagnosis is what comes back, because the rebuild has read
// every object header in the file where the tables only failed to be read. The
// tables' error is kept alongside it, since it says why there was a rebuild.
func TestOpenReportsWhatTheRebuildFound(t *testing.T) {
	cases := []struct {
		name, file, wantRebuild, wantTables string
	}{
		{
			// PostScript, or a PDF truncated before its body: no objects.
			name:        "no objects at all",
			file:        "%!PS-Adobe-2.0\nnothing a reader can use\n",
			wantRebuild: "the file holds no indirect objects",
			wantTables:  "no startxref",
		},
		{
			// Objects, but nothing that leads to a page.
			name:        "objects but no catalogue",
			file:        "%PDF-1.7\n1 0 obj\n<< /Type /Font >>\nendobj\n",
			wantRebuild: "no document catalogue found",
			wantTables:  "no startxref",
		},
	}
	for _, c := range cases {
		_, err := Open([]byte(c.file))
		if err == nil {
			t.Errorf("%s: want an error", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.wantRebuild) {
			t.Errorf("%s: %q does not say %q", c.name, err, c.wantRebuild)
		}
		if !strings.Contains(err.Error(), c.wantTables) {
			t.Errorf("%s: %q loses the tables' error %q", c.name, err, c.wantTables)
		}
	}
}

// Tables that read but lead nowhere keep what the catalogue lookup said, which
// is not the same thing twice: "/Root is a null" and "no page tree" send a
// reader to different places.
func TestOpenKeepsTheCatalogueError(t *testing.T) {
	// The catalogue has no page tree, and nothing else in the file calls
	// itself a page either, so the rebuild has nothing to fall back on.
	// The replacements keep the byte length, so the tables still read: it is
	// the catalogue lookup that has to be what fails.
	b := replaceAll(onePage(), "/Type /Catalog /Pages 2 0 R", "/Type /Catalog /Pagez 2 0 R")
	b = replaceAll(b, "/Type /Page /Parent", "/Type /Pige /Parent")
	_, err := Open(b)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "no page tree") {
		t.Errorf("%q does not say what the catalogue lookup found", err)
	}
}

// The bytes "/Encrypt" can appear in a file that is not encrypted at all — in
// a content stream, say — and the rebuild must not conclude anything from that.
func TestRepairIgnoresAStrayEncryptMention(t *testing.T) {
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 1 1] >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>")
	b.streamObj(4, "", []byte("BT (/Encrypt) Tj ET"))
	d, err := Open(withoutStartxref(b.table("/Root 1 0 R")))
	if err != nil {
		t.Fatal(err)
	}
	if d.Encrypted() {
		t.Error("the file reports itself encrypted")
	}
	if got, err := d.PageContent(1); err != nil || string(got) != "BT (/Encrypt) Tj ET" {
		t.Errorf("content %q, %v", got, err)
	}
}

// A rebuild that fails on the key says so, and stays matchable.
func TestRepairReportsAWrongPassword(t *testing.T) {
	b := withoutStartxref(protectedFile(t, true, Encryption{UserPassword: "right"}))
	_, err := OpenWithPassword(b, "wrong")
	if !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("got %v", err)
	}
}

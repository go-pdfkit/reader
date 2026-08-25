package reader

import (
	"bytes"
	"crypto/aes"
	"errors"
	"fmt"
	"testing"
)

// protectedFile builds a small document behind a password.
func protectedFile(t *testing.T, packed bool, e Encryption) []byte {
	t.Helper()
	w := NewWriter("1.7")
	if packed {
		w = NewPackedWriter("1.7")
	}
	w.Encrypt(e)
	pagesRef := w.Reserve()
	content := w.Add(&Stream{Dict: Dict{}, Raw: []byte("BT (hello) Tj ET")})
	page := w.Add(Dict{"Type": Name("Page"), "Parent": pagesRef, "Contents": content})
	w.Put(pagesRef, Dict{"Type": Name("Pages"), "Kids": Array{page}, "Count": Integer(1),
		"MediaBox": Array{Integer(0), Integer(0), Integer(100), Integer(100)}})
	root := w.Add(Dict{"Type": Name("Catalog"), "Pages": pagesRef})
	info := w.Add(Dict{"Title": String("a secret title")})
	out, err := w.Finish(Dict{"Root": root, "Info": info})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// readsBack asserts that a password opens the file and everything is intact.
func readsBack(t *testing.T, b []byte, password string) {
	t.Helper()
	d, err := OpenWithPassword(b, password)
	if err != nil {
		t.Fatalf("%q: %v", password, err)
	}
	if !d.Encrypted() {
		t.Errorf("%q: the file does not report itself encrypted", password)
	}
	data, err := d.PageContent(1)
	if err != nil || string(data) != "BT (hello) Tj ET" {
		t.Errorf("%q: content = %q, %v", password, data, err)
	}
	info, _ := d.Resolve(d.Trailer().Get("Info"))
	dict, _ := ToDict(info)
	title, _ := ToString(mustResolveObject(d, dict.Get("Title")))
	if string(title) != "a secret title" {
		t.Errorf("%q: title = %q", password, title)
	}
}

// mustResolveObject is a test helper that ignores the error a document that
// opened cannot produce.
func mustResolveObject(d *Document, o Object) Object {
	out, _ := d.Resolve(o)
	return out
}

func TestEncryptAndRead(t *testing.T) {
	for _, packed := range []bool{false, true} {
		for _, aes128 := range []bool{false, true} {
			b := protectedFile(t, packed, Encryption{
				UserPassword:  "hunter2",
				OwnerPassword: "letmein",
				Permissions:   PermPrint,
				AES128:        aes128,
			})
			readsBack(t, b, "hunter2")
			readsBack(t, b, "letmein")
			if _, err := Open(b); err != ErrWrongPassword {
				t.Errorf("packed=%v aes128=%v: no password gave %v", packed, aes128, err)
			}
			if _, err := OpenWithPassword(b, "nope"); err != ErrWrongPassword {
				t.Errorf("packed=%v aes128=%v: a wrong password gave %v", packed, aes128, err)
			}
			// Nothing readable is left lying about.
			if bytes.Contains(b, []byte("a secret title")) {
				t.Errorf("packed=%v aes128=%v: the title is in the clear", packed, aes128)
			}
			if bytes.Contains(b, []byte("hello")) {
				t.Errorf("packed=%v aes128=%v: the content is in the clear", packed, aes128)
			}
		}
	}
}

func TestAnEmptyOwnerPasswordIsTheUsers(t *testing.T) {
	// Leaving the owner password out must not mean anything at all opens the
	// file with every permission.
	for _, aes128 := range []bool{false, true} {
		b := protectedFile(t, false, Encryption{UserPassword: "hunter2", AES128: aes128})
		readsBack(t, b, "hunter2")
		if _, err := Open(b); err != ErrWrongPassword {
			t.Errorf("aes128=%v: the empty password opened it", aes128)
		}
		if _, err := OpenWithPassword(b, "nope"); err != ErrWrongPassword {
			t.Errorf("aes128=%v: a wrong password opened it", aes128)
		}
	}
}

func TestAnEmptyUserPasswordOpensWithoutOne(t *testing.T) {
	// A file protected only against editing opens with no password at all.
	b := protectedFile(t, false, Encryption{OwnerPassword: "letmein", Permissions: PermPrint})
	readsBack(t, b, "")
	readsBack(t, b, "letmein")
}

func TestPermissionsAreWrittenDown(t *testing.T) {
	b := protectedFile(t, false, Encryption{UserPassword: "x", Permissions: PermPrint | PermCopy})
	d, err := OpenWithPassword(b, "x")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := d.Resolve(d.Trailer().Get("Encrypt"))
	if err != nil {
		t.Fatal(err)
	}
	dict, ok := ToDict(enc)
	if !ok {
		t.Fatalf("/Encrypt is a %s", enc.Kind())
	}
	p, ok := ToInt(dict.Get("P"))
	if !ok {
		t.Fatalf("/P = %v", dict.Get("P"))
	}
	got := Permissions(uint32(int32(p)) &^ permissionBase)
	if got != PermPrint|PermCopy {
		t.Errorf("permissions = %b, want %b", got, PermPrint|PermCopy)
	}
	// And the file says which handler wrote it.
	if v, _ := ToInt(dict.Get("R")); v != 6 {
		t.Errorf("/R = %v", dict.Get("R"))
	}
}

func TestEncryptRefusesAfterWritingHasBegun(t *testing.T) {
	w := NewWriter("1.7")
	w.Add(Integer(1))
	w.Encrypt(Encryption{UserPassword: "x"})
	if w.Err() == nil {
		t.Error("want an error")
	}
	// And on the packed side, where the first object is only pending.
	w = NewPackedWriter("1.7")
	w.Add(Integer(1))
	w.Encrypt(Encryption{UserPassword: "x"})
	if w.Err() == nil {
		t.Error("want an error")
	}
}

func TestTheEncryptDictionaryIsNotItselfEncrypted(t *testing.T) {
	b := protectedFile(t, false, Encryption{UserPassword: "x"})
	// The handler's name is a name, not a string, so it is never encrypted;
	// what matters is that a reader with no key at all can still read the
	// dictionary that tells it how to get one.
	if !bytes.Contains(b, []byte("/Standard")) {
		t.Error("the security handler cannot be identified without a key")
	}
	d := &Document{buf: b, xref: map[int]xrefEntry{}, cache: map[int]Object{},
		loading: map[int]bool{}, objStms: map[int]map[int]Object{}}
	if err := d.loadXref(); err != nil {
		t.Fatal(err)
	}
	enc, err := d.Resolve(d.Trailer().Get("Encrypt"))
	if err != nil {
		t.Fatal(err)
	}
	dict, ok := ToDict(enc)
	if !ok {
		t.Fatalf("/Encrypt is a %s", enc.Kind())
	}
	u, _ := ToString(dict.Get("U"))
	if len(u) != 48 {
		t.Errorf("/U is %d bytes, so it was encrypted along with everything else", len(u))
	}
}

func TestTheIdentifierIsInTheTrailer(t *testing.T) {
	b := protectedFile(t, true, Encryption{UserPassword: "x", AES128: true})
	d, err := OpenWithPassword(b, "x")
	if err != nil {
		t.Fatal(err)
	}
	id, ok := ToArray(d.Trailer().Get("ID"))
	if !ok || len(id) != 2 {
		t.Fatalf("/ID = %v", d.Trailer().Get("ID"))
	}
	first, _ := ToString(id[0])
	if len(first) != 16 {
		t.Errorf("/ID[0] is %d bytes", len(first))
	}
}

func TestPermsEntryCarriesThePermissions(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	got, err := permsEntry(key, permissionBase|uint32(PermPrint))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 16 {
		t.Fatalf("/Perms is %d bytes", len(got))
	}
	// It is one block encrypted with the file key and nothing else, so it
	// decrypts back to what went in.
	plain := decryptOneBlock(t, key, got)
	if string(plain[9:12]) != "adb" {
		t.Errorf("the marker is %q", plain[9:12])
	}
	if plain[8] != 'T' {
		t.Errorf("the metadata flag is %q", plain[8])
	}
}

func TestLegacyOwnerValueUsesTheUserPasswordWhenThereIsNone(t *testing.T) {
	withOwner := legacyOwnerValue([]byte("owner"), []byte("user"), 16)
	without := legacyOwnerValue(nil, []byte("user"), 16)
	sameAsUser := legacyOwnerValue([]byte("user"), []byte("user"), 16)
	if bytes.Equal(withOwner, without) {
		t.Error("an owner password made no difference")
	}
	if !bytes.Equal(without, sameAsUser) {
		t.Error("an absent owner password is not the user's")
	}
}

func TestRandomBytes(t *testing.T) {
	a, err := randomBytes(16)
	if err != nil {
		t.Fatal(err)
	}
	b, err := randomBytes(16)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Error("two draws came out the same")
	}
	if len(a) != 16 {
		t.Errorf("got %d bytes", len(a))
	}
}

func TestEncryptedFilesAreNotReproducible(t *testing.T) {
	// Encryption needs randomness, so two writings of the same document differ
	// — which is the one place this writer is deliberately not a function.
	e := Encryption{UserPassword: "x"}
	if bytes.Equal(protectedFile(t, false, e), protectedFile(t, false, e)) {
		t.Error("two encrypted writings came out identical")
	}
}

func TestPermissionNames(t *testing.T) {
	// The values are the bit positions the specification gives them, counting
	// from one; nothing here should drift.
	for perm, bit := range map[Permissions]int{
		PermPrint: 3, PermModify: 4, PermCopy: 5, PermAnnotate: 6,
		PermFillForms: 9, PermExtract: 10, PermAssemble: 11, PermPrintFaithful: 12,
	} {
		if perm != 1<<(bit-1) {
			t.Errorf("%b is not bit %d", perm, bit)
		}
	}
	if AllPermissions == 0 {
		t.Error("AllPermissions grants nothing")
	}
}

// decryptOneBlock undoes a single AES block, which is the only place the
// format uses the cipher without chaining.
func decryptOneBlock(t *testing.T, key, data []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 16)
	block.Decrypt(out, data)
	return out
}

// noRandomness makes every draw fail, so the paths that give up rather than
// write an unprotected file can be reached.
func noRandomness(t *testing.T) {
	t.Helper()
	old := randomSource
	randomSource = func([]byte) (int, error) { return 0, errNoRandomness }
	t.Cleanup(func() { randomSource = old })
}

var errNoRandomness = errors.New("no randomness for the test")

func TestWithoutRandomnessNothingIsWritten(t *testing.T) {
	noRandomness(t)
	for _, aes128 := range []bool{false, true} {
		w := NewWriter("1.7")
		w.Encrypt(Encryption{UserPassword: "x", AES128: aes128})
		if w.Err() == nil {
			t.Errorf("aes128=%v: a file was set up to be encrypted with no randomness", aes128)
		}
		if _, err := w.Finish(Dict{}); err == nil {
			t.Errorf("aes128=%v: it was written anyway", aes128)
		}
	}
}

func TestRandomnessLostAfterSettingUp(t *testing.T) {
	// The key is derived, and only then does randomness run out. Nothing may
	// be handed back: some of the file would be in the clear.
	for _, packed := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "packed"}[packed], func(t *testing.T) {
			w := NewWriter("1.7")
			if packed {
				w = NewPackedWriter("1.7")
			}
			w.Encrypt(Encryption{UserPassword: "x"})
			if w.Err() != nil {
				t.Fatal(w.Err())
			}
			noRandomness(t)
			root := w.Add(Dict{"Type": Name("Catalog"), "Title": String("a secret")})
			if _, err := w.Finish(Dict{"Root": root}); err == nil {
				t.Error("the file was written anyway")
			}
		})
	}
}

// budgetedRandomness lets exactly n draws succeed and fails every one after.
func budgetedRandomness(t *testing.T, n int) {
	t.Helper()
	old := randomSource
	left := n
	randomSource = func(b []byte) (int, error) {
		if left == 0 {
			return 0, errNoRandomness
		}
		left--
		return old(b)
	}
	t.Cleanup(func() { randomSource = old })
}

func TestRandomnessRunningOutAtEachStep(t *testing.T) {
	// Setting up AES-256 draws five times: the identifier, the file key, the
	// salts for each of the two passwords, and the tail of the permissions
	// block. Whichever draw fails, no file comes out.
	for n := 0; n < 5; n++ {
		t.Run(fmt.Sprintf("after %d draws", n), func(t *testing.T) {
			budgetedRandomness(t, n)
			w := NewWriter("1.7")
			w.Encrypt(Encryption{UserPassword: "x"})
			if w.Err() == nil {
				t.Fatal("the file was set up anyway")
			}
			if _, err := w.Finish(Dict{}); err == nil {
				t.Error("it was written anyway")
			}
		})
	}
}

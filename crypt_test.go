package reader

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestDecryptRC4AllRevisions(t *testing.T) {
	cases := []struct {
		name string
		opt  encOptions
	}{
		{"V1 R2, 40-bit RC4", encOptions{v: 1, r: 2, length: 40}},
		{"V2 R3, 128-bit RC4", encOptions{v: 2, r: 3, length: 128}},
		{"V4 R4, RC4 crypt filter", encOptions{v: 4, r: 4, length: 128, method: cryptRC4}},
		{"V4 R4, AESV2 crypt filter", encOptions{v: 4, r: 4, length: 128, method: cryptAESV2}},
		{"V5 R5, AESV3", encOptions{v: 5, r: 5, length: 256, method: cryptAESV3}},
		{"V5 R6, AESV3", encOptions{v: 5, r: 6, length: 256, method: cryptAESV3}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkDecrypted(t, encryptedFile(t, c.opt), "")
		})
	}
}

func TestDecryptWithAUserPassword(t *testing.T) {
	for _, opt := range []encOptions{
		{v: 2, r: 3, length: 128, userPw: "hunter2"},
		{v: 4, r: 4, length: 128, method: cryptAESV2, userPw: "hunter2"},
		{v: 5, r: 6, length: 256, method: cryptAESV3, userPw: "hunter2"},
	} {
		checkDecrypted(t, encryptedFile(t, opt), "hunter2")
		// The empty password must not open it.
		if _, err := Open(encryptedFile(t, opt)); err != ErrWrongPassword {
			t.Errorf("R%d: opening with no password gave %v", opt.r, err)
		}
	}
}

func TestDecryptWithAnOwnerPassword(t *testing.T) {
	for _, opt := range []encOptions{
		{v: 2, r: 3, length: 128, userPw: "user", ownerPw: "owner"},
		{v: 1, r: 2, length: 40, userPw: "user", ownerPw: "owner"},
		{v: 5, r: 6, length: 256, method: cryptAESV3, userPw: "user", ownerPw: "owner"},
	} {
		b := encryptedFile(t, opt)
		checkDecrypted(t, b, "owner")
		checkDecrypted(t, b, "user")
		if _, err := OpenWithPassword(b, "neither"); err != ErrWrongPassword {
			t.Errorf("R%d: a wrong password gave %v", opt.r, err)
		}
	}
}

func TestDecryptWithAnOwnerPasswordOnly(t *testing.T) {
	// A file with no user password but an owner password opens with either.
	b := encryptedFile(t, encOptions{v: 2, r: 3, length: 128, ownerPw: "owner"})
	checkDecrypted(t, b, "")
	checkDecrypted(t, b, "owner")
}

func TestDecryptWithoutEncryptedMetadata(t *testing.T) {
	checkDecrypted(t, encryptedFile(t, encOptions{v: 4, r: 4, length: 128, method: cryptRC4, noMetadata: true}), "")
}

func TestIdentityCryptFilter(t *testing.T) {
	// /StmF and /StrF naming /Identity means the data is not encrypted at all.
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 1 1] >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R >>")
	b.obj(5, "<< /Producer (plain) >>")
	owner := legacyOwnerEntry(t, encOptions{r: 4}, 16)
	key := legacyFileKey(padPassword(nil), owner, testID, -4, 16, 4, true)
	user := legacyUserEntry(t, key, 4)
	b.obj(6, "<< /Filter /Standard /V 4 /R 4 /Length 128 /P -4 /O "+hexString(owner)+
		" /U "+hexString(user)+" /StmF /Identity /StrF /Identity >>")
	f := b.table("/Root 1 0 R /Info 5 0 R /Encrypt 6 0 R /ID [" + hexString(testID) + " " + hexString(testID) + "]")

	d, err := Open(f)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := d.Resolve(d.Trailer().Get("Info"))
	id, _ := ToDict(info)
	o, _ := d.Resolve(id.Get("Producer"))
	if s, _ := ToString(o); string(s) != "plain" {
		t.Errorf("/Producer = %q", s)
	}
}

func TestUnsupportedSecurityHandler(t *testing.T) {
	b := onePage()
	b = replaceAll(b, "/Root 1 0 R", "/Root 1 0 R /Encrypt << /Filter /Adobe.PubSec /V 4 /R 4 >>")
	if _, err := Open(b); err != ErrUnsupportedEncryption {
		t.Errorf("got %v, want ErrUnsupportedEncryption", err)
	}
}

func TestMalformedEncryptDictionaries(t *testing.T) {
	cases := []struct{ name, enc string }{
		{"not a dictionary", "/Encrypt 42"},
		{"no /V or /R", "/Encrypt << /Filter /Standard >>"},
		{"an unknown crypt filter method", "/Encrypt << /Filter /Standard /V 4 /R 4 /CF << /StdCF << /CFM /Nope >> >> /StmF /StdCF >>"},
		{"/U too short for revision 6", "/Encrypt << /Filter /Standard /V 5 /R 6 /U <00> >>"},
	}
	for _, c := range cases {
		b := replaceAll(onePage(), "/Root 1 0 R", "/Root 1 0 R "+c.enc)
		if _, err := Open(b); err == nil {
			t.Errorf("%s: want an error", c.name)
		}
	}
}

func TestEncryptDictionaryDefaults(t *testing.T) {
	// An absurd /Length falls back to 40 bits, and a /CF entry naming a filter
	// that is not there means no encryption for that class of data.
	b := replaceAll(onePage(), "/Root 1 0 R",
		"/Root 1 0 R /Encrypt << /Filter /Standard /V 4 /R 4 /Length 7 /StmF /Missing /StrF /Missing >>")
	if _, err := Open(b); err != ErrWrongPassword {
		t.Errorf("got %v", err)
	}
}

func TestFirstIDVariants(t *testing.T) {
	d := &Document{trailer: Dict{}}
	if got := d.firstID(); got != nil {
		t.Errorf("no /ID: %q", got)
	}
	d = &Document{trailer: Dict{"ID": Array{}}}
	if got := d.firstID(); got != nil {
		t.Errorf("empty /ID: %q", got)
	}
	d = &Document{trailer: Dict{"ID": Array{Integer(1)}}}
	if got := d.firstID(); got != nil {
		t.Errorf("/ID holding a number: %q", got)
	}
	d = &Document{trailer: Dict{"ID": Array{String("abc")}}}
	if got := d.firstID(); string(got) != "abc" {
		t.Errorf("/ID = %q", got)
	}
}

func TestNotEncrypted(t *testing.T) {
	d, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	if d.Encrypted() {
		t.Error("a plain file reports itself encrypted")
	}
	if err := d.setUpDecryption(""); err != nil {
		t.Errorf("setUpDecryption on a plain file: %v", err)
	}
}

func TestHash2BRevision5IsPlainSHA256(t *testing.T) {
	// Revision 5 fixes the input ordering: password, then salt, then the user
	// data an owner check adds.
	want := sha256.Sum256([]byte("pwsalt1234udata"))
	got := hash2B([]byte("pw"), []byte("salt1234"), []byte("udata"), 5)
	if !bytes.Equal(got, want[:]) {
		t.Errorf("hash2B = %x, want %x", got, want)
	}
}

func TestHash2BRevision6Terminates(t *testing.T) {
	got := hash2B([]byte("pw"), []byte("salt1234"), nil, 6)
	if len(got) != 32 {
		t.Fatalf("hash2B returned %d bytes", len(got))
	}
	// The hardening loop must depend on the password.
	other := hash2B([]byte("px"), []byte("salt1234"), nil, 6)
	if bytes.Equal(got, other) {
		t.Error("two different passwords hashed the same")
	}
}

func TestAESDecryptRejectsMalformedData(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	if got := aesDecrypt(key, []byte("short")); got != nil {
		t.Errorf("short data = %q", got)
	}
	if got := aesDecrypt(key, bytes.Repeat([]byte{0}, 33)); got != nil {
		t.Errorf("data that is not a whole number of blocks = %q", got)
	}
	if got := aesDecrypt([]byte("bad key length"), bytes.Repeat([]byte{0}, 32)); got != nil {
		t.Errorf("a bad key = %q", got)
	}
	// A block whose last byte is not usable padding is returned whole.
	out := aesEncrypt(key, bytes.Repeat([]byte{0}, 16), []byte("sixteen bytes!!!"))
	plain := aesDecrypt(key, out)
	if string(plain) != "sixteen bytes!!!" {
		t.Errorf("round trip = %q", plain)
	}
}

func TestRC4RejectsAnEmptyKey(t *testing.T) {
	if got := rc4Bytes(nil, []byte("x")); got != nil {
		t.Errorf("an empty key gave %q", got)
	}
}
func TestDecryptorSkipsTheEncryptDictionary(t *testing.T) {
	dec := &decryptor{key: bytes.Repeat([]byte{7}, 16), strings: cryptRC4, streams: cryptRC4, skipObj: 6, skipKnown: true}
	s := String("keep me")
	if got := dec.decryptObject(6, 0, s); string(got.(String)) != "keep me" {
		t.Errorf("the /Encrypt dictionary was decrypted: %q", got)
	}
	if got := dec.decryptObject(7, 0, String("keep me")); string(got.(String)) == "keep me" {
		t.Error("an ordinary string was not decrypted")
	}
}

func TestDecryptorLeavesXrefStreamsAlone(t *testing.T) {
	dec := &decryptor{key: bytes.Repeat([]byte{7}, 16), strings: cryptRC4, streams: cryptRC4}
	s := &Stream{Dict: Dict{"Type": Name("XRef")}, Raw: []byte("rows")}
	got := dec.decryptObject(1, 0, s).(*Stream)
	if string(got.Raw) != "rows" {
		t.Errorf("a cross-reference stream was decrypted: %q", got.Raw)
	}
}

func TestDecryptorWalksNestedObjects(t *testing.T) {
	dec := &decryptor{key: bytes.Repeat([]byte{7}, 16), strings: cryptNone, streams: cryptNone}
	in := Dict{"A": Array{String("x"), Integer(1)}, "B": Name("n")}
	got := dec.decryptObject(1, 0, in).(Dict)
	arr, _ := ToArray(got.Get("A"))
	if s, _ := ToString(arr[0]); string(s) != "x" {
		t.Errorf("the identity method changed a string: %q", s)
	}
	if got := dec.decryptObject(1, 0, Integer(3)); got != Object(Integer(3)) {
		t.Errorf("a number came back as %v", got)
	}
}

func TestObjectKeyLengths(t *testing.T) {
	dec := &decryptor{key: bytes.Repeat([]byte{1}, 5)}
	if got := dec.objectKey(1, 0, cryptRC4); len(got) != 10 {
		t.Errorf("a 40-bit key yields %d bytes", len(got))
	}
	dec = &decryptor{key: bytes.Repeat([]byte{1}, 16)}
	if got := dec.objectKey(1, 0, cryptRC4); len(got) != 16 {
		t.Errorf("a 128-bit key yields %d bytes", len(got))
	}
	dec = &decryptor{key: bytes.Repeat([]byte{1}, 32)}
	if got := dec.objectKey(1, 0, cryptAESV3); len(got) != 32 {
		t.Errorf("AESV3 must use the file key unchanged, got %d bytes", len(got))
	}
}

func TestDecryptBytesFallsBackOnAnUnusableKey(t *testing.T) {
	// An empty key makes RC4 refuse; the data is then left as it is rather
	// than lost.
	dec := &decryptor{key: nil}
	if got := dec.decryptBytes(0, 0, cryptNone, []byte("x")); string(got) != "x" {
		t.Errorf("identity = %q", got)
	}
}

func TestUserFromOwnerRejectsAShortEntry(t *testing.T) {
	if got := userFromOwner([]byte("pw"), []byte("short"), 16, 3); got != nil {
		t.Errorf("got %q", got)
	}
}

func TestResolvedAndIntOf(t *testing.T) {
	boom := func(Ref) (Object, error) { return nil, errWrong }
	if got := resolved(Dict{"K": Ref{1, 0}}, "K", boom); got.Kind() != KindNull {
		t.Errorf("resolved = %v", got)
	}
	if got := intOf(Integer(3), 9); got != 3 {
		t.Errorf("intOf = %d", got)
	}
	if got := intOf(Name("x"), 9); got != 9 {
		t.Errorf("intOf default = %d", got)
	}
}

var errWrong = ErrWrongPassword

func TestSetUpDecryptionPropagatesALookupFailure(t *testing.T) {
	d := brokenDoc()
	d.trailer = Dict{"Encrypt": Ref{5, 0}}
	if err := d.setUpDecryption(""); err == nil {
		t.Error("want an error")
	}
}

func TestCryptFilterMethodNone(t *testing.T) {
	// /CFM /None is a filter that does nothing.
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 1 1] >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R >>")
	b.obj(5, "<< /Producer (plain) >>")
	owner := legacyOwnerEntry(t, encOptions{r: 4}, 16)
	key := legacyFileKey(padPassword(nil), owner, testID, -4, 16, 4, true)
	user := legacyUserEntry(t, key, 4)
	b.obj(6, "<< /Filter /Standard /V 4 /R 4 /Length 128 /P -4 /O "+hexString(owner)+
		" /U "+hexString(user)+" /CF << /StdCF << /CFM /None >> >> /StmF /StdCF /StrF /StdCF >>")
	f := b.table("/Root 1 0 R /Info 5 0 R /Encrypt 6 0 R /ID [" + hexString(testID) + " " + hexString(testID) + "]")

	d, err := Open(f)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := d.Resolve(d.Trailer().Get("Info"))
	id, _ := ToDict(info)
	o, _ := d.Resolve(id.Get("Producer"))
	if s, _ := ToString(o); string(s) != "plain" {
		t.Errorf("/Producer = %q", s)
	}
}

func TestUnlockR5RejectsAShortWrappedKey(t *testing.T) {
	vs, ks := []byte("valsalt!"), []byte("keysalt!")
	entry := append(append(hash2B(nil, vs, nil, 6), vs...), ks...)
	if got := unlockR5(nil, entry, []byte("too short"), nil, 6); got != nil {
		t.Errorf("got %x", got)
	}
	// And a password that does not match the validation hash.
	if got := unlockR5([]byte("wrong"), entry, bytes.Repeat([]byte{0}, 32), nil, 6); got != nil {
		t.Errorf("got %x", got)
	}
}

func TestAESDecryptKeepsUnusablePadding(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	// One raw block whose last byte cannot be a padding length.
	block := []byte("---------------\xff")
	data := append(bytes.Repeat([]byte{0}, 16), aesWrap(key, block)...)
	got := aesDecrypt(key, data)
	if !bytes.Equal(got, block) {
		t.Errorf("got %q, want %q", got, block)
	}
}

func TestOpenRepairedEncryptedFileWithTheWrongPassword(t *testing.T) {
	// The tables are gone and the password is wrong: the rebuild succeeds and
	// the key derivation is what fails.
	b := withoutStartxref(encryptedFile(t, encOptions{v: 2, r: 3, length: 128, userPw: "hunter2"}))
	if _, err := Open(b); err != ErrWrongPassword {
		t.Errorf("got %v, want ErrWrongPassword", err)
	}
	// With the password, the same damaged file opens.
	checkDecrypted(t, b, "hunter2")
}

func TestDecryptAfterAnOffsetRepair(t *testing.T) {
	// An encrypted file whose table points an object at the wrong place: the
	// rebuild finds it, and it still has to be decrypted.
	d, err := Open(encryptedFile(t, encOptions{v: 4, r: 4, length: 128, method: cryptAESV2}))
	if err != nil {
		t.Fatal(err)
	}
	d.cache = map[int]Object{}
	d.xref[5] = xrefEntry{kind: 'n', offset: 3}
	o, err := d.Get(Ref{5, 0})
	if err != nil {
		t.Fatal(err)
	}
	info, ok := ToDict(o)
	if !ok {
		t.Fatalf("object 5 is a %s", o.Kind())
	}
	s, _ := ToString(info.Get("Producer"))
	if string(s) != encryptedProducer {
		t.Errorf("/Producer = %q, want %q", s, encryptedProducer)
	}
}

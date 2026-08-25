package reader

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"fmt"
	"testing"
)

// The encryptor below exists only to produce files for the tests. It is
// written from the same algorithms the reader implements, so a round trip
// proves the plumbing rather than the cryptography; the one genuinely
// independent check is the real /V 4 /R 4 file the corpus supplied, whose
// strings and content stream decrypt to readable PDF.

type encOptions struct {
	v, r, length int
	method       cryptMethod
	userPw       string
	ownerPw      string
	perm         int32
	noMetadata   bool
}

// testID is the /ID a built file carries; the derivation depends on it.
var testID = []byte("0123456789abcdef")

// fixedFileKey is the 256-bit key a /R 5 or /R 6 file is built around.
var fixedFileKey = []byte("!!a 32-byte AES key for tests!!!")

const encryptedProducer = "go-pdfkit reader test suite"
const encryptedContent = "BT /F1 12 Tf (secret) Tj ET"

// rc4Must applies RC4 or fails the test.
func rc4Must(t *testing.T, key, data []byte) []byte {
	t.Helper()
	out := rc4Bytes(key, data)
	if out == nil {
		t.Fatalf("RC4 refused a %d-byte key", len(key))
	}
	return out
}

// xorByte returns key with every byte exclusive-ored with v.
func xorByte(key []byte, v int) []byte {
	out := make([]byte, len(key))
	for i := range key {
		out[i] = key[i] ^ byte(v)
	}
	return out
}

// legacyOwnerEntry is algorithm 3: the /O value.
func legacyOwnerEntry(t *testing.T, opt encOptions, n int) []byte {
	pw := opt.ownerPw
	if pw == "" {
		pw = opt.userPw
	}
	sum := md5.Sum(padPassword([]byte(pw)))
	key := sum[:]
	if opt.r >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(key)
			key = s[:]
		}
	}
	key = key[:n]
	x := padPassword([]byte(opt.userPw))
	if opt.r == 2 {
		return rc4Must(t, key, x)
	}
	for i := 0; i <= 19; i++ {
		x = rc4Must(t, xorByte(key, i), x)
	}
	return x
}

// legacyUserEntry is algorithms 4 and 5: the /U value.
func legacyUserEntry(t *testing.T, key []byte, r int) []byte {
	if r == 2 {
		return rc4Must(t, key, pad)
	}
	h := md5.New()
	h.Write(pad)
	h.Write(testID)
	x := rc4Must(t, key, h.Sum(nil))
	for i := 1; i <= 19; i++ {
		x = rc4Must(t, xorByte(key, i), x)
	}
	return append(x, bytes.Repeat([]byte{0}, 16)...)
}

// aesEncrypt is the inverse of aesDecrypt: a leading initialisation vector and
// the padding CBC calls for.
func aesEncrypt(key, iv, data []byte) []byte {
	n := aes.BlockSize - len(data)%aes.BlockSize
	padded := append(append([]byte{}, data...), bytes.Repeat([]byte{byte(n)}, n)...)
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return append(append([]byte{}, iv...), out...)
}

// aesWrap encrypts exactly one 32-byte key with a zero initialisation vector
// and no padding, which is how /UE and /OE are written.
func aesWrap(key, data []byte) []byte {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(out, data)
	return out
}

// encryptor holds what a built file needs to protect its own data.
type encryptor struct {
	key    []byte
	method cryptMethod
	iv     []byte
}

// apply encrypts one object's bytes.
func (e *encryptor) apply(t *testing.T, num, gen int, data []byte) []byte {
	dec := &decryptor{key: e.key}
	k := dec.objectKey(num, gen, e.method)
	switch e.method {
	case cryptNone:
		return data
	case cryptRC4:
		return rc4Must(t, k, data)
	default:
		return aesEncrypt(k, e.iv, data)
	}
}

// hexString renders bytes the way a file writes an encrypted string.
func hexString(b []byte) string {
	var out bytes.Buffer
	out.WriteByte('<')
	for _, c := range b {
		fmt.Fprintf(&out, "%02x", c)
	}
	out.WriteByte('>')
	return out.String()
}

// encryptedFile builds a one-page document protected as the options ask.
func encryptedFile(t *testing.T, opt encOptions) []byte {
	t.Helper()
	if opt.length == 0 {
		opt.length = 40
	}
	if opt.perm == 0 {
		opt.perm = -4
	}
	n := opt.length / 8
	if opt.r == 2 {
		n = 5
	}
	var encDict string
	var enc encryptor
	enc.iv = []byte("0123456789abcdef")

	if opt.r >= 5 {
		enc.key, enc.method = fixedFileKey, cryptAESV3
		vs, ks := []byte("valsalt!"), []byte("keysalt!")
		u := append(append(hash2B([]byte(opt.userPw), vs, nil, opt.r), vs...), ks...)
		ue := aesWrap(hash2B([]byte(opt.userPw), ks, nil, opt.r), fixedFileKey)
		ovs, oks := []byte("ovalsalt"), []byte("okeysalt")
		ownerPw := opt.ownerPw
		if ownerPw == "" {
			ownerPw = opt.userPw
		}
		o := append(append(hash2B([]byte(ownerPw), ovs, u[:48], opt.r), ovs...), oks...)
		oe := aesWrap(hash2B([]byte(ownerPw), oks, u[:48], opt.r), fixedFileKey)
		encDict = fmt.Sprintf(
			"<< /Filter /Standard /V 5 /R %d /Length 256 /P %d /U %s /UE %s /O %s /OE %s "+
				"/CF << /StdCF << /CFM /AESV3 /Length 32 >> >> /StmF /StdCF /StrF /StdCF >>",
			opt.r, opt.perm, hexString(u), hexString(ue), hexString(o), hexString(oe))
	} else {
		owner := legacyOwnerEntry(t, opt, n)
		key := legacyFileKey(padPassword([]byte(opt.userPw)), owner, testID, opt.perm, n, opt.r, !opt.noMetadata)
		user := legacyUserEntry(t, key, opt.r)
		enc.key = key
		enc.method = opt.method
		if opt.v < 4 {
			enc.method = cryptRC4
		}
		meta := "true"
		if opt.noMetadata {
			meta = "false"
		}
		cfm := "V2"
		switch enc.method {
		case cryptAESV2:
			cfm = "AESV2"
		case cryptNone:
			// A file that declares a security handler and then protects
			// nothing with it: rare, legal, and something a person asking
			// what a file is protected with deserves to be told.
			cfm = "None"
		}
		if opt.v >= 4 {
			encDict = fmt.Sprintf(
				"<< /Filter /Standard /V %d /R %d /Length %d /P %d /O %s /U %s /EncryptMetadata %s "+
					"/CF << /StdCF << /CFM /%s /Length %d >> >> /StmF /StdCF /StrF /StdCF >>",
				opt.v, opt.r, opt.length, opt.perm, hexString(owner), hexString(user), meta, cfm, n)
		} else {
			encDict = fmt.Sprintf("<< /Filter /Standard /V %d /R %d /Length %d /P %d /O %s /U %s >>",
				opt.v, opt.r, opt.length, opt.perm, hexString(owner), hexString(user))
		}
	}

	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 100 100] >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>")
	b.streamObj(4, "", enc.apply(t, 4, 0, []byte(encryptedContent)))
	b.obj(5, "<< /Producer "+hexString(enc.apply(t, 5, 0, []byte(encryptedProducer)))+" >>")
	b.obj(6, encDict)
	return b.table(fmt.Sprintf("/Root 1 0 R /Info 5 0 R /Encrypt 6 0 R /ID [%s %s]",
		hexString(testID), hexString(testID)))
}

// checkDecrypted asserts that a built file opens with the given password and
// that both its string and its stream come back intact.
func checkDecrypted(t *testing.T, b []byte, password string) {
	t.Helper()
	d, err := OpenWithPassword(b, password)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !d.Encrypted() {
		t.Fatal("the document does not report itself encrypted")
	}
	info, err := d.Resolve(d.Trailer().Get("Info"))
	if err != nil {
		t.Fatal(err)
	}
	id, ok := ToDict(info)
	if !ok {
		t.Fatalf("/Info is a %s", info.Kind())
	}
	o, _ := d.Resolve(id.Get("Producer"))
	s, ok := ToString(o)
	if !ok || string(s) != encryptedProducer {
		t.Errorf("/Producer = %q, want %q", s, encryptedProducer)
	}
	page, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := d.Resolve(page.Get("Contents"))
	st, ok := ToStream(c)
	if !ok {
		t.Fatalf("/Contents is a %s", c.Kind())
	}
	data, _, err := d.DecodeStream(st)
	if err != nil || string(data) != encryptedContent {
		t.Errorf("content = %q, %v", data, err)
	}
}

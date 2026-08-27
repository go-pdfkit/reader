package reader

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"hash"
)

// ErrWrongPassword is returned when a file is encrypted and neither the
// password given nor the empty one opens it.
var ErrWrongPassword = errors.New("reader: the password does not open this file")

// ErrUnsupportedEncryption is returned for a security handler this package
// does not implement — anything other than the standard one.
var ErrUnsupportedEncryption = errors.New("reader: unsupported security handler")

// pad is the 32-byte string every pre-2.0 password is padded with.
var pad = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56,
	0xFF, 0xFA, 0x01, 0x08, 0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80,
	0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

// cryptMethod is how one class of data — strings or streams — is protected.
type cryptMethod uint8

const (
	cryptNone cryptMethod = iota // /Identity: not encrypted at all
	cryptRC4
	cryptAESV2 // 128-bit AES, per-object key
	cryptAESV3 // 256-bit AES, the file key used directly
)

// A decryptor holds the file encryption key and how to apply it.
type decryptor struct {
	key       []byte
	strings   cryptMethod
	streams   cryptMethod
	revision  int
	perm      Permissions
	owner     bool // the password given was the owner's, not the user's
	skipObj   int  // the /Encrypt dictionary's own object number, never decrypted
	skipKnown bool // whether skipObj is meaningful
}

// A Protection is what a file's security handler says about it: how it is
// encrypted, and what a reader that opened it with the user password may do.
type Protection struct {
	// Method names the algorithm the way a person would say it: "RC4-40",
	// "RC4-128", "AES-128", "AES-256", or "none" for a file that declares an
	// /Encrypt dictionary and then encrypts nothing with it.
	Method string
	// Revision is the standard security handler's revision: 2, 3, 4, 5 or 6.
	Revision int
	// Permissions is what the file grants whoever opened it with the user
	// password.
	Permissions Permissions
	// Owner is true when the password the file was opened with was the
	// owner's, in which case the permissions do not apply to this reader.
	Owner bool
}

// Protection reports how the file is protected, and false when it is not
// protected at all. A document that opened has already been decrypted; this
// says what it said about itself on the way.
func (d *Document) Protection() (Protection, bool) {
	if d.decrypt == nil {
		return Protection{}, false
	}
	return Protection{
		Method:      d.decrypt.methodName(),
		Revision:    d.decrypt.revision,
		Permissions: d.decrypt.perm,
		Owner:       d.decrypt.owner,
	}, true
}

// methodName says which algorithm protects the streams, which is the one that
// matters: it is where the content is.
func (dec *decryptor) methodName() string {
	switch dec.streams {
	case cryptAESV3:
		return "AES-256"
	case cryptAESV2:
		return "AES-128"
	case cryptRC4:
		return fmt.Sprintf("RC4-%d", len(dec.key)*8)
	}
	return "none"
}

// Encrypted reports whether the file declares an /Encrypt dictionary.
func (d *Document) Encrypted() bool {
	return d.trailer != nil && d.trailer.Get("Encrypt").Kind() != KindNull
}

// setUpDecryption reads /Encrypt and derives the file key. It is called once,
// before any string or stream is handed out.
func (d *Document) setUpDecryption(password string) error {
	if !d.Encrypted() {
		return nil
	}
	if ref, ok := d.trailer.Get("Encrypt").(Ref); ok {
		d.encryptNum, d.encryptKnown = ref.Num, true
	}
	o, err := d.Resolve(d.trailer.Get("Encrypt"))
	if err != nil {
		return err
	}
	enc, ok := ToDict(o)
	if !ok {
		return fmt.Errorf("reader: /Encrypt is a %s, not a dictionary", o.Kind())
	}
	id := d.firstID()
	dec, err := newDecryptor(enc, id, password, d.Get)
	if err != nil {
		return err
	}
	dec.skipObj, dec.skipKnown = d.encryptNum, d.encryptKnown
	d.decrypt = dec
	return nil
}

// firstID returns the first element of the trailer's /ID, which takes part in
// the pre-2.0 key derivation. A file without one contributes nothing.
func (d *Document) firstID() []byte {
	arr, ok := ToArray(d.trailer.Get("ID"))
	if !ok || len(arr) == 0 {
		return nil
	}
	s, ok := ToString(arr[0])
	if !ok {
		return nil
	}
	return s
}

// newDecryptor derives the file encryption key from a password.
func newDecryptor(enc Dict, id []byte, password string, r Resolver) (*decryptor, error) {
	if f, ok := ToName(resolved(enc, "Filter", r)); ok && f != "Standard" {
		return nil, ErrUnsupportedEncryption
	}
	v := int(intOf(resolved(enc, "V", r), 0))
	rev := int(intOf(resolved(enc, "R", r), 0))
	if v == 0 || rev == 0 {
		return nil, fmt.Errorf("reader: /Encrypt has no usable /V and /R")
	}
	length := int(intOf(resolved(enc, "Length", r), 40))
	if length < 40 || length > 256 || length%8 != 0 {
		length = 40
	}
	dec := &decryptor{revision: rev}
	if err := dec.readMethods(enc, v, r); err != nil {
		return nil, err
	}
	owner, _ := ToString(resolved(enc, "O", r))
	user, _ := ToString(resolved(enc, "U", r))
	perm := int32(intOf(resolved(enc, "P", r), 0))
	metadata := true
	if b, ok := ToBool(resolved(enc, "EncryptMetadata", r)); ok {
		metadata = b
	}

	dec.perm = Permissions(uint32(perm)) & AllPermissions
	if rev >= 5 {
		key, asOwner, err := deriveKeyR5(enc, password, r)
		if err != nil {
			return nil, err
		}
		dec.key, dec.owner = key, asOwner
		return dec, nil
	}
	n := length / 8
	if rev == 2 {
		n = 5
	}
	key, asOwner, err := deriveKeyLegacy(password, owner, user, id, perm, n, rev, metadata)
	if err != nil {
		return nil, err
	}
	dec.key, dec.owner = key, asOwner
	return dec, nil
}

// readMethods works out how strings and streams are protected. Before /V 4
// there is one method for everything; from /V 4 the crypt filters in /CF are
// named by /StmF and /StrF.
func (dec *decryptor) readMethods(enc Dict, v int, r Resolver) error {
	if v < 4 {
		dec.strings, dec.streams = cryptRC4, cryptRC4
		return nil
	}
	cf, _ := ToDict(resolved(enc, "CF", r))
	pick := func(key Name) (cryptMethod, error) {
		name, ok := ToName(resolved(enc, key, r))
		if !ok || name == "Identity" {
			return cryptNone, nil
		}
		f, ok := ToDict(resolved(cf, name, r))
		if !ok {
			return cryptNone, nil
		}
		switch m, _ := ToName(resolved(f, "CFM", r)); m {
		case "V2":
			return cryptRC4, nil
		case "AESV2":
			return cryptAESV2, nil
		case "AESV3":
			return cryptAESV3, nil
		case "None":
			return cryptNone, nil
		default:
			return cryptNone, fmt.Errorf("reader: unsupported crypt filter method /%s", m)
		}
	}
	var err error
	if dec.streams, err = pick("StmF"); err != nil {
		return err
	}
	dec.strings, err = pick("StrF")
	return err
}

// deriveKeyLegacy is the pre-2.0 key derivation, trying the password as the
// user password and then as the owner password.
func deriveKeyLegacy(password string, owner, user, id []byte, perm int32, n, rev int, metadata bool) (key []byte, asOwner bool, err error) {
	for _, candidate := range legacyCandidates(password, owner, n, rev) {
		k := legacyFileKey(candidate.padded, owner, id, perm, n, rev, metadata)
		if legacyUserKeyMatches(k, user, id, rev) {
			return k, candidate.owner, nil
		}
	}
	return nil, false, ErrWrongPassword
}

// A legacyCandidate is one padded password to try, and whether reaching it
// meant knowing the owner's rather than the user's.
type legacyCandidate struct {
	padded []byte
	owner  bool
}

// legacyCandidates lists the padded passwords worth trying: the one given, the
// empty one, and the user password the owner password unlocks.
func legacyCandidates(password string, owner []byte, n, rev int) []legacyCandidate {
	out := []legacyCandidate{{padded: padPassword([]byte(password))}}
	if password != "" {
		out = append(out, legacyCandidate{padded: padPassword(nil)})
	}
	if u := userFromOwner([]byte(password), owner, n, rev); u != nil {
		out = append(out, legacyCandidate{padded: u, owner: true})
	}
	return out
}

// padPassword truncates or pads a password to the 32 bytes the algorithm wants.
func padPassword(p []byte) []byte {
	out := make([]byte, 32)
	k := copy(out, p)
	copy(out[k:], pad)
	return out
}

// legacyFileKey is algorithm 2: the file encryption key from a padded password.
func legacyFileKey(padded, owner, id []byte, perm int32, n, rev int, metadata bool) []byte {
	h := md5.New()
	h.Write(padded)
	h.Write(owner)
	h.Write([]byte{byte(perm), byte(perm >> 8), byte(perm >> 16), byte(perm >> 24)})
	h.Write(id)
	if rev >= 4 && !metadata {
		h.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	}
	key := h.Sum(nil)
	if rev >= 3 {
		for i := 0; i < 50; i++ {
			sum := md5.Sum(key[:n])
			key = sum[:]
		}
	}
	return key[:n]
}

// legacyUserKeyMatches is algorithm 6: does this key reproduce /U?
func legacyUserKeyMatches(key, user, id []byte, rev int) bool {
	if rev == 2 {
		return bytes.Equal(rc4Bytes(key, pad), user)
	}
	h := md5.New()
	h.Write(pad)
	h.Write(id)
	x := rc4Bytes(key, h.Sum(nil))
	for i := 1; i <= 19; i++ {
		x = rc4Bytes(xorKey(key, i), x)
	}
	return len(user) >= 16 && bytes.Equal(x, user[:16])
}

// userFromOwner is algorithm 7: recover the padded user password from /O using
// the owner password.
func userFromOwner(password, owner []byte, n, rev int) []byte {
	if len(owner) < 32 {
		return nil
	}
	sum := md5.Sum(padPassword(password))
	key := sum[:]
	if rev >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(key)
			key = s[:]
		}
	}
	key = key[:n]
	if rev == 2 {
		return rc4Bytes(key, owner[:32])
	}
	x := owner[:32]
	for i := 19; i >= 0; i-- {
		x = rc4Bytes(xorKey(key, i), x)
	}
	return x
}

// xorKey returns key with every byte exclusive-ored with v, which is how both
// password algorithms walk their nineteen rounds.
func xorKey(key []byte, v int) []byte {
	out := make([]byte, len(key))
	for i := range key {
		out[i] = key[i] ^ byte(v)
	}
	return out
}

// deriveKeyR5 is the PDF 2.0 derivation, /R 5 and /R 6: the password is
// validated against a salted hash and then unwraps the file key.
func deriveKeyR5(enc Dict, password string, r Resolver) (key []byte, asOwner bool, err error) {
	user, _ := ToString(resolved(enc, "U", r))
	userE, _ := ToString(resolved(enc, "UE", r))
	owner, _ := ToString(resolved(enc, "O", r))
	ownerE, _ := ToString(resolved(enc, "OE", r))
	rev := int(intOf(resolved(enc, "R", r), 6))
	if len(user) < 48 {
		return nil, false, fmt.Errorf("reader: /U is %d bytes, not the 48 this revision needs", len(user))
	}
	pw := []byte(password)
	for _, candidate := range [][]byte{pw, nil} {
		if candidate == nil && password == "" {
			break
		}
		if key := unlockR5(candidate, user, userE, nil, rev); key != nil {
			return key, false, nil
		}
		if len(owner) >= 48 {
			if key := unlockR5(candidate, owner, ownerE, user[:48], rev); key != nil {
				return key, true, nil
			}
		}
	}
	return nil, false, ErrWrongPassword
}

// unlockR5 checks one password against a 48-byte /U or /O entry and, when it
// matches, unwraps the file key from the matching /UE or /OE.
func unlockR5(password, entry, wrapped, udata []byte, rev int) []byte {
	valSalt, keySalt := entry[32:40], entry[40:48]
	if !bytes.Equal(hash2B(password, valSalt, udata, rev), entry[:32]) {
		return nil
	}
	if len(wrapped) < 32 {
		return nil
	}
	inter := hash2B(password, keySalt, udata, rev)
	// inter is always the 32 bytes hash2B returns, so the cipher cannot refuse it.
	block, _ := aes.NewCipher(inter)
	out := make([]byte, 32)
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(out, wrapped[:32])
	return out
}

// hash2B is algorithm 2.B: SHA-256 for revision 5, and for revision 6 the
// hardening loop that repeatedly encrypts and re-hashes.
func hash2B(password, salt, udata []byte, rev int) []byte {
	h := sha256.New()
	h.Write(password)
	h.Write(salt)
	h.Write(udata)
	k := h.Sum(nil)
	if rev < 6 {
		return k
	}
	for round := 0; ; round++ {
		var k1 []byte
		one := append(append(append([]byte{}, password...), k...), udata...)
		for i := 0; i < 64; i++ {
			k1 = append(k1, one...)
		}
		// k is always 32 bytes, so a 16-byte key is always well formed.
		block, _ := aes.NewCipher(k[:16])
		e := make([]byte, len(k1)-len(k1)%aes.BlockSize)
		cipher.NewCBCEncrypter(block, k[16:32]).CryptBlocks(e, k1[:len(e)])
		sum := 0
		for _, b := range e[:16] {
			sum += int(b)
		}
		var next hash.Hash
		switch sum % 3 {
		case 0:
			next = sha256.New()
		case 1:
			next = sha512.New384()
		default:
			next = sha512.New()
		}
		next.Write(e)
		k = next.Sum(nil)
		if round >= 63 && int(e[len(e)-1]) <= round-31 {
			break
		}
	}
	return k[:32]
}

// objectKey is the per-object key the pre-2.0 methods use; AESV3 uses the file
// key unchanged.
func (dec *decryptor) objectKey(num, gen int, method cryptMethod) []byte {
	if method == cryptAESV3 {
		return dec.key
	}
	h := md5.New()
	h.Write(dec.key)
	h.Write([]byte{byte(num), byte(num >> 8), byte(num >> 16), byte(gen), byte(gen >> 8)})
	if method == cryptAESV2 {
		h.Write([]byte{0x73, 0x41, 0x6C, 0x54}) // "sAlT"
	}
	k := h.Sum(nil)
	if n := len(dec.key) + 5; n < 16 {
		return k[:n]
	}
	return k
}

// decryptBytes undoes one method over one object's data.
func (dec *decryptor) decryptBytes(num, gen int, method cryptMethod, data []byte) []byte {
	switch method {
	case cryptNone:
		return data
	case cryptRC4:
		return rc4Bytes(dec.objectKey(num, gen, method), data)
	default:
		return aesDecrypt(dec.objectKey(num, gen, method), data)
	}
}

// rc4Bytes applies RC4, which is its own inverse. A key length the cipher
// refuses yields nil, which every caller reads as a password that does not
// fit — the derivations here never produce one.
func rc4Bytes(key, data []byte) []byte {
	c, err := rc4.NewCipher(key)
	if err != nil {
		return nil
	}
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

// aesDecrypt undoes AES in CBC mode, the initialisation vector being the first
// block of the data and the padding the one CBC calls for.
func aesDecrypt(key, data []byte) []byte {
	if len(data) < 2*aes.BlockSize || len(data)%aes.BlockSize != 0 {
		return nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil
	}
	out := make([]byte, len(data)-aes.BlockSize)
	cipher.NewCBCDecrypter(block, data[:aes.BlockSize]).CryptBlocks(out, data[aes.BlockSize:])
	n := int(out[len(out)-1])
	if n < 1 || n > aes.BlockSize || n > len(out) {
		return out
	}
	return out[:len(out)-n]
}

// decryptObject walks an object, decrypting every string in it and, for a
// stream, its data. The /Encrypt dictionary itself, and any cross-reference
// stream, are left alone: neither is encrypted.
func (dec *decryptor) decryptObject(num, gen int, o Object) Object {
	if dec.skipKnown && num == dec.skipObj {
		return o
	}
	return dec.walk(num, gen, o)
}

// walk is decryptObject's recursion.
func (dec *decryptor) walk(num, gen int, o Object) Object {
	switch v := o.(type) {
	case String:
		return String(dec.decryptBytes(num, gen, dec.strings, v))
	case Array:
		for i, e := range v {
			v[i] = dec.walk(num, gen, e)
		}
		return v
	case Dict:
		for k, e := range v {
			v[k] = dec.walk(num, gen, e)
		}
		return v
	case *Stream:
		if t, ok := ToName(v.Dict.Get("Type")); ok && t == "XRef" {
			return v
		}
		v.Dict, _ = ToDict(dec.walk(num, gen, v.Dict))
		if streamIsPlain(v.Dict) {
			return v
		}
		v.Raw = dec.decryptBytes(num, gen, dec.streams, v.Raw)
		return v
	}
	return o
}

// streamIsPlain reports whether a stream's own filter chain says its bytes were
// left unencrypted: a leading /Crypt filter naming /Identity, which is what
// /Crypt with no /Name means too. Producers use it for the metadata stream of a
// file whose /EncryptMetadata is false, and decrypting such a stream turns
// readable XML into noise.
func streamIsPlain(d Dict) bool {
	if first, ok := firstFilter(d); !ok || first != "Crypt" {
		return false
	}
	name, ok := ToName(firstDecodeParms(d).Get("Name"))
	return !ok || name == "Identity"
}

// resolved is a helper for reading an /Encrypt entry that may be indirect.
func resolved(d Dict, key Name, r Resolver) Object {
	o, err := Resolve(d.Get(key), r)
	if err != nil {
		return Null{}
	}
	return o
}

// intOf reads an integer with a default.
func intOf(o Object, def int64) int64 {
	if n, ok := ToInt(o); ok {
		return n
	}
	return def
}

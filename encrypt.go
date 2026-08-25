package reader

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"fmt"
)

// Permissions say what a reader may do with a file it can open with the user
// password. They are advisory — nothing enforces them but the reader's own
// good manners — and a file opened with the owner password ignores them
// entirely.
type Permissions uint32

// The permissions a standard security handler can express. Anything not
// granted is refused; [AllPermissions] grants everything.
const (
	PermPrint         Permissions = 1 << 2
	PermModify        Permissions = 1 << 3
	PermCopy          Permissions = 1 << 4
	PermAnnotate      Permissions = 1 << 5
	PermFillForms     Permissions = 1 << 8
	PermExtract       Permissions = 1 << 9 // for accessibility
	PermAssemble      Permissions = 1 << 10
	PermPrintFaithful Permissions = 1 << 11 // at full resolution

	// AllPermissions grants every one of them.
	AllPermissions = PermPrint | PermModify | PermCopy | PermAnnotate |
		PermFillForms | PermExtract | PermAssemble | PermPrintFaithful
)

// permissionBase is the pattern of reserved bits the specification requires
// around the permissions themselves.
const permissionBase uint32 = 0xFFFFF0C0

// An Encryption says how a file is to be protected.
//
// Two people can open it: whoever knows the user password, subject to the
// permissions, and whoever knows the owner password, subject to nothing. An
// empty user password means the file opens without one and the permissions are
// all it says.
type Encryption struct {
	UserPassword  string
	OwnerPassword string
	Permissions   Permissions

	// AES128 asks for the older method, which readers before 2008 understand.
	// The default is AES-256, which is what a file should use today.
	AES128 bool
}

// Encrypt protects everything written from here on. It must be called before
// anything is written, and the file it produces is not byte-for-byte
// reproducible: encryption needs randomness, by design.
func (w *Writer) Encrypt(e Encryption) {
	if len(w.offsets) > 0 || len(w.pending) > 0 {
		w.note(fmt.Errorf("reader: a file cannot be encrypted after it has been written to"))
		return
	}
	enc, err := newEncrypter(e)
	if err != nil {
		w.note(err)
		return
	}
	w.encrypt = enc
}

// An encrypter holds the file key and the dictionary that describes it.
type encrypter struct {
	key    []byte
	method cryptMethod
	dict   Dict
	id     []byte
	err    error
	// number is the object the /Encrypt dictionary is written under, which is
	// the one thing in the file that is never encrypted.
	number int
}

// newEncrypter derives a file key and builds the dictionary that lets a reader
// derive it again from a password.
func newEncrypter(e Encryption) (*encrypter, error) {
	perm := permissionBase | uint32(e.Permissions)
	id, err := randomBytes(16)
	if err != nil {
		return nil, err
	}
	if e.AES128 {
		return newLegacyEncrypter(e, perm, id)
	}
	return newAES256Encrypter(e, perm, id)
}

// newAES256Encrypter builds the revision 6 form: a random file key, wrapped
// twice, once for each password.
func newAES256Encrypter(e Encryption, perm uint32, id []byte) (*encrypter, error) {
	key, err := randomBytes(32)
	if err != nil {
		return nil, err
	}
	// An owner password nobody set is the user's: leaving it empty would
	// mean anything at all opens the file with every permission, which is
	// the opposite of what asking for encryption means.
	ownerPassword := e.OwnerPassword
	if ownerPassword == "" {
		ownerPassword = e.UserPassword
	}
	user, userE, err := wrapKey([]byte(e.UserPassword), key, nil)
	if err != nil {
		return nil, err
	}
	owner, ownerE, err := wrapKey([]byte(ownerPassword), key, user)
	if err != nil {
		return nil, err
	}
	perms, err := permsEntry(key, perm)
	if err != nil {
		return nil, err
	}
	return &encrypter{
		key:    key,
		method: cryptAESV3,
		id:     id,
		dict: Dict{
			"Filter":          Name("Standard"),
			"V":               Integer(5),
			"R":               Integer(6),
			"Length":          Integer(256),
			"P":               Integer(int32(perm)),
			"U":               String(user),
			"UE":              String(userE),
			"O":               String(owner),
			"OE":              String(ownerE),
			"Perms":           String(perms),
			"CF":              Dict{"StdCF": Dict{"CFM": Name("AESV3"), "Length": Integer(32)}},
			"StmF":            Name("StdCF"),
			"StrF":            Name("StdCF"),
			"EncryptMetadata": Bool(true),
		},
	}, nil
}

// wrapKey builds one 48-byte password entry and the 32 bytes that hold the
// file key wrapped for it.
func wrapKey(password, key, udata []byte) (entry, wrapped []byte, err error) {
	salts, err := randomBytes(16)
	if err != nil {
		return nil, nil, err
	}
	validation, keySalt := salts[:8], salts[8:]
	entry = append(append(hash2B(password, validation, udata, 6), validation...), keySalt...)
	// hash2B always returns 32 bytes, so the cipher cannot refuse the key.
	block, _ := aes.NewCipher(hash2B(password, keySalt, udata, 6))
	wrapped = make([]byte, 32)
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(wrapped, key)
	return entry, wrapped, nil
}

// permsEntry is the block that lets a reader check the permissions have not
// been tampered with: they are encrypted with the file key itself.
func permsEntry(key []byte, perm uint32) ([]byte, error) {
	tail, err := randomBytes(4)
	if err != nil {
		return nil, err
	}
	plain := []byte{
		byte(perm), byte(perm >> 8), byte(perm >> 16), byte(perm >> 24),
		0xFF, 0xFF, 0xFF, 0xFF,
		'T',           // metadata is encrypted too
		'a', 'd', 'b', // the specification's own marker
		tail[0], tail[1], tail[2], tail[3],
	}
	// The file key is 32 bytes by construction.
	block, _ := aes.NewCipher(key)
	out := make([]byte, 16)
	// One block, with no chaining: this is the one place the format uses it.
	block.Encrypt(out, plain)
	return out, nil
}

// newLegacyEncrypter builds the revision 4 form, whose key comes from the
// passwords rather than the other way round.
func newLegacyEncrypter(e Encryption, perm uint32, id []byte) (*encrypter, error) {
	const n = 16 // 128 bits
	owner := legacyOwnerValue([]byte(e.OwnerPassword), []byte(e.UserPassword), n)
	key := legacyFileKey(padPassword([]byte(e.UserPassword)), owner, id, int32(perm), n, 4, true)
	user := legacyUserValue(key, id)
	return &encrypter{
		key:    key,
		method: cryptAESV2,
		id:     id,
		dict: Dict{
			"Filter":          Name("Standard"),
			"V":               Integer(4),
			"R":               Integer(4),
			"Length":          Integer(128),
			"P":               Integer(int32(perm)),
			"O":               String(owner),
			"U":               String(user),
			"CF":              Dict{"StdCF": Dict{"CFM": Name("AESV2"), "Length": Integer(16)}},
			"StmF":            Name("StdCF"),
			"StrF":            Name("StdCF"),
			"EncryptMetadata": Bool(true),
		},
	}, nil
}

// legacyOwnerValue is algorithm 3: the /O entry, which holds the user password
// encrypted under the owner's.
func legacyOwnerValue(ownerPassword, userPassword []byte, n int) []byte {
	if len(ownerPassword) == 0 {
		ownerPassword = userPassword
	}
	sum := md5Sum(padPassword(ownerPassword))
	key := sum
	for i := 0; i < 50; i++ {
		key = md5Sum(key)
	}
	key = key[:n]
	x := padPassword(userPassword)
	for i := 0; i <= 19; i++ {
		x = rc4Bytes(xorKey(key, i), x)
	}
	return x
}

// legacyUserValue is algorithm 5: the /U entry.
func legacyUserValue(key, id []byte) []byte {
	x := rc4Bytes(key, md5Sum(append(append([]byte{}, pad...), id...)))
	for i := 1; i <= 19; i++ {
		x = rc4Bytes(xorKey(key, i), x)
	}
	return append(x, make([]byte, 16)...)
}

// randomSource is where randomness comes from. It is a variable so a test
// can take it away and see that a file is refused rather than written
// without the protection it was asked for.
var randomSource = rand.Read

// randomBytes is the only source of randomness here, and the reason an
// encrypted file is not reproducible.
func randomBytes(n int) ([]byte, error) {
	out := make([]byte, n)
	if _, err := randomSource(out); err != nil {
		return nil, fmt.Errorf("reader: no randomness available: %w", err)
	}
	return out, nil
}

// apply encrypts one object's strings and, for a stream, its data.
func (e *encrypter) apply(num, gen int, o Object) Object {
	switch v := o.(type) {
	case String:
		return String(e.encryptBytes(num, gen, v))
	case Array:
		out := make(Array, len(v))
		for i, x := range v {
			out[i] = e.apply(num, gen, x)
		}
		return out
	case Dict:
		out := Dict{}
		for k, x := range v {
			out[k] = e.apply(num, gen, x)
		}
		return out
	case *Stream:
		out := &Stream{Dict: Dict{}, Raw: e.encryptBytes(num, gen, v.Raw)}
		for k, x := range v.Dict {
			out.Dict[k] = e.apply(num, gen, x)
		}
		return out
	}
	return o
}

// encryptBytes protects one object's bytes.
func (e *encrypter) encryptBytes(num, gen int, data []byte) []byte {
	dec := &decryptor{key: e.key}
	key := dec.objectKey(num, gen, e.method)
	iv, err := randomBytes(aes.BlockSize)
	if err != nil {
		// Writing the bytes as they are would hand out in the clear what was
		// asked to be protected, so the failure is remembered and the whole
		// file is refused.
		e.note(err)
		return data
	}
	// The file key is 32 bytes by construction.
	block, _ := aes.NewCipher(key)
	// The padding CBC calls for, always at least one whole block of it.
	n := aes.BlockSize - len(data)%aes.BlockSize
	padded := append(append([]byte{}, data...), bytes.Repeat([]byte{byte(n)}, n)...)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return append(iv, out...)
}

// md5Sum is the hash the pre-2.0 handlers are built on.
func md5Sum(b []byte) []byte {
	sum := md5.Sum(b)
	return sum[:]
}

// writeEncryptDict writes the /Encrypt dictionary and returns the trailer
// entries a reader needs to find and use it. The dictionary is the one object
// in the file that is never encrypted, since it says how to decrypt the rest.
func (w *Writer) writeEncryptDict() Dict {
	if w.encrypt == nil {
		return nil
	}
	ref := w.Reserve()
	w.encrypt.number = ref.Num
	w.writeInlineRaw(ref, w.encrypt.dict)
	// The identifier takes part in the older key derivation and has to be in
	// the trailer for a reader to reach it.
	id := String(w.encrypt.id)
	return Dict{"Encrypt": ref, "ID": Array{id, id}}
}

// note keeps the first thing that went wrong while encrypting, so a file that
// could not be protected is refused rather than written in the clear.
func (e *encrypter) note(err error) {
	if e.err == nil {
		e.err = err
	}
}

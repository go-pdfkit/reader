package reader

import (
	"bytes"
	"encoding/asn1"
	"errors"
	"fmt"
)

// Reading digital signatures (ISO 32000-1 §12.8). This file finds the
// signature fields and hands out the bytes a signature covers; it does not
// judge the signature — reader/signature does, on top of go-authn/cms.
//
// ⛔ A /ByteRange IS A CLAIM THE FILE MAKES ABOUT ITSELF, and the attacks on
// PDF signature validation (the signature-wrapping family) all make a validator
// believe a claim the bytes do not bear out: a gap that hides more than the
// /Contents string, ranges that overlap or run past the file so the validator
// hashes something other than what it displays, a /Contents taken from a
// different object than the one the ranges leave out. So the ranges are checked
// before a single byte is read through them, the gap must be exactly one hex
// string — Table 252 and 12.8.3.3.1 have /Contents "fit precisely in the space
// between the ranges" — and that string must be this dictionary's /Contents.
//
// ⛔ The /Contents is taken from the FILE bytes in the gap, never from the
// parsed object, because this package decrypts every string it parses and the
// bytes the ranges leave out are the raw ones. ISO 32000-1 §7.6.1 lists three
// exceptions to string encryption (the trailer /ID, strings of the Encrypt
// dictionary, strings inside streams) and a signature's /Contents is not among
// them, so the raw reading is this package's choice, not the text's: a file
// whose /Contents was encrypted fails as malformed rather than being decrypted
// into something the ranges never covered. ISO 32000-2 was not opened.

// ErrByteRange is wrapped by every refusal of a signature's /ByteRange or of
// the gap it leaves.
var ErrByteRange = errors.New("reader: invalid signature /ByteRange")

// ErrTooManyFields is returned when the interactive form is larger than this
// package will walk.
var ErrTooManyFields = errors.New("reader: the interactive form has too many fields")

const (
	// maxFieldDepth and maxFieldNodes bound the walk of /Fields and /Kids,
	// which a file is free to make arbitrarily deep, wide, or cyclic.
	maxFieldDepth = 32
	maxFieldNodes = 1 << 16
)

// A Signature is one signature field that holds a value (/V) with a signature
// dictionary. Everything here is what the FILE claims; nothing is verified
// until reader/signature does it.
type Signature struct {
	// Field is the fully qualified field name: the /T of every ancestor and of
	// the field, joined with ".".
	Field string
	// Filter and SubFilter are the dictionary's /Filter and /SubFilter names,
	// e.g. "Adobe.PPKLite" and "adbe.pkcs7.detached" (Table 252, 12.8.3).
	Filter, SubFilter string
	// ByteRange is /ByteRange as written. It is the claim, and may be invalid:
	// see Err.
	ByteRange []int64
	// Contents is the signature's DER, decoded from the hex string the
	// /ByteRange leaves out of the signed bytes, with the zero padding a signer
	// reserves after the DER removed. It is nil when Err is set.
	Contents []byte
	// Name, Reason, Location and M are the signer's claims — who, why, where and
	// when (M is the date string as written). They are covered by the signature
	// ONLY when DictionarySigned is true; otherwise an incremental update could
	// have supplied them (a new dictionary copying /ByteRange and /Contents),
	// and they are not evidence of anything. Field, the field's /T, is read from
	// the field dictionaries, which this package never ties to the signed
	// bytes: it is a label, not a signed claim, even when DictionarySigned.
	Name, Reason, Location, M string
	// DictionarySigned is true when the signature dictionary was read from an
	// uncompressed indirect object that contains the gap, so its bytes outside
	// /Contents lie inside the signed ranges. It is false when the dictionary is
	// direct, sits in an object stream, or its position cannot be established.
	DictionarySigned bool
	// CoversWholeFile is true when the ranges end at the end of the file, so no
	// byte was added after signing. False for an earlier signature once an
	// incremental update was appended (and that is legitimate: forms are filled
	// and signatures stacked that way); judging what the update changed is the
	// caller's.
	CoversWholeFile bool
	// Err is why this signature's ranges or gap were refused, or nil. A bad
	// signature does not hide the others, so it is reported here rather than
	// failing Signatures.
	Err error
}

// checkByteRange validates a claimed /ByteRange against a file of fileLen
// bytes and returns the two ranges as [start, end) offsets.
//
// The shape accepted is the one every signer writes and every viewer reads:
// exactly two ranges, the first beginning at 0, with one gap between them for
// the /Contents string. More ranges would mean more gaps, and the gap is
// required to be exactly one string. ⛔ All arithmetic is in int64 and by
// subtraction, so that no sum is formed that could overflow, and a 32-bit int
// is never asked to hold a value that is not below the file's length.
func checkByteRange(br []int64, fileLen int) (r [2][2]int, err error) {
	n := int64(fileLen)
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrByteRange}, a...)...)
	}
	// A copy, not br[0]..br[3]: a short slice reads as zeros here and is refused
	// by the length check below, and no input can make an index panic.
	var q [4]int64
	copy(q[:], br)
	off0, len0, off1, len1 := q[0], q[1], q[2], q[3]
	if len(br) != 4 {
		return r, bad("%d numbers: exactly two ranges are accepted, with one gap for the /Contents", len(br))
	}
	if off0 != 0 {
		return r, bad("the first range starts at %d, not at the start of the file", off0)
	}
	if len0 <= 0 {
		return r, bad("the first range is empty or negative")
	}
	// "Strictly ascending, not overlapping", and then "room for <>". The order
	// matters: off1 is first shown to exceed len0, which is positive, so the
	// subtraction after it cannot overflow — a negative off1 near the minimum
	// would wrap to a large positive gap. The first range cannot run past the
	// end of the file either: len0 is below off1, which the last test puts
	// inside it.
	if off1 <= len0 {
		return r, bad("the second range starts at %d, inside, touching or before the first, which ends at %d", off1, len0)
	}
	if off1-len0 < 2 {
		return r, bad("the gap between the ranges is %d bytes, too few for a hex string", off1-len0)
	}
	if len1 < 0 {
		return r, bad("the second range has a negative length %d", len1)
	}
	// Written as len1 > n-off1, never off1+len1 > n: no sum is formed, and a
	// negative n-off1 (a second range that starts past the end) is refused by
	// the same test.
	if len1 > n-off1 {
		return r, bad("the second range, from %d for %d bytes, runs past the end of the %d byte file", off1, len1, n)
	}
	return [2][2]int{{int(off0), int(len0)}, {int(off1), int(off1 + len1)}}, nil
}

// decodeGap reads the gap as exactly one hexadecimal string: "<", hex digits
// with optional white space, ">", and nothing else, not even a comment or a
// second string. An odd digit count takes a final 0, as 7.3.4.3 says.
func decodeGap(gap []byte) ([]byte, error) {
	// HasPrefix and HasSuffix, not indexing: no single byte is both "<" and ">",
	// so a gap that passes both is at least two bytes long.
	if !bytes.HasPrefix(gap, []byte("<")) || !bytes.HasSuffix(gap, []byte(">")) {
		return nil, fmt.Errorf("%w: the gap is not a single <...> string", ErrByteRange)
	}
	out := make([]byte, 0, len(gap)/2)
	hi := -1
	for _, c := range bytes.TrimSuffix(bytes.TrimPrefix(gap, []byte("<")), []byte(">")) {
		if isSpace(c) {
			continue
		}
		v := hexVal(c)
		if v < 0 {
			return nil, fmt.Errorf("%w: the gap holds %q, which is not part of a hexadecimal string", ErrByteRange, c)
		}
		if hi < 0 {
			hi = v
		} else {
			out = append(out, byte(hi<<4|v))
			hi = -1
		}
	}
	if hi >= 0 {
		out = append(out, byte(hi<<4))
	}
	return out, nil
}

// trimPadding removes the zero bytes a signer reserves after the DER, when the
// DER's own length says that is all that follows it. Anything else is returned
// whole, so the verifier sees — and refuses — exactly what the file holds.
func trimPadding(b []byte) []byte {
	var raw asn1.RawValue
	rest, err := asn1.Unmarshal(b, &raw)
	if err != nil {
		return b
	}
	for _, c := range rest {
		if c != 0 {
			return b
		}
	}
	return b[:len(b)-len(rest)]
}

// Signatures lists the document's signature fields that hold a signature,
// walking /AcroForm /Fields and /Kids (ISO 32000-1 §12.7.4.5): a field is a
// signature field when its /FT, which it may inherit from an ancestor, is /Sig,
// and it holds one when its /V, likewise inheritable, is a dictionary. The walk
// is bounded in depth and in the number of fields, and cycle-safe.
//
// A document without an interactive form has no signatures and no error. A
// signature whose /ByteRange or gap is refused is still listed, with Err set
// and no Contents.
func (d *Document) Signatures() ([]Signature, error) {
	cat, err := d.Catalog()
	if err != nil {
		return nil, err
	}
	w := &sigWalk{d: d, seen: map[Ref]bool{}, emitted: map[string]bool{},
		byDict: map[Ref]Signature{}, gaps: map[[4]int64]*gapRead{}}
	// ⛔ A holder is "ok" only when the object really was read from its xref
	// entry. A direct /Root is not an object of its own: asserting Ref{0} here
	// credited the catalogue to object 0 and sliced the file at its offset.
	var hold holder
	if root, isRef := d.trailer.Get("Root").(Ref); isRef {
		hold = holder{root, true}
	}
	form, hold, _ := w.deref(cat.Get("AcroForm"), hold)
	fd, _ := ToDict(form)
	fields, fhold, _ := w.deref(fd.Get("Fields"), hold)
	arr, _ := ToArray(fields)
	for _, kid := range arr {
		if err := w.node(kid, "", "", nil, holder{}, fhold, 1); err != nil {
			return nil, err
		}
	}
	return w.out, nil
}

// holder is the nearest enclosing object that is a numbered, indirect object:
// the place in the file the bytes of a direct dictionary sit in.
type holder struct {
	ref Ref
	ok  bool
}

type sigWalk struct {
	d     *Document
	seen  map[Ref]bool
	nodes int
	// emitted holds field name + /V reference of every signature listed.
	emitted map[string]bool
	out     []Signature
	// byDict holds the signature read from each /V reference, so that N fields
	// pointing at one dictionary cost one read, not N.
	byDict map[Ref]Signature
	// gaps holds each distinct /ByteRange's decoded gap, and gapBytes the total
	// decoded so far, against maxGapBytes.
	gaps     map[[4]int64]*gapRead
	gapBytes int
}

// maxGapBytes bounds the bytes all the gaps of one document decode to together.
// A variable so that a test can lower it.
var maxGapBytes = 1 << 25

// gapRead is the result of reading one /ByteRange's gap, once.
type gapRead struct {
	contents []byte // every decoded byte, for the comparison with the parsed /Contents
	der      []byte // the trimmed DER, a copy that does not pin contents
	r        [2][2]int
	err      error
}

// deref follows one indirect reference, returning the object and the numbered
// object it lives in (cur when o was direct). What cannot be read, a null, and
// a reference to a reference all come back as absent: a form this package cannot
// follow has no signature it can vouch for.
func (w *sigWalk) deref(o Object, cur holder) (Object, holder, bool) {
	if r, isRef := o.(Ref); isRef {
		o, _ = w.d.Get(r)
		cur = holder{r, true}
	}
	switch o.(type) {
	case nil, Null, Ref:
		return nil, cur, false
	}
	return o, cur, true
}

func (w *sigWalk) node(o Object, prefix string, ft Name, v Object, vh holder, h holder, depth int) error {
	if depth > maxFieldDepth {
		return fmt.Errorf("%w: nested deeper than %d", ErrTooManyFields, maxFieldDepth)
	}
	if r, isRef := o.(Ref); isRef {
		if w.seen[r] {
			return nil
		}
		w.seen[r] = true
	}
	w.nodes++
	if w.nodes > maxFieldNodes {
		return fmt.Errorf("%w: more than %d", ErrTooManyFields, maxFieldNodes)
	}
	obj, h, _ := w.deref(o, h)
	dict, isDict := ToDict(obj)
	if !isDict {
		return nil
	}
	name := prefix
	if t, ok := TextString(w.resolved(dict.Get("T"))); ok && t != "" {
		if name != "" {
			name += "."
		}
		name += t
	}
	if n, ok := ToName(w.resolved(dict.Get("FT"))); ok {
		ft = n
	}
	if val := dict.Get("V"); val.Kind() != KindNull {
		v, vh = val, h
	}
	if kids, ok := ToArray(w.resolved(dict.Get("Kids"))); ok {
		for _, k := range kids {
			if err := w.node(k, name, ft, v, vh, h, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if ft != "Sig" {
		return nil
	}
	vobj, vh2, _ := w.deref(v, vh)
	sd, isDict := ToDict(vobj)
	if !isDict {
		return nil
	}
	// A field with several widget annotations as /Kids reaches its one /V once
	// per widget; it is one signature.
	if r, isRef := v.(Ref); isRef {
		key := name + "|" + r.String()
		if w.emitted[key] {
			return nil
		}
		w.emitted[key] = true
		// Another field named this dictionary: same signature, its own name.
		if s, done := w.byDict[r]; done {
			s.Field = name
			w.out = append(w.out, s)
			return nil
		}
		s := w.signature(name, sd, vh2)
		w.byDict[r] = s
		w.out = append(w.out, s)
		return nil
	}
	w.out = append(w.out, w.signature(name, sd, vh2))
	return nil
}

// resolved follows references; what cannot be followed is nil, which every To*
// accessor reports as "not that type".
func (w *sigWalk) resolved(o Object) Object {
	r, _ := w.d.Resolve(o)
	return r
}

func (w *sigWalk) signature(field string, sd Dict, h holder) Signature {
	d := w.d
	s := Signature{Field: field}
	// (the /Contents string is NOT read from sd: see checkContentsObject)
	if n, ok := ToName(w.resolved(sd.Get("Filter"))); ok {
		s.Filter = string(n)
	}
	if n, ok := ToName(w.resolved(sd.Get("SubFilter"))); ok {
		s.SubFilter = string(n)
	}
	s.Name, _ = TextString(w.resolved(sd.Get("Name")))
	s.Reason, _ = TextString(w.resolved(sd.Get("Reason")))
	s.Location, _ = TextString(w.resolved(sd.Get("Location")))
	s.M, _ = TextString(w.resolved(sd.Get("M")))

	arr, ok := ToArray(w.resolved(sd.Get("ByteRange")))
	if !ok {
		s.Err = fmt.Errorf("%w: the signature dictionary has no /ByteRange array", ErrByteRange)
		return s
	}
	// The array is as long as the file made it, so at most half the file's size
	// in entries: no bound of its own is needed, and checkByteRange refuses any
	// length but four.
	s.ByteRange = make([]int64, len(arr))
	for i, e := range arr {
		// Direct integers only: Table 252 has every value of a signature
		// dictionary direct once a byte-range digest is present.
		n, isInt := e.(Integer)
		if !isInt {
			s.ByteRange = nil
			s.Err = fmt.Errorf("%w: entry %d is not an integer", ErrByteRange, i)
			return s
		}
		s.ByteRange[i] = int64(n)
	}
	g := w.readGap(s.ByteRange)
	if g.err != nil {
		s.Err = g.err
		return s
	}
	signed, err := d.checkContentsObject(sd, h, g.contents, g.r[0][1])
	if err != nil {
		s.Err = err
		return s
	}
	s.DictionarySigned = signed
	s.Contents = g.der
	s.CoversWholeFile = g.r[1][1] == len(d.buf)
	return s
}

// readGap is d.gap, once per distinct /ByteRange, and bounded in total: the
// same gap named by two thousand fields is decoded once, and ranges that each
// name a different region cannot together decode more than maxGapBytes. The
// DER is a copy, so no Signature pins the whole decoded gap.
func (w *sigWalk) readGap(br []int64) *gapRead {
	var key [4]int64
	cached := len(br) == 4
	if cached {
		copy(key[:], br)
		if g, ok := w.gaps[key]; ok {
			return g
		}
	}
	g := &gapRead{}
	if g.r, g.err = checkByteRange(br, len(w.d.buf)); g.err == nil {
		w.gapBytes += (g.r[1][0] - g.r[0][1]) / 2
		if w.gapBytes > maxGapBytes {
			g.err = fmt.Errorf("%w: the gaps of this document's signatures decode to more than %d bytes together", ErrByteRange, maxGapBytes)
		} else if g.contents, g.err = decodeGap(w.d.buf[g.r[0][1]:g.r[1][0]]); g.err == nil {
			g.der = append([]byte{}, trimPadding(g.contents)...)
		}
	}
	if cached {
		w.gaps[key] = g
	}
	return g
}

// gap validates the ranges and decodes the one hex string between them. The
// ranges come back for the caller to read through; on an error, nothing does.
func (d *Document) gap(br []int64) (contents []byte, r [2][2]int, err error) {
	if r, err = checkByteRange(br, len(d.buf)); err == nil {
		contents, err = decodeGap(d.buf[r[0][1]:r[1][0]])
	}
	return contents, r, err
}

// checkContentsObject ties the gap to the signature dictionary: the string left
// out of the signed bytes must be THE /Contents of this dictionary.
//
// On a file that is not encrypted the parsed /Contents must equal the gap's
// bytes. On an encrypted one the parser's value is decrypted and so useless, and
// the check is positional instead (when the dictionary sits in an uncompressed
// indirect object): the gap must lie inside that object's bytes.
//
// signed reports whether that positional check ran and passed, that is whether
// the dictionary's own bytes lie inside the signed ranges. It is false, with no
// error, for a dictionary the position of which cannot be established (direct
// or in an object stream) in a file that is not encrypted.
func (d *Document) checkContentsObject(sd Dict, h holder, contents []byte, gapStart int) (signed bool, err error) {
	if d.decrypt == nil {
		parsed, ok := ToString(sd.Get("Contents"))
		if !ok {
			return false, fmt.Errorf("%w: the signature dictionary has no /Contents string", ErrByteRange)
		}
		if !bytes.Equal(parsed, contents) {
			return false, fmt.Errorf("%w: the string in the gap is not the dictionary's /Contents", ErrByteRange)
		}
	}
	e := d.xref[h.ref.Num]
	if !h.ok || e.kind != 'n' {
		if d.decrypt != nil {
			// Nothing ties the gap to the dictionary, and the value check
			// above could not run: refuse rather than guess.
			return false, fmt.Errorf("%w: in an encrypted file the signature dictionary must sit in its own uncompressed object, so that the gap can be placed in it", ErrByteRange)
		}
		return false, nil
	}
	// h.ok means the object was read through this xref entry, but the slice is
	// guarded all the same: 0 <= offset <= gapStart <= len(d.buf) (the ranges
	// were checked against the file before this runs).
	if e.offset < 0 || e.offset > int64(gapStart) || bytes.Contains(d.buf[e.offset:gapStart], []byte("endobj")) {
		return false, fmt.Errorf("%w: the gap is not inside object %d, which holds the signature dictionary", ErrByteRange, h.ref.Num)
	}
	return true, nil
}

// SignedBytes returns the bytes the signature covers: the file's two ranges,
// concatenated, exactly what a detached CMS signature (adbe.pkcs7.detached,
// ETSI.CAdES.detached) was computed over (ISO 32000-1 §12.8.3.3).
//
// The /ByteRange is validated first, against this document, whatever s says, and
// s.Contents must be the string the file holds in the gap: a Signature built or
// altered by hand cannot name bytes the file's own ranges do not.
func (d *Document) SignedBytes(s Signature) ([]byte, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	contents, r, err := d.gap(s.ByteRange)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(trimPadding(contents), s.Contents) {
		return nil, fmt.Errorf("%w: the signature's /Contents is not the string in the gap", ErrByteRange)
	}
	out := make([]byte, 0, r[0][1]+r[1][1]-r[1][0])
	out = append(out, d.buf[r[0][0]:r[0][1]]...)
	out = append(out, d.buf[r[1][0]:r[1][1]]...)
	return out, nil
}

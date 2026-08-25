// Package reader takes existing PDF bytes apart: the object syntax, the
// cross-reference machinery, stream filters, and the document structure built
// on top of them. It is the parsing counterpart of github.com/go-pdfkit/pdfkit,
// which writes PDF.
//
// Nothing outside the Go standard library is used, so the package builds for
// js/wasm and for every architecture the fleet targets.
package reader

import "fmt"

// Kind names the eight basic PDF object types plus the two the file format
// adds on top of them: streams and indirect references.
type Kind uint8

// The object kinds. [Object.Kind] reports one of these.
const (
	KindNull Kind = iota
	KindBool
	KindInteger
	KindReal
	KindString
	KindName
	KindArray
	KindDict
	KindStream
	KindRef
)

var kindNames = [...]string{
	KindNull:    "null",
	KindBool:    "boolean",
	KindInteger: "integer",
	KindReal:    "real",
	KindString:  "string",
	KindName:    "name",
	KindArray:   "array",
	KindDict:    "dictionary",
	KindStream:  "stream",
	KindRef:     "reference",
}

// String names the kind, for error messages.
func (k Kind) String() string {
	if int(k) >= len(kindNames) {
		return fmt.Sprintf("Kind(%d)", uint8(k))
	}
	return kindNames[k]
}

// An Object is any value the PDF object syntax can express. The concrete types
// are [Null], [Bool], [Integer], [Real], [String], [Name], [Array], [Dict],
// [Ref] and *[Stream]; Kind tells them apart without a type switch.
type Object interface {
	Kind() Kind
}

// Null is the PDF null object. A missing dictionary entry resolves to Null
// rather than to a nil Object, so callers never have to nil-check.
type Null struct{}

// Kind implements [Object].
func (Null) Kind() Kind { return KindNull }

// Bool is a PDF boolean.
type Bool bool

// Kind implements [Object].
func (Bool) Kind() Kind { return KindBool }

// Integer is a PDF integer. Numbers too large for an int64 are parsed as
// [Real], which is what every other reader does.
type Integer int64

// Kind implements [Object].
func (Integer) Kind() Kind { return KindInteger }

// Real is a PDF real number.
type Real float64

// Kind implements [Object].
func (Real) Kind() Kind { return KindReal }

// String is a PDF string, already unescaped: the bytes a literal or hex string
// denotes, with no interpretation of their text encoding.
type String []byte

// Kind implements [Object].
func (String) Kind() Kind { return KindString }

// Name is a PDF name with its #xx escapes resolved and the leading slash
// dropped, so /Type is Name("Type").
type Name string

// Kind implements [Object].
func (Name) Kind() Kind { return KindName }

// Array is a PDF array.
type Array []Object

// Kind implements [Object].
func (Array) Kind() Kind { return KindArray }

// Dict is a PDF dictionary.
type Dict map[Name]Object

// Kind implements [Object].
func (Dict) Kind() Kind { return KindDict }

// Get returns the entry, or [Null] when it is absent. The value may still be a
// [Ref]; use [Resolve] to follow it.
func (d Dict) Get(k Name) Object {
	if v, ok := d[k]; ok && v != nil {
		return v
	}
	return Null{}
}

// Ref is an indirect reference: the "12 0 R" that stands in for an object
// stored elsewhere in the file.
type Ref struct {
	Num int
	Gen int
}

// Kind implements [Object].
func (Ref) Kind() Kind { return KindRef }

// String renders the reference the way it appears in a file.
func (r Ref) String() string { return fmt.Sprintf("%d %d R", r.Num, r.Gen) }

// Stream is a PDF stream: a dictionary plus the raw, still-encoded bytes
// between the stream and endstream keywords. [Decode] applies the filter chain.
type Stream struct {
	Dict Dict
	Raw  []byte
}

// Kind implements [Object].
func (*Stream) Kind() Kind { return KindStream }

// A Resolver fetches the object an indirect reference names. A document
// supplies one; a nil Resolver makes every reference resolve to [Null], which
// is what a caller parsing an isolated fragment wants.
type Resolver func(Ref) (Object, error)

// maxRefDepth bounds a chain of references pointing at references. A file can
// make that chain a cycle, and a cycle must not hang the reader.
const maxRefDepth = 64

// Resolve follows indirect references until it reaches a direct object. A nil
// object, an absent entry and an unresolvable reference all yield [Null].
func Resolve(o Object, r Resolver) (Object, error) {
	if o == nil {
		return Null{}, nil
	}
	for depth := 0; ; depth++ {
		ref, ok := o.(Ref)
		if !ok {
			return o, nil
		}
		if r == nil {
			return Null{}, nil
		}
		if depth >= maxRefDepth {
			return nil, fmt.Errorf("reader: reference chain longer than %d at %s", maxRefDepth, ref)
		}
		v, err := r(ref)
		if err != nil {
			return nil, err
		}
		if v == nil {
			return Null{}, nil
		}
		o = v
	}
}

// ToBool reports the value of a boolean object.
func ToBool(o Object) (bool, bool) {
	v, ok := o.(Bool)
	return bool(v), ok
}

// ToInt reports the value of an integer object, and of a real whose value is a
// whole number — files do write "3.0" where an integer belongs.
func ToInt(o Object) (int64, bool) {
	switch v := o.(type) {
	case Integer:
		return int64(v), true
	case Real:
		if float64(int64(v)) == float64(v) {
			return int64(v), true
		}
	}
	return 0, false
}

// ToFloat reports the value of any numeric object.
func ToFloat(o Object) (float64, bool) {
	switch v := o.(type) {
	case Integer:
		return float64(v), true
	case Real:
		return float64(v), true
	}
	return 0, false
}

// ToString reports the bytes of a string object.
func ToString(o Object) ([]byte, bool) {
	v, ok := o.(String)
	return []byte(v), ok
}

// ToName reports the value of a name object.
func ToName(o Object) (Name, bool) {
	v, ok := o.(Name)
	return v, ok
}

// ToArray reports the elements of an array object.
func ToArray(o Object) (Array, bool) {
	v, ok := o.(Array)
	return v, ok
}

// ToDict reports the dictionary of a dictionary object, or a stream's own
// dictionary — a stream is a dictionary everywhere a dictionary is expected.
func ToDict(o Object) (Dict, bool) {
	switch v := o.(type) {
	case Dict:
		return v, true
	case *Stream:
		return v.Dict, true
	}
	return nil, false
}

// ToStream reports the stream object.
func ToStream(o Object) (*Stream, bool) {
	v, ok := o.(*Stream)
	return v, ok
}

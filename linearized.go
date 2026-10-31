// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"errors"
	"fmt"
)

// Reading the linearization of a file: ISO 32000-1:2008 Annex F, "Linearized
// PDF" — Adobe calls it Fast Web View.
//
// A linearized file is ordinary PDF rearranged so that a reader with a
// byte-range-capable transport can show the first page before the rest has
// arrived. The rearrangement is described by a parameter dictionary at the
// very front of the file (F.3.3, Table F.1) and by hint tables (F.4) that say
// where each page's objects are.
//
// ⛔ THIS READS AND CHECKS; IT DOES NOT LINEARIZE. The checker comes first
// because a writer cannot be trusted without one: "qpdf says it is linearized"
// is a judgement from outside, and what a writer needs during development is
// the same judgement from inside, on the bytes it has just produced.
//
// ⛔ AND IT IS DELIBERATELY NOT A "yes/no". Table F.1 says of the file length
// that "a mismatch indicates that the file is not linearized and shall be
// treated as ordinary PDF, ignoring linearization information". A file can
// carry a perfectly well-formed parameter dictionary and still not be
// linearized, because something was appended after it was written — which is
// the normal fate of these files, since F.3.6 says any incremental update
// invalidates the hint tables. So the result says WHICH claim failed.

// A Linearization is the parameter dictionary at the front of a linearized
// file (ISO 32000-1 Table F.1). Offsets and lengths are in bytes from the
// beginning of the file.
type Linearization struct {
	// Version is /Linearized, "a version identification for the linearized
	// format". Files in the wild carry 1 and 1.0; nothing is known to depend
	// on it.
	Version float64

	// FileLength is /L, which "shall be exactly equal to the actual length of
	// the PDF file".
	FileLength int

	// HintOffset and HintLength are the first pair of /H: where the primary
	// hint stream starts — "the beginning of the stream object, not the
	// beginning of the stream data" — and how long it is including object
	// overhead.
	HintOffset, HintLength int

	// OverflowHintOffset and OverflowHintLength are the optional second pair
	// of /H, for the overflow hint stream (part 10). HasOverflowHint says
	// whether they were given, because zero is a legitimate-looking value for
	// an offset that was never written.
	OverflowHintOffset, OverflowHintLength int
	HasOverflowHint                        bool

	// FirstPageObject is /O, "the object number of the first page's page
	// object".
	FirstPageObject int

	// FirstPageEnd is /E, the offset of the end of the first page.
	FirstPageEnd int

	// PageCount is /N, the number of pages in the document.
	PageCount int

	// MainXrefOffset is /T. For a file with a traditional cross-reference
	// table this is "the offset of the white-space character preceding the
	// first entry of the main cross-reference table (the entry for object
	// number 0)"; for a file using cross-reference streams exclusively (PDF
	// 1.5) it is "the offset of the main cross-reference stream object".
	MainXrefOffset int

	// FirstPageNumber is /P, optional, default 0.
	FirstPageNumber int
}

// Errors from [Linearized]. They are distinguishable because the difference
// matters to a caller: a file with no parameter dictionary was never
// linearized, while one whose dictionary disagrees with the file was
// linearized and then changed.
var (
	// ErrNotLinearized says the file carries no linearization parameter
	// dictionary where Annex F requires one.
	ErrNotLinearized = errors.New("reader: the file has no linearization parameter dictionary")

	// ErrLinearizationStale says the dictionary is there and well formed but
	// disagrees with the file around it — which Table F.1 says shall be
	// treated as ordinary PDF. Appending an incremental update does this, and
	// F.3.6 says any such change invalidates the hint tables.
	ErrLinearizationStale = errors.New("reader: the linearization parameters do not describe this file")

	// ErrLinearizationMalformed says the dictionary is present but breaks a
	// rule of Table F.1 — a missing required entry, a wrong type, or a value
	// that is an indirect reference where "all values in this dictionary
	// shall be direct objects".
	ErrLinearizationMalformed = errors.New("reader: the linearization parameter dictionary is malformed")
)

// linearizationWindow is the first 1024 bytes, which F.3.3 says the parameter
// dictionary "shall be entirely contained within" — the limit exists so that a
// reader need not fetch more than that to decide whether a file is linearized,
// which is the whole point of the format.
const linearizationWindow = 1024

// Linearized reads the linearization parameter dictionary of b and checks it
// against the file.
//
// It returns the parameters on success. On failure the error wraps one of
// [ErrNotLinearized], [ErrLinearizationMalformed] or [ErrLinearizationStale],
// and names the claim that failed.
//
// ⛔ It does NOT validate the hint tables. Their format is F.4 and checking
// them is a separate, much larger job; a file can pass this and still have
// hint tables that send a reader to the wrong offset. What this establishes is
// that the front of the file says what Annex F requires and that what it says
// matches the file — which is the part a reader consults first, and the part
// that goes wrong when a file is edited after being linearized.
func Linearized(b []byte) (*Linearization, error) {
	dict, end, err := firstBodyDict(b)
	if err != nil {
		return nil, err
	}

	lin := &Linearization{}
	// ⛔ Every value is required to be DIRECT (F.3.3), so these are read
	// without a resolver on purpose: a /L that is an indirect reference would
	// have to be chased through the cross-reference table, which is exactly
	// the work the first-1024-bytes rule exists to avoid.
	// /Linearized is known present — firstBodyDict refuses the file without it,
	// because its absence means "not linearized" while a wrong TYPE means
	// "malformed", and those are different answers. So the value is passed in
	// rather than looked up again: a second presence check here would be a
	// branch no test could reach.
	num, err := linearizedVersion(dict["Linearized"])
	if err != nil {
		return nil, err
	}
	lin.Version = num

	for _, f := range []struct {
		key Name
		to  *int
	}{
		{"L", &lin.FileLength},
		{"O", &lin.FirstPageObject},
		{"E", &lin.FirstPageEnd},
		{"N", &lin.PageCount},
		{"T", &lin.MainXrefOffset},
	} {
		v, err := directInteger(dict, f.key)
		if err != nil {
			return nil, err
		}
		*f.to = v
	}
	if v, ok := dict["P"]; ok {
		n, ok := v.(Integer)
		if !ok {
			return nil, fmt.Errorf("%w: /P is %T, want an integer", ErrLinearizationMalformed, v)
		}
		lin.FirstPageNumber = int(n)
	}
	if err := readHintArray(dict, lin); err != nil {
		return nil, err
	}

	// F.3.3: the whole dictionary within the first 1024 bytes. The rule is
	// about what a reader must FETCH, so it is checked against the byte offset
	// the parse ended at rather than against anything in the parse.
	if end > linearizationWindow {
		return nil, fmt.Errorf("%w: the dictionary ends at byte %d, past the %d-byte window Annex F allows",
			ErrLinearizationMalformed, end, linearizationWindow)
	}

	// Table F.1: /L "shall be exactly equal to the actual length of the PDF
	// file. A mismatch indicates that the file is not linearized."
	if lin.FileLength != len(b) {
		return nil, fmt.Errorf("%w: /L says %d bytes and the file is %d",
			ErrLinearizationStale, lin.FileLength, len(b))
	}
	return lin, nil
}

// firstBodyDict parses the first indirect object of the body and returns its
// dictionary with the offset one past its end.
//
// F.3.3: "Following the header, the first object in the body of the file
// (part 2) shall be an indirect dictionary object, the linearization parameter
// dictionary". So this does not search for the dictionary — it reads whatever
// is first and reports that the file is not linearized if that is not it.
// Searching would find a linearization dictionary left behind by an earlier
// write and report a file as linearized when the thing a reader looks at first
// is something else entirely.
func firstBodyDict(b []byte) (Dict, int, error) {
	if len(b) < 5 || string(b[:5]) != "%PDF-" {
		return nil, 0, fmt.Errorf("%w: it does not begin with %%PDF-", ErrNotLinearized)
	}
	// Skip the header line and the binary comment line F.3.2 recommends,
	// along with any other comments before the body.
	i := 0
	for i < len(b) && i < linearizationWindow {
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i >= len(b) || b[i] != '%' {
			break
		}
		for i < len(b) && b[i] != '\n' && b[i] != '\r' {
			i++
		}
	}
	// ⛔ No resolver. Every value here is required to be direct (F.3.3), and a
	// reader deciding whether a file is linearized has read only these first
	// bytes — it has no cross-reference table yet to resolve anything with.
	_, obj, n, err := ParseIndirectObject(b[i:], nil)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: the first object does not parse: %v", ErrNotLinearized, err)
	}
	d, ok := obj.(Dict)
	if !ok {
		return nil, 0, fmt.Errorf("%w: the first object is %T, not a dictionary", ErrNotLinearized, obj)
	}
	if _, ok := d["Linearized"]; !ok {
		return nil, 0, fmt.Errorf("%w: the first object has no /Linearized entry", ErrNotLinearized)
	}
	return d, i + n, nil
}

// linearizedVersion reads /Linearized, which Table F.1 calls "a number".
//
// ⛔ BOTH SPELLINGS ARE REAL. The spec's own Example 2 writes
// `/Linearized 1.0` and qpdf writes `/Linearized 1`, so this accepts an
// integer and a real alike. My first tests used only the integer and left the
// real branch uncovered, which is how I noticed that the example and the
// implementation everybody uses disagree about it.
func linearizedVersion(v Object) (float64, error) {
	switch n := v.(type) {
	case Integer:
		return float64(n), nil
	case Real:
		return float64(n), nil
	}
	return 0, fmt.Errorf("%w: /Linearized is %T, want a direct number", ErrLinearizationMalformed, v)
}

// directInteger reads a required entry that must be a direct integer.
func directInteger(d Dict, key Name) (int, error) {
	v, ok := d[key]
	if !ok {
		return 0, fmt.Errorf("%w: /%s is required", ErrLinearizationMalformed, key)
	}
	n, ok := v.(Integer)
	if !ok {
		return 0, fmt.Errorf("%w: /%s is %T, want a direct integer", ErrLinearizationMalformed, key, v)
	}
	return int(n), nil
}

// readHintArray reads /H, "an array of two or four integers".
func readHintArray(d Dict, lin *Linearization) error {
	v, ok := d["H"]
	if !ok {
		return fmt.Errorf("%w: /H is required", ErrLinearizationMalformed)
	}
	a, ok := v.(Array)
	if !ok {
		return fmt.Errorf("%w: /H is %T, want an array", ErrLinearizationMalformed, v)
	}
	if len(a) != 2 && len(a) != 4 {
		return fmt.Errorf("%w: /H has %d entries, want two or four", ErrLinearizationMalformed, len(a))
	}
	n := make([]int, len(a))
	for i, e := range a {
		v, ok := e.(Integer)
		if !ok {
			return fmt.Errorf("%w: /H[%d] is %T, want a direct integer", ErrLinearizationMalformed, i, e)
		}
		n[i] = int(v)
	}
	lin.HintOffset, lin.HintLength = n[0], n[1]
	if len(n) == 4 {
		lin.OverflowHintOffset, lin.OverflowHintLength = n[2], n[3]
		lin.HasOverflowHint = true
	}
	return nil
}

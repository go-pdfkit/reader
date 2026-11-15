// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"errors"
	"fmt"
)

// ErrLinearizationTooWide reports a parameter dictionary that will not fit the
// span reserved for it.
var ErrLinearizationTooWide = errors.New("reader: the linearization parameters do not fit their reserved span")

// paramSpan is the bytes part 2 occupies in a linearized file: the
// linearization parameter dictionary's indirect object followed by enough
// spaces to reach this total, whatever width the numbers turned out to need.
//
// ⛔⛔⛔ THIS IS THE FIXPOINT MECHANISM, AND IT IS NOT THE ONE #40 PRESCRIBED.
// That issue says to use "placeholders of FIXED WIDTH in the first [pass], or
// the second pass moves what the first pass measured". Both producers do
// something simpler, and measuring them is how it was found. Neither pads
// INSIDE the dictionary:
//
//	qpdf  << /Linearized 1 /L 1367 /H [ 550 127 ] /O 7 /E 902 /N 2 /T 1169 >>
//	gs    <</Linearized 1/L 4107/H[ 829 138]/O 9/E 829/N 2/T 3926>>
//
// Every integer is at its natural width. What they pad is AFTER `endobj`, with
// a run of spaces before the first-page cross-reference section, and the
// arithmetic says why:
//
//	testdata/linearized.pdf          span 201 = object 82 + padding 119
//	testdata/linearized-shared.pdf   span 201 = object 83 + padding 118
//	testdata/linearized-gs.pdf       span 131 = object 72 + padding  59
//
// ⭐ qpdf's span is the SAME 201 on both files while its object differs by a
// byte — /O 10 and /N 3 against /O 7 and /N 2 — and the padding absorbs the
// difference exactly. So the technique is a RESERVED SPAN: write the numbers at
// whatever width they need, pad to a constant, and everything after part 2 sits
// at an offset the second pass cannot move. That is strictly simpler than
// fixed-width placeholders, which would need every value padded and would make
// the dictionary's own text depend on the pass.
//
// ⚠ 256 IS THIS PACKAGE'S CHOICE, not either producer's, and the margin is
// MEASURED rather than estimated: the widest object this function can produce —
// seven int64 values at their maximum and a six-digit object number — is
// **202 bytes**, asserted by TestTheParameterObjectIsAlwaysTheReservedWidth.
//
// ⭐ So qpdf's 201 misses the worst case by exactly ONE byte. That is the whole
// reason not to copy it, and it is a measurement rather than the "would not fit
// a file at the top of the range" hand-wave that stood here first.
//
// ⚠ AND IT ALREADY ACCOMMODATES THE FORM THIS FUNCTION CANNOT YET WRITE. Table
// F.1's /H is an array of two integers, or FOUR when the file carries an
// overflow hint stream — which [Linearization] reads (HasOverflowHint) and
// linParams has no field for. Two more int64 values at 19 digits plus a space
// each is 40 bytes, so the widest four-value dictionary is about 242 and still
// inside the span. Supporting it later will not need a wider reservation; it is
// recorded here so that nobody re-derives the number when it does.
//
// ⛔ IT IS A var SO THAT A TEST CAN SHRINK IT, the same seam intMax and
// maxDecodedSize already use in this package. Without that the refusal below
// is unreachable — 202 of 256 means no int64 value and no int object number can
// overflow it — and an unreachable refusal is an untested claim, which this
// package's exact coverage gate rightly declines to carry.
var paramSpan = 256

// linParams are Table F.1's values, as a writer knows them once the layout is
// measured.
type linParams struct {
	L      int64 // the file's length in bytes
	H0, H1 int64 // the primary hint stream's offset and length
	O      int64 // the first page's page object number
	E      int64 // the offset of the end of the first page
	N      int64 // the page count
	T      int64 // the offset of the main cross-reference table's first entry
}

// object renders part 2: the parameter dictionary as object num, padded with
// spaces to exactly paramSpan bytes.
//
// ⛔ IT REFUSES RATHER THAN OVERFLOWING THE SPAN, because a dictionary one byte
// too long does not produce a broken dictionary — it produces a file in which
// every offset after part 2 is one byte further along than /E, /T and the
// first-page cross-reference section say. The file opens, and a reader
// following those offsets lands inside the wrong object. That is the failure
// this whole arc exists to prevent, and it is worth a refusal rather than a
// best effort.
func (p linParams) object(num int) ([]byte, error) {
	body := fmt.Sprintf("%d 0 obj\n<< /Linearized 1 /L %d /H [ %d %d ] /O %d /E %d /N %d /T %d >>\nendobj\n",
		num, p.L, p.H0, p.H1, p.O, p.E, p.N, p.T)
	if len(body) > paramSpan {
		return nil, fmt.Errorf("%w: it needs %d bytes and %d are reserved",
			ErrLinearizationTooWide, len(body), paramSpan)
	}
	out := make([]byte, paramSpan)
	copy(out, body)
	// ⛔ SPACES AND NOT NUL, which is what both producers write and what a
	// parser skipping whitespace between objects expects. A NUL is not
	// whitespace in ISO 32000-1's lexical rules.
	for i := len(body); i < paramSpan; i++ {
		out[i] = ' '
	}
	return out, nil
}

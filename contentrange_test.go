// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"strings"
	"testing"
)

// ⛔ The reason Operation carries a byte range: a tool that wants to put
// something before every BT cannot find one by searching for the bytes. "BT"
// appears inside strings, inside names, and inside an inline image's data,
// and none of those is an operator. Only the scan knows which is which.
func TestTheRangeIsOfTheOperationAndNotOfTheBytes(t *testing.T) {
	// A "BT" inside a string, and then a real one.
	const stream = "(BT in a string) Tj\nBT /F1 12 Tf ET\n"
	var starts []int
	var ops []string
	s := NewContentScanner([]byte(stream))
	for {
		op, ok := s.Next()
		if !ok {
			break
		}
		ops = append(ops, op.Operator)
		starts = append(starts, op.Start)
		// Every range must be inside the stream and non-empty.
		if op.Start < 0 || op.End > len(stream) || op.End <= op.Start {
			t.Errorf("%s: range [%d,%d) of a %d-byte stream",
				op.Operator, op.Start, op.End, len(stream))
		}
		// ⛔ And the operator's own text must be the END of its range, which
		// is what makes splicing possible: insert at Start to go before the
		// operation, at End to go after it.
		if op.Operator != "BI" {
			got := stream[op.End-len(op.Operator) : op.End]
			if got != op.Operator {
				t.Errorf("%s ends at %d, where the stream says %q",
					op.Operator, op.End, got)
			}
		}
	}
	if strings.Join(ops, ",") != "Tj,BT,Tf,ET" {
		t.Fatalf("operators = %v", ops)
	}
	// ⛔ THE DISCRIMINATOR. A byte search for "BT" finds index 1 — inside the
	// string the Tj shows — while the scan reports the real one at index 20.
	// A tool inserting "before the first BT" by searching would splice into
	// the middle of somebody's text and corrupt the string.
	byteSearch := strings.Index(stream, "BT")
	scanned := starts[1] // the BT operation
	if byteSearch != 1 {
		t.Fatalf("the fixture's first \"BT\" byte is at %d, not inside the "+
			"string, so this proves nothing", byteSearch)
	}
	if scanned == byteSearch {
		t.Error("the scan agrees with the byte search, so the range adds nothing")
	}
	if got := stream[scanned : scanned+2]; got != "BT" {
		t.Errorf("the scanned BT is at %d, where the stream says %q", scanned, got)
	}
}

// An operation with operands starts at the FIRST operand, so that splicing
// before it does not land between the operands and the operator.
func TestAnOperationStartsAtItsFirstOperand(t *testing.T) {
	const stream = "  1 0 0 RG  10 20 30 40 re  S\n"
	s := NewContentScanner([]byte(stream))
	op, ok := s.Next()
	if !ok {
		t.Fatal("no operation")
	}
	if op.Operator != "RG" {
		t.Fatalf("first operator = %q", op.Operator)
	}
	// "1" is at index 2, after the two leading spaces.
	if op.Start != 2 {
		t.Errorf("Start = %d, want 2 (the first operand, not the operator)", op.Start)
	}
	if got := stream[op.Start:op.End]; got != "1 0 0 RG" {
		t.Errorf("the range holds %q", got)
	}
}

// An operator with no operands starts at itself.
func TestAnOperatorWithNoOperandsStartsAtItself(t *testing.T) {
	const stream = "q BT ET Q"
	s := NewContentScanner([]byte(stream))
	for want := range map[string]bool{"q": true} {
		_ = want
	}
	var got []string
	for {
		op, ok := s.Next()
		if !ok {
			break
		}
		got = append(got, stream[op.Start:op.End])
	}
	if strings.Join(got, "|") != "q|BT|ET|Q" {
		t.Errorf("the ranges hold %v, want each operator alone", got)
	}
}

// ⛔ An inline image's range runs to its EI, three keywords after the operator
// the scan reports. Measuring the end from the operator's own length would put
// it in the middle of the image's data — and splicing there would corrupt the
// image rather than the stream around it.
func TestAnInlineImageRangeCoversAllThreeKeywords(t *testing.T) {
	const stream = "q BI /W 1 /H 1 /CS /G /BPC 8 /F /AHx ID 00> EI Q"
	s := NewContentScanner([]byte(stream))
	var image Operation
	found := false
	for {
		op, ok := s.Next()
		if !ok {
			break
		}
		if op.Operator == "BI" {
			image, found = op, true
		}
	}
	if !found {
		t.Fatal("no inline image was scanned")
	}
	held := stream[image.Start:image.End]
	if !strings.HasPrefix(held, "BI") {
		t.Errorf("the range begins %q", held[:min(4, len(held))])
	}
	if !strings.Contains(held, "ID") || !strings.Contains(held, "EI") {
		t.Errorf("the range holds %q, want all three keywords", held)
	}
	// And the Q after it is outside the range, or the splice would swallow it.
	if strings.Contains(held, "Q") {
		t.Errorf("the range reaches past the image into %q", held)
	}
}

// The ranges of a whole stream must not overlap and must stay in order, which
// is what lets a caller splice at several of them in one pass.
func TestTheRangesAreOrderedAndDisjoint(t *testing.T) {
	const stream = "q 1 0 0 RG BT /F1 12 Tf (hello) Tj ET Q 0 g 1 1 2 2 re f"
	s := NewContentScanner([]byte(stream))
	prevEnd := 0
	n := 0
	for {
		op, ok := s.Next()
		if !ok {
			break
		}
		n++
		if op.Start < prevEnd {
			t.Errorf("%s starts at %d, before the previous end %d",
				op.Operator, op.Start, prevEnd)
		}
		prevEnd = op.End
	}
	if n < 8 {
		t.Fatalf("only %d operations, so this proves little", n)
	}
	if prevEnd > len(stream) {
		t.Errorf("the last end is %d past the stream", prevEnd-len(stream))
	}
}

// Rubbish in the stream must not make a range that points outside it: the scan
// steps over what it cannot read and the operations around it keep honest
// ranges.
func TestRubbishDoesNotBreakTheRanges(t *testing.T) {
	const stream = "q ) ) ) 1 0 0 RG Q"
	s := NewContentScanner([]byte(stream))
	for {
		op, ok := s.Next()
		if !ok {
			break
		}
		if op.Start < 0 || op.End > len(stream) || op.End <= op.Start {
			t.Errorf("%s: range [%d,%d) of a %d-byte stream",
				op.Operator, op.Start, op.End, len(stream))
		}
	}
	if s.Err() == nil {
		t.Error("the scan reported no error over a stream with rubbish in it")
	}
}

// Operations, which is the whole-stream form, carries the ranges too — a
// caller that used it rather than the scanner would otherwise have to rescan.
func TestOperationsCarriesTheRanges(t *testing.T) {
	const stream = "1 0 0 RG S"
	ops, err := Operations([]byte(stream))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("%d operations", len(ops))
	}
	if got := stream[ops[0].Start:ops[0].End]; got != "1 0 0 RG" {
		t.Errorf("the first range holds %q", got)
	}
	if got := stream[ops[1].Start:ops[1].End]; got != "S" {
		t.Errorf("the second range holds %q", got)
	}
}

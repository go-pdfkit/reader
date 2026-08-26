package reader_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/go-pdfkit/reader"
)

// attempts is how many times a check that depends on map order is repeated.
// Go randomises the order on every walk, so one run of a two-key collision
// picks the wrong entry about half the time; two hundred runs make a defect
// that survives certain to show.
const attempts = 200

// TestInlineImageExpandedIsDeterministic pins down which entry wins when an
// inline image dictionary carries both spellings of the same one.
//
// issue14256.pdf in mozilla's pdf.js corpus holds images written like this —
// /BPC 8 beside /BitsPerComponent 4 — and the answer used to be whichever one
// Go's randomised map iteration reached last. The same file read twice by the
// same program gave two different pictures, and nothing reported anything.
func TestInlineImageExpandedIsDeterministic(t *testing.T) {
	cases := []struct {
		name  string
		dict  reader.Dict
		key   reader.Name
		want  string
		other string
	}{
		{
			name: "BPC beats BitsPerComponent",
			dict: reader.Dict{"BPC": reader.Integer(8), "BitsPerComponent": reader.Integer(4)},
			key:  "BitsPerComponent", want: "8", other: "4",
		},
		{
			name: "W beats Width",
			dict: reader.Dict{"W": reader.Integer(20), "Width": reader.Integer(10)},
			key:  "Width", want: "20", other: "10",
		},
		{
			name: "H beats Height",
			dict: reader.Dict{"H": reader.Integer(10), "Height": reader.Integer(40)},
			key:  "Height", want: "10", other: "40",
		},
		{
			name: "F beats Filter",
			dict: reader.Dict{"F": reader.Name("AHx"), "Filter": reader.Name("A85")},
			key:  "Filter", want: "/AHx", other: "/A85",
		},
		{
			name: "CS beats ColorSpace, and is written out",
			dict: reader.Dict{"CS": reader.Name("RGB"), "ColorSpace": reader.Name("3chanRGB")},
			key:  "ColorSpace", want: "/DeviceRGB", other: "/3chanRGB",
		},
		{
			name: "D beats Decode",
			dict: reader.Dict{"D": reader.Array{reader.Integer(0), reader.Integer(1)},
				"Decode": reader.Array{reader.Integer(1), reader.Integer(0)}},
			key: "Decode", want: "[0 1]", other: "[1 0]",
		},
		{
			name: "I beats Interpolate",
			dict: reader.Dict{"I": reader.Bool(false), "Interpolate": reader.Bool(true)},
			key:  "Interpolate", want: "false", other: "true",
		},
		{
			name: "L beats Length",
			dict: reader.Dict{"L": reader.Integer(1240), "Length": reader.Integer(99)},
			key:  "Length", want: "1240", other: "99",
		},
		{
			name: "IM beats ImageMask",
			dict: reader.Dict{"IM": reader.Bool(true), "ImageMask": reader.Bool(false)},
			key:  "ImageMask", want: "true", other: "false",
		},
		{
			name: "DP beats DecodeParms",
			dict: reader.Dict{"DP": reader.Dict{"K": reader.Integer(1)},
				"DecodeParms": reader.Dict{"K": reader.Integer(2)}},
			key: "DecodeParms", want: "<</K 1>>", other: "<</K 2>>",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			im := &reader.InlineImage{Dict: c.dict}
			for i := 0; i < attempts; i++ {
				got := string(reader.FormatObject(im.Expanded().Get(c.key)))
				if got == c.other {
					t.Fatalf("on attempt %d /%s came out as %s: the long spelling won, "+
						"so which entry is used depends on map order", i, c.key, got)
				}
				if got != c.want {
					t.Fatalf("on attempt %d /%s came out as %s, want %s", i, c.key, got, c.want)
				}
			}
		})
	}
}

// TestInlineImageExpandedKeepsWhatItDoesNotKnow checks the two passes did not
// drop the entries neither spelling covers.
func TestInlineImageExpandedKeepsWhatItDoesNotKnow(t *testing.T) {
	im := &reader.InlineImage{Dict: reader.Dict{
		"W": reader.Integer(3), "SMask": reader.Ref{Num: 7}, "Custom": reader.Name("x"),
	}}
	for i := 0; i < attempts; i++ {
		e := im.Expanded()
		if got := string(reader.FormatObject(e.Get("Width"))); got != "3" {
			t.Fatalf("Width is %s", got)
		}
		if got := string(reader.FormatObject(e.Get("SMask"))); got != "7 0 R" {
			t.Fatalf("SMask is %s", got)
		}
		if got := string(reader.FormatObject(e.Get("Custom"))); got != "/x" {
			t.Fatalf("Custom is %s", got)
		}
		if len(e) != 3 {
			t.Fatalf("expanded dictionary has %d entries, want 3", len(e))
		}
	}
}

// TestOperationsIsAFunctionOfItsBytes is the defect where it bites: the
// expanded dictionary says how long an inline image's data is, so an entry
// chosen at random moves the end of the image — and every operation after it
// belongs to a different stream. The count used to swing between 58 and 118
// on the same bytes.
func TestOperationsIsAFunctionOfItsBytes(t *testing.T) {
	var content bytes.Buffer
	content.WriteString("q 1 0 0 1 0 0 cm\n")
	// Twenty samples of two-by-two RGB at eight bits: 12 bytes, written as
	// hex, is what /BPC 8 gives. /BitsPerComponent 4 would say six.
	// Both spellings of the filter, naming different filters. Which one the
	// expanded dictionary carried decided whether the candidate stretch of
	// bytes decoded at all, and so where the image ended.
	content.WriteString("BI /W 2 /H 2 /CS /RGB /BPC 8 /F [/AHx] /Filter [/A85] ID\n")
	content.WriteString("00112233445566778899aabb>\nEI\n")
	content.WriteString("Q\n")
	for i := 0; i < 6; i++ {
		content.WriteString(fmt.Sprintf("%d %d m %d %d l S\n", i, i, i+1, i+1))
	}
	data := content.Bytes()

	first, err := reader.Operations(append([]byte(nil), data...))
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	for i := 0; i < attempts; i++ {
		ops, err := reader.Operations(append([]byte(nil), data...))
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if len(ops) != len(first) {
			t.Fatalf("attempt %d found %d operations where the first pass found %d: "+
				"the same bytes tokenised two different ways", i, len(ops), len(first))
		}
		for j := range ops {
			if ops[j].Operator != first[j].Operator {
				t.Fatalf("attempt %d operation %d is %q where the first pass had %q",
					i, j, ops[j].Operator, first[j].Operator)
			}
		}
	}
}

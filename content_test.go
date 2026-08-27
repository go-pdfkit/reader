package reader

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
)

// opNames lists the operators of a tokenised stream, for compact assertions.
func opNames(ops []Operation) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = o.Operator
	}
	return out
}

func TestOperations(t *testing.T) {
	src := "q 1 0 0 1 10 20 cm BT /F1 12 Tf (hi) Tj ET Q"
	ops, err := Operations([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"q", "cm", "BT", "Tf", "Tj", "ET", "Q"}
	if got := opNames(ops); !reflect.DeepEqual(got, want) {
		t.Fatalf("operators = %v, want %v", got, want)
	}
	if n := len(ops[1].Operands); n != 6 {
		t.Errorf("cm has %d operands", n)
	}
	if s, _ := ToString(ops[4].Operands[0]); string(s) != "hi" {
		t.Errorf("Tj operand = %v", ops[4].Operands[0])
	}
}

func TestOperationsKeywordOperands(t *testing.T) {
	ops, err := Operations([]byte("true false null gs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Operator != "gs" || len(ops[0].Operands) != 3 {
		t.Fatalf("got %+v", ops)
	}
	if b, _ := ToBool(ops[0].Operands[0]); !b {
		t.Error("true was not an operand")
	}
	if ops[0].Operands[2].Kind() != KindNull {
		t.Error("null was not an operand")
	}
}

func TestOperationsNumbersRunTogether(t *testing.T) {
	// Producers do write two numbers with no space between them.
	ops, err := Operations([]byte("3.4-5 m"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || len(ops[0].Operands) != 2 {
		t.Fatalf("got %+v", ops)
	}
	if v, _ := ToFloat(ops[0].Operands[0]); v != 3.4 {
		t.Errorf("first operand = %v", ops[0].Operands[0])
	}
	if v, _ := ToFloat(ops[0].Operands[1]); v != -5 {
		t.Errorf("second operand = %v", ops[0].Operands[1])
	}
}

func TestOperationsStepsOverRubbish(t *testing.T) {
	// A stray delimiter must not cost the operations around it.
	ops, err := Operations([]byte("1 0 m ) 2 3 l"))
	if err == nil {
		t.Error("the error was not reported")
	}
	if got := opNames(ops); !reflect.DeepEqual(got, []string{"m", "l"}) {
		t.Errorf("operators = %v", got)
	}
}

func TestOperationsUnparsableOperand(t *testing.T) {
	ops, err := Operations([]byte("[1 2 m"))
	if err == nil {
		t.Error("the error was not reported")
	}
	if len(ops) != 0 {
		t.Errorf("got %+v", ops)
	}
}

func TestOperationsErrorAtTheVeryEnd(t *testing.T) {
	if _, err := Operations([]byte("1 0 m (")); err == nil {
		t.Error("want an error")
	}
}

func TestScannerErrKeepsTheFirst(t *testing.T) {
	s := NewContentScanner([]byte(") ) m"))
	for {
		if _, ok := s.Next(); !ok {
			break
		}
	}
	if s.Err() == nil {
		t.Fatal("no error reported")
	}
	if e, ok := s.Err().(*SyntaxError); !ok || e.Offset != 0 {
		t.Errorf("Err() = %v, want the first one", s.Err())
	}
}

func TestInlineImage(t *testing.T) {
	data := []byte("BI /W 2 /H 2 /BPC 8 /CS /G ID \x01\x02\x03\x04 EI Q")
	ops, err := Operations(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := opNames(ops); !reflect.DeepEqual(got, []string{"BI", "Q"}) {
		t.Fatalf("operators = %v", got)
	}
	img := ops[0].Image
	if img == nil {
		t.Fatal("no image attached")
	}
	if !bytes.Equal(img.Raw, []byte{1, 2, 3, 4}) {
		t.Errorf("data = % x", img.Raw)
	}
	exp := img.Expanded()
	if v, _ := ToInt(exp.Get("Width")); v != 2 {
		t.Errorf("Width = %v", exp.Get("Width"))
	}
	if n, _ := ToName(exp.Get("ColorSpace")); n != "DeviceGray" {
		t.Errorf("ColorSpace = %v", exp.Get("ColorSpace"))
	}
}

func TestInlineImageExpandedLeavesUnknownKeys(t *testing.T) {
	im := &InlineImage{Dict: Dict{"Odd": Integer(1), "CS": Integer(2)}}
	exp := im.Expanded()
	if _, ok := exp["Odd"]; !ok {
		t.Error("an unabbreviated key was dropped")
	}
	if v, _ := ToInt(exp.Get("ColorSpace")); v != 2 {
		t.Errorf("a colour space that is not a name was changed: %v", exp.Get("ColorSpace"))
	}
}

func TestInlineImageMalformed(t *testing.T) {
	cases := []struct{ name, src string }{
		{"no ID", "BI /W 2 /H 2"},
		{"a key that is not a name", "BI 42 2 ID x EI"},
		{"a value that does not parse", "BI /W ] ID x EI"},
		{"a lexer error in the dictionary", "BI /W#2 1 ID x EI"},
		{"no EI", "BI /W 2 /H 2 /BPC 8 /CS /G ID 12345678"},
	}
	for _, c := range cases {
		if _, err := Operations([]byte(c.src)); err == nil {
			t.Errorf("%s: want an error", c.name)
		}
	}
}

func TestInlineImageAtTheVeryEnd(t *testing.T) {
	// EI as the last two bytes of the stream, with nothing after it.
	ops, err := Operations([]byte("BI /W 1 /H 1 /BPC 8 /CS /G ID \x00 EI"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Image == nil || len(ops[0].Image.Raw) != 1 {
		t.Fatalf("got %+v", ops)
	}
}
func TestPageContent(t *testing.T) {
	d, err := Open(onePage())
	if err != nil {
		t.Fatal(err)
	}
	data, err := d.PageContent(1)
	if err != nil || string(data) != "BT ET" {
		t.Fatalf("content = %q, %v", data, err)
	}
	ops, err := d.PageOperations(1)
	if err != nil {
		t.Fatal(err)
	}
	if got := opNames(ops); !reflect.DeepEqual(got, []string{"BT", "ET"}) {
		t.Errorf("operators = %v", got)
	}
	if _, err := d.PageContent(2); err == nil {
		t.Error("PageContent(2) should fail")
	}
	if _, err := d.PageOperations(2); err == nil {
		t.Error("PageOperations(2) should fail")
	}
}

func TestPageContentArrayOfStreams(t *testing.T) {
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 1 1] >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R /Contents [4 0 R 9 0 R 5 0 R] >>")
	b.streamObj(4, "", []byte("q"))
	b.streamObj(5, "", []byte("Q"))
	d, err := Open(b.table("/Root 1 0 R"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := d.PageContent(1)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "q\nQ" {
		t.Errorf("content = %q", data)
	}
}

func TestPageContentAbsentOrOdd(t *testing.T) {
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 /MediaBox [0 0 1 1] >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R >>")
	b.obj(4, "<< /Type /Page /Parent 2 0 R /Contents 42 >>")
	d, err := Open(b.table("/Root 1 0 R"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		data, err := d.PageContent(i)
		if err != nil || len(data) != 0 {
			t.Errorf("page %d: %q, %v", i, data, err)
		}
	}
}

func TestPageContentImageFilterIsRefused(t *testing.T) {
	b := newBuilder()
	b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 1 1] >>")
	b.obj(3, "<< /Type /Page /Parent 2 0 R /Contents 4 0 R >>")
	b.streamObj(4, "/Filter /DCTDecode", []byte("not really a jpeg"))
	d, err := Open(b.table("/Root 1 0 R"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.PageContent(1); err == nil {
		t.Error("want an error")
	}
}

func TestPageContentUndecodable(t *testing.T) {
	for _, contents := range []string{"4 0 R", "[4 0 R]"} {
		b := newBuilder()
		b.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
		b.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 1 1] >>")
		b.obj(3, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /Contents %s >>", contents))
		b.streamObj(4, "/Filter /FlateDecode", []byte("not deflate data"))
		d, err := Open(b.table("/Root 1 0 R"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.PageContent(1); err == nil {
			t.Errorf("%s: want an error", contents)
		}
	}
}

func TestContentOfPropagatesLookupFailures(t *testing.T) {
	d := brokenDoc()
	if _, err := d.contentOf(Dict{"Contents": Ref{5, 0}}); err == nil {
		t.Error("want an error")
	}
	d = brokenDoc()
	if _, err := d.contentOf(Dict{"Contents": Array{Ref{5, 0}}}); err == nil {
		t.Error("want an error for an element that cannot be read")
	}
}

func TestOperandFailingAtTheVeryEnd(t *testing.T) {
	// An operand that runs off the end of the stream leaves nothing to skip.
	ops, err := Operations([]byte("m ["))
	if err == nil {
		t.Error("want an error")
	}
	if got := opNames(ops); len(got) != 1 || got[0] != "m" {
		t.Errorf("operators = %v", got)
	}
}

func TestOperandThatIsNotAnObject(t *testing.T) {
	// A closing bracket where an operand belongs: the scan steps over it and
	// keeps the operands on either side.
	ops, err := Operations([]byte("1 ] 2 3 l"))
	if err == nil {
		t.Error("the error was not reported")
	}
	if len(ops) != 1 || ops[0].Operator != "l" || len(ops[0].Operands) != 3 {
		t.Fatalf("got %+v", ops)
	}
}

// TestInlineImageExpandedIsDeterministic pins the one property that matters
// more than which spelling wins: that the answer is the same every time.
//
// safedocs' Inline_Image_Abbreviations fixture carries seven images that each
// say the same thing twice and disagree — /W 20 beside /Width 10, /CS /RGB
// beside /ColorSpace /3chanRGB. Expanding in one pass over the map let Go's
// randomised iteration order pick the winner, so the same page drew a
// different picture on different runs of the same binary. Two hundred
// expansions of the same dictionary is enough to catch that: against the one
// pass this loop sees both answers within the first few.
func TestInlineImageExpandedIsDeterministic(t *testing.T) {
	im := &InlineImage{Dict: Dict{
		"W":          Integer(20),
		"Width":      Integer(10),
		"H":          Integer(10),
		"Height":     Integer(40),
		"BPC":        Integer(8),
		"CS":         Name("RGB"),
		"ColorSpace": Name("3chanRGB"),
		"I":          Bool(false),
	}}
	first := im.Expanded()
	for i := 0; i < 200; i++ {
		got := im.Expanded()
		if len(got) != len(first) {
			t.Fatalf("expansion %d has %d keys, the first had %d", i, len(got), len(first))
		}
		for k, v := range first {
			if got[k] != v {
				t.Fatalf("expansion %d gave %s = %v, the first gave %v", i, k, got[k], v)
			}
		}
	}
	// And the abbreviation is what wins, which is the choice this makes and
	// the reason it is written down in Expanded's own comment.
	for k, want := range map[Name]Object{
		"Width":       Integer(20),
		"Height":      Integer(10),
		"ColorSpace":  Name("DeviceRGB"),
		"Interpolate": Bool(false),
	} {
		if first[k] != want {
			t.Errorf("%s = %v, want %v", k, first[k], want)
		}
	}
}

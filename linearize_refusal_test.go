// Copyright (c) 2026, the go-pdfkit/reader authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package reader

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// timeAfterSeconds is time.After, named so the cycle test reads as prose.
func timeAfterSeconds(n int) <-chan time.Time {
	return time.After(time.Duration(n) * time.Second)
}

// ⛔⛔ EVERY WAY THE EMISSION REFUSES, each named for what is wrong. A writer
// that produced a file anyway would produce one whose offsets point at the
// wrong bytes — and such a file OPENS, which is the whole hazard this arc
// exists to avoid.
func TestEveryWayLinearizeRefuses(t *testing.T) {
	t.Run("a trailer whose /Root is a direct dictionary", func(t *testing.T) {
		// ⛔ Part 3's trailer has to name the catalogue by object number, and a
		// direct dictionary has none. Partition refuses this too, for its own
		// reason, so the document here reaches the emission by having a
		// catalogue object AND a direct /Root.
		d := bareDoc(Dict{"Root": Dict{"Type": Name("Catalog")}}, nil, nil)
		if _, err := d.Linearize(); err == nil {
			t.Fatal("a direct /Root was accepted")
		}
	})

	t.Run("a first-page trailer too wide for its span", func(t *testing.T) {
		// ⛔ THE SPAN IS SHRUNK, the seam paramSpan and maxDecodedSize already
		// use here: at 192 bytes the refusal is unreachable, because the
		// trailer's three numbers cannot together need that much.
		defer func(n int) { frontTrailerSpan = n }(frontTrailerSpan)
		frontTrailerSpan = 8

		plain := buildPages(t, 1)
		d, err := Open(plain)
		if err != nil {
			t.Fatal(err)
		}
		_, err = d.Linearize()
		if !errors.Is(err, ErrCannotLinearize) {
			t.Fatalf("a trailer of 8 bytes' span was accepted: %v", err)
		}
		if !strings.Contains(err.Error(), "are reserved") {
			t.Errorf("it was refused without saying what the span is: %v", err)
		}
	})
}

// ⛔⛔ THE INHERITED ATTRIBUTES, EVERY WAY THE WALK ENDS. pushInherited walks
// /Parent for the nearest definition, and each of these is a shape where the
// walk cannot continue — a page object that is not a dictionary, no parent at
// all, a parent that is not a dictionary, a parent that does not have the key,
// and a /Parent CYCLE, which a crafted file can hold and which would otherwise
// spin.
func TestPushInheritedEndsItsWalk(t *testing.T) {
	t.Run("the page is not a dictionary", func(t *testing.T) {
		d := bareDoc(nil, nil, nil)
		if got := d.pushInherited(Ref{Num: 1}, Integer(7)); got != Object(Integer(7)) {
			t.Errorf("a non-dictionary page was rewritten to %v", got)
		}
	})

	t.Run("the page already has the attribute", func(t *testing.T) {
		box := Array{Integer(0), Integer(0), Integer(1), Integer(1)}
		d := bareDoc(nil, nil, map[int]Object{
			2: Dict{"Type": Name("Pages"), "MediaBox": Array{Integer(9)}},
		})
		page := Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}, "MediaBox": box}
		got, _ := ToDict(d.pushInherited(Ref{Num: 1}, page))
		if arr, _ := got["MediaBox"].(Array); len(arr) != 4 {
			t.Errorf("the page's own MediaBox was replaced by its parent's: %v", got["MediaBox"])
		}
	})

	t.Run("there is no parent", func(t *testing.T) {
		d := bareDoc(nil, nil, nil)
		page := Dict{"Type": Name("Page")}
		got, _ := ToDict(d.pushInherited(Ref{Num: 1}, page))
		if _, has := got["MediaBox"]; has {
			t.Error("a MediaBox appeared from nowhere")
		}
	})

	t.Run("the parent is not a dictionary", func(t *testing.T) {
		d := bareDoc(nil, nil, map[int]Object{2: Integer(7)})
		page := Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}}
		if _, has := mustDict(t, d.pushInherited(Ref{Num: 1}, page))["MediaBox"]; has {
			t.Error("a MediaBox came out of an integer")
		}
	})

	t.Run("the parent chain ends without the key", func(t *testing.T) {
		d := bareDoc(nil, nil, map[int]Object{
			2: Dict{"Type": Name("Pages"), "Parent": Ref{Num: 3}},
			3: Dict{"Type": Name("Pages")},
		})
		page := Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}}
		if _, has := mustDict(t, d.pushInherited(Ref{Num: 1}, page))["Rotate"]; has {
			t.Error("a Rotate appeared although no ancestor defines one")
		}
	})

	t.Run("a parent cycle", func(t *testing.T) {
		// ⛔ 2's parent is 3 and 3's parent is 2. The walk must stop on the
		// objects it has seen rather than on a depth: this package has already
		// paid for one unbounded walk.
		d := bareDoc(nil, nil, map[int]Object{
			2: Dict{"Type": Name("Pages"), "Parent": Ref{Num: 3}},
			3: Dict{"Type": Name("Pages"), "Parent": Ref{Num: 2}},
		})
		page := Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}}
		done := make(chan Object, 1)
		go func() { done <- d.pushInherited(Ref{Num: 1}, page) }()
		select {
		case got := <-done:
			if _, has := mustDict(t, got)["MediaBox"]; has {
				t.Error("a MediaBox appeared from a cycle")
			}
		case <-timeAfterSeconds(5):
			t.Fatal("pushInherited did not return on a /Parent cycle")
		}
	})

	t.Run("the parent is missing from the file", func(t *testing.T) {
		d := bareDoc(nil, nil, nil)
		page := Dict{"Type": Name("Page"), "Parent": Ref{Num: 99}}
		if _, has := mustDict(t, d.pushInherited(Ref{Num: 1}, page))["MediaBox"]; has {
			t.Error("a MediaBox came from an object that is not there")
		}
	})

	t.Run("the parent does not read", func(t *testing.T) {
		// ⛔ DIFFERENT FROM "missing": an absent object resolves to null with
		// no error, so the case above does NOT reach the walk's error branch.
		// This one names an object at an offset the file does not contain, so
		// Get fails and the walk must stop rather than carry a nil parent.
		d := bareDoc(nil, nil, nil)
		d.buf = []byte("%PDF-1.7\n")
		d.xref[2] = xrefEntry{kind: 'n', offset: 100000}
		page := Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}}
		if _, has := mustDict(t, d.pushInherited(Ref{Num: 1}, page))["MediaBox"]; has {
			t.Error("a MediaBox came from an object that does not read")
		}
	})
}

// ⛔⛔ THE EMISSION'S OWN ERROR PATHS, REACHED THROUGH THE SEAM. Partition
// guarantees that every part is one Order knows and that every object it placed
// reads — so from Linearize these four branches are unreachable, and the gate
// would refuse them. linearizeFrom takes the partition Partition would never
// produce.
//
// ⛔ The errors are not removed, because a future caller of linearizeFrom has
// no such guarantee. They are reached instead.
func TestTheEmissionRefusesAPartitionItCannotUse(t *testing.T) {
	t.Run("a part the layout does not place", func(t *testing.T) {
		d := bareDoc(nil, nil, nil)
		p := &Partition{Of: map[int]Part{1: Part(5)}, Pages: [][]int{{1}}}
		_, err := d.linearizeFrom(p)
		if !errors.Is(err, ErrCannotLayOut) {
			t.Fatalf("Order's refusal did not reach the caller: %v", err)
		}
	})

	t.Run("a first-page object that does not read", func(t *testing.T) {
		d := bareDoc(nil, nil, nil)
		d.buf = []byte("%PDF-1.7\n")
		d.xref[1] = xrefEntry{kind: 'n', offset: 100000}
		p := &Partition{
			Of:              map[int]Part{1: PartCatalog},
			Pages:           [][]int{{1}},
			FirstPageObject: 1,
		}
		_, err := d.linearizeFrom(p)
		if !errors.Is(err, ErrCannotLinearize) {
			t.Fatalf("an unreadable first-page object was accepted: %v", err)
		}
		if !strings.Contains(err.Error(), "does not read") {
			t.Errorf("it was refused, but not for the object failing to read: %v", err)
		}
	})

	t.Run("a later object that does not read", func(t *testing.T) {
		// ⛔ The SECOND call site, after the hint stream. Both loops call the
		// same emit and both must propagate; a test of one would leave the
		// other free to swallow the error.
		d := bareDoc(nil, nil, map[int]Object{1: Dict{"Type": Name("Catalog")}})
		d.buf = []byte("%PDF-1.7\n")
		d.xref[9] = xrefEntry{kind: 'n', offset: 100000}
		p := &Partition{
			Of:              map[int]Part{1: PartCatalog, 9: PartOther},
			Pages:           [][]int{{1}},
			FirstPageObject: 1,
		}
		_, err := d.linearizeFrom(p)
		if !errors.Is(err, ErrCannotLinearize) {
			t.Fatalf("an unreadable part-9 object was accepted: %v", err)
		}
	})

	t.Run("parameters too wide for their span", func(t *testing.T) {
		defer func(n int) { paramSpan = n }(paramSpan)
		paramSpan = 16

		plain := buildPages(t, 1)
		d, err := Open(plain)
		if err != nil {
			t.Fatal(err)
		}
		_, err = d.Linearize()
		if !errors.Is(err, ErrLinearizationTooWide) {
			t.Fatalf("a 16-byte span held the parameter dictionary: %v", err)
		}
	})
}

// ⛔ And stripInheritable declines what is not a dictionary rather than
// panicking on it: the emission calls it for every page tree node, and a file
// can name a node that is an integer.
func TestStripInheritableLeavesNonDictionariesAlone(t *testing.T) {
	if got := stripInheritable(Integer(7)); got != Object(Integer(7)) {
		t.Errorf("stripInheritable(7) = %v", got)
	}
	in := Dict{"Type": Name("Pages"), "MediaBox": Array{Integer(1)}, "Count": Integer(2)}
	out := mustDict(t, stripInheritable(in))
	if _, has := out["MediaBox"]; has {
		t.Error("MediaBox survived the strip")
	}
	if _, has := out["Count"]; !has {
		t.Error("Count was stripped and it is not inheritable")
	}
	// ⛔ The input is not mutated, or the document being read would change
	// under the caller.
	if _, has := in["MediaBox"]; !has {
		t.Error("stripInheritable mutated its argument")
	}
}

// ⛔⛔ RENUMBERING REACHES EVERY SHAPE AN OBJECT CAN TAKE, and a reference the
// layout did not place becomes Null. ⛔ That is a decision and not a fallback:
// a reference to an object this file does not define would send a reader to a
// number with no entry, and Null is what a dangling reference already means.
func TestRenumberRewritesEveryShape(t *testing.T) {
	num := map[int]int{7: 1, 8: 2}

	got := renumber(Array{
		Ref{Num: 7},
		Ref{Num: 99}, // not placed
		Dict{"K": Ref{Num: 8}},
		&Stream{Dict: Dict{"Length": Integer(3), "R": Ref{Num: 7}}, Raw: []byte("abc")},
		Integer(5),
		Name("x"),
	}, num)

	arr, ok := got.(Array)
	if !ok || len(arr) != 6 {
		t.Fatalf("renumber gave %T %v", got, got)
	}
	if r, _ := arr[0].(Ref); r.Num != 1 {
		t.Errorf("a placed reference became %v", arr[0])
	}
	if _, isNull := arr[1].(Null); !isNull {
		t.Errorf("an unplaced reference became %v, want null", arr[1])
	}
	if d, _ := ToDict(arr[2]); d != nil {
		if r, _ := d["K"].(Ref); r.Num != 2 {
			t.Errorf("a reference inside a dictionary became %v", d["K"])
		}
	}
	s, isStream := arr[3].(*Stream)
	if !isStream {
		t.Fatalf("a stream became %T", arr[3])
	}
	if r, _ := s.Dict["R"].(Ref); r.Num != 1 {
		t.Errorf("a reference in a stream's dictionary became %v", s.Dict["R"])
	}
	if string(s.Raw) != "abc" {
		t.Errorf("the stream's bytes changed to %q", s.Raw)
	}
	if arr[4] != Object(Integer(5)) || arr[5] != Object(Name("x")) {
		t.Errorf("a scalar was rewritten: %v, %v", arr[4], arr[5])
	}
}

func mustDict(t *testing.T, o Object) Dict {
	t.Helper()
	d, ok := ToDict(o)
	if !ok {
		t.Fatalf("%v is not a dictionary", o)
	}
	return d
}

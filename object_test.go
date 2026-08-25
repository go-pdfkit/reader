package reader

import (
	"errors"
	"testing"
)

func TestKindString(t *testing.T) {
	for k, want := range map[Kind]string{
		KindNull: "null", KindBool: "boolean", KindInteger: "integer",
		KindReal: "real", KindString: "string", KindName: "name",
		KindArray: "array", KindDict: "dictionary", KindStream: "stream",
		KindRef: "reference",
	} {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
	if got := Kind(99).String(); got != "Kind(99)" {
		t.Errorf("Kind(99).String() = %q", got)
	}
}

func TestObjectKinds(t *testing.T) {
	objs := []struct {
		o    Object
		want Kind
	}{
		{Null{}, KindNull},
		{Bool(true), KindBool},
		{Integer(1), KindInteger},
		{Real(1.5), KindReal},
		{String("x"), KindString},
		{Name("X"), KindName},
		{Array{}, KindArray},
		{Dict{}, KindDict},
		{&Stream{}, KindStream},
		{Ref{1, 0}, KindRef},
	}
	for _, c := range objs {
		if got := c.o.Kind(); got != c.want {
			t.Errorf("%T.Kind() = %v, want %v", c.o, got, c.want)
		}
	}
}

func TestRefString(t *testing.T) {
	if got := (Ref{12, 3}).String(); got != "12 3 R" {
		t.Errorf("Ref.String() = %q", got)
	}
}

func TestDictGet(t *testing.T) {
	d := Dict{"A": Integer(1), "N": nil}
	if got := d.Get("A"); got != Object(Integer(1)) {
		t.Errorf("Get(A) = %v", got)
	}
	if got := d.Get("Missing"); got.Kind() != KindNull {
		t.Errorf("Get(Missing) = %v, want null", got)
	}
	if got := d.Get("N"); got.Kind() != KindNull {
		t.Errorf("Get(N) = %v, want null", got)
	}
}

func TestResolve(t *testing.T) {
	table := map[Ref]Object{
		{1, 0}: Integer(7),
		{2, 0}: Ref{1, 0},
		{3, 0}: nil,
	}
	r := func(ref Ref) (Object, error) {
		if ref.Num == 9 {
			return nil, errors.New("boom")
		}
		return table[ref], nil
	}

	if got, err := Resolve(nil, r); err != nil || got.Kind() != KindNull {
		t.Errorf("Resolve(nil) = %v, %v", got, err)
	}
	if got, err := Resolve(Integer(3), r); err != nil || got != Object(Integer(3)) {
		t.Errorf("Resolve(direct) = %v, %v", got, err)
	}
	if got, err := Resolve(Ref{1, 0}, nil); err != nil || got.Kind() != KindNull {
		t.Errorf("Resolve(ref, nil resolver) = %v, %v", got, err)
	}
	if got, err := Resolve(Ref{2, 0}, r); err != nil || got != Object(Integer(7)) {
		t.Errorf("Resolve(chain) = %v, %v", got, err)
	}
	if got, err := Resolve(Ref{3, 0}, r); err != nil || got.Kind() != KindNull {
		t.Errorf("Resolve(missing) = %v, %v", got, err)
	}
	if _, err := Resolve(Ref{9, 0}, r); err == nil {
		t.Error("Resolve: want the resolver's error")
	}

	// A file may point a reference at itself; that must end, not hang.
	cyc := func(ref Ref) (Object, error) { return ref, nil }
	if _, err := Resolve(Ref{1, 0}, cyc); err == nil {
		t.Error("Resolve(cycle): want an error")
	}
}

func TestConversions(t *testing.T) {
	if v, ok := ToBool(Bool(true)); !ok || !v {
		t.Error("ToBool(true)")
	}
	if _, ok := ToBool(Integer(1)); ok {
		t.Error("ToBool(integer) should fail")
	}
	if v, ok := ToInt(Integer(5)); !ok || v != 5 {
		t.Error("ToInt(integer)")
	}
	if v, ok := ToInt(Real(3)); !ok || v != 3 {
		t.Error("ToInt(whole real)")
	}
	if _, ok := ToInt(Real(3.5)); ok {
		t.Error("ToInt(3.5) should fail")
	}
	if _, ok := ToInt(Name("x")); ok {
		t.Error("ToInt(name) should fail")
	}
	if v, ok := ToFloat(Integer(2)); !ok || v != 2 {
		t.Error("ToFloat(integer)")
	}
	if v, ok := ToFloat(Real(2.5)); !ok || v != 2.5 {
		t.Error("ToFloat(real)")
	}
	if _, ok := ToFloat(Null{}); ok {
		t.Error("ToFloat(null) should fail")
	}
	if v, ok := ToString(String("hi")); !ok || string(v) != "hi" {
		t.Error("ToString")
	}
	if _, ok := ToString(Name("hi")); ok {
		t.Error("ToString(name) should fail")
	}
	if v, ok := ToName(Name("N")); !ok || v != "N" {
		t.Error("ToName")
	}
	if _, ok := ToName(String("N")); ok {
		t.Error("ToName(string) should fail")
	}
	if v, ok := ToArray(Array{Integer(1)}); !ok || len(v) != 1 {
		t.Error("ToArray")
	}
	if _, ok := ToArray(Dict{}); ok {
		t.Error("ToArray(dict) should fail")
	}
	if v, ok := ToDict(Dict{"A": Integer(1)}); !ok || len(v) != 1 {
		t.Error("ToDict(dict)")
	}
	if v, ok := ToDict(&Stream{Dict: Dict{"A": Integer(1)}}); !ok || len(v) != 1 {
		t.Error("ToDict(stream)")
	}
	if _, ok := ToDict(Integer(1)); ok {
		t.Error("ToDict(integer) should fail")
	}
	if v, ok := ToStream(&Stream{}); !ok || v == nil {
		t.Error("ToStream")
	}
	if _, ok := ToStream(Dict{}); ok {
		t.Error("ToStream(dict) should fail")
	}
}

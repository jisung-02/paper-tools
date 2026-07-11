package pdf

import "testing"

func TestOwnedSubCopiesInlineNestedResourceDictionary(t *testing.T) {
	shared := Dict{"F0": Ref{Num: 1}}
	resources := Dict{"Font": shared}
	b := &builder{}
	owned := b.ownedSub(resources, "Font")
	owned["F1"] = Ref{Num: 2}
	if _, mutated := shared["F1"]; mutated {
		t.Fatal("ownedSub mutated a shared inline resource dictionary")
	}
	if _, ok := resources["Font"].(Dict)["F1"]; !ok {
		t.Fatal("owned resource dictionary did not retain the new entry")
	}
}

func TestAppendContentFlattensIndirectContentsArray(t *testing.T) {
	b := &builder{objs: []any{
		Array{Ref{Num: 2}, Ref{Num: 3}},
		&Stream{Dict: Dict{"Length": 1}, Data: []byte("a")},
		&Stream{Dict: Dict{"Length": 1}, Data: []byte("b")},
	}}
	pd := Dict{"Contents": Ref{Num: 1}}
	b.appendContent(pd, []byte("x"))
	contents, ok := pd["Contents"].(Array)
	if !ok || len(contents) != 4 {
		t.Fatalf("Contents = %#v, want flat 4-element array", pd["Contents"])
	}
	for i, value := range contents {
		if _, nested := value.(Array); nested {
			t.Fatalf("Contents[%d] is a nested array", i)
		}
	}
}

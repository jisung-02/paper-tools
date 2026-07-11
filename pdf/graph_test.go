package pdf

import (
	"bytes"
	"testing"
)

func TestFinalizeDropsUnreachableObjectsAndKeepsTrailerRoots(t *testing.T) {
	b := &builder{objs: []any{
		Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}},
		Dict{"Type": Name("Pages"), "Kids": Array{Ref{Num: 3}}, "Count": 1},
		Dict{"Type": Name("Page"), "Parent": Ref{Num: 2}, "Metadata": Ref{Num: 4}},
		Dict{"Canary": String("remove-me")},
		Dict{"Title": String("keep-me")},
		Dict{"Canary": String("also-remove-me")},
	}, infoRef: Ref{Num: 5}}

	page := b.objs[2].(Dict)
	delete(page, "Metadata")
	root, err := b.finalize(Ref{Num: 1})
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(b.objs) != 4 {
		t.Fatalf("live object count = %d, want 4", len(b.objs))
	}
	if b.infoRef.Num != 4 {
		t.Fatalf("Info ref = %v, want object 4", b.infoRef)
	}
	out, err := b.bytes(root)
	if err != nil {
		t.Fatalf("bytes: %v", err)
	}
	if bytes.Contains(out, []byte("72656D6F76652D6D65")) {
		t.Fatalf("unreachable canary remained in output")
	}
	if !bytes.Contains(out, []byte("6B6565702D6D65")) {
		t.Fatalf("Info-only object was removed")
	}
}

func TestFinalizeRejectsDanglingReference(t *testing.T) {
	b := &builder{objs: []any{Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}}}}
	if _, err := b.finalize(Ref{Num: 1}); err == nil {
		t.Fatal("finalize accepted dangling reference")
	}
}

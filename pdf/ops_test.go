package pdf

import (
	"bytes"
	"testing"
)

func TestPreservedCatalogDropsPageDependentEntries(t *testing.T) {
	d := &Doc{objs: map[int]any{1: Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}, "Lang": String("en"), "ViewerPreferences": Dict{"DisplayDocTitle": true}, "AcroForm": Ref{Num: 3}, "Outlines": Ref{Num: 4}}}, trailer: Dict{"Root": Ref{Num: 1}}}
	cat, _ := preservedCatalog(d)
	if cat["Lang"] == nil || cat["ViewerPreferences"] == nil {
		t.Fatal("page-independent catalog entries were not preserved")
	}
	for _, k := range []Name{"Pages", "AcroForm", "Outlines"} {
		if _, ok := cat[k]; ok {
			t.Fatalf("stale catalog key %s preserved", k)
		}
	}
}

func TestPreservedCatalogKeepsPageDependentEntriesWhenIdentityIsStable(t *testing.T) {
	d := &Doc{objs: map[int]any{1: Dict{"Type": Name("Catalog"), "Pages": Ref{Num: 2}, "AcroForm": Ref{Num: 3}, "Outlines": Ref{Num: 4}}}, trailer: Dict{"Root": Ref{Num: 1}}}
	cat, _ := preservedCatalog(d, true)
	for _, k := range []Name{"AcroForm", "Outlines"} {
		if _, ok := cat[k]; !ok {
			t.Fatalf("stable page catalog key %s was dropped", k)
		}
	}
	if _, ok := cat["Pages"]; ok {
		t.Fatal("Pages root must always be rebuilt")
	}
}

func TestPreservedCatalogDropsAllPageIndexedEntriesAfterTopologyChange(t *testing.T) {
	d := &Doc{objs: map[int]any{1: Dict{
		"Type": Name("Catalog"), "Pages": Ref{Num: 2},
		"AcroForm": Ref{Num: 3}, "Outlines": Ref{Num: 4},
		"Names": Ref{Num: 5}, "Dests": Ref{Num: 6},
		"PageLabels": Ref{Num: 7}, "StructTreeRoot": Ref{Num: 8},
	}}, trailer: Dict{"Root": Ref{Num: 1}}}
	cat, _ := preservedCatalog(d)
	for _, k := range []Name{"Pages", "AcroForm", "Outlines", "Names", "Dests", "PageLabels", "StructTreeRoot"} {
		if _, ok := cat[k]; ok {
			t.Fatalf("page-indexed catalog key %s survived topology change", k)
		}
	}
}

func TestRotatePreservesInheritedIndirectResources(t *testing.T) {
	source := ocrInheritedImagePDF(t)
	out, err := Rotate(source, "1", 90)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil || len(pages) != 1 {
		t.Fatalf("Pages: %v, count=%d", err, len(pages))
	}
	pd := d.Get(pages[0].Num).(Dict)
	resources, ok := d.R(pd["Resources"]).(Dict)
	if !ok {
		t.Fatalf("inherited Resources were lost: %v", pd["Resources"])
	}
	xobjects, ok := d.R(resources["XObject"]).(Dict)
	if !ok {
		t.Fatalf("inherited XObject dictionary was lost: %v", resources)
	}
	image, ok := d.R(xobjects["Im0"]).(*Stream)
	if !ok || !bytes.Equal(image.Data, []byte{17, 34, 51}) {
		t.Fatalf("inherited image was lost: %v", xobjects["Im0"])
	}
}

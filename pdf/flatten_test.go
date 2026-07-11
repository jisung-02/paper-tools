package pdf

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func annotatedPDF() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")

	offsets := make([]int, 6)
	writeObjRaw := func(num int, body string) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	writeStream := func(num int, dict, data string) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nstream\n%s\nendstream\nendobj\n", num, dict, data)
	}

	ap := "0 0 1 rg 0 0 80 30 re f"
	writeObjRaw(1, "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] >> >>")
	writeObjRaw(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 300 400] >>")
	writeObjRaw(3, "<< /Type /Page /Parent 2 0 R /Annots [4 0 R] >>")
	writeObjRaw(4, "<< /Type /Annot /Subtype /Widget /Rect [100 200 180 230] /AP << /N 5 0 R >> >>")
	writeStream(5, fmt.Sprintf("<< /Type /XObject /Subtype /Form /BBox [0 0 80 30] /Length %d >>", len(ap)), ap)

	xrefOff := buf.Len()
	buf.WriteString("xref\n0 6\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	buf.WriteString("trailer\n<< /Root 1 0 R /Size 6 >>\nstartxref\n")
	fmt.Fprintf(&buf, "%d\n", xrefOff)
	buf.WriteString("%%EOF\n")
	return buf.Bytes()
}

func annotatedPDFWithExistingXObjectName() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")

	offsets := make([]int, 7)
	writeObjRaw := func(num int, body string) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	writeStream := func(num int, dict, data string) {
		offsets[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nstream\n%s\nendstream\nendobj\n", num, dict, data)
	}

	ap := "0 0 1 rg 0 0 80 30 re f"
	existing := "0 0 10 10 re f"
	writeObjRaw(1, "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] >> >>")
	writeObjRaw(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 300 400] >>")
	writeObjRaw(3, "<< /Type /Page /Parent 2 0 R /Resources << /XObject << /FltAnn0 6 0 R >> >> /Annots [4 0 R] >>")
	writeObjRaw(4, "<< /Type /Annot /Subtype /Widget /Rect [100 200 180 230] /AP << /N 5 0 R >> >>")
	writeStream(5, fmt.Sprintf("<< /Type /XObject /Subtype /Form /BBox [0 0 80 30] /Length %d >>", len(ap)), ap)
	writeStream(6, fmt.Sprintf("<< /Type /XObject /Subtype /Form /BBox [0 0 10 10] /Length %d >>", len(existing)), existing)

	xrefOff := buf.Len()
	buf.WriteString("xref\n0 7\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	buf.WriteString("trailer\n<< /Root 1 0 R /Size 7 >>\nstartxref\n")
	fmt.Fprintf(&buf, "%d\n", xrefOff)
	buf.WriteString("%%EOF\n")
	return buf.Bytes()
}

func TestFlattenDrawsAnnotationAppearanceAndRemovesAnnots(t *testing.T) {
	out, err := Flatten(annotatedPDF())
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if cat, ok := d.R(d.trailer["Root"]).(Dict); ok {
		if _, stale := cat["AcroForm"]; stale {
			t.Fatal("flattened catalog retains stale AcroForm")
		}
	}
	pd := d.Get(pages[0].Num).(Dict)
	if _, ok := pd["Annots"]; ok {
		t.Fatalf("flattened page still has Annots: %v", pd["Annots"])
	}

	data, has := lastContentData(t, d, pd)
	if !has {
		t.Fatal("flattened page missing appended content")
	}
	want := "q 1.00 0 0 1.00 100.00 200.00 cm /FltAnn0 Do Q"
	if !strings.Contains(string(data), want) {
		t.Fatalf("flatten content = %q, want %q", data, want)
	}

	res, ok := d.R(pd["Resources"]).(Dict)
	if !ok {
		t.Fatal("flattened page missing resources")
	}
	xobjs, ok := d.R(res["XObject"]).(Dict)
	if !ok {
		t.Fatal("flattened page missing XObject resources")
	}
	st, ok := d.R(xobjs["FltAnn0"]).(*Stream)
	if !ok {
		t.Fatalf("/FltAnn0 is not an XObject stream")
	}
	if !bytes.Contains(st.Data, []byte("0 0 80 30 re f")) {
		t.Fatalf("appearance stream data = %q", st.Data)
	}
}

func TestFlattenAvoidsExistingXObjectResourceNames(t *testing.T) {
	out, err := Flatten(annotatedPDFWithExistingXObjectName())
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	pd := d.Get(pages[0].Num).(Dict)
	data, has := lastContentData(t, d, pd)
	if !has {
		t.Fatal("flattened page missing appended content")
	}
	if strings.Contains(string(data), "/FltAnn0 Do") {
		t.Fatalf("flatten content reused existing resource name: %q", data)
	}
	if !strings.Contains(string(data), "/FltAnn1 Do") {
		t.Fatalf("flatten content = %q, want /FltAnn1 Do", data)
	}

	res := d.R(pd["Resources"]).(Dict)
	xobjs := d.R(res["XObject"]).(Dict)
	existing, ok := d.R(xobjs["FltAnn0"]).(*Stream)
	if !ok {
		t.Fatalf("existing /FltAnn0 resource was not preserved")
	}
	if !bytes.Contains(existing.Data, []byte("0 0 10 10 re f")) {
		t.Fatalf("existing resource data = %q", existing.Data)
	}
	if _, ok := d.R(xobjs["FltAnn1"]).(*Stream); !ok {
		t.Fatalf("flattened appearance resource /FltAnn1 is missing")
	}
}

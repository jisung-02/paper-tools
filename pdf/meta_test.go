package pdf

import "testing"

func TestSetMetadata(t *testing.T) {
	out, err := SetMetadata(classicPDF(), DocInfo{Title: "한글 제목", Author: "Chae"}, false)
	if err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	info, ok := d.R(d.trailer["Info"]).(Dict)
	if !ok {
		t.Fatalf("trailer Info missing or not a dict")
	}
	title, ok := info["Title"].(String)
	if !ok {
		t.Fatalf("Title missing or not a string")
	}
	if len(title) < 2 || title[0] != 0xFE || title[1] != 0xFF {
		t.Fatalf("Title %v does not start with the UTF-16BE BOM", title)
	}
	if got := decodeInfoString(title); got != "한글 제목" {
		t.Errorf("Title decoded = %q, want %q", got, "한글 제목")
	}

	gi, err := GetInfo(out)
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	if gi["title"] != "한글 제목" {
		t.Errorf("GetInfo title = %v, want %q", gi["title"], "한글 제목")
	}
	if gi["author"] != "Chae" {
		t.Errorf("GetInfo author = %v, want %q", gi["author"], "Chae")
	}
}

func TestStripMetadata(t *testing.T) {
	withInfo, err := SetMetadata(classicPDF(), DocInfo{Title: "한글 제목", Author: "Chae"}, false)
	if err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	stripped, err := SetMetadata(withInfo, DocInfo{}, true)
	if err != nil {
		t.Fatalf("SetMetadata strip: %v", err)
	}
	d, err := Parse(stripped)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := d.trailer["Info"]; ok {
		t.Errorf("trailer still has /Info after stripAll")
	}
}

func TestGetInfo(t *testing.T) {
	gi, err := GetInfo(classicPDF())
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	if gi["pages"] != 2 {
		t.Errorf("pages = %v, want 2", gi["pages"])
	}
	if gi["version"] != "1.7" {
		t.Errorf("version = %v, want 1.7", gi["version"])
	}
	if gi["encrypted"] != false {
		t.Errorf("encrypted = %v, want false", gi["encrypted"])
	}
	sizes, ok := gi["pageSizes"].([]map[string]any)
	if !ok || len(sizes) != 2 {
		t.Fatalf("pageSizes = %v, want a slice of 2", gi["pageSizes"])
	}
	if sizes[1]["rotate"] != 90 {
		t.Errorf("pageSizes[1].rotate = %v, want 90", sizes[1]["rotate"])
	}
}

func TestGetInfoEncrypted(t *testing.T) {
	gi, err := GetInfo(encryptedPDF())
	if err != nil {
		t.Fatalf("GetInfo: unexpected error %v", err)
	}
	if gi["encrypted"] != true {
		t.Errorf("encrypted = %v, want true", gi["encrypted"])
	}
}

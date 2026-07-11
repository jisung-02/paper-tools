package pdf

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func zipOfficeFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDocxTextRejectsConfiguredEntryBudget(t *testing.T) {
	old := officeParseLimits
	t.Cleanup(func() { officeParseLimits = old })
	officeParseLimits.maxZipEntryBytes = 8

	data := zipOfficeFixture(t, map[string]string{
		"word/document.xml": `<document><body><p><t>too long</t></p></body></document>`,
	})
	if _, err := DocxText(data); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("DocxText error = %v, want entry budget error", err)
	}
}

func TestHwpxTextSortsSectionsNumerically(t *testing.T) {
	data := zipOfficeFixture(t, map[string]string{
		"Contents/section10.xml": `<sec><p><t>ten</t></p></sec>`,
		"Contents/section2.xml":  `<sec><p><t>two</t></p></sec>`,
	})
	text, err := HwpxText(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(text, "two") > strings.Index(text, "ten") {
		t.Fatalf("section order = %q, want section2 before section10", text)
	}
}

func TestHwpxTextPropagatesDamagedSection(t *testing.T) {
	data := zipOfficeFixture(t, map[string]string{
		"Contents/section0.xml": `<sec><p><t>unterminated`,
	})
	if _, err := HwpxText(data); err == nil {
		t.Fatal("HwpxText accepted malformed section XML")
	}
}

func TestHwpTextRejectsConfiguredInputBudget(t *testing.T) {
	old := officeParseLimits
	t.Cleanup(func() { officeParseLimits = old })
	data := buildSyntheticHWP(t)
	officeParseLimits.maxHWPInputBytes = int64(len(data) - 1)
	if _, err := HwpText(data); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("HwpText error = %v, want input budget error", err)
	}
}

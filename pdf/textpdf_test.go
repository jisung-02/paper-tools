package pdf

import (
	"os"
	"strings"
	"testing"
)

func TestTextToPDFBasic(t *testing.T) {
	font, err := os.ReadFile("../web/NanumGothic-Regular.ttf")
	if err != nil {
		t.Fatalf("read font: %v", err)
	}
	out, err := TextToPDF("안녕하세요 Hello World\n둘째 줄 123", font, TextPDFOpts{})
	if err != nil {
		t.Fatalf("TextToPDF: %v", err)
	}
	if !strings.HasPrefix(string(out), "%PDF") {
		t.Fatalf("output does not start with %%PDF")
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) < 1 {
		t.Fatalf("expected >=1 page, got %d", len(pages))
	}
	text, err := ExtractText(out)
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	for _, want := range []string{"안녕하세요", "Hello World", "둘째"} {
		if !strings.Contains(text, want) {
			t.Errorf("extracted text missing %q; got:\n%s", want, text)
		}
	}
	t.Logf("basic PDF size: %d bytes", len(out))
}

func TestTextToPDFMultiPage(t *testing.T) {
	font, err := os.ReadFile("../web/NanumGothic-Regular.ttf")
	if err != nil {
		t.Fatalf("read font: %v", err)
	}
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("가나다라 mixed Korean and Latin paragraph line ")
		sb.WriteString("테스트 문장입니다.\n")
	}
	out, err := TextToPDF(sb.String(), font, TextPDFOpts{FontSize: 12})
	if err != nil {
		t.Fatalf("TextToPDF: %v", err)
	}
	d, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pages, err := d.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) <= 1 {
		t.Fatalf("expected >1 page, got %d", len(pages))
	}
	t.Logf("multipage: %d pages, %d bytes", len(pages), len(out))
}

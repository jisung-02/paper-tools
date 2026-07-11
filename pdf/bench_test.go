package pdf

import (
	"strings"
	"testing"
)

func TestLargeTextToPDFOutputBudget(t *testing.T) {
	font := testFont(t)
	input := strings.Repeat("large input regression line\n", 32*1024)
	out, err := TextToPDF(input, font, TextPDFOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 16*1024*1024 {
		t.Fatalf("large text output exceeded 16 MiB budget: %d", len(out))
	}
}

func BenchmarkTextToPDF(b *testing.B) {
	font := testFont(b)
	text := "Paper Tools deterministic benchmark line.\n"
	text += "The same input is rendered on every run to track allocations and latency.\n"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := TextToPDF(text, font, TextPDFOpts{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMerge(b *testing.B) {
	left, right := classicPDF(), classicPDF()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Merge([][]byte{left, right}); err != nil {
			b.Fatal(err)
		}
	}
}

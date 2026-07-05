package pdf

import (
	"strings"
	"testing"
)

// linesContent builds a page content stream (for use with textPDF, defined
// in text_test.go) that renders each of lines as its own line of text, one
// Tj per line separated by T* line breaks.
func linesContent(lines []string) string {
	var b strings.Builder
	b.WriteString("BT /F1 24 Tf 72 700 Td (")
	b.WriteString(lines[0])
	b.WriteString(") Tj")
	for _, l := range lines[1:] {
		b.WriteString(" T* (")
		b.WriteString(l)
		b.WriteString(") Tj")
	}
	b.WriteString(" ET")
	return b.String()
}

func TestDiffTextIdentical(t *testing.T) {
	lines := []string{"Alpha", "Beta", "Gamma"}
	a := textPDF(linesContent(lines), "")
	b := textPDF(linesContent(lines), "")

	report, identical, err := DiffText(a, b)
	if err != nil {
		t.Fatalf("DiffText: %v", err)
	}
	if !identical {
		t.Fatalf("expected identical=true, report:\n%s", report)
	}
	if strings.Count(report, "\n") > 1 {
		t.Fatalf("expected a one-line report, got:\n%s", report)
	}
	if !strings.Contains(report, "identical") {
		t.Fatalf("report %q does not mention identical", report)
	}
}

func TestDiffTextOneLineChange(t *testing.T) {
	// 8 lines with the 4th changed, leaving >=3 lines of context on both
	// sides so the hunk boundaries are exact and easy to assert on.
	linesA := []string{"L1", "L2", "L3", "L4", "L5", "L6", "L7", "L8"}
	linesB := []string{"L1", "L2", "L3", "L4X", "L5", "L6", "L7", "L8"}
	a := textPDF(linesContent(linesA), "")
	b := textPDF(linesContent(linesB), "")

	report, identical, err := DiffText(a, b)
	if err != nil {
		t.Fatalf("DiffText: %v", err)
	}
	if identical {
		t.Fatalf("expected identical=false, report:\n%s", report)
	}

	if !strings.HasPrefix(report, "--- A (1 page)\n+++ B (1 page)\n") {
		t.Fatalf("unexpected header, report:\n%s", report)
	}

	wantHunk := "@@ -2,7 +2,7 @@\n" +
		"  L1\n" +
		"  L2\n" +
		"  L3\n" +
		"- L4\n" +
		"+ L4X\n" +
		"  L5\n" +
		"  L6\n" +
		"  L7\n"
	if !strings.Contains(report, wantHunk) {
		t.Fatalf("report does not contain expected hunk.\ngot:\n%s\nwant substring:\n%s", report, wantHunk)
	}
	// L8 falls outside the 3-line trailing context and should not appear.
	if strings.Contains(report, "L8") {
		t.Fatalf("report unexpectedly contains out-of-context line L8:\n%s", report)
	}
}

func TestDiffTextInsertedPageIsAllAdditions(t *testing.T) {
	pageA := textPDF(linesContent([]string{"Alpha", "Beta"}), "")
	pageC := textPDF(linesContent([]string{"Gamma", "Delta"}), "")
	b, err := Merge([][]byte{pageA, pageC})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	report, identical, err := DiffText(pageA, b)
	if err != nil {
		t.Fatalf("DiffText: %v", err)
	}
	if identical {
		t.Fatalf("expected identical=false, report:\n%s", report)
	}
	if !strings.HasPrefix(report, "--- A (1 page)\n+++ B (2 pages)\n") {
		t.Fatalf("unexpected header, report:\n%s", report)
	}
	for _, want := range []string{"+ Gamma", "+ Delta"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
	for _, unwanted := range []string{"- Gamma", "- Delta"} {
		if strings.Contains(report, unwanted) {
			t.Fatalf("report unexpectedly contains %q:\n%s", unwanted, report)
		}
	}
}

// TestDiffTextFallbackThreshold forces DiffText onto the trim/replace
// fallback path (normally reserved for huge inputs) by temporarily lowering
// maxDiffCells, and checks it still produces a sane report for a small,
// hand-checkable change.
func TestDiffTextFallbackThreshold(t *testing.T) {
	old := maxDiffCells
	maxDiffCells = 4 // 5 lines x 5 lines (with the leading blank Td line) > 4
	defer func() { maxDiffCells = old }()

	a := textPDF(linesContent([]string{"one", "two", "three"}), "")
	b := textPDF(linesContent([]string{"one", "two", "THREE"}), "")

	report, identical, err := DiffText(a, b)
	if err != nil {
		t.Fatalf("DiffText: %v", err)
	}
	if identical {
		t.Fatalf("expected identical=false, report:\n%s", report)
	}
	if !strings.Contains(report, "- three") || !strings.Contains(report, "+ THREE") {
		t.Fatalf("fallback report missing expected change lines:\n%s", report)
	}
}

func TestTrimReplaceDiff(t *testing.T) {
	a := []int32{1, 2, 3, 4, 5}
	b := []int32{1, 2, 9, 9, 5}

	ops := trimReplaceDiff(a, b)

	want := []diffOp{
		{kind: opEqual, aPos: 0, bPos: 0},
		{kind: opEqual, aPos: 1, bPos: 1},
		{kind: opDelete, aPos: 2, bPos: 2},
		{kind: opDelete, aPos: 3, bPos: 2},
		{kind: opInsert, aPos: 4, bPos: 2},
		{kind: opInsert, aPos: 4, bPos: 3},
		{kind: opEqual, aPos: 4, bPos: 4},
	}
	if len(ops) != len(want) {
		t.Fatalf("got %d ops, want %d: %+v", len(ops), len(want), ops)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Errorf("op[%d] = %+v, want %+v", i, ops[i], want[i])
		}
	}
}

func TestDiffTextEncryptedError(t *testing.T) {
	_, _, err := DiffText(encryptedPDF(), classicPDF())
	if err == nil {
		t.Fatalf("expected error for encrypted file A")
	}
	if !strings.Contains(err.Error(), "encrypted files are not supported") {
		t.Fatalf("error %q does not preserve underlying message", err.Error())
	}
}

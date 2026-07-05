package pdf

import (
	"fmt"
	"strings"
)

// diffContext is the number of unchanged lines shown around each hunk of
// changes, matching the conventional unified-diff default.
const diffContext = 3

// maxDiffCells caps the line-based LCS dynamic-programming table at roughly
// 25,000,000 cells (e.g. two ~5,000-line documents). Above that ceiling the
// O(lines_a * lines_b) table would be too slow/memory-hungry to build in a
// browser tab, so DiffText falls back to a much cheaper prefix/suffix trim
// plus a single block replace for the differing middle section. It's a var
// (not a const) so tests can lower it to exercise the fallback path without
// needing multi-thousand-line fixtures.
var maxDiffCells int64 = 25_000_000

// DiffText extracts the selectable text of two PDFs and compares it line by
// line, returning a unified-diff-style report, whether the extracted text is
// identical, and any error encountered while reading either file.
func DiffText(a, b []byte) (string, bool, error) {
	textA, err := ExtractText(a)
	if err != nil {
		return "", false, fmt.Errorf("file A: %w", err)
	}
	textB, err := ExtractText(b)
	if err != nil {
		return "", false, fmt.Errorf("file B: %w", err)
	}

	pagesA := pageCount(a)
	pagesB := pageCount(b)

	linesA := normalizeLines(textA)
	linesB := normalizeLines(textB)
	idsA, idsB := hashLines(linesA, linesB)

	if intSlicesEqual(idsA, idsB) {
		report := fmt.Sprintf(
			"A (%d %s) and B (%d %s) have identical extracted text.\n",
			pagesA, pageWord(pagesA), pagesB, pageWord(pagesB),
		)
		return report, true, nil
	}

	var ops []diffOp
	if int64(len(idsA))*int64(len(idsB)) > maxDiffCells {
		ops = trimReplaceDiff(idsA, idsB)
	} else {
		ops = lcsDiff(idsA, idsB)
	}

	return formatReport(pagesA, pagesB, linesA, linesB, ops), false, nil
}

// pageCount returns file's page count, or 0 if it can't be determined. It's
// only used for the report header, so failures (which would already have
// surfaced through ExtractText) are silently ignored here.
func pageCount(file []byte) int {
	d, err := Parse(file)
	if err != nil {
		return 0
	}
	pages, err := d.Pages()
	if err != nil {
		return 0
	}
	return len(pages)
}

// pageWord returns "page" or "pages" depending on n.
func pageWord(n int) string {
	if n == 1 {
		return "page"
	}
	return "pages"
}

// normalizeLines splits text into lines, trimming only trailing whitespace
// (spaces, tabs, carriage returns) from each line. Everything else — leading
// whitespace, blank lines, interior spacing — is preserved verbatim so the
// diff reflects real content differences rather than incidental extraction
// artifacts.
func normalizeLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	return lines
}

// hashLines interns every distinct line across both inputs to a small
// integer, so the diff algorithms below compare cheap ints instead of
// strings.
func hashLines(linesA, linesB []string) (idsA, idsB []int32) {
	ids := make(map[string]int32, len(linesA)+len(linesB))
	var next int32
	idOf := func(s string) int32 {
		if id, ok := ids[s]; ok {
			return id
		}
		id := next
		ids[s] = id
		next++
		return id
	}
	idsA = make([]int32, len(linesA))
	for i, l := range linesA {
		idsA[i] = idOf(l)
	}
	idsB = make([]int32, len(linesB))
	for i, l := range linesB {
		idsB[i] = idOf(l)
	}
	return idsA, idsB
}

func intSlicesEqual(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- diff ops

// diffOpKind identifies whether a diffOp keeps, removes, or adds a line.
type diffOpKind int

const (
	opEqual diffOpKind = iota
	opDelete
	opInsert
)

// diffOp is one step of an edit script turning A's lines into B's lines.
// aPos/bPos are always populated (0-based indices into linesA/linesB), even
// for ops that only touch one side, so hunk headers can be computed directly
// from the first op in a hunk without any extra bookkeeping:
//   - opEqual:  aPos/bPos are the (equal) positions consumed on both sides.
//   - opDelete: aPos is the line removed from A; bPos is B's position at
//     that point (unconsumed, since a delete doesn't advance B).
//   - opInsert: bPos is the line added from B; aPos is A's position at that
//     point (unconsumed, since an insert doesn't advance A).
type diffOp struct {
	kind diffOpKind
	aPos int
	bPos int
}

// lcsDiff computes a line-based edit script between a and b using the
// classic dynamic-programming longest-common-subsequence algorithm. It's
// O(len(a)*len(b)) time and space, which is why callers guard against huge
// inputs with maxDiffCells before reaching here.
func lcsDiff(a, b []int32) []diffOp {
	n, m := len(a), len(b)
	// dp[i*(m+1)+j] = length of the LCS of a[i:] and b[j:].
	dp := make([]int32, (n+1)*(m+1))
	row := m + 1
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i*row+j] = dp[(i+1)*row+(j+1)] + 1
			} else if down, right := dp[(i+1)*row+j], dp[i*row+(j+1)]; down >= right {
				dp[i*row+j] = down
			} else {
				dp[i*row+j] = right
			}
		}
	}

	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{kind: opEqual, aPos: i, bPos: j})
			i++
			j++
		case dp[(i+1)*row+j] >= dp[i*row+(j+1)]:
			ops = append(ops, diffOp{kind: opDelete, aPos: i, bPos: j})
			i++
		default:
			ops = append(ops, diffOp{kind: opInsert, aPos: i, bPos: j})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{kind: opDelete, aPos: i, bPos: j})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{kind: opInsert, aPos: i, bPos: j})
	}
	return ops
}

// trimReplaceDiff is the size-guarded fallback for inputs too large for
// lcsDiff's O(n*m) table. It trims the common prefix and common suffix and
// treats everything in between as a single replaced block (all of A's
// middle lines deleted, then all of B's middle lines inserted) rather than
// computing a fine-grained alignment of the middle section.
func trimReplaceDiff(a, b []int32) []diffOp {
	n, m := len(a), len(b)
	p := 0
	for p < n && p < m && a[p] == b[p] {
		p++
	}
	s := 0
	for s < n-p && s < m-p && a[n-1-s] == b[m-1-s] {
		s++
	}

	var ops []diffOp
	for i := 0; i < p; i++ {
		ops = append(ops, diffOp{kind: opEqual, aPos: i, bPos: i})
	}
	for i := p; i < n-s; i++ {
		ops = append(ops, diffOp{kind: opDelete, aPos: i, bPos: p})
	}
	for j := p; j < m-s; j++ {
		ops = append(ops, diffOp{kind: opInsert, aPos: n - s, bPos: j})
	}
	for i := 0; i < s; i++ {
		ops = append(ops, diffOp{kind: opEqual, aPos: n - s + i, bPos: m - s + i})
	}
	return ops
}

// ------------------------------------------------------------- formatting

// hunk is one @@ ... @@ section of the report: a contiguous run of ops
// (context plus changes) along with the 1-based line ranges it covers in A
// and B.
type hunk struct {
	aStart, aCount int
	bStart, bCount int
	ops            []diffOp
}

// opSegment is a maximal run of consecutive ops that are all equal (context)
// or all non-equal (changes).
type opSegment struct {
	equal bool
	ops   []diffOp
}

func segmentOps(ops []diffOp) []opSegment {
	var segs []opSegment
	for _, op := range ops {
		eq := op.kind == opEqual
		if n := len(segs); n > 0 && segs[n-1].equal == eq {
			segs[n-1].ops = append(segs[n-1].ops, op)
		} else {
			segs = append(segs, opSegment{equal: eq, ops: []diffOp{op}})
		}
	}
	return segs
}

// buildHunks groups an edit script into unified-diff hunks, keeping
// `context` lines of unchanged text around each change and merging hunks
// whose surrounding context overlaps.
func buildHunks(ops []diffOp, context int) []hunk {
	segs := segmentOps(ops)

	var hunks []hunk
	var cur []diffOp
	flush := func() {
		if len(cur) > 0 {
			hunks = append(hunks, makeHunk(cur))
			cur = nil
		}
	}

	for i, seg := range segs {
		if !seg.equal {
			cur = append(cur, seg.ops...)
			continue
		}
		isFirst := i == 0
		isLast := i == len(segs)-1
		switch {
		case isFirst && isLast:
			// Nothing but equal lines; DiffText's identical check should
			// have already short-circuited before we get here.
		case isFirst:
			keep := seg.ops
			if len(keep) > context {
				keep = keep[len(keep)-context:]
			}
			cur = append(cur, keep...)
		case isLast:
			keep := seg.ops
			if len(keep) > context {
				keep = keep[:context]
			}
			cur = append(cur, keep...)
			flush()
		default:
			if len(seg.ops) > 2*context {
				cur = append(cur, seg.ops[:context]...)
				flush()
				cur = append(cur, seg.ops[len(seg.ops)-context:]...)
			} else {
				cur = append(cur, seg.ops...)
			}
		}
	}
	flush()
	return hunks
}

// makeHunk computes a hunk's 1-based line ranges from its ops. aPos/bPos on
// the first op are always valid (see diffOp's doc comment), so the range
// start is simply that position, converted to 1-based when the range is
// non-empty (an empty side, e.g. a pure insertion, keeps the 0-based
// position per the conventional unified-diff zero-count format).
func makeHunk(ops []diffOp) hunk {
	var aCount, bCount int
	for _, op := range ops {
		if op.kind == opEqual || op.kind == opDelete {
			aCount++
		}
		if op.kind == opEqual || op.kind == opInsert {
			bCount++
		}
	}
	aStart, bStart := ops[0].aPos, ops[0].bPos
	if aCount > 0 {
		aStart++
	}
	if bCount > 0 {
		bStart++
	}
	return hunk{aStart: aStart, aCount: aCount, bStart: bStart, bCount: bCount, ops: ops}
}

// hunkRange formats one side of a hunk header, e.g. "-12,4" or "+6" (the
// count is omitted when it's exactly 1, matching GNU diff).
func hunkRange(sign byte, start, count int) string {
	if count == 1 {
		return fmt.Sprintf("%c%d", sign, start)
	}
	return fmt.Sprintf("%c%d,%d", sign, start, count)
}

// formatReport renders the full unified-diff-style report: a two-line
// header naming "A" and "B" with their page counts, followed by each hunk.
func formatReport(pagesA, pagesB int, linesA, linesB []string, ops []diffOp) string {
	var out strings.Builder
	fmt.Fprintf(&out, "--- A (%d %s)\n", pagesA, pageWord(pagesA))
	fmt.Fprintf(&out, "+++ B (%d %s)\n", pagesB, pageWord(pagesB))

	for _, h := range buildHunks(ops, diffContext) {
		fmt.Fprintf(&out, "@@ %s %s @@\n", hunkRange('-', h.aStart, h.aCount), hunkRange('+', h.bStart, h.bCount))
		for _, op := range h.ops {
			switch op.kind {
			case opEqual:
				out.WriteString("  ")
				out.WriteString(linesA[op.aPos])
			case opDelete:
				out.WriteString("- ")
				out.WriteString(linesA[op.aPos])
			case opInsert:
				out.WriteString("+ ")
				out.WriteString(linesB[op.bPos])
			}
			out.WriteString("\n")
		}
	}
	return out.String()
}

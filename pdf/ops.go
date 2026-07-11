package pdf

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ------------------------------------------------------------- builder ---

type builder struct {
	objs []any // object number = index+1

	// caches for shared overlay resources, lazily allocated.
	fontRef Ref
	gsRef   Ref

	infoRef    Ref    // if set, written as /Info N 0 R in the trailer
	id         String // if non-nil, written as /ID [<hex> <hex>] (same value twice)
	encryptRef Ref    // if set, written as /Encrypt N 0 R in the trailer
	finalized  bool
}

func (b *builder) alloc() Ref {
	if b.finalized {
		panic("pdf: allocation after finalization")
	}
	b.objs = append(b.objs, nil)
	return Ref{Num: len(b.objs), Gen: 0}
}

// reachable returns, sorted and deduplicated, the object numbers reachable
// from roots by following Refs, skipping /Parent and /P (backrefs into the
// page tree that would otherwise drag in the whole source document).
func (d *Doc) reachable(roots []int) []int {
	seen := map[int]bool{}
	queue := append([]int(nil), roots...)
	var scan func(v any)
	scan = func(v any) {
		switch t := v.(type) {
		case Ref:
			if !seen[t.Num] {
				seen[t.Num] = true
				queue = append(queue, t.Num)
			}
		case Dict:
			for k, vv := range t {
				if k == "Parent" || k == "P" {
					continue
				}
				scan(vv)
			}
		case Array:
			for _, vv := range t {
				scan(vv)
			}
		case *Stream:
			scan(t.Dict)
		}
	}
	for _, n := range roots {
		seen[n] = true
	}
	for i := 0; i < len(queue); i++ {
		v := d.Get(queue[i])
		if v == nil {
			continue
		}
		scan(v)
	}
	nums := make([]int, 0, len(seen))
	for n := range seen {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	return nums
}

// importDoc copies every object reachable from roots in d into b, returning
// a map from the source object number to its new Ref.
func (b *builder) importDoc(d *Doc, roots []int) map[int]Ref {
	all := d.reachable(roots)
	var nums []int
	for _, num := range all {
		v := d.Get(num)
		if v == nil {
			continue
		}
		if st, ok := v.(*Stream); ok {
			if t, _ := st.Dict["Type"].(Name); t == "XRef" || t == "ObjStm" {
				continue
			}
		}
		nums = append(nums, num)
	}
	sort.Ints(nums)

	m := make(map[int]Ref, len(nums))
	for _, num := range nums {
		m[num] = b.alloc()
	}
	for _, num := range nums {
		nr := m[num]
		b.objs[nr.Num-1] = translate(d.Get(num), m)
	}
	return m
}

// translate deep-copies v, rewriting Refs through m. Dangling refs become
// null. There is no cycle risk here: cycles only exist through Refs, which
// do not recurse.
func translate(v any, m map[int]Ref) any {
	switch t := v.(type) {
	case Ref:
		if nr, ok := m[t.Num]; ok {
			return nr
		}
		return nil
	case Dict:
		nd := make(Dict, len(t))
		for k, vv := range t {
			nd[k] = translate(vv, m)
		}
		return nd
	case Array:
		na := make(Array, len(t))
		for i, vv := range t {
			na[i] = translate(vv, m)
		}
		return na
	case *Stream:
		nd := make(Dict, len(t.Dict))
		for k, vv := range t.Dict {
			nd[k] = translate(vv, m)
		}
		nd["Length"] = len(t.Data)
		return &Stream{Dict: nd, Data: t.Data}
	default:
		return v
	}
}

var preservedCatalogKeys = map[Name]bool{
	"Lang":              true,
	"ViewerPreferences": true,
	"Metadata":          true,
	"OutputIntents":     true,
}

// page-dependent catalog entries are intentionally omitted when assembling a
// new page tree; retaining them would leave references to removed pages.
var pageDependentCatalogKeys = map[Name]bool{
	"Pages": true, "AcroForm": true, "Outlines": true, "StructTreeRoot": true,
	"PageLabels": true, "Names": true, "Dests": true,
}

func collectRefs(v any, out *[]int) {
	switch t := v.(type) {
	case Ref:
		*out = append(*out, t.Num)
	case Dict:
		for _, vv := range t {
			collectRefs(vv, out)
		}
	case Array:
		for _, vv := range t {
			collectRefs(vv, out)
		}
	case *Stream:
		collectRefs(t.Dict, out)
	}
}

func preservedCatalog(d *Doc, keepPageDependent ...bool) (Dict, []int) {
	root, _ := d.R(d.trailer["Root"]).(Dict)
	copy := Dict{}
	var refs []int
	keep := firstBool(keepPageDependent)
	for k, v := range root {
		if k == "Pages" || (!preservedCatalogKeys[k] && !(keep && pageDependentCatalogKeys[k])) {
			continue
		}
		copy[k] = v
		collectRefs(v, &refs)
	}
	return copy, refs
}

func firstBool(values []bool) bool {
	return len(values) > 0 && values[0]
}

func preservesPageIdentity(d *Doc, order []pageSel) bool {
	pages, err := d.Pages()
	if err != nil || len(pages) != len(order) {
		return false
	}
	for i, sel := range order {
		if sel.doc != 0 || sel.pg.Num != pages[i].Num {
			return false
		}
	}
	return true
}

// pageMutator lets callers post-process an output page dict during buildDoc,
// e.g. to append content-stream overlays.
type pageMutator func(b *builder, pageIndex int, pd Dict, m map[int]Ref) error

// pageSel names a single output page: page pg from docs[doc].
type pageSel struct {
	doc int
	pg  Page
}

func materializeInheritedPageAttrs(d *Doc, page Page) error {
	pd, ok := d.Get(page.Num).(Dict)
	if !ok {
		return fmt.Errorf("page object %d is not a dictionary", page.Num)
	}
	for _, key := range inheritable {
		if _, exists := pd[key]; exists {
			continue
		}
		if value, inherited := page.Attrs[key]; inherited {
			pd[key] = value
		}
	}
	return nil
}

// buildOrdered assembles a new document containing exactly the pages in
// order, in that order, under a fresh catalog and page tree. Each doc is
// imported once, with roots covering every page selected from it. It
// returns the builder and catalog ref without serializing.
func buildOrdered(docs []*Doc, order []pageSel, mut pageMutator) (*builder, Ref, error) {
	b := &builder{}
	catalogRef := b.alloc() // object 1
	pagesRef := b.alloc()   // object 2

	roots := make([][]int, len(docs))
	for _, sel := range order {
		if err := materializeInheritedPageAttrs(docs[sel.doc], sel.pg); err != nil {
			return nil, Ref{}, err
		}
		roots[sel.doc] = append(roots[sel.doc], sel.pg.Num)
	}
	var catalog Dict
	if len(docs) > 0 {
		var extra []int
		catalog, extra = preservedCatalog(docs[0], preservesPageIdentity(docs[0], order))
		roots[0] = append(roots[0], extra...)
		if info, ok := docs[0].trailer["Info"].(Ref); ok {
			roots[0] = append(roots[0], info.Num)
		}
	}
	maps := make([]map[int]Ref, len(docs))
	for i, d := range docs {
		if len(roots[i]) == 0 {
			continue
		}
		maps[i] = b.importDoc(d, roots[i])
	}

	var kids Array
	for pageIndex, sel := range order {
		m := maps[sel.doc]
		pg := sel.pg
		nr, ok := m[pg.Num]
		if !ok {
			return nil, Ref{}, fmt.Errorf("page object %d not imported", pg.Num)
		}
		pd, ok := b.objs[nr.Num-1].(Dict)
		if !ok {
			return nil, Ref{}, fmt.Errorf("page object %d is not a dict", pg.Num)
		}
		pd["Parent"] = pagesRef
		for k, v := range pg.Attrs {
			if _, exists := pd[k]; !exists {
				pd[k] = translate(v, m)
			}
		}
		if _, ok := pd["MediaBox"]; !ok {
			pd["MediaBox"] = Array{0, 0, 612, 792}
		}
		for k, v := range pg.Force {
			pd[k] = translate(v, m)
		}
		b.objs[nr.Num-1] = pd
		if mut != nil {
			if err := mut(b, pageIndex, pd, m); err != nil {
				return nil, Ref{}, err
			}
		}
		kids = append(kids, nr)
	}

	b.objs[pagesRef.Num-1] = Dict{
		"Type":  Name("Pages"),
		"Kids":  kids,
		"Count": len(kids),
	}
	b.objs[catalogRef.Num-1] = Dict{
		"Type":  Name("Catalog"),
		"Pages": pagesRef,
	}
	if len(catalog) > 0 && maps[0] != nil {
		outCatalog := b.objs[catalogRef.Num-1].(Dict)
		for k, v := range catalog {
			if copied := translate(v, maps[0]); copied != nil {
				outCatalog[k] = copied
			}
		}
	}
	if len(docs) > 0 && maps[0] != nil {
		if info, ok := docs[0].trailer["Info"].(Ref); ok {
			if mapped, ok := maps[0][info.Num]; ok {
				b.infoRef = mapped
			}
		}
	}

	return b, catalogRef, nil
}

// buildDoc assembles a new document containing, for each doc, the selected
// pages (with inherited attributes stamped on), under a fresh catalog and
// page tree. It returns the builder and catalog ref without serializing.
func buildDoc(docs []*Doc, selections [][]Page, mut pageMutator) (*builder, Ref, error) {
	var order []pageSel
	for i, sel := range selections {
		for _, pg := range sel {
			order = append(order, pageSel{doc: i, pg: pg})
		}
	}
	return buildOrdered(docs, order, mut)
}

// build assembles a new document containing, for each doc, the selected
// pages, under a fresh catalog and page tree.
func build(docs []*Doc, selections [][]Page) ([]byte, error) {
	return buildWith(docs, selections, nil)
}

// buildWith is build with a per-page mutation hook, e.g. for content overlays.
func buildWith(docs []*Doc, selections [][]Page, mut pageMutator) ([]byte, error) {
	b, root, err := buildDoc(docs, selections, mut)
	if err != nil {
		return nil, err
	}
	return b.bytes(root)
}

func (b *builder) bytes(root Ref) ([]byte, error) {
	root, err := b.finalize(root)
	if err != nil {
		return nil, err
	}
	return b.writeBytes(root), nil
}

func (b *builder) writeBytes(root Ref) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")

	offsets := make([]int, len(b.objs))
	for i, v := range b.objs {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n", i+1)
		writeObj(&buf, v)
		buf.WriteString("\nendobj\n")
	}

	xrefOff := buf.Len()
	n := len(b.objs)
	fmt.Fprintf(&buf, "xref\n0 %d\n", n+1)
	buf.WriteString("0000000000 65535 f \n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Root %d 0 R /Size %d", root.Num, n+1)
	if b.infoRef.Num != 0 {
		fmt.Fprintf(&buf, " /Info %d 0 R", b.infoRef.Num)
	}
	if b.encryptRef.Num != 0 {
		fmt.Fprintf(&buf, " /Encrypt %d 0 R", b.encryptRef.Num)
	}
	if b.id != nil {
		buf.WriteString(" /ID [")
		writeObj(&buf, b.id)
		buf.WriteByte(' ')
		writeObj(&buf, b.id)
		buf.WriteByte(']')
	}
	fmt.Fprintf(&buf, " >>\nstartxref\n%d\n%%%%EOF\n", xrefOff)
	return buf.Bytes()
}

func writeName(w *bytes.Buffer, n Name) {
	w.WriteByte('/')
	for i := 0; i < len(n); i++ {
		c := n[i]
		if c <= 0x20 || c >= 0x7f || isDelim(c) || c == '#' {
			fmt.Fprintf(w, "#%02X", c)
		} else {
			w.WriteByte(c)
		}
	}
}

func writeObj(w *bytes.Buffer, v any) {
	switch t := v.(type) {
	case nil:
		w.WriteString("null")
	case bool:
		if t {
			w.WriteString("true")
		} else {
			w.WriteString("false")
		}
	case int:
		w.WriteString(strconv.Itoa(t))
	case float64:
		w.WriteString(strconv.FormatFloat(t, 'f', -1, 64))
	case Ref:
		fmt.Fprintf(w, "%d %d R", t.Num, t.Gen)
	case Name:
		writeName(w, t)
	case String:
		w.WriteByte('<')
		for _, c := range t {
			fmt.Fprintf(w, "%02X", c)
		}
		w.WriteByte('>')
	case Array:
		w.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				w.WriteByte(' ')
			}
			writeObj(w, e)
		}
		w.WriteByte(']')
	case Dict:
		w.WriteString("<<")
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, string(k))
		}
		sort.Strings(keys)
		for _, k := range keys {
			w.WriteByte(' ')
			writeName(w, Name(k))
			w.WriteByte(' ')
			writeObj(w, t[Name(k)])
		}
		w.WriteString(" >>")
	case *Stream:
		writeObj(w, t.Dict)
		w.WriteString("\nstream\n")
		w.Write(t.Data)
		w.WriteString("\nendstream")
	default:
		fmt.Fprintf(w, "null")
	}
}

// -------------------------------------------------------------- public ---

// Merge concatenates all pages of all files, in order, into one PDF.
func Merge(files [][]byte) ([]byte, error) {
	if len(files) < 2 {
		return nil, fmt.Errorf("merge requires at least 2 files")
	}
	docs := make([]*Doc, len(files))
	selections := make([][]Page, len(files))
	for i, f := range files {
		d, err := Parse(f)
		if err != nil {
			return nil, fmt.Errorf("file %d: %w", i, err)
		}
		pages, err := d.Pages()
		if err != nil {
			return nil, fmt.Errorf("file %d: %w", i, err)
		}
		docs[i] = d
		selections[i] = pages
	}
	return build(docs, selections)
}

// Split extracts the requested page ranges from file into a new PDF.
func Split(file []byte, ranges string) ([]byte, error) {
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}
	nums, err := ParseRanges(ranges, len(pages))
	if err != nil {
		return nil, err
	}
	sel := make([]Page, len(nums))
	for i, n := range nums {
		sel[i] = pages[n-1]
	}
	return build([]*Doc{d}, [][]Page{sel})
}

// Reorder rearranges all pages, requiring each page number exactly once.
func Reorder(file []byte, order string) ([]byte, error) {
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}
	nums, err := ParseRanges(order, len(pages))
	if err != nil {
		return nil, err
	}
	if len(nums) != len(pages) {
		return nil, fmt.Errorf("reorder must include every page exactly once")
	}
	seen := map[int]bool{}
	sel := make([]Page, len(nums))
	for i, n := range nums {
		if seen[n] {
			return nil, fmt.Errorf("reorder must include every page exactly once")
		}
		seen[n] = true
		sel[i] = pages[n-1]
	}
	return build([]*Doc{d}, [][]Page{sel})
}

// ParseRanges parses a comma-separated list of 1-based page ranges
// ("a", "a-b", or "a-" for open-ended) against a document of n pages.
func ParseRanges(s string, n int) ([]int, error) {
	var out []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var lo, hi int
		if idx := strings.Index(p, "-"); idx >= 0 {
			loStr := strings.TrimSpace(p[:idx])
			hiStr := strings.TrimSpace(p[idx+1:])
			var err error
			lo, err = strconv.Atoi(loStr)
			if err != nil {
				return nil, fmt.Errorf("invalid range %q", p)
			}
			if hiStr == "" {
				hi = n
			} else {
				hi, err = strconv.Atoi(hiStr)
				if err != nil {
					return nil, fmt.Errorf("invalid range %q", p)
				}
			}
		} else {
			v, err := strconv.Atoi(p)
			if err != nil {
				return nil, fmt.Errorf("invalid page %q", p)
			}
			lo, hi = v, v
		}
		if lo < 1 || hi > n || lo > hi {
			return nil, fmt.Errorf("range %q out of bounds (1-%d)", p, n)
		}
		for i := lo; i <= hi; i++ {
			out = append(out, i)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no pages selected")
	}
	return out, nil
}

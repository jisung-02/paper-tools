package pdf

import (
	"fmt"
)

// ------------------------------------------------------------ resolution ---

// rnum converts a PDF numeric (int/float64) to float64.
func rnum(v any) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case float64:
		return t, true
	}
	return 0, false
}

// rv resolves a possibly-indirect value against the builder's own objects.
func (b *builder) rv(v any) any {
	hops := 0
	for {
		r, ok := v.(Ref)
		if !ok {
			return v
		}
		if hops >= 64 || r.Num < 1 || r.Num > len(b.objs) {
			return nil
		}
		hops++
		v = b.objs[r.Num-1]
	}
}

// rect resolves v (Array of 4 numerics, possibly behind Refs) to x0,y0,x1,y1
// normalized so x0<x1, y0<y1.
func (b *builder) rect(v any) (x0, y0, x1, y1 float64, ok bool) {
	arr, isArr := b.rv(v).(Array)
	if !isArr || len(arr) != 4 {
		return 0, 0, 0, 0, false
	}
	vals := make([]float64, 4)
	for i, e := range arr {
		n, isNum := rnum(b.rv(e))
		if !isNum {
			return 0, 0, 0, 0, false
		}
		vals[i] = n
	}
	x0, y0, x1, y1 = vals[0], vals[1], vals[2], vals[3]
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	return x0, y0, x1, y1, true
}

// ------------------------------------------------------------ resources ---

// ensureResources returns a Resources dict owned by this page, copying a
// shared or indirect one first (copy-on-write) and storing it directly on pd.
func (b *builder) ensureResources(pd Dict) Dict {
	res, ok := pd["Resources"].(Dict)
	if !ok {
		if shared, isShared := b.rv(pd["Resources"]).(Dict); isShared {
			res = make(Dict, len(shared))
			for k, v := range shared {
				res[k] = v
			}
		} else {
			res = Dict{}
		}
		pd["Resources"] = res
	}
	return res
}

// ownedSub does the same one level deeper for Resources subdicts like Font,
// ExtGState, XObject.
func (b *builder) ownedSub(res Dict, key Name) Dict {
	sub := Dict{}
	if existing, ok := b.rv(res[key]).(Dict); ok {
		sub = make(Dict, len(existing))
		for k, v := range existing {
			sub[k] = v
		}
	}
	res[key] = sub
	return sub
}

// ------------------------------------------------------------- content ---

// wrapContent puts pre ops before and post ops after the page's existing
// content streams: Contents = [preStream, ...orig, postStream].
func (b *builder) wrapContent(pd Dict, pre, post []byte) {
	preStream := &Stream{Dict: Dict{"Length": len(pre)}, Data: pre}
	postStream := &Stream{Dict: Dict{"Length": len(post)}, Data: post}

	preRef := b.alloc()
	b.objs[preRef.Num-1] = preStream
	postRef := b.alloc()
	b.objs[postRef.Num-1] = postStream

	var contents Array
	switch c := pd["Contents"].(type) {
	case nil:
		contents = Array{preRef, postRef}
	case Ref:
		switch resolved := b.rv(c).(type) {
		case Array:
			contents = append(Array{preRef}, resolved...)
			contents = append(contents, postRef)
		case *Stream:
			contents = Array{preRef, c, postRef}
		default:
			contents = Array{preRef, postRef}
		}
	case Array:
		contents = append(Array{preRef}, c...)
		contents = append(contents, postRef)
	case *Stream:
		csRef := b.alloc()
		b.objs[csRef.Num-1] = c
		contents = Array{preRef, csRef, postRef}
	default:
		contents = Array{preRef, postRef}
	}
	pd["Contents"] = contents
}

// appendContent wraps the page's existing content stream(s) in q/Q and
// appends ops as a new stream.
//
// ponytail: overlay streams are stored uncompressed, they are tiny
func (b *builder) appendContent(pd Dict, ops []byte) {
	b.wrapContent(pd, []byte("q\n"), append([]byte("Q\n"), ops...))
}

// --------------------------------------------------------------- text ---

// escapeText encodes s for a PDF literal string in WinAnsi-ish Latin-1.
// Runes > 255 return an error.
func escapeText(s string) (string, error) {
	var out []byte
	for _, r := range s {
		if r > 255 {
			return "", fmt.Errorf("only Latin-1 text is supported (got %q)", s)
		}
		switch r {
		case '\\':
			out = append(out, '\\', '\\')
		case '(':
			out = append(out, '\\', '(')
		case ')':
			out = append(out, '\\', ')')
		default:
			if r >= 128 {
				out = append(out, '\\', byte('0'+(r>>6)&7), byte('0'+(r>>3)&7), byte('0'+r&7))
			} else {
				out = append(out, byte(r))
			}
		}
	}
	return string(out), nil
}

// helveticaRef lazily allocates a shared /Type1 Helvetica font object and
// caches its ref in the builder.
func (b *builder) helveticaRef() Ref {
	if b.fontRef != (Ref{}) {
		return b.fontRef
	}
	r := b.alloc()
	b.objs[r.Num-1] = Dict{
		"Type":     Name("Font"),
		"Subtype":  Name("Type1"),
		"BaseFont": Name("Helvetica"),
		"Encoding": Name("WinAnsiEncoding"),
	}
	b.fontRef = r
	return r
}

// gstateRef lazily allocates a shared ExtGState object with /ca /CA = alpha
// and caches its ref in the builder.
func (b *builder) gstateRef(alpha float64) Ref {
	if b.gsRef != (Ref{}) {
		return b.gsRef
	}
	r := b.alloc()
	b.objs[r.Num-1] = Dict{
		"Type": Name("ExtGState"),
		"ca":   alpha,
		"CA":   alpha,
	}
	b.gsRef = r
	return r
}

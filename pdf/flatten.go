package pdf

import (
	"fmt"
	"sort"
)

// Flatten draws annotation/widget appearance streams into page content and
// removes page annotations from the output document. Annotations without a
// normal appearance stream cannot be rendered by this lightweight PDF engine,
// so they are removed without drawing.
func Flatten(file []byte) ([]byte, error) {
	d, err := Parse(file)
	if err != nil {
		return nil, err
	}
	pages, err := d.Pages()
	if err != nil {
		return nil, err
	}

	mut := func(b *builder, pageIndex int, pd Dict, m map[int]Ref) error {
		annots, ok := b.rv(pd["Annots"]).(Array)
		if !ok || len(annots) == 0 {
			delete(pd, "Annots")
			return nil
		}

		res := b.ensureResources(pd)
		xobjs := b.ownedSub(res, "XObject")
		var ops []byte
		nextName := 0
		for _, av := range annots {
			ad, ok := b.rv(av).(Dict)
			if !ok {
				continue
			}
			x0, y0, x1, y1, ok := b.rect(ad["Rect"])
			if !ok {
				continue
			}
			apRef, ap, ok := b.normalAppearance(ad)
			if !ok {
				continue
			}

			b.prepareFormAppearance(ap, x1-x0, y1-y0)
			if apRef == (Ref{}) {
				apRef = b.alloc()
				b.objs[apRef.Num-1] = ap
			}

			bx0, by0, bx1, by1, ok := b.rect(ap.Dict["BBox"])
			if !ok {
				bx0, by0, bx1, by1 = 0, 0, x1-x0, y1-y0
			}
			bw, bh := bx1-bx0, by1-by0
			if bw <= 0 || bh <= 0 {
				continue
			}
			sx, sy := (x1-x0)/bw, (y1-y0)/bh
			tx, ty := x0-bx0*sx, y0-by0*sy
			name := uniqueResourceName(xobjs, "FltAnn", &nextName)
			xobjs[name] = apRef
			ops = append(ops, []byte(fmt.Sprintf("q %.2f 0 0 %.2f %.2f %.2f cm /%s Do Q\n", sx, sy, tx, ty, name))...)
		}

		delete(pd, "Annots")
		if len(ops) > 0 {
			b.appendContent(pd, ops)
		}
		return nil
	}

	return buildWith([]*Doc{d}, [][]Page{pages}, mut)
}

func uniqueResourceName(resources Dict, prefix string, next *int) Name {
	for {
		name := Name(fmt.Sprintf("%s%d", prefix, *next))
		*next = *next + 1
		if _, exists := resources[name]; !exists {
			return name
		}
	}
}

func (b *builder) normalAppearance(ad Dict) (Ref, *Stream, bool) {
	ap, ok := b.rv(ad["AP"]).(Dict)
	if !ok {
		return Ref{}, nil, false
	}
	n := ap["N"]
	if r, ok := n.(Ref); ok {
		if st, ok := b.rv(r).(*Stream); ok {
			return r, st, true
		}
	}
	if st, ok := b.rv(n).(*Stream); ok {
		return Ref{}, st, true
	}
	choices, ok := b.rv(n).(Dict)
	if !ok {
		return Ref{}, nil, false
	}
	if as, ok := b.rv(ad["AS"]).(Name); ok {
		if r, ok := choices[as].(Ref); ok {
			if st, ok := b.rv(r).(*Stream); ok {
				return r, st, true
			}
		}
		if st, ok := b.rv(choices[as]).(*Stream); ok {
			return Ref{}, st, true
		}
	}
	keys := make([]string, 0, len(choices))
	for k := range choices {
		if k != "Off" {
			keys = append(keys, string(k))
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := choices[Name(k)]
		if r, ok := v.(Ref); ok {
			if st, ok := b.rv(r).(*Stream); ok {
				return r, st, true
			}
			continue
		}
		if st, ok := b.rv(v).(*Stream); ok {
			return Ref{}, st, true
		}
	}
	return Ref{}, nil, false
}

func (b *builder) prepareFormAppearance(st *Stream, width, height float64) {
	if st.Dict == nil {
		st.Dict = Dict{}
	}
	st.Dict["Type"] = Name("XObject")
	st.Dict["Subtype"] = Name("Form")
	if _, ok := st.Dict["BBox"]; !ok {
		st.Dict["BBox"] = Array{0, 0, width, height}
	}
	st.Dict["Length"] = len(st.Data)
}

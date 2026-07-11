package pdf

import "fmt"

// finalize removes objects no longer reachable from the output trailer roots,
// rewrites every surviving reference, and seals the builder for serialization.
// It must run before object-number-dependent encryption.
func (b *builder) finalize(root Ref) (Ref, error) {
	if b.finalized {
		return root, nil
	}
	if root.Num == 0 {
		return Ref{}, fmt.Errorf("missing output root")
	}

	live := make(map[int]bool, len(b.objs))
	var visitValue func(any) error
	var visitRef func(Ref) error
	visitRef = func(ref Ref) error {
		if ref.Num <= 0 || ref.Num > len(b.objs) || b.objs[ref.Num-1] == nil {
			return fmt.Errorf("dangling reference %d %d R", ref.Num, ref.Gen)
		}
		if live[ref.Num] {
			return nil
		}
		live[ref.Num] = true
		return visitValue(b.objs[ref.Num-1])
	}
	visitValue = func(v any) error {
		switch t := v.(type) {
		case Ref:
			return visitRef(t)
		case Dict:
			for _, vv := range t {
				if err := visitValue(vv); err != nil {
					return err
				}
			}
		case Array:
			for _, vv := range t {
				if err := visitValue(vv); err != nil {
					return err
				}
			}
		case *Stream:
			return visitValue(t.Dict)
		}
		return nil
	}

	if err := visitRef(root); err != nil {
		return Ref{}, err
	}
	if b.infoRef.Num != 0 {
		if err := visitRef(b.infoRef); err != nil {
			return Ref{}, err
		}
	}
	if b.encryptRef.Num != 0 {
		if err := visitRef(b.encryptRef); err != nil {
			return Ref{}, err
		}
	}

	refs := make(map[int]Ref, len(live))
	for old := 1; old <= len(b.objs); old++ {
		if live[old] {
			refs[old] = Ref{Num: len(refs) + 1}
		}
	}
	var rewrite func(any) (any, error)
	rewrite = func(v any) (any, error) {
		switch t := v.(type) {
		case Ref:
			r, ok := refs[t.Num]
			if !ok {
				return nil, fmt.Errorf("reference %d %d R is not live", t.Num, t.Gen)
			}
			return r, nil
		case Dict:
			out := make(Dict, len(t))
			for k, vv := range t {
				rv, err := rewrite(vv)
				if err != nil {
					return nil, err
				}
				out[k] = rv
			}
			return out, nil
		case Array:
			out := make(Array, len(t))
			for i, vv := range t {
				rv, err := rewrite(vv)
				if err != nil {
					return nil, err
				}
				out[i] = rv
			}
			return out, nil
		case *Stream:
			d, err := rewrite(t.Dict)
			if err != nil {
				return nil, err
			}
			return &Stream{Dict: d.(Dict), Data: t.Data}, nil
		default:
			return v, nil
		}
	}

	objs := make([]any, 0, len(refs))
	for old := 1; old <= len(b.objs); old++ {
		if !live[old] {
			continue
		}
		v, err := rewrite(b.objs[old-1])
		if err != nil {
			return Ref{}, err
		}
		objs = append(objs, v)
	}
	b.objs = objs
	b.infoRef = refs[b.infoRef.Num]
	b.encryptRef = refs[b.encryptRef.Num]
	b.finalized = true
	return refs[root.Num], nil
}

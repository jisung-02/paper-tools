package pdf

import (
	"encoding/ascii85"
	"errors"
	"fmt"
)

var (
	ErrUnsupportedFilter    = errors.New("unsupported stream filter")
	ErrFilterDecodeParms    = errors.New("misaligned stream DecodeParms")
	ErrFilterOutputTooLarge = errors.New("decoded stream output exceeds limit")
)

func decodeStreamWithLimit(resolve func(any) any, s *Stream, limit int) ([]byte, error) {
	filters, err := streamFilters(resolve, s.Dict["Filter"])
	if err != nil {
		return nil, err
	}
	if len(filters) == 0 {
		if len(s.Data) > limit {
			return nil, ErrFilterOutputTooLarge
		}
		return s.Data, nil
	}
	parms, err := streamDecodeParms(resolve, s.Dict, len(filters))
	if err != nil {
		return nil, err
	}

	data := s.Data
	used := 0
	for i, filter := range filters {
		remaining := limit - used
		if remaining < 0 {
			return nil, ErrFilterOutputTooLarge
		}
		data, err = decodeFilterStage(resolve, filter, parms[i], data, remaining)
		if err != nil {
			return nil, err
		}
		if len(data) > remaining {
			return nil, ErrFilterOutputTooLarge
		}
		used += len(data)
	}
	return data, nil
}

func streamFilters(resolve func(any) any, value any) ([]Name, error) {
	value = resolve(value)
	if value == nil {
		return nil, nil
	}
	switch v := value.(type) {
	case Name:
		return []Name{v}, nil
	case Array:
		filters := make([]Name, len(v))
		for i, item := range v {
			name, ok := resolve(item).(Name)
			if !ok {
				return nil, fmt.Errorf("%w: non-name stage", ErrUnsupportedFilter)
			}
			filters[i] = name
		}
		return filters, nil
	default:
		return nil, fmt.Errorf("%w: invalid Filter", ErrUnsupportedFilter)
	}
}

func streamDecodeParms(resolve func(any) any, dict Dict, count int) ([]Dict, error) {
	value := resolve(dict["DecodeParms"])
	if value == nil {
		value = resolve(dict["DP"])
	}
	parms := make([]Dict, count)
	if value == nil {
		return parms, nil
	}
	if one, ok := value.(Dict); ok {
		if count != 1 {
			return nil, ErrFilterDecodeParms
		}
		parms[0] = one
		return parms, nil
	}
	array, ok := value.(Array)
	if !ok || len(array) != count {
		return nil, ErrFilterDecodeParms
	}
	for i, item := range array {
		item = resolve(item)
		if item == nil {
			continue
		}
		parm, ok := item.(Dict)
		if !ok {
			return nil, ErrFilterDecodeParms
		}
		parms[i] = parm
	}
	return parms, nil
}

func decodeFilterStage(resolve func(any) any, filter Name, parms Dict, data []byte, limit int) ([]byte, error) {
	var (
		out []byte
		err error
	)
	switch filter {
	case "ASCIIHexDecode", "AHx":
		out, err = decodeASCIIHex(data, limit)
	case "ASCII85Decode", "A85":
		out, err = decodeASCII85(data, limit)
	case "FlateDecode", "Fl":
		out, err = inflateWithLimit(data, limit)
		if err == nil && parms != nil {
			out, err = applyPredictor(resolve, parms, out)
		}
	case "RunLengthDecode", "RL":
		out, err = decodeRunLength(data, limit)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFilter, filter)
	}
	if err != nil {
		return nil, err
	}
	if len(out) > limit {
		return nil, ErrFilterOutputTooLarge
	}
	return out, nil
}

func decodeASCIIHex(data []byte, limit int) ([]byte, error) {
	out := make([]byte, 0, len(data)/2)
	var high byte
	haveHigh := false
	ended := false
	for _, c := range data {
		if isWS(c) {
			continue
		}
		if c == '>' {
			ended = true
			break
		}
		if !isHex(c) {
			return nil, fmt.Errorf("invalid ASCIIHex data")
		}
		if !haveHigh {
			high = hexVal(c)
			haveHigh = true
			continue
		}
		if len(out) >= limit {
			return nil, ErrFilterOutputTooLarge
		}
		out = append(out, high<<4|hexVal(c))
		haveHigh = false
	}
	if !ended {
		return nil, fmt.Errorf("unterminated ASCIIHex data")
	}
	if haveHigh {
		if len(out) >= limit {
			return nil, ErrFilterOutputTooLarge
		}
		out = append(out, high<<4)
	}
	return out, nil
}

func decodeASCII85(data []byte, limit int) ([]byte, error) {
	clean := make([]byte, 0, len(data))
	terminated := false
	for i := 0; i < len(data); i++ {
		if isWS(data[i]) {
			continue
		}
		if data[i] == '~' && i+1 < len(data) && data[i+1] == '>' {
			terminated = true
			break
		}
		clean = append(clean, data[i])
	}
	if !terminated {
		return nil, fmt.Errorf("unterminated ASCII85 data")
	}
	out := make([]byte, len(clean))
	n, _, err := ascii85.Decode(out, clean, true)
	if err != nil {
		return nil, fmt.Errorf("decode ASCII85: %w", err)
	}
	if n > limit {
		return nil, ErrFilterOutputTooLarge
	}
	return out[:n], nil
}

func decodeRunLength(data []byte, limit int) ([]byte, error) {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		length := data[i]
		i++
		if length == 128 {
			return out, nil
		}
		if length <= 127 {
			n := int(length) + 1
			if n > len(data)-i {
				return nil, fmt.Errorf("truncated RunLength data")
			}
			if n > limit-len(out) {
				return nil, ErrFilterOutputTooLarge
			}
			out = append(out, data[i:i+n]...)
			i += n
			continue
		}
		if i >= len(data) {
			return nil, fmt.Errorf("truncated RunLength data")
		}
		n := 257 - int(length)
		if n > limit-len(out) {
			return nil, ErrFilterOutputTooLarge
		}
		for j := 0; j < n; j++ {
			out = append(out, data[i])
		}
		i++
	}
	return nil, fmt.Errorf("unterminated RunLength data")
}

func applyPredictor(resolve func(any) any, parms Dict, data []byte) ([]byte, error) {
	predictor := 1
	columns := 1
	colors := 1
	bpc := 8
	if v, ok := resolve(parms["Predictor"]).(int); ok {
		predictor = v
	}
	if v, ok := resolve(parms["Columns"]).(int); ok {
		columns = v
	}
	if v, ok := resolve(parms["Colors"]).(int); ok {
		colors = v
	}
	if v, ok := resolve(parms["BitsPerComponent"]).(int); ok {
		bpc = v
	}
	return unpredict(data, predictor, columns, colors, bpc)
}

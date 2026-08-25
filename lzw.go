package reader

import "fmt"

// LZW code points that are not table entries.
const (
	lzwClear = 256
	lzwEOD   = 257
	lzwFirst = 258
	lzwMax   = 4096
)

// lzwDecode expands PDF's variable-code-width LZW.
//
// The standard library's compress/lzw cannot be used here: PDF's /EarlyChange
// parameter defaults to 1, which widens the code one entry before the table
// actually fills, and compress/lzw implements neither that convention nor a
// way to ask for it. Getting this wrong does not fail — it silently produces
// plausible rubbish a few hundred bytes in.
func lzwDecode(data []byte, early bool) ([]byte, error) {
	var table [lzwMax][]byte
	for i := 0; i < 256; i++ {
		table[i] = []byte{byte(i)}
	}
	next := lzwFirst
	width := 9
	bit := 0
	var out, prev []byte

	for {
		if bit+width > len(data)*8 {
			// A truncated stream keeps what it managed to say.
			return out, nil
		}
		code := 0
		for k := 0; k < width; k++ {
			code = code<<1 | int(data[(bit+k)/8]>>(7-(bit+k)%8)&1)

		}
		bit += width

		switch code {
		case lzwEOD:
			return out, nil
		case lzwClear:
			next, width, prev = lzwFirst, 9, nil
			continue
		}

		var entry []byte
		switch {
		case code < next && table[code] != nil:
			entry = table[code]
		case code == next && prev != nil:
			// The encoder may name the entry it is about to define.
			entry = append(append([]byte{}, prev...), prev[0])
		default:
			return nil, fmt.Errorf("reader: LZWDecode: code %d is not in the table", code)
		}
		out = append(out, entry...)

		if prev != nil && next < lzwMax {
			table[next] = append(append([]byte{}, prev...), entry[0])
			next++
		}
		prev = entry

		// With EarlyChange the width grows one entry sooner. Both conventions
		// are the same comparison against a shifted count.
		count := next
		if early {
			count++
		}
		switch {
		case count >= 2048:
			width = 12
		case count >= 1024:
			width = 11
		case count >= 512:
			width = 10
		default:
			width = 9
		}
	}
}

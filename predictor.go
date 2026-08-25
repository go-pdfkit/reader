package reader

import "fmt"

// applyPredictor undoes the PNG or TIFF predictor a Flate or LZW stream was
// filtered through. /Predictor 1, and an absent parameter dictionary, mean
// there is nothing to undo.
func applyPredictor(data []byte, parm Dict, r Resolver) ([]byte, error) {
	pred := intParm(parm, "Predictor", 1, r)
	if pred <= 1 {
		return data, nil
	}
	colors := intParm(parm, "Colors", 1, r)
	bpc := intParm(parm, "BitsPerComponent", 8, r)
	columns := intParm(parm, "Columns", 1, r)
	if colors < 1 || bpc < 1 || columns < 1 {
		return nil, fmt.Errorf("reader: predictor: /Colors %d /BitsPerComponent %d /Columns %d", colors, bpc, columns)
	}
	switch {
	case pred == 2:
		return tiffPredictor(data, colors, bpc, columns)
	case pred >= 10:
		return pngPredictor(data, colors, bpc, columns)
	}
	return nil, fmt.Errorf("reader: predictor: /Predictor %d is not defined", pred)
}

// rowGeometry gives the bytes per row and the byte distance between a sample
// and the one to its left, both rounded up as the specification requires.
func rowGeometry(colors, bpc, columns int) (rowLen, bpp int) {
	return (colors*bpc*columns + 7) / 8, (colors*bpc + 7) / 8
}

// pngPredictor undoes the per-row filters PNG defines, each row of the stream
// being preceded by its filter type byte.
func pngPredictor(data []byte, colors, bpc, columns int) ([]byte, error) {
	rowLen, bpp := rowGeometry(colors, bpc, columns)
	prev := make([]byte, rowLen)
	cur := make([]byte, rowLen)
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		ft := data[i]
		i++
		n := copy(cur, data[i:])
		// A damaged file can end mid-row; treat the missing bytes as zero
		// rather than dropping the row.
		clear(cur[n:])
		i += n
		switch ft {
		case 0:
		case 1:
			for k := bpp; k < rowLen; k++ {
				cur[k] += cur[k-bpp]
			}
		case 2:
			for k := 0; k < rowLen; k++ {
				cur[k] += prev[k]
			}
		case 3:
			for k := 0; k < rowLen; k++ {
				left := 0
				if k >= bpp {
					left = int(cur[k-bpp])
				}
				cur[k] += byte((left + int(prev[k])) / 2)
			}
		case 4:
			for k := 0; k < rowLen; k++ {
				var left, upLeft byte
				if k >= bpp {
					left, upLeft = cur[k-bpp], prev[k-bpp]
				}
				cur[k] += paeth(left, prev[k], upLeft)
			}
		default:
			return nil, fmt.Errorf("reader: predictor: unknown PNG row filter %d", ft)
		}
		out = append(out, cur...)
		copy(prev, cur)
	}
	return out, nil
}

// paeth is the PNG predictor of that name: the neighbour closest to a+b-c.
func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

// abs is the integer absolute value.
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// tiffPredictor undoes horizontal differencing. Only the 8- and 16-bit
// component depths are implemented, which is every one seen in practice; a
// sub-byte depth is reported rather than silently mis-decoded.
func tiffPredictor(data []byte, colors, bpc, columns int) ([]byte, error) {
	if bpc != 8 && bpc != 16 {
		return nil, fmt.Errorf("reader: predictor: TIFF differencing with /BitsPerComponent %d is not supported", bpc)
	}
	rowLen, bpp := rowGeometry(colors, bpc, columns)
	out := append([]byte{}, data...)
	for r := 0; r+rowLen <= len(out); r += rowLen {
		row := out[r : r+rowLen]
		if bpc == 8 {
			for k := bpp; k < rowLen; k++ {
				row[k] += row[k-bpp]
			}
			continue
		}
		for k := bpp; k+1 < rowLen; k += 2 {
			v := uint16(row[k])<<8 | uint16(row[k+1])
			p := uint16(row[k-bpp])<<8 | uint16(row[k-bpp+1])
			v += p
			row[k], row[k+1] = byte(v>>8), byte(v)
		}
	}
	return out, nil
}

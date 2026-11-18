package reader

import "fmt"

// maxUnbackedRow is how long a predictor row may be without the stream
// carrying at least that many bytes. It is a var so a test can shrink it, the
// way maxDecodedSize already is in this package.
//
// ⛔ IT IS A FLOOR, NOT A SIZE CAP, and the difference is what maxDecodedSize
// could not see: that cap is 1 GiB and the row that prompted this was 932 MiB,
// just under it. An absolute limit cannot express "a hundred bytes must not
// become a gigabyte" — only a comparison with the data present can.
var maxUnbackedRow = 1 << 20

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

	// ⛔⛔⛔ A ROW LONGER THAN THE WHOLE STREAM IS A BOMB, NOT A ROW. /Columns,
	// /Colors and /BitsPerComponent are numbers the FILE chose, and their
	// product is a buffer size: pngPredictor allocates two rows of it and then
	// walks every byte. 102 bytes carrying
	//
	//	/DecodeParms << /Columns 977777777 /Predictor 10 >>
	//
	// asks for a row of about 932 MiB — two of them, plus a per-byte pass over
	// each — from a file that fits in a tweet. Found by fuzzing the writer
	// (#69), filed as #70, and the amplification is about ten million to one.
	//
	// ⛔ maxDecodedSize DID NOT CATCH IT, and the near miss is the point: that
	// cap is 1 GiB and this row is 932 MiB, just under. A bound on the absolute
	// size cannot see a ratio; this one is relative to the bytes actually
	// present, which is what makes it a bound on AMPLIFICATION.
	//
	// ⛔⛔ THE FIRST VERSION OF THIS GUARD WAS `rowLen > len(data)` AND THE
	// SUITE REFUTED IT IMMEDIATELY. TestPNGPredictorTruncatedRow decodes
	// /Columns 4 from three bytes — one filter byte and two of data — so a
	// legitimately truncated single row ALREADY has a row longer than the whole
	// stream. The comment here claimed the opposite ("a truncated last row
	// still leaves rowLen no larger than the data that preceded it"), which is
	// false for a stream holding exactly one row. Salvage is deliberate: #16
	// zero-fills what a damaged stream ends inside of.
	//
	// ⭐ SO THE BOUND IS ON AMPLIFICATION ABOVE A FLOOR, not on the ratio
	// alone: under a megabyte, inventing the zeros a damaged row is missing
	// costs nothing worth defending; above it, a row must be BACKED by at
	// least as many bytes as it claims. 4 bytes from 3 is salvage; 932 MiB
	// from 102 is a bomb, and nothing real sits between them.
	if rowLen, _ := rowGeometry(colors, bpc, columns); rowLen > maxUnbackedRow &&
		rowLen > len(data) {
		return nil, fmt.Errorf("reader: predictor: /Colors %d /BitsPerComponent %d "+
			"/Columns %d make a row of %d bytes and the stream holds %d, so a row "+
			"past %d bytes is not backed by the data it claims",
			colors, bpc, columns, rowLen, len(data), maxUnbackedRow)
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

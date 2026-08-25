package reader

import "bytes"

// inlineImageEnd finds where an inline image's data stops — the one genuinely
// ambiguous thing in a content stream, since the data is raw bytes that may
// spell EI themselves.
//
// The answers are tried in order of how much they can be trusted: an image
// with no filter says exactly how long it is through its width, height, depth
// and colour space; a declared /L is believed when an EI really does follow
// it; and failing both, every EI in the data is tried in turn and the first
// one whose data actually decodes is taken. That last step is what a scan
// alone gets wrong, because compressed bytes do spell EI by accident.
func inlineImageEnd(b []byte, start int, d Dict) (int, error) {
	if n, ok := inlineImageDataLength(d); ok && start+n <= len(b) && eiFollows(b, start+n) {
		return start + n, nil
	}
	if n, ok := ToInt(d.Get("L")); ok && n >= 0 && start+int(n) <= len(b) && eiFollows(b, start+int(n)) {
		return start + int(n), nil
	}
	expanded := (&InlineImage{Dict: d}).Expanded()
	want, haveWant := inlineSampleBytes(d)
	// White-space before EI is how the keyword is meant to be written; a
	// second pass allows for producers that run the data straight into it.
	for _, needSpace := range []bool{true, false} {
		for from := start; ; {
			end, resume := nextEI(b, start, from, needSpace)
			if end < 0 {
				break
			}
			if inlineDataDecodes(expanded, b[start:end], want, haveWant) {
				return end, nil
			}
			from = resume
		}
	}
	return 0, &SyntaxError{start, "an inline image has no EI"}
}

// nextEI returns where the image data would end for the next EI candidate at
// or after from, and where to resume searching. It reports -1 when there is no
// candidate left.
func nextEI(b []byte, start, from int, needSpace bool) (end, resume int) {
	for i := from; i+1 < len(b); i++ {
		if b[i] != 'E' || b[i+1] != 'I' {
			continue
		}
		if i+2 < len(b) && isRegular(b[i+2]) {
			continue
		}
		if i > start && isSpace(b[i-1]) {
			return i - 1, i + 2
		}
		if !needSpace {
			return i, i + 2
		}
	}
	return -1, len(b)
}

// eiFollows reports whether an EI keyword stands at i, after white-space.
func eiFollows(b []byte, i int) bool {
	for i < len(b) && isSpace(b[i]) {
		i++
	}
	if !bytes.HasPrefix(b[i:], []byte("EI")) {
		return false
	}
	return i+2 >= len(b) || !isRegular(b[i+2])
}

// inlineDataDecodes reports whether a candidate stretch of bytes is really the
// whole of the image: it has to run through the declared filters without
// complaint, produce the number of samples the image says it has, and — for a
// JPEG, which no filter here can check — begin and end where a JPEG does.
func inlineDataDecodes(expanded Dict, data []byte, want int, haveWant bool) bool {
	if expanded.Get("Filter").Kind() == KindNull {
		return true
	}
	out, img, err := Decode(expanded, data, nil)
	if err != nil {
		return false
	}
	if img == "DCTDecode" {
		return len(out) > 4 && out[0] == 0xFF && out[1] == 0xD8 &&
			out[len(out)-2] == 0xFF && out[len(out)-1] == 0xD9
	}
	if img != "" {
		return true
	}
	return !haveWant || len(out) == want
}

// inlineComponents reports how many samples one pixel of an inline image has,
// for the colour spaces that can be named inline.
func inlineComponents(d Dict) (int, bool) {
	if b, ok := ToBool(d.Get("IM")); ok && b {
		return 1, true
	}
	cs, ok := ToName(d.Get("CS"))
	if !ok {
		if _, isArray := ToArray(d.Get("CS")); isArray {
			// An inline [/Indexed …] space has one component per sample.
			return 1, true
		}
		return 0, false
	}
	switch cs {
	case "G", "DeviceGray", "CalGray", "I", "Indexed":
		return 1, true
	case "RGB", "DeviceRGB", "CalRGB":
		return 3, true
	case "CMYK", "DeviceCMYK":
		return 4, true
	}
	// A name that refers to a colour space in the page's resources: not
	// something the arithmetic can settle here.
	return 0, false
}

// inlineSampleBytes computes how many bytes an inline image's samples occupy
// once decoded.
func inlineSampleBytes(d Dict) (int, bool) {
	w, okW := ToInt(d.Get("W"))
	h, okH := ToInt(d.Get("H"))
	if !okW || !okH || w <= 0 || h <= 0 {
		return 0, false
	}
	bpc := int64(8)
	if b, ok := ToInt(d.Get("BPC")); ok {
		bpc = b
	}
	if im, ok := ToBool(d.Get("IM")); ok && im {
		bpc = 1
	}
	if bpc <= 0 || bpc > 16 {
		return 0, false
	}
	comps, ok := inlineComponents(d)
	if !ok {
		return 0, false
	}
	row := (w*int64(comps)*bpc + 7) / 8
	return int(row * h), true
}

// inlineImageDataLength is inlineSampleBytes for an image with no filter, for
// which the samples are the data.
func inlineImageDataLength(d Dict) (int, bool) {
	if d.Get("F").Kind() != KindNull || d.Get("Filter").Kind() != KindNull {
		return 0, false
	}
	return inlineSampleBytes(d)
}

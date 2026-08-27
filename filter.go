package reader

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"fmt"
	"io"
)

// maxDecodedSize caps what one stream may expand to. A few hundred bytes of
// Flate can name gigabytes, and a reader that runs in a browser tab must
// refuse rather than fill the heap. Tests lower it.
var maxDecodedSize int64 = 1 << 30

// ImageFilter reports whether a filter yields an encoded image rather than a
// byte stream. [Decode] stops at one of these and hands the caller the still
// encoded bytes, because decoding them is an image decoder's job.
//
// /CCITTFaxDecode was on this list and is not any more. It does not carry an
// image with its own idea of how many components it has and how deep they are,
// the way DCT and JPX do: it produces bilevel samples, one bit a pixel, and the
// stream dictionary says what they mean. That is a byte stream, so it is a
// filter — and decoding it here means every caller gets it rather than each
// writing its own. See ccitt.go.
func ImageFilter(n Name) bool {
	switch n {
	case "DCTDecode", "DCT", "JPXDecode", "JBIG2Decode":
		return true
	}
	return false
}

// Decode applies a stream dictionary's filter chain to raw. It returns the
// decoded bytes and, when the chain ends in an image filter, that filter's
// name together with the bytes still encoded in it.
func Decode(d Dict, raw []byte, resolve Resolver) ([]byte, Name, error) {
	filters, parms, err := filterChain(d, resolve)
	if err != nil {
		return nil, "", err
	}
	data := raw
	for i, f := range filters {
		if ImageFilter(f) {
			return data, f, nil
		}
		if data, err = applyFilter(f, data, parms[i], resolve); err != nil {
			return nil, "", err
		}
	}
	return data, "", nil
}

// DecodeStream is Decode for a parsed stream.
func DecodeStream(s *Stream, resolve Resolver) ([]byte, Name, error) {
	return Decode(s.Dict, s.Raw, resolve)
}

// filterChain reads /Filter and /DecodeParms, each of which may be a single
// value or an array, and returns them aligned.
func filterChain(d Dict, r Resolver) ([]Name, []Dict, error) {
	fo, err := Resolve(d.Get("Filter"), r)
	if err != nil {
		return nil, nil, err
	}
	var names []Name
	switch v := fo.(type) {
	case Null:
	case Name:
		names = []Name{v}
	case Array:
		for _, e := range v {
			eo, err := Resolve(e, r)
			if err != nil {
				return nil, nil, err
			}
			n, ok := ToName(eo)
			if !ok {
				return nil, nil, fmt.Errorf("reader: /Filter array holds a %s, not a name", eo.Kind())
			}
			names = append(names, n)
		}
	default:
		return nil, nil, fmt.Errorf("reader: /Filter is a %s, not a name or an array", fo.Kind())
	}

	parms := make([]Dict, len(names))
	po, err := Resolve(d.Get("DecodeParms"), r)
	if err != nil {
		return nil, nil, err
	}
	switch v := po.(type) {
	case Null:
	case Dict:
		if len(parms) > 0 {
			parms[0] = v
		}
	case Array:
		for i, e := range v {
			if i >= len(parms) {
				break
			}
			eo, err := Resolve(e, r)
			if err != nil {
				return nil, nil, err
			}
			if pd, ok := ToDict(eo); ok {
				parms[i] = pd
			}
		}
	default:
		return nil, nil, fmt.Errorf("reader: /DecodeParms is a %s, not a dictionary or an array", po.Kind())
	}
	return names, parms, nil
}

// applyFilter runs one filter. The abbreviated names are the ones inline
// images use; regular streams may legally carry them too.
func applyFilter(f Name, data []byte, parm Dict, r Resolver) ([]byte, error) {
	switch f {
	case "FlateDecode", "Fl":
		out, err := flateDecode(data)
		if err != nil {
			return nil, err
		}
		return applyPredictor(out, parm, r)
	case "LZWDecode", "LZW":
		early := intParm(parm, "EarlyChange", 1, r)
		out, err := lzwDecode(data, early != 0)
		if err != nil {
			return nil, err
		}
		return applyPredictor(out, parm, r)
	case "ASCIIHexDecode", "AHx":
		return asciiHexDecode(data)
	case "ASCII85Decode", "A85":
		return ascii85Decode(data)
	case "RunLengthDecode", "RL":
		return runLengthDecode(data)
	case "CCITTFaxDecode", "CCF":
		return ccittDecode(data, ccittParamsOf(parm, r))
	}
	return nil, fmt.Errorf("reader: unsupported filter /%s", f)
}

// intParm reads an integer decode parameter, falling back to its default.
func intParm(parm Dict, key Name, def int, r Resolver) int {
	if parm == nil {
		return def
	}
	o, err := Resolve(parm.Get(key), r)
	if err != nil {
		return def
	}
	n, ok := ToInt(o)
	if !ok {
		return def
	}
	return int(n)
}

// flateDecode inflates a stream. Producers emit both zlib-wrapped and bare
// deflate data, sometimes with leading white-space, and truncate the last
// stream in a damaged file; all four cases yield whatever bytes are there.
func flateDecode(data []byte) ([]byte, error) {
	i := 0
	for i < len(data) && isSpace(data[i]) {
		i++
	}
	data = data[i:]
	if zr, err := zlib.NewReader(bytes.NewReader(data)); err == nil {
		out, err := readAllCapped(zr)
		if err == nil || len(out) > 0 {
			return out, nil
		}
	}
	out, err := readAllCapped(flate.NewReader(bytes.NewReader(data)))
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("reader: FlateDecode: %w", err)
	}
	return out, nil
}

// readAllCapped reads r, refusing to grow past maxDecodedSize.
func readAllCapped(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(r, maxDecodedSize+1))
	if err != nil {
		return buf.Bytes(), err
	}
	if n > maxDecodedSize {
		return nil, fmt.Errorf("reader: decoded stream exceeds %d bytes", maxDecodedSize)
	}
	return buf.Bytes(), nil
}

// asciiHexDecode reads hexadecimal digits up to the '>' terminator, padding a
// final odd digit with zero.
func asciiHexDecode(data []byte) ([]byte, error) {
	out := make([]byte, 0, len(data)/2)
	hi := -1
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c == '>' {
			break
		}
		if isSpace(c) {
			continue
		}
		v := hexVal(c)
		if v < 0 {
			return nil, fmt.Errorf("reader: ASCIIHexDecode: invalid digit %q", rune(c))
		}
		if hi < 0 {
			hi = v
			continue
		}
		out = append(out, byte(hi<<4|v))
		hi = -1
	}
	if hi >= 0 {
		out = append(out, byte(hi<<4))
	}
	return out, nil
}

// ascii85Decode reads base-85 groups up to the "~>" terminator.
func ascii85Decode(data []byte) ([]byte, error) {
	if bytes.HasPrefix(data, []byte("<~")) {
		data = data[2:]
	}
	out := make([]byte, 0, len(data)*4/5)
	var group [5]byte
	n := 0
	flush := func(n int) {
		for i := n; i < 5; i++ {
			group[i] = 'u'
		}
		v := uint32(0)
		for _, c := range group {
			v = v*85 + uint32(c-'!')
		}
		var b [4]byte
		b[0], b[1], b[2], b[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
		out = append(out, b[:n-1]...)
	}
	for i := 0; i < len(data); i++ {
		c := data[i]
		if isSpace(c) {
			continue
		}
		if c == '~' {
			break
		}
		if c == 'z' && n == 0 {
			out = append(out, 0, 0, 0, 0)
			continue
		}
		if c < '!' || c > 'u' {
			return nil, fmt.Errorf("reader: ASCII85Decode: invalid character %q", rune(c))
		}
		group[n] = c
		n++
		if n == 5 {
			if err := checkA85(group); err != nil {
				return nil, err
			}
			flush(5)
			n = 0
		}
	}
	if n == 1 {
		return nil, fmt.Errorf("reader: ASCII85Decode: truncated final group")
	}
	if n > 1 {
		flush(n)
	}
	return out, nil
}

// checkA85 rejects a full group that names more than 2^32-1.
func checkA85(g [5]byte) error {
	v := uint64(0)
	for _, c := range g {
		v = v*85 + uint64(c-'!')
	}
	if v > 0xFFFFFFFF {
		return fmt.Errorf("reader: ASCII85Decode: group overflows 32 bits")
	}
	return nil
}

// runLengthDecode expands the byte-oriented run-length encoding: a length byte
// below 128 introduces that many literal bytes plus one, above 128 repeats the
// next byte, and 128 ends the data.
func runLengthDecode(data []byte) ([]byte, error) {
	out := []byte{}
	for i := 0; i < len(data); {
		n := int(data[i])
		i++
		switch {
		case n == 128:
			return out, nil
		case n < 128:
			end := i + n + 1
			if end > len(data) {
				return nil, fmt.Errorf("reader: RunLengthDecode: truncated literal run")
			}
			out = append(out, data[i:end]...)
			i = end
		default:
			if i >= len(data) {
				return nil, fmt.Errorf("reader: RunLengthDecode: truncated repeat run")
			}
			out = append(out, bytes.Repeat(data[i:i+1], 257-n)...)
			i++
		}
	}
	return out, nil
}

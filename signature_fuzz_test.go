package reader

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// signatureSeeds are the files the signature fuzz targets start from: the
// committed signed fixtures, which open and hold real signatures, and a small
// hand-made one. ⛔ The error is fatal, not discarded: a seed list that
// silently drops a file it cannot read fuzzes nothing and reports clean.
func signatureSeeds(tb testing.TB) [][]byte {
	tb.Helper()
	seeds := [][]byte{miniSigned(tb)}
	for _, name := range []string{"rsa-detached.pdf", "incremental.pdf", "gap-is-decoy.pdf"} {
		b, err := os.ReadFile(filepath.Join("signature", "testdata", name))
		if err != nil {
			tb.Fatalf("seed %s: %v", name, err)
		}
		seeds = append(seeds, b)
	}
	return seeds
}

func miniSigned(tb testing.TB) []byte {
	zeros := strings.Repeat("0", 64)
	dict := "<< /Type /Sig /SubFilter /adbe.pkcs7.detached /ByteRange [0 0000000000 0000000000 0000000000] /Contents <" + zeros + "> >>"
	file := formDoc("[3 0 R]", map[int]string{
		3: "<< /T (s) /FT /Sig /V 4 0 R >>",
		4: dict,
	})
	start := bytes.Index(file, []byte("/Contents <")) + len("/Contents ")
	end := start + 66
	copy(file[bytes.Index(file, []byte("/ByteRange "))+len("/ByteRange "):],
		fmt.Sprintf("[0 %010d %010d %010d]", start, end, len(file)-end))
	return file
}

// FuzzSignatures: whatever the file, listing its signatures terminates, never
// panics, and every signature it lists as sound hands out bytes that lie inside
// the file and a /Contents that is the gap's.
func FuzzSignatures(f *testing.F) {
	for _, s := range signatureSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			t.Skip()
		}
		d, err := Open(b)
		if err != nil {
			return
		}
		sigs, err := d.Signatures()
		if err != nil {
			return
		}
		for _, s := range sigs {
			signed, err := d.SignedBytes(s)
			if s.Err != nil {
				if err == nil || s.Contents != nil {
					t.Fatalf("a refused signature handed out bytes or contents: %v", s.Err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("a sound signature's SignedBytes failed: %v", err)
			}
			if len(signed) >= len(b) || len(s.ByteRange) != 4 {
				t.Fatalf("signed %d bytes of %d with %v", len(signed), len(b), s.ByteRange)
			}
		}
	})
}

// FuzzByteRange: the validator never panics, and what it accepts is a shape
// inside the file: two ascending, disjoint ranges with a gap of at least "<>".
func FuzzByteRange(f *testing.F) {
	f.Add(int64(0), int64(10), int64(20), int64(30), 50)
	f.Add(int64(0), int64(10), int64(12), int64(0), 12)
	f.Add(int64(-1), int64(1)<<62, int64(1)<<62, int64(1)<<62, 100)
	f.Add(int64(0), int64(1), int64(1)<<32+3, int64(1)<<32, 1<<20)
	f.Fuzz(func(t *testing.T, a, b, c, e int64, n int) {
		if n < 0 || n > 1<<30 {
			t.Skip()
		}
		r, err := checkByteRange([]int64{a, b, c, e}, n)
		if err != nil {
			return
		}
		if r[0][0] != 0 || r[0][1] <= 0 || r[1][0]-r[0][1] < 2 || r[1][1] < r[1][0] || r[1][1] > n {
			t.Fatalf("accepted %v for %d bytes: %v", []int64{a, b, c, e}, n, r)
		}
		if int64(r[0][1]) != b || int64(r[1][0]) != c || int64(r[1][1]) != c+e {
			t.Fatalf("converted %v wrongly: %v", []int64{a, b, c, e}, r)
		}
	})
}

// TestWriteFuzzSeeds writes the committed corpus under testdata/fuzz when asked
// (FUZZ_SEEDS_WRITE=1). The corpus is small hand-made inputs: the fuzzer
// starts from files that reach the code after Open, which a random string
// never does.
func TestWriteFuzzSeeds(t *testing.T) {
	if os.Getenv("FUZZ_SEEDS_WRITE") != "1" {
		t.Skip("set FUZZ_SEEDS_WRITE=1 to rewrite testdata/fuzz/FuzzSignatures and FuzzByteRange")
	}
	write := func(target, name, body string) {
		dir := filepath.Join("testdata", "fuzz", target)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("go test fuzz v1\n"+body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	good := miniSigned(t)
	write("FuzzSignatures", "mini-signed", "[]byte("+strconv.Quote(string(good))+")")
	wide := bytes.Replace(good, []byte("[0 "), []byte("[1 "), 1)
	write("FuzzSignatures", "mini-first-range-not-at-zero", "[]byte("+strconv.Quote(string(wide))+")")
	nogap := bytes.Replace(good, []byte("/Contents <0"), []byte("/Contents (0"), 1)
	write("FuzzSignatures", "mini-contents-not-hex", "[]byte("+strconv.Quote(string(nogap))+")")
	for name, v := range map[string][5]string{
		"typical":      {"0", "10", "20", "30", "50"},
		"empty-tail":   {"0", "10", "12", "0", "12"},
		"huge":         {"-1", "4611686018427387904", "4611686018427387904", "4611686018427387904", "100"},
		"past-32-bits": {"0", "1", "4294967299", "4294967296", "1048576"},
		"overflow-sum": {"0", "10", "20", "9223372036854775797", "100"},
	} {
		write("FuzzByteRange", name, fmt.Sprintf("int64(%s)\nint64(%s)\nint64(%s)\nint64(%s)\nint(%s)", v[0], v[1], v[2], v[3], v[4]))
	}
}

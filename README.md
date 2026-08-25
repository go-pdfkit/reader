# reader

[![CI](https://github.com/go-pdfkit/reader/actions/workflows/ci.yml/badge.svg)](https://github.com/go-pdfkit/reader/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-pdfkit/reader.svg)](https://pkg.go.dev/github.com/go-pdfkit/reader)
[![Go Report Card](https://goreportcard.com/badge/github.com/go-pdfkit/reader)](https://goreportcard.com/report/github.com/go-pdfkit/reader)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)
[![Coverage](https://img.shields.io/badge/coverage-100%25-brightgreen.svg)](#testing)

A pure-Go, **zero-C** PDF **reader** — the parsing half of
[go-pdfkit](https://github.com/go-pdfkit). Where
[`pdfkit`](https://github.com/go-pdfkit/pdfkit) writes PDF 1.7, this module
takes existing PDF bytes apart: lexer, object model, cross-reference tables and
streams, stream filters, the standard security handler, and the page tree.

Nothing outside the Go standard library is required, so it builds for
`GOOS=js/wasm` and every 64-bit architecture the fleet targets.

## Status

Landing in waves. This module currently provides the **object layer** and
the **document structure**:

- **Lexer** — the full PDF object syntax: integers and reals in the shapes
  producers actually write, literal strings with their escapes, hex strings,
  names with `#xx` escapes, arrays, dictionaries, streams, and keywords.
- **Objects** — `Null`, `Bool`, `Integer`, `Real`, `String`, `Name`, `Array`,
  `Dict`, `Stream`, and indirect `Ref`, with the `N G R` / `N G obj` lookahead
  resolved the way the specification requires.
- **Filters** — `FlateDecode`, `LZWDecode` (with PDF's `EarlyChange`, which the
  standard library's LZW does not implement), `ASCIIHexDecode`,
  `ASCII85Decode`, `RunLengthDecode`, plus the PNG and TIFF predictors.
  Image filters (`DCTDecode`, `JPXDecode`, `CCITTFaxDecode`, `JBIG2Decode`) are
  reported rather than applied, so an image consumer gets the encoded bytes.
- **Cross-references** — classic tables, cross-reference **streams**, **object
  streams**, `/Prev` chains, and the `/XRefStm` of a hybrid file, newest
  definition winning.
- **Repair** — a file whose tables are missing, truncated or simply wrong is
  rebuilt by scanning it for object headers, trailers and, failing those, for
  a catalogue; a file that kept its pages but lost its catalogue gets one.
  This is not an exceptional path: it is what makes a reader usable.
- **Documents** — `Open`, object resolution with cycle and recursion guards,
  the trailer, the catalogue, and the page tree with the four attributes a
  page inherits from its ancestors.

Measured against a corpus of **118 863 real PDFs** — Matplotlib, cairo,
pdfTeX, Ghostscript, Adobe, R, Apache FOP, PDF 1.3 through 1.7 — `Open`
succeeds on **118 833** of them and finds 138 337 pages, in 8 seconds, with no
panics. Of the thirty it refuses, twenty-seven are PNG files with a `.pdf`
extension, one is PostScript, and two are PDFs truncated past recovery.

Next waves: the standard security handler (RC4, AESV2, AESV3), the
content-stream tokeniser, and a serialiser.

## Install

```sh
go get github.com/go-pdfkit/reader
```

## Testing

```sh
go test -covermode=set ./...
```

CI gates on **exact 100% statement coverage**, `go vet`, and a cross-compile
across `linux/{amd64,arm64,riscv64,loong64,ppc64le,s390x}`, `js/wasm`,
`darwin/arm64` and `windows/amd64`.

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-pdfkit/reader authors.

# reader

[![CI](https://github.com/go-pdfkit/reader/actions/workflows/ci.yml/badge.svg)](https://github.com/go-pdfkit/reader/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-pdfkit/reader.svg)](https://pkg.go.dev/github.com/go-pdfkit/reader)
[![Go Report Card](https://goreportcard.com/badge/github.com/go-pdfkit/reader)](https://goreportcard.com/report/github.com/go-pdfkit/reader)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)
[![Coverage](https://img.shields.io/badge/coverage-100%25-brightgreen.svg)](#testing)

A pure-Go, **zero-C** PDF **reader** — the parsing half of
[go-pdfkit](https://github.com/go-pdfkit). Where
[`pdfkit`](https://github.com/go-pdfkit/pdfkit) authors new PDF 1.7, this
module takes existing PDF bytes apart: lexer, object model, cross-reference
tables and streams, stream filters, the standard security handler, the page
tree, and content streams. It also writes the same object graph back out,
which is what every operation on an existing file needs.
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
- **Encryption** — the standard security handler in every revision: RC4 at 40
  and 128 bits, AESV2, and the AES-256 of `/R 5` and `/R 6`, with crypt
  filters, `/Identity`, and `/EncryptMetadata`. A password is tried as the
  user password and as the owner password, and `Open` uses the empty one, so
  a file protected only against editing opens with no password at all.
- **Content streams** — a tokeniser that yields operators with their operands
  and steps over rubbish rather than losing the operations around it, with
  inline images read whole. Where an inline image ends is the one genuinely
  ambiguous thing in a content stream, since its data may spell EI itself;
  the length is computed from the image's own geometry where it can be, and
  otherwise every candidate EI is tried until the data before one actually
  decodes.
- **Writing** — the exact inverse of the parser: objects rendered in PDF
  syntax with stable dictionary ordering, and a `Writer` that copies whole
  object graphs out of one or more documents, renumbering as it goes, then
  lays down a cross-reference table and a trailer.

Measured against a corpus of **118 863 real PDFs** — Matplotlib, cairo,
pdfTeX, Ghostscript, Adobe, R, Apache FOP, PDF 1.3 through 1.7 — `Open`
succeeds on **118 833** of them and finds 138 337 pages, in 8 seconds, with no
panics. Of the thirty it refuses, twenty-seven are PNG files with a `.pdf`
extension, one is PostScript, and two are PDFs truncated past recovery.

The one encrypted PDF the corpus holds — `/V 4 /R 4`, written by macOS
Quartz — decrypts to readable metadata and a valid content stream, which is
the only genuinely independent check of the key derivation there is here;
the other revisions are round-tripped against a test encryptor written from
the same algorithms.

The content-stream tokeniser reads **1 536 769 753 operations** across those
138 337 pages in a minute and a half, with no panics, no page failing to
decode, and no operator outside the seventy the format defines — beyond four
`arc` and six `nan` written by a producer that was simply wrong.

Rewriting is checked the same way: every one of the **118 833** files that
open is copied object by object into a new file, which is then re-read and
compared on page count, media boxes and the bytes of every content stream.
**All 118 833 match**, 35.5 GB in and 35.2 GB out, in twenty-eight seconds.

Next wave: the operations built on all of this.

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

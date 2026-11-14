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
  `ASCII85Decode`, `RunLengthDecode`, `Crypt`, plus the PNG and TIFF
  predictors. Image filters (`DCTDecode`, `JPXDecode`, `CCITTFaxDecode`,
  `JBIG2Decode`) are reported rather than applied, so an image consumer gets
  the encoded bytes.
- **Filters that fail** — a chain that cannot be run to the end is not the end
  of the stream. `DecodeRecovering` returns what the filters before the failure
  produced — the prefix a damaged Flate stream did inflate, not its compressed
  bytes — and says so through `Decoded.Recovered` and `Decoded.Cause`. Bytes no
  filter decoded arrive in `Decoded.Undecoded`, never in `Decoded.Data`, so a
  compressed stream cannot be painted as samples or tokenised as content by a
  caller that forgot to check a flag.
  `Decode` is the strict reading and refuses such a stream outright, so a
  caller that must not act on damaged data says which it wants by which one it
  calls. Page content takes the lenient reading, because a page whose content
  will not decode is still a page; `PageContentDecoded` reports whether any
  salvaging happened. Cross-reference streams take the strict one: a table that
  has to be guessed at is worse than no table, and the repair below is the
  answer to one.
- **Cross-references** — classic tables, cross-reference **streams**, **object
  streams**, `/Prev` chains, and the `/XRefStm` of a hybrid file, newest
  definition winning.
- **Repair** — a file whose tables are missing, truncated or simply wrong is
  rebuilt by scanning it for object headers, trailers and, failing those, for
  a catalogue; a file that kept its pages but lost its catalogue gets one.
  This is not an exceptional path: it is what makes a reader usable. The
  rebuild establishes the file key before it reads object streams, so a file
  whose catalogue lives in an encrypted one can be rebuilt at all; it takes
  `/Encrypt` from a cross-reference stream's dictionary when the file has no
  trailer keyword to name it in. Where two object streams claim the same
  object, the later one in the file wins, the same rule the header scan
  applies to objects written directly — so the same damaged file reads the
  same way twice. When a rebuild fails, the error says what the rebuild found
  and not merely that the tables could not be read.
- **Documents** — `Open`, object resolution with cycle and recursion guards,
  the trailer, the catalogue, and the page tree with the four attributes a
  page inherits from its ancestors.
- **Encryption** — the standard security handler in every revision, both ways:
  reading RC4 at 40 and 128 bits, AESV2, and the AES-256 of `/R 5` and `/R 6`,
  with crypt filters, `/Identity` and `/EncryptMetadata`; and writing AES-256
  or AES-128, with permissions, a user password and an owner password.
  A password is tried as the user password and as the owner password, and
  `Open` uses the empty one, so a file protected only against editing opens
  with no password at all.
  and steps over rubbish rather than losing the operations around it, with
  inline images read whole. Where an inline image ends is the one genuinely
  ambiguous thing in a content stream, since its data may spell EI itself;
  the length is computed from the image's own geometry where it can be, and
  otherwise every candidate EI is tried until the data before one actually
  decodes.
- **Writing** — the exact inverse of the parser: objects rendered in PDF
  syntax with stable dictionary ordering and strings in whichever of the two
  forms is shorter, and a `Writer` that copies whole object graphs out of one
  or more documents, renumbering as it goes, then lays down a cross-reference
  table and a trailer. `NewPackedWriter` instead puts what it can into
  compressed **object streams** and ends in a **cross-reference stream**,
  which is what makes a file small.
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
**All 118 833 match**, 35.5 GB in and 35.2 GB out, in twenty-eight seconds —
and **all 118 833 match again** when the packed writer is used instead, which
brings the output down to **94.6%** of the input.

### Linearization (Fast Web View)

`Linearized(b)` reads the linearization parameter dictionary at the front of a
file (ISO 32000-1 Annex F, Table F.1) and checks it against the file. A
linearized PDF is ordinary PDF rearranged so a reader with a byte-range
transport can show the first page before the rest has arrived.

⛔ **It is not a yes/no.** Table F.1 says a file-length mismatch "indicates
that the file is not linearized and shall be treated as ordinary PDF", and
F.3.6 says any incremental update invalidates the hint tables — which is the
ordinary fate of these files. So the three errors mean different things:
`ErrNotLinearized` (never was), `ErrLinearizationMalformed` (breaks a rule of
Table F.1), `ErrLinearizationStale` (was, and the file changed underneath it).

Checked: the dictionary is the **first** object of the body — not "one found
somewhere near the front", because searching would turn up a dictionary left
by an earlier write and call the file linearized when what a reader looks at
first is something else; that it lies entirely within the **first 1024 bytes**,
the rule that makes the format worth having; that every value is **direct**, so
no cross-reference table is needed to read it; the required entries and their
types; and `/L` against the real file length.

Not checked: the hint tables themselves (F.4) — a file can pass this and still
send a reader to the wrong offset.

⛔⛔ **`Linearized` on its own is a weaker test than it looks, and a writer
developed against it would pass without linearizing anything.** Of Table F.1's
claims it checks exactly one against the file — `/L` against the byte count —
because it is deliberately cheap: a reader deciding whether to stream a file
has fetched 1024 bytes and has no cross-reference table to resolve anything
with. `/O`, `/E`, `/N` and `/T` are read for type and never consulted, so a
file carrying a correct `/L` and four **invented integers** is accepted. That
is precisely what a first linearizer emits.

`CheckLinearized(b)` is the other question, for the other caller: not "may a
reader trust the front of this file" but "is this file actually what the front
says". It opens the document, so it costs what opening costs, and it is the
judgement a writer needs on bytes it has just produced. It establishes that
every parameter **points at what it claims to** — `/N` is the real page count,
`/O` is the real first page *and* is a `/Page`, `/H` begins at a real stream
object **whose dictionary names the shared object hint table Table F.2
requires**, `/T` reaches the main cross-reference (either shape: Table F.1
gives it two meanings and both are real), `/E` is where the first page really
ends.

⛔ **`/S` is the first thing checked *inside* a hint stream rather than about
it.** Everything else above is an offset or a length; `/S` is content. F.3.6
says the primary stream's dictionary "shall contain entries giving the
position of the beginning of each hint table in the stream", and Table F.2
marks the shared object hint table **Required** — so a primary stream naming
none leaves a reader that fetched the front of the file unable to find one.
Both committed qpdf fixtures and the Ghostscript one carry it; a same-width
edit of `/S` to `/X` is refused, and `qpdf --check` refuses that file too,
less helpfully (`ERROR: Integer operation attempted on invalid object`).

⚠ Only the **primary** stream. F.3.6 is explicit that "the dictionary of the
overflow hint stream shall not contain these entries", so the same
requirement on the overflow half would refuse exactly the conforming file.
The two halves of `/H` are not symmetric, and this is where they differ.

`/S` must also be a **direct integer and not negative**: F.3.6 says these
positions "shall be counted in bytes relative to the beginning of the stream
data", so a name, an indirect reference or `-1` names no position at all. A
reader that has fetched only the front of the file can resolve nothing, which
is the whole point of the format.

⚠ **The upper bound on `/S` is NOT checked, and that is an open limit.** It is
an offset into the *decoded* stream data, so bounding it means decoding the
hint stream — which is the first step of reading the tables themselves (F.4)
and belongs with that work. `qpdf --check` reports `/S (shared object) offset
is out of bounds` for a `/S` past the data and this does not. Tracked in #40.

⚠ And `qpdf --check` prints `File is linearized` from the parameter
dictionary **before** it validates the hint tables, then reports errors after
it. That line is not qpdf's approval, which matters to anyone reading its
output as a judgement rather than as a report.

What gates it are files `Linearized` **accepts** and
`CheckLinearized` refuses: the committed qpdf fixture with one integer
replaced by one of **the same width**, so `/L` still matches and nothing at
the front of the file is wrong — what is wrong is that it describes a
different document. The replacement's width is asserted by the helper, because
an edit one byte longer would be refused for the length instead and the deep
check would never run.

⛔ **`/E` is checked against the objects the file says are the first page's,
not merely against the file's length.** Table F.1 calls `/E` the offset of the
end of the first page. This used to bound it only between 1 and the file
length, on the reasoning that the first-page partition "is the hint tables'
job" — which was true of the hint tables and false of the **first-page
cross-reference section**, which Annex F puts at the front and the file's own
`startxref` points at. Its entries *are* the first page's objects; that is the
whole trick of the format. So `/E` must equal the end of the last of them, and
a writer that is one byte out is now told so instead of passing.

⛔⛔ **The hint stream is excluded, and a second producer is why that is not a
guess.** F.3.1 permits the primary hint stream on either side of the first
page, so the highest-offset entry in that section is not always a first-page
object:

| producer | `/H` offset | `/E` | hint stream sits |
|---|---|---|---|
| qpdf | 550 | 902 | **before** the first page |
| Ghostscript `-dFastWebView` | 829 | 829 | **after** it, at `/E` exactly |

On Ghostscript's file the naive maximum *is* the hint stream, ending at 967
against an `/E` of 829 — so a rule that took it would refuse every file that
tool writes. Both qpdf fixtures place it before and **cannot tell the two
rules apart**, which is why `testdata/linearized-gs.pdf` is committed beside
them: removing the exclusion turns exactly that test red and leaves the qpdf
ones green. A second producer measures what the first cannot.

⛔⛔ **What this proves is that `/E` agrees with the first-page section — not
that the section is right.** A writer that leaves objects page one genuinely
needs out of that section produces a file that is internally consistent and
passes here. Ghostscript does exactly this on a document with shared fonts,
outlines and links: it writes `/H` = `/E` = 1941, this checker accepts it, and
`qpdf --check` computes `5500..5501` and reports the first page as having 9
objects where the hint table claims 4. Deciding which objects page one *needs*
means walking its resources, which is the partition the hint tables describe —
so that judgement is still outstanding, and a pass here is not evidence about
it.

⚠ **Stricter than qpdf, deliberately.** qpdf computes `/E` as a *range*
(`901..902` on the committed fixture), accepting the end with or without the
line ending after `endobj`. This requires the end including it, which is what
both measured producers actually write. A file carrying the lower value would
pass `qpdf --check` and be refused here; none has been observed, and the
alternative is a window that cannot tell a correct `/E` from one a byte short.

⚠ The **overflow** hint stream is excluded by the same reasoning and no file
can reach it — it needs a four-entry `/H` whose second pair is both a real
stream and the highest-offset object, and neither producer writes one. So the
question is asked of a function rather than inline, where four plain cases can
put it and a mutation can kill it.

⛔⛔ **A value this build cannot address is refused, not truncated**, and the
same bytes therefore get the same answer on a 32-bit build as on a 64-bit one.
`int(Integer)` discards the high half where `int` is 32 bits, so a `/H` length
of 2³²+127 reads as **127** — not a wild value some bound would catch, but the
committed fixture's own hint length, which every later check then accepts. A
file would be judged sound on one machine and refused on another, with nothing
to say which had happened. Every integer the dictionary offers now goes through
one conversion that refuses what it cannot represent, `/P` included: it is
never used as an offset, but it is a public field, and a caller handed `0` for
2³² has no way to know. The refusal is `ErrLinearizationMalformed`.

This was found twice over, which is worth saying because the two findings were
not the same size. CI's 386 and arm lanes went red first, on values that become
`-1` and happen to land where a bound objects — the small half. Review named
the hazard that has no symptom: the truncation that lands on a *plausible*
value. On a 64-bit build the refusal is unreachable from any file, so the limit
is a variable the tests shrink; otherwise the branch is exercised only by CI,
which is to say only after pushing.

⛔ **The `/H` length is compared with the object exactly**, not within a
window. A first version allowed the declared length to exceed the parsed
extent by 0–2 bytes, reasoning that a writer might use CRLF; that accepts
`/H [ 550 126 ]` and `/H [ 550 128 ]` for a 126-byte object, both of which
qpdf refuses. The end-of-line is now **read** rather than guessed, and every
spelling of one (LF, CR, CRLF, none) has its own test — a tolerance wide
enough to cover every line ending is wide enough to hide a wrong length.

Still not checked, by either: the hint tables (F.4). `qpdf --check` remains the
foreign judge, and it is run across 1/2/5/25 pages × plain, object-streams and
uncompressed-streams.

The positives are **qpdf's** output, not fixtures written to agree with this
reading of Annex F. On five real library scans linearized by qpdf and judged by
`qpdf --check` alongside: **5 agree, 0 disagree**, including a 68-page, 13 MB
document.

⛔ **Reading is all it does — there is no linearizing writer yet.** The checker
came first on purpose: a writer cannot be trusted without one.

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

## The rest of the family

[**reader**](https://github.com/go-pdfkit/reader) reads and writes the format ·
[**ops**](https://github.com/go-pdfkit/ops) is the verbs and the `pdfops` command ·
[**render**](https://github.com/go-pdfkit/render) turns a page into pixels ·
[**pdffont**](https://github.com/go-pdfkit/pdffont) is what a document says about a font ·
[**extract**](https://github.com/go-pdfkit/extract) reads a page back ·
[**coedit**](https://github.com/go-pdfkit/coedit) is a PDF several people edit at once ·
[**app**](https://github.com/go-pdfkit/app) is the browser workbench.

Documentation for all of it: <https://go-pdfkit.github.io/docs/>

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-pdfkit/reader authors.

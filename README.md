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

`/S` must also be a **direct integer**: F.3.6 says these positions "shall be
counted in bytes relative to the beginning of the stream data", so a name, an
indirect reference, an array, a real, a string, a boolean or null names no
position at all. A reader that has fetched only the front of the file can
resolve nothing, which is the whole point of the format. All seven wrong kinds
have a case, asked of the function directly — a same-width edit of the fixture
can only express `/S /x`, so a file-level test alone left "refuse only a name"
passing.

⛔⛔ **And it cannot be below 36**, which is arithmetic rather than a guess.
F.3.6 puts the page offset hint table first and at offset 0, and Table F.3
gives its header as thirteen items totalling 288 bits — so whatever follows it
begins at byte 36 at the earliest. The floor subsumes the old "not negative"
rule, and it is **36 and not more because the margin was measured**:
Ghostscript writes `/S 38` where qpdf writes 43 for the same document, so a
bound taken from qpdf alone would have refused every Ghostscript file.

⛔⛔ **The upper bound is checked only where it costs nothing: an unfiltered
hint stream.** `/S` indexes the *decoded* data, so bounding it in general means
decoding an attacker's bytes in the cheap path — the expansion cap is 1 GiB,
three orders of magnitude past anything a hint stream needs, so the real bound
wants a size limit derived from `/N` and the object count. That is F.4 work,
tracked in #40.

But a stream with **no filter** needs no decoding at all: its raw bytes *are*
its data, and that case is free. It is also the one that matters most while a
writer is being developed, because an uncompressed hint stream is what somebody
debugging one emits — Ghostscript writes one unfiltered, and qpdf does under
`--compress-streams=n`.

⛔⛔ **And what must fit is 24 bytes, not one.** Table F.5 gives the shared
object hint table's own header as seven items of 32, 32, 32, 32, 16, 32 and 16
bits — 192 bits — so a table beginning at `/S` occupies at least bytes
`[S, S+24)`. The first version of this bound required only byte `S` itself to
exist, which accepts a stream leaving room for a fifth of a header.

⛔ It is 24 and not 25, deliberately. F.4.2 says the header is "followed by one
or more shared object group entries", so a conforming table has at least one
entry too — but Table F.6's items are sized by Table F.5's items 7 and 5, which
may both be zero, so a group entry can be a **single bit**. Demanding a whole
further byte would refuse a conforming file over one bit, and this check has
already been corrected once for refusing what it could not evaluate.

⛔ The question of whether the bound applies at all is "does the dictionary
**directly state** that the raw bytes are the data", and it has three answers,
not two. *Filtered*, *not filtered* and *cannot tell* are distinct, and only
the second licenses the bound:

| the dictionary says | the bound |
|---|---|
| no `/Filter`, `/Filter null`, `/Filter [ ]` | **applied** — raw is the data |
| `/Filter /FlateDecode`, or a chain | skipped — `/S` indexes decoded bytes |
| `/Filter 9 0 R`, or `[ 9 0 R ]` | skipped — a front-of-file reader resolves nothing |
| `/Filter 5`, or any kind that cannot name one | skipped — the chain cannot be read |
| `/F` present | skipped — the data is in an external file |

⚠ The filter condition is **load-bearing and measured**, not defensive coding:
applying the bound to filtered streams too turns the generated sweep's
twenty-five page cases red, where the hint table compresses well below `/S`.
No smaller document can see it.

⭐ **On an unfiltered stream the two judges now agree, where only qpdf caught
it before.** On the Ghostscript fixture with `/S` moved from 38 to 99 — same
width, so `/L` still matches and nothing at the front of the file is wrong —
this refuses it and `qpdf --check` reports

```
error encountered while checking linearization data:
  (linearization hint table, offset 882): /S (shared object) offset is out of bounds
```

⚠ qpdf exits **3** there, not 0, because it calls this a warning: `operation
succeeded with warnings`. And it prints `File is linearized` three lines
earlier, from the parameter dictionary, before validating anything — so neither
that line nor a zero exit is its approval.

⛔ **That agreement is held by the suite, not by a terminal.** The test runs
`qpdf --check` on the edited fixture and requires the message, *and* requires
that the unedited fixture does **not** draw it — otherwise the phrase would be
something qpdf says about every Ghostscript file and the positive would prove
nothing. Under `REQUIRE_QPDF=1` a missing qpdf fails rather than skips, so the
claim cannot quietly stop being tested.

⚠ **And the agreement is specific to an unfiltered stream.** For a *filtered*
one — qpdf's own default output — with `/S` past the **decoded** data, qpdf
still reports `offset is out of bounds` and this still accepts. That gap is the
open limit above, not a disagreement about what `/S` means.

### ⛔⛔ The Ghostscript fixture's hint tables are wrong, and it is still a positive here

`testdata/linearized-gs.pdf` is committed as a file this checker **accepts**,
and `qpdf --check` on that exact file exits 3 with five warnings:

```
shared object 0 length mismatch: hint table = 65536; computed = 115
shared object 1 length mismatch: hint table = 65536; computed = 118
shared object 2 length mismatch: hint table = 65536; computed = 19
page length mismatch for page 0: hint table = 230; computed length = 252
page length mismatch for page 1: hint table = 242; computed length = 248
```

It is a positive for the **parameters** and a negative for the **hint tables**,
which is exactly the property #40 predicted in writing — "a file can pass this
and still have hint tables that send a reader to the wrong offset" — now a
named file in the corpus rather than a risk noted in an issue.

⛔ **So Ghostscript is not the model for the writer's hint tables.**
`min_group_length: 65536` is `0x10000`, a placeholder never filled in. On the
same run qpdf's own fixture reports `min_group_length: 31` with group lengths
96, 98 and 31, and exits **0 with no warnings**. When #40's step 3 writes hint
tables, the shape to copy is qpdf's.

⚠ This is recorded as a test (`TestTheGhostscriptFixtureHasHintTablesQpdfRefuses`),
which is a **witness and not a guard**: nothing in this package can make it
fail, because it asserts a fact about a fixture and an external tool. Its
control — that qpdf's own output draws no `mismatch` — is what keeps it from
being vacuous. It is also the todo marker: when hint-table checking lands,
`CheckLinearized` should refuse this fixture, and that test is where the change
will announce itself.

⛔⛔ The last three rows are there because an earlier version had only the first
two, and refused what it could not evaluate. An indirect `/Filter` reported as
*zero* filters — a reference resolves to null when there is no resolver, which
is indistinguishable from an absent `/Filter` — so a stream that really was
compressed got refused for a length belonging to different bytes. That is the
same mistake as refusing on an unreadable chain, which the code already
declined to make one line higher.

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

Still not checked **here**: the hint tables (F.4). ⛔ Not "by either" — qpdf
does check them, which is how the Ghostscript fixture's five warnings above
were found, and saying otherwise understated the foreign judge while this
package was the one with the gap. `qpdf --check` remains that judge, and it is
run across 1/2/5/25 pages × plain, object-streams and uncompressed-streams.

The positives are **qpdf's** output, not fixtures written to agree with this
reading of Annex F. On five real library scans linearized by qpdf and judged by
`qpdf --check` alongside: **5 agree, 0 disagree**, including a 68-page, 13 MB
document.

### The partition, which is the writer's first step

`d.Partition()` answers which section of Annex F.3.1's eleven-part layout every
object of a document belongs to — part 4 (the catalogue and what opening the
file needs), part 6 (the first page), part 7 (another page's private objects),
part 8 (shared between later pages) and part 9 (everything a reader does not
need to display a page). The numbers are F.3.1's own, so a `Part` compares with
the annex and with qpdf without a translation table.

⭐ **It has a foreign judge, which is why it is a step of its own.**
`qpdf --show-linearization` prints its own *Computed Linearization Part
Assignments* — the reference implementation's answer to exactly this question —
so the partition is measured before a single byte of a linearized file is
written. It agrees with qpdf across 1/2/5/25 pages × plain, object-streams and
uncompressed-streams: **twelve shapes, no disagreements.**

⛔⛔ **The two judges count different things, and that is not a defect in
either.** qpdf names the objects it will *write*; `Partition` names the objects
the document *has*. With object streams those differ — for the two-page
`--object-streams=generate` fixture qpdf puts object 3 in part 9, and object 3
is an `/ObjStm` whose one member is object 5, the page tree node, which is what
F.3.10 actually places. The test expands qpdf's containers into their members
rather than "correcting" the partition to name containers: an object stream is
a packing decision that comes *after* the partition.

⛔ **What it declines, by name.** Each of these changes which part an object
belongs to, and none is exercised by any file in this corpus, so placing them
would be writing untested rules into a format where a wrong placement is
silent — the reader does not fail, it reads the wrong bytes:

| refused | because |
|---|---|
| `/OpenAction` | F.3.7 makes the page it opens at the *first* page |
| `/PageMode /UseOutlines` with `/Outlines` | F.3.7 then puts the whole outline hierarchy in part 6, not part 9 |
| `/Threads` | F.3.5 puts thread dictionaries in part 4, F.3.10 their `/I` in part 9, beads with their pages |
| `/AcroForm` | F.3.5 takes only the top-level form dictionary; F.3.10 takes the field hierarchy |
| `/Encrypt` | F.3.5 requires the encryption dictionary *and every value in it* in part 4 |
| a page with `/Thumb` | F.3.10 gives thumbnails and their shared objects two ordered categories of their own |

⛔ **`/Thumb` and `/Parent` are skipped at the page**, which F.3.7 states
outright: part 6 holds what "its Contents, Resources, Annots, and B entries"
reach "but not the Thumb entry". A thumbnail in part 6 puts a picture nobody is
looking at in front of the bytes that draw the page.

⛔⛔ **And a page that names another page directly does not drag it in** — F.3.7
excludes "page tree nodes **or other page objects**". A link annotation's
`/Dest` and `/B` article beads are page-to-page references that do not pass
through the tree, and without that exclusion one page with a link would pull
the linked page, its contents and its resources into part 6. The generated
corpus **cannot see this**: its only page-to-page route is `/Parent`, which is
already skipped, so emptying that half of the boundary left all twelve qpdf
shapes green. It was found by mutation and is now held by a test of its own.

⚠ Part 8 has no witness in the generated corpus either — qpdf reports
`part 8 (other pages, shared): none` for all twelve shapes, because the
fixtures give every page its own resources. So F.3.9's whole subject is held by
a hand-built document rather than by the sweep.

### Inside the hint tables (F.4), where the parameter checks could not go

The page offset hint table's header — Table F.3's thirteen items — is decoded
now, which is the first time this package reads the index a reader would
actually fetch by rather than the numbers at the front of the file. That is the
gap #40 named: "a file can pass this and still have hint tables that send a
reader to the wrong offset."

⭐ **It has the best judge in the arc.** `qpdf --show-linearization` prints every
field of the decoded table by name, so all thirteen items are compared against
the reference implementation's reading of the same bytes — across 1/2/5/25
pages × plain, object-streams and uncompressed-streams. Twelve shapes, and a
field left out of that comparison is a field nothing checks.

⛔⛔ **Every item is bit-aligned, not byte-aligned**, which is why there is a bit
reader rather than `encoding/binary`. Table F.3's header happens to be whole
bytes, but Table F.4's per-page items take their widths *from* that header —
"See Table F.3, item 3" — and those are any value in 0–32. A two-page document
with `nbits_delta_nobjects` of 1 writes one bit per page, so page two's entry
begins mid-byte. Reading such a table a byte at a time gives numbers that look
plausible and are wrong, which is the worst failure available here.

⛔ A width of 0 is legal and means 0, which F.4.1 requires rather than
permits: qpdf writes `nbits_delta_nobjects: 0` for the one-page fixtures.

⛔⛔ **And a 16-bit field can hold a number that is not a width.** F.4.1 bounds
every bit count to 0–32 but has them stored as 16-bit numbers, so the field can
say 65535 — and a reader that believed it would consume 65535 bits of whatever
followed and report the result as a page's object count. All seven width items
are refused above 32, before any of them is used. Not hypothetical: the
Ghostscript fixture writes 65536 into the *other* table's `min_group_length`,
which is `0x10000` and plainly a placeholder nobody filled in.

⚠ **None of this is exported yet, deliberately.** The decoder is reached only
from inside the package, so an exported `HintTables` or `PageOffsetHeader`
would be a name no caller could obtain a value of — surface that exists to be
renamed later. When `CheckLinearized` consults the tables, exporting them is a
deliberate change with a caller to justify each name.

⛔⛔ **Item 2 is not the offset it looks like.** The table stores the first
page's location computed **as if the primary hint stream were not there** —
Table F.4 item 2 says it outright, "a conforming product shall skip over the
primary hint stream, wherever it is located" — so the effective offset adds the
hint stream's length when it lies at or before that point. Measured across all
twelve shapes: the stored value equals `/H`'s own offset and qpdf reports
stored + `/H`'s length, exactly.

| producer | stored | `/H` | qpdf reports |
|---|---|---|---|
| qpdf, 2 pages | 550 | `[550 127]` | **677** |
| qpdf, 25 pages (objstm) | 613 | `[613 141]` | **754** |
| Ghostscript | 577 | `[829 138]` | **577** — unadjusted |

⚠ **Only a second producer could show that the condition is needed.** Ghostscript
puts `/H` *after* the first page, so no length is added — and adding it
unconditionally agrees with all twelve qpdf shapes and is wrong about every
Ghostscript file. Measured: that mutation reddens exactly one test, the
Ghostscript one.

⚠ Decoding the hint stream is the cost the `/S` upper bound declined to pay, and
it is paid here because reading the tables *is* the purpose. The expansion cap
in `filter.go` bounds it.

⛔ **Reading, partitioning and the page offset header is all it does — there is
no linearizing writer yet.** The checker came first on purpose: a writer cannot
be trusted without one. What remains for #40 is the per-page entries (Table
F.4), the shared object hint table (F.4.2), the layout, and the two-pass
fixpoint for `/L`, `/H`, `/E` and `/T`. `CheckLinearized` does not yet consult
any of this, so the Ghostscript fixture is still accepted.

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

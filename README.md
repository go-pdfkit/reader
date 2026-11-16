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

### ⭐ And the per-page sets, which `Partition` was computing and discarding

`Partition.Pages[i]` is every object page *i+1* needs. The hint tables cannot be
built without it: Table F.4 item 1 is "the number of objects in a page" and item
2 is that page's length, both **per page**, while `Of` only answers which *part*
an object is in — and part 7 is "belongs to exactly one page that is not the
first" without ever saying which page.

The walk already produced these sets, once per page, and threw them away.

⛔ **They hold what is PRIVATE to the page, not its reach, and qpdf said so on
the first run.** The first version kept each page's whole reach and disagreed
with item 1 on every page but the first — ours `[1 2 15]` against qpdf's 2. The
extra number is the same on every page of a document because it is a part-6
object **the first page also reaches**, which qpdf hands to later pages as a
first-page shared entry instead. Counting it in both places would make every
page claim an object the layout puts at the front.

⛔⛔ **The count is a weak judge and three invariants carry the rest.** On every
fixture the cardinalities are *uniform* — page one is all of part 6, every other
page exactly 2 — and the union of the sets is exactly parts 6 and 7. So swapping
two pages' private objects keeps every count correct. qpdf publishes nothing
per-page that attributes a part-7 object to a page, so that judge cannot pin
membership **even in principle**. What does: every page holds its own object, the
sets are pairwise disjoint, and their union is exactly the part-6 and part-7
objects. A swap reddens eight shapes.

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

⛔⛔⛔ **Each of F.4.1's seven lettered groups is padded to a byte boundary, and
the specification does not say so anywhere.** F.4.1 gives the order (a)–(g) and
the widths and says nothing about alignment between groups; "byte boundary"
appears in the whole of ISO 32000-1 only in the filter and image-sample
clauses, never in Annex F. A decoder written from the text alone is wrong on
every real file.

It was found by the *refusal firing on files qpdf had produced* — "the first
page claims 1 shared object" where Table F.4 item 3 requires 0 and qpdf itself
reports 0 — which is the shape of a decoder error, not a producer error. Then
measured by hand from the bytes of the two-page fixture, after the 36-byte
header:

```text
byte 36: 10000000   item 1: page 0 delta 1, page 1 delta 0, SIX BITS PADDING
byte 37: 11111000   item 2: page 0 delta 31, then
byte 38: 00000000     page 1 delta 0, then padding
byte 39: 01000000   item 3: page 0 count 0, page 1 count 1, padding
byte 40: 10000000   item 4: page 1's identifier 2, padding
byte 41: 11111000   item 7: page 0 delta 31, then
byte 42: 00000000     page 1 delta 0, padding
```

Item 3 landing on byte 39 is the proof: without the padding page 0's count
reads as 1, with it as 0, and 0 is what qpdf says.

⚠ So it is a **producer convention this package follows rather than a rule it
can cite.** Two independent producers agree — qpdf across twelve shapes and the
Ghostscript fixture — which is the most this format offers.

⛔⛔ **The entries are column-major**, which is the other thing a page-at-a-time
reading gets wrong: F.4.1 says "the items making up each per-page entry are not
contiguous; they are broken up with items from entries for other pages". Items
4 and 5 sit between items 3 and 6, which no per-page loop would produce.

⛔ **Item 3 is read for the first page too and must be zero there** (Table F.4
item 3). The count is present for every page — skipping it for page one shifts
every later count — and a non-zero first-page count makes groups (d) and (e)
consume bits belonging to the pages after it, so every later value is wrong
while the table still parses.

### ⛔⛔ F.4.2's "two sequences" are one run, settled by a fixture built for it

The shared object hint table's group entries come in two sequences — those for
first-page objects, then those for the shared objects section — and F.4.2 gives
the item order "in **each** sequence". Read strictly, each would be
column-major and byte-aligned on its own, which differs from one run of
`nshared_total` entries whenever the first sequence's widths do not end on a
byte.

⛔ **The qpdf corpus cannot settle it.** Every fixture has
`nshared_total == nshared_first_page`, so the second sequence is empty and both
readings agree on all twelve shapes. A green suite said nothing about the
question, which is why an earlier version *refused* a non-empty second sequence
rather than guess.

⭐ So a document was built for the question: pages 2..n all point at one
`/Resources` dictionary page 1 does not reference, which is F.3.9's definition
of a part 8 object. ⛔ The first attempt still proved nothing — with a **small**
shared object its length *is* `min_group_length`, so its delta is zero and the
padding is zero too, and both readings give identical numbers. With a fat one
they separate:

```text
min_group_length 99, nbits_delta_group_length 9, lengths 108, 99, 601
  so the deltas are 9, 0 and 502
bits after the 24-byte header:
  00000100 10000000 00111110 11000000
ONE RUN:       bits 18-26 = 111110110 = 502  → 601  ✓
TWO SEQUENCES: align to bit 24, read 384     → 483  ✗
```

qpdf writes **one** column-major run. ⚠ The specification's wording stays
ambiguous and the measurement does not — and the mutation confirms which test
settles it: the two-sequence reading reddens the new fixture on both shapes and
leaves the twelve older ones green.

### ⛔⛔ Measuring #56 moved the answer: the term it named was the smaller one

The page offset table's counts were bounded by the **bits** remaining, not by
the document's objects, so a small stream became a large allocation. #56
reasoned about it: a `sharedRef` is 8 bytes, so one bit in gives eight bytes
out, 64×. Running it says otherwise:

```text
2 MiB stream, every width 0 but a one-bit count, 8 388 608 pages
  the pageEntry slice alone   352.1 MiB   176x   <- /N, which #56 never names
  with the shared references  416.1 MiB   208x   <- +32x, the term #56 named
  and no error in either case
after the bound                  3 064 B   0.0x, refused by name
```

⭐ **The dominant cost is the 40-byte `pageEntry` per page, driven by `/N`** —
five and a half times the term the issue was filed about. And the term it *did*
name measures 32×, not 64×: the issue was **2× high about the half it named
while being 5.5× low about the total.** Both errors come from reasoning about
the sizes instead of running it. The attribution came
from a control that declared no references at all, so the two terms could be
separated rather than guessed at. Estimating would have fixed the smaller half
and left a 176× amplification in place.

`/N` is as untrusted here as `/S` was, and for the same reason: nothing on this
path calls `checkHintStream`. Both ceilings are now the document's object count
— a page is an indirect object, and F.4.2's identifiers index a table of
objects, so neither a page count nor **one page's** references can exceed what
the file holds.

⛔⛔ **"One page's" is doing real work there, and the first version of this bound
got it wrong.** It compared the **sum** of every page's count against the object
count, which refuses ordinary documents: a hundred pages each referencing fifty
shared objects is about 255 objects and a sum of 99 × 50 = 4950. Sharing is the
entire purpose of part 8 — the same object is referenced from many pages — so
the sum is bounded by pages × objects and never by objects.

This is #52's lesson repeating: a work budget there refused a 150,000-page file
and had to be removed. **A ceiling in the wrong units does not fail loudly on
nonsense; it fails on somebody's real document.** The counterexample is now a
test, because the wrong bound looked right and passed every other case. What
still bounds the total is the page ceiling plus the two bit bounds: with
`pages ≤ objects` and each count `≤ objects` the sum cannot exceed `objects²`,
and the bits bound independently caps it at one reference per remaining bit. The
176× term is closed by the page ceiling, which is the unambiguous half.

⛔⛔ **And the ceiling itself was untested, which a mutation found.** Both bounds
take the cap as an argument, and both had tests passing it directly — so both
were satisfied by a caller passing *any* number. Replacing `int64(len(d.xref))`
with `1<<40` in `readHintTables` left the **entire suite green**, on the page
side and the group side alike. A bound whose ceiling is untested is a bound
against a number of its own choosing. Two cases now go through `readHintTables`
so the document supplies the ceiling, and each reddens when its own call site is
given a constant.

### ⭐⭐ The encoder's oracle: re-encode qpdf's own tables and compare bytes

Every check above compares **numbers** against what `qpdf --json` prints, and a
number cannot see the layout around it — the padding, the group order, the
width of a field whose value happens to fit either way. So the hint table
encoder is judged differently: decode qpdf's tables, re-encode from exactly
those values, and compare the **bytes**, over fourteen shapes.

That oracle earned its keep twice on the first run.

⛔ **Table F.3's item 13 was being written as 32 bits.** It is 16. The header
came out 304 bits instead of 288, so every shape differed — and a numbers-only
comparison could never have seen it, because the value read back was right.

⛔⛔ **And it found a missing check in the *reader*.** Ghostscript's fixture
puts `/S` at byte 38 (bit 304) while its page offset entries run to bit 330:
twenty-six bits of what the reader was decoding as "page lengths" are really
the first bytes of the shared object table. The reader accepted that silently.
It now refuses it, naming the overrun in bits — a defect found only because an
encoder could not reproduce those bytes. ⛔ This is why Ghostscript is excluded
from the round-trip list, with the reason written at the exclusion.

### ⛔⛔ Three of the eleven lettered groups write nothing on any file here

The pad above is not the only thing the byte oracle cannot see. Measured across
all fourteen shapes, qpdf writes:

```text
Table F.3 item 7  (nbits_delta_content_offset) = 0
Table F.3 item 12 (nbits_shared_numerator)     = 0
Table F.5 item 5  (nbits_nobjects)             = 0, and every group has 1 object
```

So F.4's groups (e) and (f) and F.6's group (d) emit **zero bits** on every file
in the corpus, and Table F.6 item 4's "1 less than the number of objects" has no
byte witness at all. Swapping (e) with (f) leaves all fourteen shapes green.

⭐ So those three groups get cases whose expected bytes are spelled out from the
annex's item sizes, with every width set to 4 bits so the arithmetic is checkable
by eye. The (e)/(f) swap reddens **only** those cases — which is the measurement
that says the corpus could not have caught it.

⛔⛔ **And the first version of that test was blind in the same way.** Two pages
at 4 bits each is exactly 8 bits per group, so every pad landed on a byte
boundary anyway and deleting one changed nothing: the suite stayed green with
group (f)'s `align()` removed. The case is three pages and three shared
references, making every group 12 bits, so all seven pads are observable — and no
two groups hold the same bytes, so the order is pinned as well as the padding.
Item 1's deltas are 0,1,3 rather than 0,1,2 for exactly that reason, since group
(c)'s values are forced to be the reference counts 0,1,2.

The lesson generalises past this file: **a test written to witness padding has to
use a shape whose padding is not zero.** A witness built on a round number
witnesses nothing, and it looks identical to one that works.

⛔ An earlier version of the oracle's own comment claimed it proved "the whole
layout". It does not, and the claim is now the measured list above. The oracle is
the strongest instrument in this arc and it is still only as wide as its corpus.

### ⛔⛔ The writer emitted widths its own reader refuses

F.4.1 bounds every bit width to 0..32 and says 16-bit fields shall hold them, so
a width of 40 **fits its field perfectly**. The per-value guard cannot catch it:
the `n > 32` refusal lives in the lettered group that uses the width, and an
empty group writes nothing. For a one-page table groups (d) and (e) never run,
so the encoder produced a valid-looking 36-byte header ending `00 28 00 00 00 00`
that `readPageOffsetHeader` then refused — an encoder whose output its own
decoder rejects.

Both header writers now check the same nine width items their readers check,
before writing anything. One consequence is worth stating: with the widths
bounded, every item of Table F.5's header fits its field by construction, so it
is written with `push` and carries no refusal. That is not "discard the error
because it cannot happen" — the guarantee is a refusal three lines above that
the tests exercise item by item, not a comment.

### ⛔⛔ And the overrun check's own arithmetic overflowed

The `/S` refusal described above was itself the sixth site of a class this fleet
has already swept twice. `/S` arrives from the stream dictionary as an `int64`
and nothing bounded it, so `8*int64(pos)` with `/S = 2^61+40` **wraps to 320**:
the refusal did not fire, and `seekByte` caught it afterwards by luck. On a
32-bit build `int(pos)` keeps the low bits — 40 — which `seekByte` can accept.

⛔ The comment above the check justified the missing bound by naming
`checkHintStream`, which does require `/S ≥ 36` — and which only
`CheckLinearized` calls, so it is not on this path at all. **A justification that
cites another function's check is worth nothing until the call is there.** `/S`
is now bounded against the decoded stream before any arithmetic touches it.

⚠ Worth recording how this was found: 100% block coverage, `go vet` on
`linux/386` and `linux/arm`, and nine CI jobs including six architectures under
qemu all passed. The check was *covered* — the Ghostscript fixture exercises it.
It was its arithmetic that overflowed, and a gate an honest input walks through
says nothing about a hostile one. An external review found it by reading the
type of `pos` instead of the comment above it.

### ⛔⛔ A pad that every fixture in the corpus is blind to

Table F.6's item 2 is a one-bit signature flag and item 3 is the 16-byte MD5 it
guards. **Not one file in the corpus sets that flag** — qpdf 12.4.2 and
Ghostscript 10.08.0 both leave it clear everywhere. So the byte oracle, the
strongest instrument here, is silent about sixteen bytes of Table F.6.

Measured by mutation: deleting the `align()` after group (b) leaves **every**
qpdf fixture byte-identical. Group (c) writes nothing when nothing is signed,
and (c)'s own unconditional `align()` then restores the boundary the deleted
one would have set. The two pads are redundant on every file that exists here,
and separable only once item 3 is actually present.

⭐ So the signed group is a **synthetic** witness whose expectation is built
from F.4.2's item sizes rather than from a round trip: the reader turns those
bytes into these values, the writer turns these values back into those bytes.
A round trip through both of my own functions would pass on any pair that
agreed with each other and with neither annex. Its control — an unsigned group
writing one byte of flag and nothing else — is what makes the case able to
fail at all.

### ⭐ The checker now reads the hint tables — and refuses the fixture for a sixth defect, not for qpdf's five

⛔ **The heading had to be rewritten, and the reason is the point.** It first
read "the gap #40 named is closed", sitting beside "hint tables `qpdf --check`
reports five problems in" — which implies this package now detects those five.
**It detects none of them.** The refusal below is the `/S` overrun, which this
repository's own notes call "a SIXTH thing wrong with that file, after the five
qpdf warns about". All five mismatches remain undetected. The narrow paragraph
further down was exact; the heading beside that sentence was not, and review
caught the juxtaposition rather than the claim.

#40 said in writing that "a file can pass this and still have hint tables that
send a reader to the wrong offset", and `testdata/linearized-gs.pdf` was
committed as the file that does exactly that. For five merged pull requests this
package accepted it.

`CheckLinearized` now decodes both tables as its last step and refuses the
fixture:

```text
reader: the hint tables do not describe this file: reader: the hint table
cannot be read: the page offset table's entries end at bit 330 and /S puts the
shared object table at byte 38 (bit 304), so 26 bit(s) of the entries are
really the next table
```

⛔ **`ErrHintTablesInvalid` is a separate error, and that is not tidiness.** The
hint tables are an **optimisation**: F.3.6's point is that a reader can show
page one before the rest arrives. A file whose tables are wrong is still a
correct PDF openable by the ordinary route — what it has lost is the fast path.
A caller that could not tell this from `ErrLinearizationStale` would have to
either reject readable files or accept parameters that lie. The distinction the
README used to make in prose — "a positive for the parameters and a negative
for the tables" — is now in the type.

⚠ **What this checks and what it does not.** It requires the tables to *decode*
as F.4 describes: every width in 0..32, the entries inside the stream, the page
offset table ending at or before `/S`, the counts bounded by the document. It
does **not** compare their *values* against the document. The page lengths and
group lengths qpdf also reports on need the partition, and are the next step. So
this closes "the tables are unreadable", not "the tables are wrong".

### ⭐ And the values too — "the tables are wrong" is closed as well

The paragraph above describes `CheckLinearized` before #61. It now also holds
**Table F.4 item 2** (a page's length) and **Table F.6 item 1** (a shared group's)
against the file.

⛔ **The span is consecutive object numbers from the page object**, which is
qpdf's definition rather than an obvious one: `Lin::lengthNextN` takes the page
dictionary's number and the next *nobjects−1* **numbers** and sums each one's own
length, never consulting which objects the page reaches. A checker summing *"what
this page reaches"* would disagree with qpdf on exactly the files qpdf complains
about — which the writer learned the hard way in the same arc.

⛔ **The shared groups describe two runs**, not one: the first `nshared_first_page`
of them are part 6's objects counted from `/O`, the rest part 8's counted from
Table F.5 item 1. Measured on qpdf's own output — `nshared_first_page: 2`,
`nshared_total: 3`, `first_shared_obj: 5` for `testdata/linearized-shared.pdf`,
which is the one fixture that exercises both.

⭐ **The oracle is qpdf from the other side.** On every file qpdf accepts, its
silence means its own computed length equals the length the table states — so our
figure matching the table *is* our figure matching qpdf's. Dropping the line
ending after `endobj` (which Table F.1 counts and qpdf calls `end_after_space`)
reddens **41 tests**, including the twelve-shape agreement suite, each off by
exactly one byte per object.

⚠ **A span inside an object stream is not measured**, and `LengthsMeasured`,
`LengthsChecked` and `LengthsSkipped` say so — because such an object has no
offset of its own and qpdf substitutes its *container's*, which is its accounting
rather than a fact about the file. An object stream is legal here, so the file is
not refused for it. The boolean exists for the reason `HasOverflowHint` does: a
count of zero cannot distinguish "nothing was measurable" from "nothing measured
it".

⛔ **What #61 asked for and did not get:** the Ghostscript fixture refused *naming
a length*. Its tables do not decode at all — the entries overrun `/S` by 26 bits —
so no ordering reaches a length in that file. qpdf prints `computed = 252` for it
because its decoder reads through the overrun; reading through it here would
compare bits of the *shared* table reinterpreted as page lengths, refusing the
right file for the wrong reason. The witness is a byte flipped in a file **this
package wrote** instead, which is possible because our hint stream is unfiltered
and item 2 is pinned at 32 bits.

⛔ **Two tests had to stop calling `CheckLinearized`,** which is worth stating
rather than hiding in a diff. The Ghostscript fixture is the only producer here
that puts `/H` after the first page (the witness for `/E`'s exclusion rule) and
the only one writing an *unfiltered* hint stream (the control for `/S`'s upper
bound). Both needed that file to reach the check they are named for, so each now
calls that check — `checkFirstPageEnd` and `checkHintStream` — instead of the
whole pipeline. That is strictly more precise: they were only ever end-to-end
because nothing end-to-end disagreed with the file.

⛔ **Reading, partitioning, both hint tables, encoding them and checking that
they decode is all it does — there is no linearizing writer yet.** The checker
came first on purpose: a writer cannot be trusted without one.

### ⭐⭐ `Document.Linearize` — and the hint tables are real

That paragraph is now out of date and is kept because the order it describes is
the point: the checker was built first, and every step of the writer was judged
by it and by qpdf before the next one started.

`Linearize()` writes Annex F's eleven parts and returns the bytes.
`qpdf --check` says **`File is linearized`** and raises no complaint it does not
also raise about the input, on one, two, five and twenty-five page documents
**whose pages share no objects**.

⛔⛔ **That scope is load-bearing, and review found it rather than the tests.**
The shared object hint table (F.4.2) is how a page after the first says which of
part 6's and part 8's objects it depends on, and this writer emits a *required
but empty* one. For a document whose pages are independent that is the truth;
for one whose pages are not, it is a hint table that contradicts the file:

```
WARNING: page 1: shared object 3: in computed list but not hint table
```

measured on two pages sharing one content stream — while our own
`CheckLinearized` **accepted** that file, so nothing in this package would have
caught it.

⛔ **And the whole judged corpus could not express the case.** `buildPages` gives
every page its own content stream, so no object is reached by two pages and
`Partition.Shared` is empty on every shape the suite builds. The foreign judge
was green because the fixtures were silent — the second time in this arc that a
passing oracle meant "the corpus says nothing". `TestADocumentWhoseLaterPagesShareAnObjectIsRefused`
commits the missing fixture, and `TestTheJudgedCorpusSharesNothingBetweenPages`
is its negative control: a suite in which every input is refused measures as
little as one in which every input passes.

⚠ So `Linearize` **refuses** such a document, naming the objects, rather than
describing it wrongly. Filling that table is a partition question — qpdf reports
`nshared_total: 3` with part 8 **empty** for the same input — and it is #61.

### ⛔⛔⛔ And that refusal is far wider than "pages sharing a content stream"

The rule is about **sharing**, and nothing else:

> refused iff an object a page after the first reaches is **also** reached by the
> first page or by another page — other than the **catalogue**, which part 4
> delivers ahead of the first page

⛔ The catalogue exception is not a special case in the code, which is why it has
to be in the sentence: `of[catNum] = PartCatalog` is assigned *after* the readers
loop and overwrites whatever sharing would have made it, so `Shared`'s "part 6 or
part 8" test cannot see it. That is correct rather than convenient — part 4
precedes part 6 and arrives with the first page, so a later page depending on the
catalogue needs no F.4.2 entry. Measured: two pages each carrying `/PieceInfo`
give the catalogue part 4, `Shared` empty, the document accepted, and
`qpdf --check` silent. Part 4 holds nothing but the catalogue, so there is no
second instance.

⛔ **Two narrower rules were written here first and both were wrong**, which is
why this one is stated in terms of *reaching* rather than of any mechanism:

- *"a multi-page document with an indirect inherited value"* — refuted by a
  three-level tree whose sub-node carries the reference with exactly **one** page
  under it: that object is part 7, private to that page, and accepted.
- *"an inheritable indirect value inherited by more than one page"* — only
  **sufficient**. Refuted by a page that states its *own* `/Resources` naming the
  node's object under `/XObject` while the other page inherits it: exactly one
  page inherits, and the object is shared all the same.

⛔⛔ **Inheritance is a route by which a page reaches an object**, not a separate
case. Table 30 lets a page omit `/Resources`, `/MediaBox`, `/CropBox` and
`/Rotate`, and the writer pushes those down because F.3.7 needs part 6
self-contained — so an inherited reference makes the page reach that object
exactly as naming it would. It matters here only because it is the route that is
easy to miss: one shared resource dictionary on the page tree node is the
ordinary way to write a multi-page PDF, so a large share of real files are
refused today.

| shape | accepted? |
|---|---|
| one page, inherited indirect `/Resources` | **yes** — nothing to share with |
| multi-page, inheritable values **direct** (an inline `/MediaBox` array) | **yes** — adds no object to reach |
| multi-page, each page naming a **different** `/Resources` object | **yes** — nothing shared |
| a **sub-node**'s indirect `/Resources` with **one** page under it | **yes** — part 7, private to that page |
| a **sub-node**'s indirect `/Resources` with **two** pages under it | **no** — part 8, shared |
| flat multi-page inheriting one indirect `/Resources` | **no** — needs F.4.2 |
| one page inherits it, the other reaches it via its **own** `/Resources` | **no** — one inheritor, still shared |
| two pages naming the **same** `/Resources` object, no inheritance at all | **no** — stating your own is not a cure |

⚠ **"Each page states its own" is not an exemption**, and that row above says
"a different object" for a reason: the earlier wording passed for an unstated
second reason — the fixture gave each page a distinct object. Stating your own
removes the *inheritance* route, not the sharing.

⛔ Every fixture but the two sub-node rows is a **flat** page tree, which is
exactly why the first boundary was mis-stated: a flat tree cannot express
"inherited by one page out of several". The accepting rows carry more weight than
the refusing ones — without them a stricter reach would refuse everything and the
suite would stay green.

⚠ **The error names the inheritable key only when that key's own object is the
shared one.** Asking merely "does this page inherit anything by reference"
conflates two causes and can state a falsehood: two pages naming the *same*
`/Contents` explicitly, with one of them under a sub-node carrying an unrelated
indirect `/Rotate`, produced *"shares object 4 … which it inherits as /Rotate"* —
where object 4 was written in both page dictionaries and `/Rotate`'s object was
not shared at all. Both halves were true and the conjunction was not. The
transitive case is given up deliberately: an object deep under an inherited
`/Resources` is shared without being that key's value, and there the plain
message is the honest one.

⛔ **The partition had to learn this before the refusal could be right.**
`reachFromPage` now follows the inherited attributes, because the writer pushes
them down at emission: if the node's value is a reference, the emitted page names
an object no *source* page named, and the partition was measuring a different
document from the one being written. Before that, such a file was **accepted**
with three defects — and two of them, `/E` and Table F.4 item 1, were older than
the hint tables:

```
end of first page section (/E) mismatch: /E = 905; computed = 1190
object count mismatch for page 0: hint table = 2; computed = 3
page 1: shared object 7: in computed list but not hint table
```

⭐ The one-page inherited shape is in both judged loops, because the repair is
observable only on a document the writer **accepts** — the two-page fixture stops
at the refusal. Removing the inheritance from the reach reddens it on exactly
those first two warnings.

⛔⛔ **THE LAYOUT IS PAGE-MAJOR IN PARTS 6 AND 7, AND A FOREIGN CHECKER IS WHY.**
Within those parts the order is "page by page, each page's own object first",
not "ascending object number" — because qpdf measures a page by taking its page
object's number and the next *nobjects−1* **consecutive** numbers and summing
their lengths (`Lin::lengthNextN`, read in qpdf's source). It never looks at
which objects the page actually reaches. A page whose content stream is numbered
below its page object is therefore measured against whatever happens to be
numbered above it:

```
page length mismatch for page 1: hint table = 186; computed length = 162
```

Those 162 bytes were our page object (101) plus the **page tree** (61) — an
object belonging to no page at all.

⛔ **And the other page passed by accident,** which is the half worth keeping.
Its byte extent was 186 and the sum of its two objects was 101 + 85 = 186: the
same total over a *different* range. One green page and one red page came from
one defect, so the green one was never evidence.

⛔ **Table F.4 item 2 is a SUM, not an extent.** With the page-major ordering the
two agree, because a page's objects are now adjacent — but they agree as a
consequence of the layout, not by definition.

⭐ **Three bit widths are pinned at 32, and that is what makes the file
converge.** The hint stream sits before the first page, so if its *length*
changed between the two passes every later offset would move. Of Table F.4's
seven items only three are measured in bytes — items 2, 6 and 7 — so pinning
those widths makes the encoded length a function of the page count and the
shared-reference counts alone, neither of which depends on where anything
landed. The other widths are still derived from the data, which is already
pass-invariant. qpdf solves the same problem by padding its hint stream to a
fixed 200 bytes; pinning the widths costs four bytes per page per item and needs
no padding at all.

⛔⛔ **`qpdf --check` validates three fields of the page offset table and no
more:** item 2, the per-page object counts, and the per-page lengths. It never
reads items 6 and 7 — the content stream's offset and length — and it never
reads the shared object hint table's groups. So the writer could emit nonsense
there and every foreign judge in this repository would stay silent. That is the
same shape as part 5's placement, where `--check` passed a file whose hint
stream sat in the wrong part. `TestTheHintTablesDescribeTheFileWeWrote` holds
every field against the bytes we wrote instead, and zeroing item 6 reddens it on
every page of every shape while qpdf stays quiet.

⛔ **The judge no longer exempts hint-table complaints.** While the tables were
zeros, the qpdf diff had to skip any complaint matching a keyword list — which
meant the one subject the step was about was the one subject the judge could not
speak on. The list grew one entry at a time as qpdf named them and is now
deleted: every complaint counts.

What remains for #61 is the shared object hint table's *values*. This writer's
partition puts nothing in part 8, so its table is a required header and no
groups — but qpdf's own output shows the accounting is wider than part 8:
linearizing the same two-page input, qpdf reports `nshared_total: 3` with part 8
**empty**, so it counts first-page objects that later pages also use. Matching
that is a partition change, not a table change.

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

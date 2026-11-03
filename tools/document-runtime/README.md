# Camp document text runtime

This independent pnpm workspace pins **PDF.js 6.4.299** for local PDF text preparation. It does not run an agent or contact a model provider. DOCX text uses Camp's Go standard-library reader and does not require this Node runtime.

From the Camp repository root, with Node **24.16.0**:

```sh
make install-document-runtime build-sandbox
CAMP_MAC_HELPER="$PWD/dist/camp-sandbox" \
CAMP_TEST_DOCUMENT_RUNTIME="$PWD/tools/document-runtime" \
  go test -race ./apps/server/internal/documenttext -count=1 -v
```

The installer uses pnpm **12.11.2**, its frozen lockfile and `--ignore-scripts`. PDF.js is Apache-2.0; the installed package includes its license. Its optional `@napi-rs/canvas` dependency supplies the Node adapter's DOMMatrix implementation; the lockfile fixes canvas **1.0.13**, including platform packages. Keep optional dependencies enabled. Camp itself still builds with `CGO_ENABLED=0`.

This runtime belongs to Server, separately from Runner, model settings and the frontend bundle. Complete Mac Server archives bundle fixed Node/PDF.js/canvas files and the trusted process helper under `documents/`. A SHA-256 manifest compiled into the Server verifies every bundled regular file before use; Homebrew and Go installation preserve that same relative layout. No reader download occurs during startup or document preparation. Source builds use `./tools/document-runtime` and the helper adjacent to their executable. `CAMP_DOCUMENT_RUNTIME` and `CAMP_DOCUMENT_NODE` are explicit process-only overrides, never loaded from `.env`; the old Server `CAMP_DSH_NODE` override is ignored. An invalid explicit Node selection fails without falling back to a host or bundled executable. A recursively readable runtime cannot contain Server data or reader job storage, including filesystem aliases; a separate runtime subdirectory is allowed. Runner runtime configuration remains separate. The lightweight Linux Server image has no reader runtime or Bubblewrap by default. Missing readers do not block discussion, plain-text files or original downloads.

For a background Mac Server, `service install` resolves a bare `CAMP_DOCUMENT_NODE` command using the installer's PATH and saves its absolute path. Unknown commands prevent installation; a saved executable that later disappears makes readers unavailable without selecting another Node.

The private `extract.mjs` process consumes raw PDF bytes on stdin and emits one JSON response on stdout: `{ result: { format, sections, warnings } }` or `{ error: { code, message, retryable } }`. Diagnostics use bounded stderr. Do not launch this script directly for user documents: `documenttext.Engine` owns Linux Bubblewrap or Mac Seatbelt isolation and confirmed descendant cleanup. The Go reader uses the private `camp document-worker` entry point with the same protocol for DOCX.

## Preparation boundary

Each preparation uses an owned isolated process tree, no network, an explicit non-secret environment, read-only system/runtime files and private temporary storage. Linux uses a private PID namespace; Mac uses Seatbelt and a kernel-observed native job. Original bytes travel through stdin and are never written into the worker filesystem. Camp data, private configuration, host home and working projects remain inaccessible. Cancellation confirms cleanup of detached descendants before accepting a result. There is no resident parser or login service to install.

Limits are **8 MiB input**, **200 physical PDF pages**, **2,000 nonempty DOCX paragraphs**, **1 MiB extracted UTF-8 text**, **30 seconds**, and bounded response and diagnostic pipes. DOCX additionally bounds archive entries, expanded sizes, XML nesting and token counts. Limits fail the whole preparation; Camp never returns a successful but silently shortened document. The Node heap and Go runtime memory target are bounded separately; these are runtime settings, not a hard operating-system ceiling on all native allocations.

PDF.js reads text content from locally supplied bytes and local font/CMap assets. It does not render pages, run document actions, fetch user URLs, perform OCR or interpret images. Page labels refer to physical pages, including empty pages. Multi-column reading order, spacing and diagrams may not match visual layout. Locked, damaged and text-free documents produce explicit errors; empty pages in an otherwise readable document remain visible with an omission warning.

DOCX uses bounded ZIP reads and streaming XML; archives are never unpacked to paths. The main body and table-cell paragraphs are read in document order. Empty paragraphs do not receive labels. Paragraph labels are not Word page numbers. Tracked deletions, moved-away text, field instructions, drawings, text boxes, equations and embedded alternative content are excluded. Inserted text and saved field values are included. Headers, footers, footnotes, endnotes, comments and images are not read. Warnings describe detected omissions. External relationships are never followed, embedded objects never opened, and field values never recalculated. Macro-enabled and legacy Office formats are unsupported.

## Deterministic verification

The opt-in command above exercises real PDF.js, Go DOCX workers and the host isolation backend: business fixtures, physical page/paragraph sources, password protection, malformed input, no-text and mixed empty pages, page/text limits, secret and host-file omission, network isolation, bounded output, and cancellation of a detached child. No `.env` file or model credential is read. Ordinary Go tests cover DOCX parsing, Unicode XML, ZIP/XML limits, malformed inputs and the worker protocol without external runtime dependencies.

Synthetic reproducible documents and source expectations are in [`examples/documents`](../../examples/documents/README.md). The [API contract](../../docs/api.md#uploads-and-documents) owns preparation identity, retry, frozen evidence and citation behavior.

Sources: [PDF.js Node example](https://github.com/mozilla/pdf.js/blob/v6.4.299/examples/node/getinfo.mjs), [PDF.js license](https://github.com/mozilla/pdf.js/blob/v6.4.299/LICENSE), and [Microsoft WordprocessingML structure](https://learn.microsoft.com/en-us/office/open-xml/word/structure-of-a-wordprocessingml-document).

## Mac process ownership and packaging

Mac readers reuse `nativejob` with a dedicated Server-owned `document-jobs/` directory, without Runner enrollment, identities, journals or model authority. A parser receives document bytes only on stdin, exact read/execute grants for its immutable runtime, and a fresh writable HOME/TMP. IP/DNS/loopback/Unix sockets, Server state, sibling jobs and host credentials remain denied. Cancellation and parent death terminate the authenticated kernel-observed job tree, including detached descendants. Unknown ownership preserves receipts and suspends only that Engine until stopped recovery at the next Server startup; it never publishes partial text. Startup probes use synthetic documents. Settings reads return cached observations without starting another parser.

Build a complete host Mac candidate after `make install build`, then validate its relocated runtime without a model or Runner:

```sh
node scripts/package-server.mjs --platform darwin-arm64
CAMP_SMOKE_BINARY="$PWD/dist/camp-server-darwin-arm64/camp" node scripts/smoke-documents.mjs
CAMP_SMOKE_BINARY="$PWD/dist/camp-server-darwin-arm64/camp" node scripts/smoke-documents.mjs --service
```

The packager builds flat, link-free dependencies from the frozen pnpm lockfile and fixed native canvas package. It reuses the exact pinned host Node for a matching build; cross-architecture Node archives use `node-pin.json` checksums from the official distribution. Node/PDF.js/canvas licenses remain with the runtime. The Go launcher preserves private executable modes and repairs a changed cached reader package from its original release archive. Reader format, limits, immutable originals and citation identities are shared across Linux and Mac; packaging does not re-extract historical ready documents.

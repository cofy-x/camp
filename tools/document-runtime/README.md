# Camp document text runtime

This independent pnpm workspace pins **PDF.js 6.4.299** for local PDF text preparation. It does not run an agent or contact a model provider. DOCX text uses Camp's Go standard-library reader and does not require this Node runtime.

From the Camp repository root, with Node **24.16.0**:

```sh
make install-document-runtime
CAMP_TEST_DOCUMENT_RUNTIME="$PWD/tools/document-runtime" \
  go test -race ./apps/server/internal/documenttext -count=1 -v
```

The installer uses pnpm **12.11.2**, its frozen lockfile and `--ignore-scripts`. PDF.js is Apache-2.0; the installed package includes its license. Its optional `@napi-rs/canvas` dependency supplies the Node adapter's DOMMatrix implementation; the lockfile fixes canvas **1.0.13**, including platform packages. Keep optional dependencies enabled. Camp itself still builds with `CGO_ENABLED=0`.

This runtime belongs to Server, separately from Runner and the frontend bundle. `CAMP_DOCUMENT_RUNTIME` is process-only configuration (not loaded from `.env`); its default is `./tools/document-runtime` relative to the Server working directory. Use an absolute path when deploying a binary elsewhere, and provide the pinned Node executable through `CAMP_DSH_NODE` when it is not on PATH. The lightweight Server image has no reader runtime or Bubblewrap by default. Missing readers do not block discussion, plain-text files or original downloads.

The private `extract.mjs` process consumes raw PDF bytes on stdin and emits one JSON response on stdout: `{ result: { format, sections, warnings } }` or `{ error: { code, message, retryable } }`. Diagnostics use bounded stderr. Do not launch this script directly for user documents: `documenttext.Engine` owns its Linux Bubblewrap isolation and cleanup. The Go reader uses the private `camp document-worker` entry point with the same protocol for DOCX.

## Preparation boundary

Each preparation uses a new child process with a private PID namespace, no network, an explicit non-secret environment, read-only system/runtime files, and private temporary mounts. Original bytes travel through stdin and are never written into the worker filesystem. Camp's data directory, private configuration, host home and working projects are not mounted. Cancellation kills the supervisor and its PID namespace, including detached descendants. There is no resident worker or background service to install.

Limits are **8 MiB input**, **200 physical PDF pages**, **2,000 nonempty DOCX paragraphs**, **1 MiB extracted UTF-8 text**, **30 seconds**, and bounded response and diagnostic pipes. DOCX additionally bounds archive entries, expanded sizes, XML nesting and token counts. Limits fail the whole preparation; Camp never returns a successful but silently shortened document. The Node heap and Go runtime memory target are bounded separately; these are runtime settings, not a hard operating-system ceiling on all native allocations.

PDF.js reads text content from locally supplied bytes and local font/CMap assets. It does not render pages, run document actions, fetch user URLs, perform OCR or interpret images. Page labels refer to physical pages, including empty pages. Multi-column reading order, spacing and diagrams may not match visual layout. Locked, damaged and text-free documents produce explicit errors; empty pages in an otherwise readable document remain visible with an omission warning.

DOCX uses bounded ZIP reads and streaming XML; archives are never unpacked to paths. The main body and table-cell paragraphs are read in document order. Empty paragraphs do not receive labels. Paragraph labels are not Word page numbers. Tracked deletions, moved-away text, field instructions, drawings, text boxes, equations and embedded alternative content are excluded. Inserted text and saved field values are included. Headers, footers, footnotes, endnotes, comments and images are not read. Warnings describe detected omissions. External relationships are never followed, embedded objects never opened, and field values never recalculated. Macro-enabled and legacy Office formats are unsupported.

## Deterministic verification

The opt-in command above exercises real PDF.js, Go DOCX workers and Bubblewrap: business fixtures, physical page/paragraph sources, password protection, malformed input, no-text and mixed empty pages, page/text limits, secret and host-file omission, network isolation, bounded output, and cancellation of a detached child. No `.env` file or model credential is read. Ordinary Go tests cover DOCX parsing, Unicode XML, ZIP/XML limits, malformed inputs and the worker protocol without external runtime dependencies.

Synthetic reproducible documents and source expectations are in [`examples/documents`](../../examples/documents/README.md). The [API contract](../../docs/api.md#uploads-and-documents) owns preparation identity, retry, frozen evidence and citation behavior.

Sources: [PDF.js Node example](https://github.com/mozilla/pdf.js/blob/v6.4.299/examples/node/getinfo.mjs), [PDF.js license](https://github.com/mozilla/pdf.js/blob/v6.4.299/LICENSE), and [Microsoft WordprocessingML structure](https://learn.microsoft.com/en-us/office/open-xml/word/structure-of-a-wordprocessingml-document).

# Document review example

These small files contain synthetic business information, not customer data. Use the [team workflow](../../docs/workflow.md) and [document preparation](../../tools/document-runtime/README.md) to inspect them. Regenerate byte-identical fixtures with the repository's pinned Node:

```sh
node examples/documents/generate.mjs
```

Attach both files in a Room and ask an agent to recommend a two-week support pilot, identify missing measurements, and cite the original evidence. The originals remain immutable; a resulting plan belongs in working files.

- `customer-brief.pdf`, physical **Page 1**: Morgan, three specialists, 40 tickets each week, 6 staff-hours per week, no new software, and a 25% reduction target.
- `customer-brief.pdf`, physical **Page 2**: no existing satisfaction baseline, consistent weekly measurement, Morgan's approval, and the short pilot's limits.
- `interview-notes.docx`, **Paragraph 3**: repeat setup questions and private notes.
- **Paragraph 4**: the customer prefers a short checklist.
- **Paragraph 5**: inaccurate automation is worse than waiting.
- **Paragraph 8**, a table cell: Morgan approves every published guide update.
- **Paragraph 9**: no satisfaction baseline has been measured.

DOCX paragraph labels count nonempty main-body and table-cell paragraphs in source order. They are not Word page numbers. The generator also creates a text-free PDF fixture for deterministic no-text handling; no OCR or model call is involved.

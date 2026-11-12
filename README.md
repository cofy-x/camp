Camp alpha.13 history UI qualification

Original browser PNGs from isolated, keyless Servers with 10,000 retained messages and 1,000 executions. Before: main 0902bac equivalent immutable preview binary. After: frontend 8ee4393 with backend 146759b. No model execution, real customer records, credentials or application assets are included. Desktop viewport 1440x900, touch 390x844 at device scale 2; captures crop the actual Workspace window.

The evidence branch is separate from application source. Images are embedded directly in PR #139.


## Bounded execution activity and result reports — 2026-11-12

The `bounded-*.png` comparisons use the actual public alpha.13 embedded UI and the candidate activity/report UI, the same synthetic Notes Studio dataset, Chromium at 1440 × 900 / DPR 1, and a 390 × 844 narrow view. The long execution has 20,000 retained mock read-response events. No personal team data, credentials, real Runner state or paid model calls are included.

The old UI issues 41 raw log requests (7,016,112 response bytes), retains 20,000 events and renders 500 display rows. The new UI issues one summary request (5,660 response bytes), retains at most 50 entries and renders 50 newest-first rows. The real SQLite/index/window and original-byte guarantees are tested separately in the application PR. These are controlled fixture observations, not production latency percentiles or native execution acceptance.

The GitHub connector does not provide native image-attachment upload. These reviewed PNGs are embedded into the application PR through immutable raw links to this isolated evidence commit.


## Simpler Library and optional human approval

The `simple-*.png` images compare clean source `b8bed7e1c6d2fcdc9184ce3f09e4054dc622fc5c` with `b86af4d3f2e0d04db28b6161bbc6fad4035dd955`. Both are actual production-mode embedded UIs, using the same synthetic Notes Studio team, Weekly planning Room and weekly-plan.md document. Desktop: Chromium, 1440 × 900, DPR 1. Narrow: 390 × 844, DPR 1. The original P9 chrome and document fonts are retained. No mocked read responses, private team data, credentials, paid model calls or original runtime state are included. Ordinary file reading issued zero review-history requests.

Native GitHub image attachment upload is unavailable through the connected tools. These reviewed images are retained on this separate image-only branch so the PR can embed immutable raw PNG URLs directly. They are not application assets.

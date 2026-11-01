# Alpha 6 synthetic UI review evidence

This separate review branch hosts inline images for the reliability PR. It is
not merged into main or shipped in Camp packages; generated test results remain
ignored in development checkouts. No private team data or credentials are used.

- Before: application behavior at `a465aafd7b9b459605eb5c111a05ed9c918d97fc`.
  The Library regression test was added before its production fix.
- After: behavior at `a0b95dc68df7c8832b434b5a6f17a6ad1cd7a62d`.
- Library: Chromium, 390 x 844 viewport, DPR 2, synthetic Browser Owner and
  rooms, Sources opened while the initial Library response is held, then released.
  The before image captures the incorrect automatic closure; the after image
  preserves the open drawer and Done focus. The combined after suite includes
  additional synthetic rooms from other Library scenarios.
- Runner setup: Chromium, 1440 x 900 viewport, DPR 1, 820 x 650 Runner window,
  Mac platform selected by keyboard and requirements expanded. Both scenes use
  the same synthetic installation metadata and Studio Mac draft.

The PR links these files by immutable commit SHA. Supporting traces, logs and
raw test results remain in the private workspace and CI artifacts.

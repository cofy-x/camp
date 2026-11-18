# Work state screenshots

Chromium rendered the embedded production Dashboard with synthetic fixture data. The test opens a waiting collaboration, types a private reply and refreshes after the task stops. Both renders retain the same saved file and unanswered task record in their fixture input.

- Before source `6efbe4b54cf5d8aa2e6dc734453c69720474dd97`.
- After source `e5a6afa995b9c1a291c4bd4315abb5da190b9590`.
- Desktop viewport 1440 × 900, Work bounds 940 × 650 at (86, 76).
- Narrow viewport 390 × 844, Work bounds 382 × 784 at (4, 26).
- Both use device scale 1 and the same regression in `tests/work-items.spec.ts`.

Before, the old question suppresses the cancelled state and the saved file. After, the cancelled state and saved team draft are visible alongside the private reply.

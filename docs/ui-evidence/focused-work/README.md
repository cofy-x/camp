# Focused Work UI evidence

Actual production UI captures for Camp PR #156, taken on 2026-11-14 in Chromium
1.64.1's bundled browser on Linux. All people, messages, Rooms and matters are
synthetic. The two Servers use independent new keyless data directories. No
private team data, model credentials or native execution appears in these images.

- Before source: `238f69cc98b6fc3af8f25c1fbdd5487fa101d93c`.
- After source: `efa8a0b52ba5360b26325bc43e8b23c5ac07a0a5`.
- Paired desktop captures: 1440 × 900 viewport, DPR 1; Workspace at (64, 56),
  1120 × 680; Work at (86, 76), 940 × 650, active in front of Workspace.
- Paired narrow captures resize the same Chromium page to 390 × 844, DPR 1.
- Additional after-only matter captures use DPR 1.5 and DPR 2 at 1440 × 900.
- Discussion has three short ordinary messages, no active execution. Work detail
  contains the same multiline release request and open judgment on both sides.
- New Work is captured after its title and private draft have settled, before a
  Room is chosen. Only the after version has an editable goal at that point.
- Work grant captures select the same Room and enter the same goal. Agent access,
  finite allowance and team-visible draft scope remain visible. Agent execution
  is unavailable in the intentionally keyless installation; recording is usable.
- Room-setting failure captures inject a 503 for the selected Room's members
  read, then verify Retry Room settings succeeds. The after page retains its
  editable goal through that failure and recovery; creation stays disabled until
  matching context is accepted.

`before.json` and `after.json` record the visible action labels and measured goal
height at a 760-pixel content width: 188.4375 and 37.6875 pixels. This is layout
evidence, not an application latency benchmark. Both narrow pages have no
horizontal content overflow. The complete authored request remains available
under Show full goal, with its original paragraph formatting.

Local behavior validation: 236 frontend cases and 88 selected browser cases across
Chromium, Firefox, WebKit and touch passed. The final collapsed-paragraph style
adjustment received eight further affected matter/task reading checks across all
four projects. Keyboard opening/Escape focus return, source handoff, Room setting
isolation, legacy completion semantics and exact creation-receipt recovery are
covered. Touch tests run separately at 390 × 844 and DPR 2. TypeScript, ESLint,
changed-file formatting, documentation consistency and production build passed.
This change did not run a new paid-model campaign.

These files belong to a separate immutable image evidence commit. They are not
part of the application source branch or a proposal to merge generated images.

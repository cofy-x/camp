# Portable remote execution validation

Run from the Camp checkout with the [repository toolchain](../../CONTRIBUTING.md#development) and Docker Compose:

```sh
node scripts/remote.mjs up
node scripts/remote.mjs test
node scripts/remote.mjs native
```

`up` leaves one lightweight Server, two independent fixture daemons and a test service running. The Server image contains Git and Camp, with no Node, DSH, LayerFS, Docker socket or FUSE. The test image uses Node and OpenSSL for setup and TLS. No container is privileged. Each daemon has its own private SQLite journal and data volume; each sees only its own one-use enrollment credential and the public local CA. The test service uses the ordinary setup/login/admin APIs. It alone can read the setup token and its generated test account.

The localhost browser port defaults to 13443 (`--port` overrides it). Each run uses a named Compose project beginning with `camp-remote-`; `--project` can select one explicitly. `test` chooses an available localhost port and removes only its own containers and volumes on success. On failure, the project and its private logs stay available under `.data/remote/<project>`. Use the printed `down` command to remove that project. No service autostart is installed.

The test service generates a private local CA and Server certificate. All machine requests verify certificates. For browser use, export and trust that project's `/trust/ca.crt`; never disable certificate validation. The test account is private at `/control/account.json` inside the tests container. Generated credentials are not printed by setup or saved in the checkout.

The fixture executor recognizes a fixed vocabulary in a task prompt:

- ordinary text: produce `result.txt` deterministically;
- `[fixture:wait]`: emit a durable waiting event, then await cancellation;
- `[fixture:fail]`: return an execution failure;
- `[fixture:check-fail]`: return a failing configured check;
- `[fixture:corrupt-check]`: return a mismatched check receipt for rejection tests.

For a collaboration task with approved participants on two Runners, the fixture also recognizes these fixed scenarios in the current round's instruction:

- `[fixture:handoff]`: pass the draft to another approved Agent, who finishes;
- `[fixture:handoff-ask]`: pass the draft, ask a person to choose the audience, then finish after the answer;
- `[fixture:handoff-wait]`: pass the draft, then reach the cancellation barrier;
- `[fixture:ask-person]`: ask a fixed question with two answer choices;
- `[fixture:finish]`: finish and run the configured fixture checks.

Quoted history and the original task goal cannot repeat a previous round's control marker. Handoffs and questions retain unpublished checkpoints; final completion publishes once. The fixture chooses only an explicitly approved participant; Agent routing on the Server determines the machine.

Fixture text is never executed as shell code. The Server must explicitly opt in with `--allow-fixture-runners`; the UI identifies test execution. Configured checks produce deterministic receipts with the same names and rendered commands as the native executor, but do not run actual tools.

`test` verifies two-Runner routing, message idempotency, cancellation isolation, source snapshot transfer, review/revision/delivery and immutable delivery. It also hands a collaboration draft across Runners, restarts Server and both Runners at a durable question, answers it, checks replay safety, and delivers the one final publication through human review. It uses the persisted waiting event as a barrier before Server restart, daemon SIGKILL and network disconnect/reconnect. Host Node controls Docker; containers have no Docker socket or hidden mutation API. Reports identify the host platform and clearly say they establish fixture protocol/business guarantees only.

`native` requires the installed pinned Linux runtimes and Docker/FUSE/Bubblewrap. It enables the gated Go acceptance checks, including actual DSH/LayerFS and CA trust inside the DSH Bubblewrap boundary. Deterministic local model endpoints are used; no provider credential or paid model request is involved. Run this separately from the portable Compose suite. Native and browser results do not follow from a successful fixture test.

Build a deployable Server with `make build-server-image`. Runner packaging, installer/runtime separation, offline archives and staging have one owner in [installation](../../docs/install.md#build-and-publish-a-release). The fixture environment is a protocol/failure harness, not the member installer.

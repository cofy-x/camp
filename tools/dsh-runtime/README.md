# Camp DSH runtime

This independent pnpm workspace pins DSH and its public provider packages in [package.json](package.json) and the frozen lockfile. The shared harness checkout is read-only reference material, not the production runtime. Use the [repository toolchain](../../README.md#local-development).

From the Camp root:

```sh
make install-dsh
node tools/dsh-runtime/probe.mjs
```

The installer allows only the required native build steps recorded in [pnpm-workspace.yaml](pnpm-workspace.yaml). Linux needs Bubblewrap, Bash and native platform libraries. The probe requires working PID namespaces and fails rather than bypassing isolation. Packaged Runner users install none of these components individually; see [installation](../../docs/install.md).

## Wire lifecycle

Camp launches the public CLI with `--profile sdk-minimal --patch ...`. JSON-RPC 2.0 is one message per stdio line; stdout is protocol-only and stderr diagnostic. The adapter lives in [internal/dsh](../../apps/server/internal/dsh).

1. `initialize` supplies cwd, provider, model and per-request output limit.
2. `session/resume` restores an eligible session; `session/prompt` supplies text content blocks. A message ID acknowledges admission, not completion.
3. `session.event` notifications carry `{sessionId,event:{type,seq,time,data}}`. Observe tool/assistant/step/turn events; idle alone is not success.
4. `session/wait` resolves the root turn outcome. Only a completed end reason is normal model completion. `shutdown` and confirmed process exit follow.

There is no public per-session cancellation/close method. EOF or SIGTERM asks for cooperative disposal; Camp escalates and verifies the outer process boundary when needed. An RPC shutdown response or process-group kill alone cannot prove detached descendant cleanup. Session logs/attachments are private, outside tool workspaces.

`initialize` has no credential field. Camp launches DSH with Messages-provider environment values; production uses an Attempt-scoped Server proxy capability, never the real provider key. DSH environment scrubbing alone cannot hide host files, so the Bubblewrap mount boundary excludes configuration, databases and host homes. Use [configuration and budgets](../../docs/dsh.md#configuration-and-budgets) for model selection/limits and [Camp tools](camp-tools/README.md) for production tools.

## Probe boundary

`probe.mjs` exercises the installed CLI with the literal fake credential `camp-keyless-probe` and a local deterministic Messages endpoint. It never reads `.env`; evidence goes to ignored `.data/dsh-probe/run-*` paths. It checks actual Bash writes/tests, protocol completion, rejected out-of-project writes, credential scrubbing, EOF cancellation and forced termination of a detached descendant.

This **standalone upstream-tool probe** exposes a writable workspace and permits network access. It does not establish production command networking or LayerFS isolation. Production disables native filesystem, PTY, persistent Bash and background jobs and substitutes [RemoteOverlay](../../apps/server/internal/dsh/remote_overlay.go) plus Camp capability services. Use `make test-dsh` and `make test-remote-native` with installed native prerequisites for those paths. Ordinary Go tests skip gated native cases; a probe result is not a real-provider or browser result.

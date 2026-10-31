# Project package manager

This independent installation pins the pnpm executable for prepared Node projects. The [manifest](package.json) and lockfile own the version; use the [repository toolchain](../../CONTRIBUTING.md#development) and run from Camp root:

```sh
make install-project-runtime
make test-project-env
```

Camp checks the executable's exact version rather than a global shim that could download another package manager. It resolves this directory beside the DSH runtime. [Profile validation](../../apps/server/internal/projectenv/profile.go) owns supported Node/command checks, manifests and snapshot exclusions.

Dependency preparation uses the platform sandbox with an empty home and a project-owned store. Linux uses Bubblewrap with network access. Mac uses an owned Seatbelt job whose only network grant is an authenticated per-install loopback CONNECT broker: public HTTPS on port 443, checked numeric DNS results, a 2 GiB transfer bound and the existing 180-second preparation timeout. Private/reserved destinations, direct IP/DNS/Unix networking and other ports/methods are denied. TLS remains end-to-end at the package manager. The broker capability is private to preparation and redacted from output.

Frozen installation disables lifecycle scripts and pnpm hooks and exposes no model credentials or host npm configuration. Build-script-dependent packages and arbitrary automatic language-toolchain installation are unsupported; missing tools or failed preparation remain visible. Mac confirms descendant cleanup before publishing a ready cache or removing staging. Uncertain cleanup retains staging and process receipts, suspends the Runner and is reconciled at startup without replay; a Server result receipt alone cannot authorize deleting those recovery records.

In production, [nativeexecutor](../../apps/server/internal/nativeexecutor/executor.go) copies prepared dependencies into the selected private workspace (Linux LayerFS or Mac local). Model commands and [configured checks](../../apps/server/internal/nativeexecutor/checks.go) run there without network or the dependency broker capability. Dependencies are **not** separately mounted read-only there; snapshot exclusions keep managed dependencies out of publication. Do not generalize the local `projectenv.Verify` regression harness's read-only mounts, disposable verification copy or tmpfs cache layout to production.

An additional opt-in regression imports a separate copy of Camp, installs locked workspace dependencies and runs its configured checks. It uses ignored `.data/project-env-camp-check` evidence and no model key:

```sh
CAMP_PROJECT_ENV_CAMP=1 go test -v ./apps/server/internal/projectenv -run TestActualCampEnvironment
```

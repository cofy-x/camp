# Seatbelt qualification

This independent workspace pins the published sandbox-runtime package and its integrity for actual Mac permission probes. The shared source checkout is a read-only reference. The source reference and package identity are distinct: `pin.json` does not claim the npm archive was built from an unreported git SHA.

```sh
pnpm --dir tools/seatbelt-runtime install --frozen-lockfile --ignore-scripts
node tools/seatbelt-runtime/probe.mjs
CAMP_TEST_MAC_NATIVE=1 go test -race ./apps/server/internal/processowner -v
```

The upstream default read policy is measured as permissive, then the explicit read/network configuration is checked against synthetic private files and actual sockets. No model key or existing credential is read. Upstream macOS cleanup is not a process-owner receipt; the Go proof separately exercises independent launchd jobs, double-fork/setsid children and kernel PID-generation signalling. An unsupported kernel operation fails the proof; there is no PID-only fallback. See the [design and complete acceptance contract](../../docs/native-runner.md).

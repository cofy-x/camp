# Linux container sandbox helper

This document covers the Linux x86_64 container operations of `cmd/camp-sandbox`. The host owns the Docker container and installs the executable as a root-owned, non-writable file. Every container request runs as the admitted non-root UID/GID. This helper does not create or administer containers. The same executable's Mac `native-job`/`native-client` modes use the separate [native Runner process boundary](../../../../docs/native-runner.md); Linux mounts and privilege requirements do not apply to that unprivileged Mac path.

## Protocol

Use `camp-sandbox request` with one JSON request on stdin and close stdin after the request. Small requests may instead use `request <base64-json>`; large file writes must use stdin because operating systems limit command argument sizes. Requests and NDJSON response types are defined in `protocol.go`.

The trusted host supplies `roots.workspace` and optional `roots.references`. Tool paths always use `/workspace` or `/references`. The host serializes all file operations and commands for a Run. Shell strings are not rewritten; the container must expose `/workspace` as the working mount or a root-owned alias.

File operations are `read`, `stat`, `lstat`, `list`, `write`, `mkdir`, `remove`, `rename`, and literal-text `search`. A result is one `result` or `error` frame. Binary bytes use JSON base64. Full reads and regular-file stat return a SHA-256 version; writes can require that version or `missing`, using an atomic temporary file and rename. Renames never replace existing destinations. Range reads accept `offset` and optional `length`, read only that range, and do not claim a complete content version. Files and ranges are limited to 64 MiB; traversal and search have explicit entry, byte, depth and match limits. Search reports truncation.

Paths use `openat2` beneath opened roots with no symlinks or magic links. `lstat` and listing may return final-link metadata without following it. Other access rejects links and special files. References are read-only even if the caller forgets its own access check. Partial recursive operations can fail after some entries have changed; only successful atomic writes represent complete saves.

Commands provide either `argv` or `command`; shell commands use Bash without startup profiles. The fixed child environment contains PATH, HOME, TMPDIR and locale, and pinned pnpm settings that prevent automatic dependency reinstallation or package-manager switching. Dependencies are prepared before model admission. A trusted `start` frame reports the supervisor PID and kernel start time. `stdout` and `stderr` frames carry base64 child bytes. A single final `exit` frame includes the exit code and `clean:true` only after all descendants are gone. Reasons include `timeout`, `cancelled`, `output_limit`, and `cleanup_failed`. Output streams while the command is running.

`maxOutputBytes` is a hard streamed-output budget, separate from a caller's retained-output tail. It defaults to 1 MiB and accepts up to 16 MiB. Exceeding the byte budget or 4096 output frames cancels the command; each data frame is at most 16 KiB. Timeouts default to 180 seconds and accept at most 900 seconds.

`camp-sandbox cancel <pid> <startTicks>` checks process identity and signals via a pidfd. The host must allow the cleanup grace period. Missing or malformed framing, helper death, transport failure, or missing `clean:true` makes the container unusable: the host must stop it before any later operation.

## Process and network boundary

The supervisor is non-dumpable so a same-UID command cannot reopen its protocol descriptor or memory through `/proc`. It is a child subreaper, terminates background work even after normal shell exit, and reaps detached/double-forked children before reporting completion. A same-UID command can kill its supervisor; container destruction by the host is therefore part of the boundary.

Commands receive a synchronized seccomp filter that denies IPv4/IPv6 socket creation and `io_uring_setup`, including direct syscalls. Compatibility ABIs (x32 and 32-bit `socketcall`) are rejected. The filter is inherited by spawned processes; file/FUSE operations and local Unix IPC remain available. This keeps model commands away from LayerFS's container-local TCP control service. The trusted host's LayerFS connection is unaffected. Network-dependent preparation must happen through a separate trusted preparation step, not a model command.

Container capabilities, device access, read-only helper/references, backing-store permissions, process/memory limits, and transport cleanup remain host duties.

## Verification

```sh
go test -race ./apps/server/internal/sandboxexec -count=1
go vet ./apps/server/internal/sandboxexec ./apps/server/cmd/camp-sandbox
go build -o dist/camp-sandbox ./apps/server/cmd/camp-sandbox
```

Tests use real non-root subprocesses and cover binary framing, streaming, timeouts, cancellation, output limits, detached descendants, supervisor death, protocol-spoof attempts, network syscall/ABI bypasses, exact file bytes, guarded writes, symlinks, special files, range limits and racing path replacement. LayerFS/Docker integration establishes the additional FUSE and container transport guarantees; subprocess tests alone do not establish those guarantees.

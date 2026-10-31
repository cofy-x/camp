# Native directory to LayerFS checkpoint qualification

This probe tests the pinned LayerFS public `fs.apply` API from an exclusively owned, quiescent regular-file tree. It does not select a Runner backend, start Agent commands, grant enrollment/publication authority, or implement a production Mac transport.

Run on Camp's supported privileged Linux host after `make install-project-runtime install-layerfs`:

```sh
node tools/layerfs-runtime/hybrid/probe.mjs dist/hybrid-proof.json
```

The script creates private generated fixtures under `.data`, starts an owned Store and project, and closes only its own project/sandboxes. It never resets shared LayerFS checkouts or an existing Runner. No model calls occur. Failed fixtures and uncertain receipts remain for diagnosis; successful workload inputs and tar files are removed after measurement. Generated outputs stay ignored.

The proof covers three repetitions each of a small text tree, 10,000 small files and 128 MiB of deterministic incompressible binary data, followed by edit/delete/rename/executable-mode changes. It compares full and incremental administrative transfer plus Commit, independently remounts each resulting branch through FUSE and checks aggregate file bytes/modes against the sealed manifest. An unchanged generation must retain the same head and transfer no bytes. Explicit generation/branch receipts prohibit replay after uncertainty. The native tree and each worker branch have one writer; this is not a concurrent branch reconciliation algorithm.

Failure injection occurs at the SDK call boundary: one refusal before applying, and one lost reply after a real Commit. The source remains intact and reopening an uncertain receipt refuses another mutation. These are controlled outcome tests, not a real network outage. Symlinks/hardlinks/special files, oversized trees and case/Unicode aliases are refused. The fixture represents the bounded accepted regular-file checkpoint surface; dependency symlinks and full host working trees are outside this proof.

The report distinguishes scan, local GNU tar creation, transfer/Commit, logical bytes sent, fixed runtime/image identity and Controller RSS. Store runs in the Linux Node process, so memory includes that Store and excludes Docker containers/VM memory. Peak RSS can contain inherited process history and has a recorded baseline. This is **not** Mac/APFS, Linux ARM64, separate-worker transport or complete-VM performance. Local GNU tar timing is also not the Go native capture implementation measured by Mac CI.

The implemented [Mac checkpoint worker](../../layerfs-worker/README.md) separately owns authenticated bounded transport, native ARM64 artifacts, complete-tree receipts and actual Mac interruption/performance qualification. This regular-file probe cannot establish those guarantees. The upstream comparison route encodes binary payloads as base64 JSON frames; this local API proof establishes neither zero-copy nor negligible memory/wire cost. Native-local remains the recommended Mac default under the [worker decision](../../../docs/layerfs-worker.md#default-and-deployment-decision). See the [Runner contract](../../../docs/native-runner.md) for shared ownership and acceptance.

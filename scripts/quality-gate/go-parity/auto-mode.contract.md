# Classifier rules and CLI version boundaries

`backend/internal/automode` implements the original external classifier template
recipe and `auto-mode defaults` / `config` business handlers. The embedded
`permissions_external.txt` is copied byte for byte from
`src/utils/permissions/yolo-classifier-prompts/permissions_external.txt`; ordinary
tests verify that provenance and never overwrite the original or a golden.

The configuration getter validates each complete raw source object, then merges
user, local, flag and policy settings in that order. Project settings are excluded
from this permission-policy input. Configured empty arrays replace defaults;
absent sections inherit them; every exact `$defaults` expands in place. Internal
`deny` and `classifyAllShell` validation retain the Source behavior. Quoted raw
strings preserve UTF-16 units and use the shared JavaScript JSON codec at output.

The ordinary parity lane executes the actual Source getter and handlers twice
from independent disposable processes, using original raw source snapshots. Only
the settings getter's external input is frozen for these component cases. Two
additional Source profiles run the actual `entrypoints/cli.tsx` → `main` →
Commander `preAction` → real `init` / sinks / migrations → real handler with
private files and native stdout/stderr/exit. The Source emits one allowed
loopback HEAD preconnect and zero model requests. Exact system policy and Git
ancestor `.git` paths have observable absent adapters; no real user/system
configuration or secure-store data is read. The Go side is the real business
handler component, **not the migrated startup host**. These tests compare output
and do not claim startup/file-effect equivalence.

The actual Go development binary separately implements Source's single-argument
`--version`, `-v`, `-V` fast route, including its linker-injected version. The
version lane runs the real Source entrypoint twice and the real Go executable;
it asserts no settings, credential, policy or other Source module loading. The
Bun observer serves exactly the original file bytes, without rewriting code.
The explicit Recovery route retains its separate version contract.

Normal Go CLI business dispatch remains guarded until shared initialization,
settings loading/cache, global-config persistence/locking/backup, MDM/keychain,
safe environment/CA/mTLS/proxy, sinks, migration ordering, remote policy and
cleanup are implemented. The actual cold Source startup writes firstStartTime,
migrationVersion 11 and model-migration markers even for `auto-mode defaults`;
printing the same rules is insufficient to open that dispatcher. Configuration
errors, full registration circuit breaker, critique/model execution, normal
auth/plugin/MCP/agents handlers, complete headless host and TUI remain pending.

All tests use fake credentials, private HOME/config/cwd and no public network.
They provide deterministic component and single fast-route evidence; they are
not the live DeepSeek Benchmark or a production cutover gate.

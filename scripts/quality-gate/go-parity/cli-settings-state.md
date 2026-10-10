# CLI settings runtime state

`backend/internal/clisettings.State` ports the actual runtime state in
`src/utils/settings/settingsCache.ts` and `internalWrites.ts`. It is retained for
the CLI process, rather than allocated for each request/session. It is separate
from desktop settings and CLI global-config persistence.

The three caches retain Source references: the merged session record, per-source
settings and path-keyed parsed settings/errors. Per-source cache miss and cached
null are distinct. `ResetSettingsCache` clears all three, but retains the plugin
base and internal-write marks. Plugin clear affects only the plugin base. The
parse layer must clone returned validated settings in the same place Source does;
this state holder does not silently clone stored references.

Write marks preserve exact keys and Date.now-style clock calls. A mark is consumed
only when `now - timestamp < windowMs`; equality fails, failed matches retain the
mark, successful matches consume it once, overwrite replaces its timestamp, and
the explicit clear removes all marks. Reversed clocks and fractional windows are
included. These semantics matter when a file watcher suppresses an internal write.

The Source oracle imports the actual two modules after a disposable HOME/config
guard and executes original cache values plus operations. It records reference
identity, null/missing tags, full JavaScript-stringified settings/error content and
clock-call counts. The independent Go fixture receives only those original values
and operations. Five profiles cover cache replacement/reset, retained plugin state,
shared references and errors, raw UTF-16/numeric unknown values, independent exact
source/path keys and write-mark transitions. Native goldens are captured from
Source output, and runtime concurrency tests exercise independent cache/write
slots under race. Comparator mutants and pre-import guard failures are separate
negative evidence, not successful migration profiles.

This component does not implement SettingsSchema, permission validation/filtering,
cached parser clone policy, the CLI settings file writer, file watcher, managed
policy selection, plugin loading or startup migrations. In particular the writer
uses the path parse cache despite bypassing the per-source cache, marks an internal
write before flush, and resets these caches only after a successful write. Those
callers must be migrated and integrated before this state boundary can count as a
complete CLI settings path. No persisted format has changed.

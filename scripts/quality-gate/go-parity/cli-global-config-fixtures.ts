export type GlobalConfigStep = {
  operation: 'enable' | 'get' | 'save' | 'firstStart' | 'externalWrite' | 'remove' | 'poll' | 'pollPending' | 'releasePoll' | 'advance' | 'project' | 'saveProject' | 'leaseAcquire' | 'leaseTick' | 'leaseRelease' | 'leaseSteal' | 'leaseRemove'
  updater?: 'same' | 'clone' | 'patch' | 'throwOnce' | 'throw'
  patch?: string
  raw?: string
  rawBytes?: number[]
  key?: string
  milliseconds?: number
}
export type GlobalConfigProfile = {
  name: string
  raw?: string
  legacy?: boolean
  suffix?: string
  mode?: number
  symlink?: boolean
  backups?: Record<string, string>
  backupBytes?: Record<string, number[]>
  lock?: 'held' | 'stale'
  faults?: Array<{ operation: string; match?: string; occurrence: number; repeat?: boolean }>
  steps: GlobalConfigStep[]
}

export const globalConfigProfiles: GlobalConfigProfile[] = [
  { name: 'absent-first-start-idempotent', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'firstStart' }, { operation: 'firstStart' }, { operation: 'get' }] },
  { name: 'unknown-order-utf16-numbers', raw: '{"future":{"z":9007199254740993,"2":1,"a":"\\ud800"},"\\ud800":1,"\\udfff":2,"theme":"dark","10":10,"2":2}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'clone' }, { operation: 'get' }] },
  { name: 'bom-and-default-filter', raw: '\ufeff{"theme":"light","verbose":false,"env":{},"customApiKeyResponses":{"approved":[],"rejected":[]},"unknown":null}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'patch', patch: '{"theme":"dark","verbose":false,"future":"keep"}' }, { operation: 'get' }] },
  { name: 'same-reference-does-not-write', raw: '{"unknown":true}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'same' }, { operation: 'get' }] },
  { name: 'throw-once-fallback-updater-twice', raw: '{"unknown":true}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'throwOnce', patch: '{"after":1}' }, { operation: 'get' }] },
  { name: 'throw-both-fallback-preserves-file', raw: '{}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'throw' }, { operation: 'get' }] },
  { name: 'missing-file-backup-hint', backups: { '.claude.json.backup.1000': '{"old":true}' }, steps: [{ operation: 'enable' }, { operation: 'get' }] },
  { name: 'strict-corrupt-enable-before-recovery', raw: '{', steps: [{ operation: 'enable' }, { operation: 'enable' }, { operation: 'get' }, { operation: 'get' }] },
  { name: 'blank-is-corrupt', raw: '', steps: [{ operation: 'enable' }, { operation: 'get' }] },
  { name: 'array-startup-spread', raw: '["a",{"z":2}]', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'clone' }] },
  { name: 'string-startup-utf16-spread', raw: '"a\\ud800b"', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'clone' }] },
  { name: 'null-startup-spread', raw: 'null', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'clone' }] },
  { name: 'boolean-startup-spread', raw: 'false', steps: [{ operation: 'enable' }, { operation: 'get' }] },
  { name: 'read-before-enable', raw: '{}', steps: [{ operation: 'get' }, { operation: 'enable' }, { operation: 'get' }] },
  { name: 'legacy-config-path-wins', legacy: true, raw: '{"future":true}', suffix: '-fixture', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'patch', patch: '{"after":1}' }] },
  { name: 'explicit-oauth-suffix', suffix: '-fixture', raw: '{}', steps: [{ operation: 'enable' }, { operation: 'firstStart' }] },
  { name: 'new-file-secure-mode', steps: [{ operation: 'enable' }, { operation: 'save', updater: 'patch', patch: '{"fresh":true}' }] },
  { name: 'existing-mode-preserved', mode: 0o640, raw: '{}', steps: [{ operation: 'enable' }, { operation: 'save', updater: 'patch', patch: '{"fresh":true}' }] },
  { name: 'relative-symlink-preserved', symlink: true, mode: 0o640, raw: '{}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'patch', patch: '{"after":1}' }] },
  { name: 'held-lock-real-fallback', lock: 'held', raw: '{}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'patch', patch: '{"after":1}' }] },
  { name: 'stale-lock-reclaimed', lock: 'stale', raw: '{}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'patch', patch: '{"after":1}' }] },
  { name: 'auth-object-corrupt-reread', raw: '{"oauthAccount":{"id":"fake"},"hasCompletedOnboarding":true,"future":1}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'externalWrite', raw: '{' }, { operation: 'save', updater: 'patch', patch: '{"after":1}' }, { operation: 'get' }] },
  { name: 'auth-null-missing-reread', raw: '{"oauthAccount":null}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'externalWrite', raw: '{}' }, { operation: 'save', updater: 'patch', patch: '{"after":1}' }, { operation: 'get' }] },
  { name: 'onboarding-fallback-guard', lock: 'held', raw: '{"hasCompletedOnboarding":true}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'externalWrite', raw: '{"hasCompletedOnboarding":false}' }, { operation: 'save', updater: 'patch', patch: '{"after":1}' }, { operation: 'get' }] },
  { name: 'latest-projects-win-clean-history', raw: '{"projects":{"/fixture":{"history":[1],"unknown":true},"/other":{"future":2}},"top":3}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'patch', patch: '{"projects":{"/wrong":{"mustNotPersist":true}},"after":1}' }, { operation: 'get' }] },
  { name: 'legacy-install-migration-memory-only', raw: '{"autoUpdaterStatus":"disabled","unknown":1}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'clone' }, { operation: 'get' }] },
  { name: 'first-start-falsy-empty', raw: '{"firstStartTime":""}', steps: [{ operation: 'enable' }, { operation: 'firstStart' }, { operation: 'get' }] },
  { name: 'first-start-falsy-zero', raw: '{"firstStartTime":0}', steps: [{ operation: 'enable' }, { operation: 'firstStart' }, { operation: 'get' }] },
  { name: 'first-start-null', raw: '{"firstStartTime":null}', steps: [{ operation: 'enable' }, { operation: 'firstStart' }, { operation: 'get' }] },
  { name: 'backup-throttle-and-retain-five', raw: '{}', backups: Object.fromEntries([1, 2, 3, 4, 5, 6].map(n => [`.claude.json.backup.${946684800000 - n * 100000}`, String(n)])), steps: [{ operation: 'enable' }, { operation: 'save', updater: 'patch', patch: '{"n":1}' }, { operation: 'save', updater: 'patch', patch: '{"n":2}' }, { operation: 'advance', milliseconds: 60000 }, { operation: 'save', updater: 'patch', patch: '{"n":3}' }] },
  { name: 'backup-nan-timestamp', raw: '{}', backups: { '.claude.json.backup.invalid': 'previous' }, steps: [{ operation: 'enable' }, { operation: 'save', updater: 'patch', patch: '{"after":1}' }] },
  { name: 'memory-hit-no-external-reread', raw: '{"future":"old"}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'externalWrite', raw: '{"future":"new"}' }, { operation: 'get' }, { operation: 'save', updater: 'patch', patch: '{"after":1}' }, { operation: 'get' }] },
  { name: 'watcher-external-object', raw: '{}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'externalWrite', raw: '{"new":1}' }, { operation: 'poll' }, { operation: 'get' }] },
  { name: 'watcher-array-spread', raw: '{}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'externalWrite', raw: '["index",2]' }, { operation: 'poll' }, { operation: 'get' }] },
  { name: 'watcher-null-rejected', raw: '{}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'externalWrite', raw: 'null' }, { operation: 'poll' }, { operation: 'get' }] },
  { name: 'watcher-invalid-rejected', raw: '{}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'externalWrite', raw: '{' }, { operation: 'poll' }, { operation: 'get' }] },
  { name: 'rename-failure-real-direct-fallback', raw: '{}', faults: [{ operation: 'rename', occurrence: 1 }], steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'patch', patch: '{"fallback":true}' }] },
  { name: 'chmod-failure-real-direct-fallback', raw: '{}', faults: [{ operation: 'chmod', occurrence: 1 }], steps: [{ operation: 'enable' }, { operation: 'save', updater: 'patch', patch: '{"fallback":true}' }] },
  { name: 'temp-write-failure-cleanup-then-direct', raw: '{}', faults: [{ operation: 'write', match: '.tmp.', occurrence: 1 }], steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'patch', patch: '{"fallback":true}' }] },
  { name: 'both-writes-fail-lock-fallback-retry', raw: '{}', faults: [{ operation: 'write', occurrence: 1, repeat: true }], steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'patch', patch: '{"never":1}' }, { operation: 'get' }] },
  { name: 'backup-copy-failure-still-writes', raw: '{}', faults: [{ operation: 'copy', match: '.backup.', occurrence: 1 }], steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'patch', patch: '{"after":1}' }] },
  { name: 'corrupt-copy-failure-retains-diagnostic', raw: '{', faults: [{ operation: 'copy', match: '.corrupted.', occurrence: 1 }], steps: [{ operation: 'enable' }, { operation: 'get' }] },
  { name: 'non-json-read-error-returns-defaults', raw: '{"old":1}', faults: [{ operation: 'read', occurrence: 1 }], steps: [{ operation: 'enable' }, { operation: 'get' }] },
  { name: 'project-read-and-write-preserves-siblings', raw: '{"projects":{"<HOME>":{"allowedTools":"[\\"Read\\"]","unknown":1},"/other":{"future":2}},"top":3}', steps: [{ operation: 'enable' }, { operation: 'project' }, { operation: 'saveProject', updater: 'patch', patch: '{"enabledMcpjsonServers":["fixture"]}' }, { operation: 'get' }] },
  { name: 'project-default-same-reference', raw: '{}', steps: [{ operation: 'enable' }, { operation: 'project' }, { operation: 'saveProject', updater: 'same' }, { operation: 'get' }] },
  { name: 'project-fallback-updater-twice', raw: '{}', steps: [{ operation: 'enable' }, { operation: 'saveProject', updater: 'throwOnce', patch: '{"fixture":true}' }, { operation: 'get' }] },
  { name: 'reserved-pickBy-property-keys', raw: '{"__proto__":{"future":1},"constructor":"preserve-if-source-does","prototype":true,"toString":"own","unknown":2}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'clone' }, { operation: 'get' }] },
  { name: 'backup-number-infinite-hex', raw: '{}', backups: { '.claude.json.backup.0x10000000000000000': 'old' }, steps: [{ operation: 'enable' }, { operation: 'save', updater: 'clone' }] },
  { name: 'backup-number-invalid-underscore', raw: '{}', backups: { '.claude.json.backup.946_684800000': 'old' }, steps: [{ operation: 'enable' }, { operation: 'save', updater: 'clone' }] },
  { name: 'actual-proper-lock-lease-refresh', raw: '{}', steps: [{ operation: 'leaseAcquire' }, { operation: 'advance', milliseconds: 5000 }, { operation: 'leaseTick' }, { operation: 'leaseRelease' }] },
  { name: 'actual-proper-lock-lease-compromised-mtime', raw: '{}', steps: [{ operation: 'leaseAcquire' }, { operation: 'leaseSteal' }, { operation: 'advance', milliseconds: 5000 }, { operation: 'leaseTick' }, { operation: 'leaseRelease' }] },
  { name: 'actual-proper-lock-lease-compromised-missing', raw: '{}', steps: [{ operation: 'leaseAcquire' }, { operation: 'leaseRemove' }, { operation: 'advance', milliseconds: 5000 }, { operation: 'leaseTick' }, { operation: 'leaseRelease' }] },
  { name: 'actual-proper-lock-release-twice', raw: '{}', steps: [{ operation: 'leaseAcquire' }, { operation: 'leaseRelease' }, { operation: 'leaseRelease' }] },
  { name: 'projects-array-no-history-shape-preserved', raw: '{"projects":[{"future":1},"scalar",2]}', steps: [{ operation: 'enable' }, { operation: 'save', updater: 'clone' }, { operation: 'get' }] },
  { name: 'projects-array-history-converts-entries', raw: '{"projects":[{"history":[1],"future":1},"scalar",2]}', steps: [{ operation: 'enable' }, { operation: 'save', updater: 'clone' }, { operation: 'get' }] },
  { name: 'projects-string-and-number-passthrough', raw: '{"projects":"a\\ud800"}', steps: [{ operation: 'enable' }, { operation: 'save', updater: 'clone' }, { operation: 'get' }, { operation: 'externalWrite', raw: '{"projects":1e400}' }, { operation: 'save', updater: 'clone' }, { operation: 'get' }] },
  { name: 'projects-null-entry-source-error-and-fallback', raw: '{"projects":{"/fixture":null}}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'clone' }, { operation: 'get' }] },
  { name: 'projects-reserved-entry-cleanup', raw: '{"projects":{"__proto__":{"future":1},"constructor":{"history":[],"future":2},"\\ud800":{"history":[],"future":3},"\\udfff":{"future":4}}}', steps: [{ operation: 'enable' }, { operation: 'save', updater: 'clone' }, { operation: 'get' }] },
  { name: 'startup-corrupt-mkdir-failed-uncached-retry', raw: '{', faults: [{ operation: 'mkdir', match: 'backups', occurrence: 1 }], steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'get' }] },
  { name: 'corrupt-reread-deduplicates-byte-backup', raw: '{', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'same' }, { operation: 'save', updater: 'same' }] },
  { name: 'corrupt-original-with-good-backup-hint', raw: '{', backups: { '.claude.json.backup.1000': '{"good":1}' }, steps: [{ operation: 'enable' }, { operation: 'get' }] },
  { name: 'backup-cleanup-failure-does-not-reject-write', raw: '{}', backups: Object.fromEntries([1, 2, 3, 4, 5, 6].map(n => [`.claude.json.backup.${946684800000 - n * 100000}`, String(n)])), faults: [{ operation: 'remove', match: '.backup.', occurrence: 1 }], steps: [{ operation: 'enable' }, { operation: 'save', updater: 'clone' }] },
  ...['migrated', 'installed', 'enabled'].map(status => ({ name: 'legacy-install-' + status, raw: JSON.stringify({ autoUpdaterStatus: status, autoUpdates: null }), steps: [{ operation: 'enable' }, { operation: 'get' }] as GlobalConfigStep[] })),
  { name: 'explicit-install-method-bypasses-migration', raw: '{"installMethod":null,"autoUpdates":false}', steps: [{ operation: 'enable' }, { operation: 'get' }] },
  { name: 'legacy-dot-backup-hint', backups: { '../.claude.json.backup': '{"good":1}' }, steps: [{ operation: 'enable' }, { operation: 'get' }] },
  ...['', '\ufeffInfinity\u00a0', '-Infinity', '0b10', '0o10', '+0x10', '0xG', '.5', '1e9999'].map(timestamp => ({ name: 'backup-number-grammar-' + JSON.stringify(timestamp), raw: '{}', backups: { ['.claude.json.backup.' + timestamp]: 'previous' }, steps: [{ operation: 'enable' }, { operation: 'save', updater: 'clone' }] as GlobalConfigStep[] })),
  ...['{', 'null', '"\ud800"', '1e400', String.fromCharCode(34, 92, 0xd800, 34)].map(text => ({ name: 'project-legacy-allowedTools-' + JSON.stringify(text), raw: JSON.stringify({ projects: { '<HOME>': { allowedTools: text, future: 1 } } }), steps: [{ operation: 'enable' }, { operation: 'project' }, { operation: 'get' }, { operation: 'saveProject', updater: 'clone' }] as GlobalConfigStep[] })),
  { name: 'backup-filename-sorts-JS-UTF16', backups: { '.claude.json.backup.\ue000': 'bmp', '.claude.json.backup.\u{10000}': 'astral' }, steps: [{ operation: 'enable' }, { operation: 'get' }] },
  { name: 'watcher-pending-read-old-get-then-completion', raw: '{"future":"old"}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'externalWrite', raw: '{"future":"new"}' }, { operation: 'pollPending' }, { operation: 'get' }, { operation: 'releasePoll' }, { operation: 'get' }] },
  { name: 'watcher-pending-read-cannot-regress-write-through', raw: '{"future":"old"}', steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'externalWrite', raw: '{"future":"new"}' }, { operation: 'pollPending' }, { operation: 'get' }, { operation: 'advance', milliseconds: 3000 }, { operation: 'save', updater: 'patch', patch: '{"future":"write-through"}' }, { operation: 'releasePoll' }, { operation: 'get' }] },
  { name: 'corrupt-byte-backups-deduplicate-decoded-utf8', rawBytes: [123, 255], backupBytes: { '.claude.json.corrupted.1000': [123, 128] }, steps: [{ operation: 'enable' }, { operation: 'get' }] },
  { name: 'valid-json-invalid-utf8-decoded-before-parse-original-backup-bytes', rawBytes: [123, 34, 102, 34, 58, 34, 226, 130, 34, 125], steps: [{ operation: 'enable' }, { operation: 'get' }, { operation: 'save', updater: 'clone' }] },
]

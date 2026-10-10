export type CLIMigrationsProfile = {
  name: string
  global?: string
  user?: string
  local?: string
  changelog?: string
  env?: Record<string, string>
  override?: string
  fault?: 'user' | 'local' | 'global' | 'changelog'
  faultStart?: number
  schema?: string
  twice?: boolean
}
export const cliMigrationsProfiles: CLIMigrationsProfile[] = [
  { name: 'missing-defaults' },
  { name: 'numeric-eleven-async', global: '{"migrationVersion":11,"cachedChangelog":"old notes","autoUpdates":false}' },
  { name: 'string-eleven-sync', global: '{"migrationVersion":"11","cachedChangelog":"old notes"}' },
  { name: 'false-auto-updates', global: '{"autoUpdates":false,"autoUpdatesProtectedForNative":false,"future":{"\\ud800":1,"�":2}}', user: '{"env":{"DISABLE_AUTOUPDATER":"0","KEEP":1},"future":true}' },
  { name: 'native-protected', global: '{"autoUpdates":false,"autoUpdatesProtectedForNative":true}' },
  { name: 'truthy-bypass', global: '{"bypassPermissionsModeAccepted":"yes"}', user: '{"skipDangerousModePermissionPrompt":false}' },
  { name: 'local-bypass-accepted', global: '{"bypassPermissionsModeAccepted":true}', local: '{"skipDangerousModePermissionPrompt":true}' },
  { name: 'mcp-merge-local-first', global: '{"projects":{"<HOME>/project":{"enableAllProjectMcpServers":true,"enabledMcpjsonServers":["old","same","\\ud800"],"disabledMcpjsonServers":["bad"],"future":{"keep":1}}}}', local: '{"enableAllProjectMcpServers":false,"enabledMcpjsonServers":["local","same","�"],"disabledMcpjsonServers":["bad","local"]}' },
  { name: 'mcp-already-boolean', global: '{"projects":{"<HOME>/project":{"enableAllProjectMcpServers":false,"enabledMcpjsonServers":[],"disabledMcpjsonServers":[]}}}', local: '{"enableAllProjectMcpServers":true}' },
  { name: 'sonnet-1m-override', user: '{"model":"sonnet[1m]"}', override: 'sonnet[1m]', twice: true },
  { name: 'legacy-opus-chain', user: '{"model":"claude-opus-4-1-20250805","future":true}' },
  { name: 'legacy-remap-disabled', user: '{"model":"claude-opus-4-1"}', env: { CLAUDE_CODE_DISABLE_LEGACY_MODEL_REMAP: 'true' } },
  { name: 'opus-one-million', user: '{"model":"opus"}' },
  { name: 'opus-context-disabled', user: '{"model":"opus"}', env: { CLAUDE_CODE_DISABLE_1M_CONTEXT: '1' } },
  { name: 'remote-old-false', global: '{"replBridgeEnabled":false}' },
  { name: 'remote-truthy-empty-array', global: '{"replBridgeEnabled":[]}' },
  { name: 'remote-new-null-preserves-old', global: '{"replBridgeEnabled":true,"remoteControlAtStartup":null}' },
  { name: 'auto-offer-is-real-opt-in', global: '{"cachedGrowthBookFeatures":{"tengu_auto_mode_config":{"enabled":"enabled"}}}', user: '{"skipAutoPermissionPrompt":true,"permissions":{"defaultMode":"default","future":"\\ud800"}}' },
  { name: 'user-return-error-continues', global: '{"autoUpdates":false,"bypassPermissionsModeAccepted":true}', fault: 'user' },
  { name: 'local-return-error-removes-legacy', global: '{"projects":{"<HOME>/project":{"enableAllProjectMcpServers":true,"enabledMcpjsonServers":["old"]}}}', fault: 'local' },
  { name: 'invalid-json-user-continues', global: '{"autoUpdates":false,"bypassPermissionsModeAccepted":true}', user: '{' },
  { name: 'invalid-model-raw-fallback', user: '{"model":null,"future":42}', global: '{"autoUpdates":false}' },
  { name: 'changelog-existing-untouched', global: '{"migrationVersion":11,"cachedChangelog":"old"}', changelog: 'existing' },
  { name: 'changelog-write-error-still-clears', global: '{"migrationVersion":11,"cachedChangelog":"old"}', fault: 'changelog' },
  { name: 'global-failure-partial-no-marker', global: '{"autoUpdates":false}', fault: 'global' },
  { name: 'already-marker-truthy-nonstring-changelog', global: '{"migrationVersion":11,"cachedChangelog":123}' },
  { name: 'already-marked-scientific-number', global: '{"migrationVersion":1.1e1,"cachedChangelog":false}' },
  { name: 'completion-flags-noop', global: '{"opusProMigrationComplete":1,"sonnet1m45MigrationComplete":true,"hasResetAutoModeOptInForDefaultOffer":true,"replBridgeEnabled":null}', user: '{"model":"sonnet[1m]"}', override: 'sonnet[1m]' },
  { name: 'empty-changelog-still-started', global: '{"migrationVersion":11,"cachedChangelog":""}' },
  { name: 'changelog-lone-surrogate-bytes', global: '{"migrationVersion":11,"cachedChangelog":"a\\ud800b\\udfff"}' },
  { name: 'bypass-user-short-circuit', global: '{"bypassPermissionsModeAccepted":[]}', user: '{"skipDangerousModePermissionPrompt":true}', local: '{"model":null}' },
  { name: 'bypass-falsy-preserves-field', global: '{"bypassPermissionsModeAccepted":0}' },
  { name: 'mcp-new-local-bool-and-lists', global: '{"projects":{"<HOME>/project":{"enableAllProjectMcpServers":false,"enabledMcpjsonServers":["x","\\u0078"],"disabledMcpjsonServers":["d","d"]}}}' },
  { name: 'mcp-null-enable-stored', global: '{"projects":{"<HOME>/project":{"enableAllProjectMcpServers":null,"disabledMcpjsonServers":["d"]}}}' },
  { name: 'legacy-remap-js-trim', user: '{"model":"claude-opus-4-0"}', env: { CLAUDE_CODE_DISABLE_LEGACY_MODEL_REMAP: '\ufeffTRUE\u00a0' } },
  { name: 'global-failure-bypass-caught', global: '{"bypassPermissionsModeAccepted":true}', fault: 'global' },
  { name: 'global-failure-mcp-caught', global: '{"projects":{"<HOME>/project":{"enableAllProjectMcpServers":true}}}', fault: 'global' },
  { name: 'global-failure-sonnet-after-model', user: '{"model":"sonnet[1m]"}', override: 'sonnet[1m]', fault: 'global', faultStart: 2 },
  { name: 'global-failure-legacy-after-model', user: '{"model":"claude-opus-4-0"}', fault: 'global', faultStart: 3 },
  { name: 'global-failure-only-final-marker', fault: 'global', faultStart: 3 },
  { name: 'global-failure-background-swallowed', global: '{"migrationVersion":11,"cachedChangelog":"old"}', fault: 'global' },
  ...[
    '{"skipDangerousModePermissionPrompt":true,"skipAutoPermissionPrompt":false,"enableAllProjectMcpServers":true,"enabledMcpjsonServers":["\\ud800","�"],"disabledMcpjsonServers":[],"permissions":{"defaultMode":"auto","future":123}}',
    '{"skipDangerousModePermissionPrompt":null,"skipAutoPermissionPrompt":1,"enableAllProjectMcpServers":"yes","enabledMcpjsonServers":[1],"disabledMcpjsonServers":null}',
    '{"permissions":{"defaultMode":"invalid","disableBypassPermissionsMode":null,"disableAutoMode":"x","additionalDirectories":[1]}}',
    '{"permissions":{"defaultMode":"plan","disableBypassPermissionsMode":"disable","disableAutoMode":"disable","additionalDirectories":["\\ud800"],"future":1}}',
    '{"permissions":null,"enabledMcpjsonServers":false,"disabledMcpjsonServers":"x"}',
    '{"permissions":{"additionalDirectories":null},"enabledMcpjsonServers":[],"skipAutoPermissionPrompt":true}',
  ].map((schema, index) => ({ name: 'migration-schema-' + index, schema })),
]

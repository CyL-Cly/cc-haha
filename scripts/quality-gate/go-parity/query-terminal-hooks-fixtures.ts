export type HookOperation = { kind: string; event?: string; source?: string; raw?: string; sid?: string; mode?: string; value?: boolean }
export type HookProfile = { name: string; files?: Record<string, string>; simple?: string; interactive?: boolean; trust?: boolean; querySource?: string; agentID?: string; operations: HookOperation[]; supported?: boolean }
const common: HookOperation[] = [{ kind: 'snapshot' }, { kind: 'post' }, { kind: 'failure' }, { kind: 'normal' }, { kind: 'cache' }]
export const queryTerminalHookProfiles: HookProfile[] = [
  { name: 'absent-original-files', operations: common },
  { name: 'empty-original-files', files: { userSettings: '{}', projectSettings: '{}', localSettings: '{}' }, operations: common },
  { name: 'empty-hooks-original-schema', files: { userSettings: '{"hooks":{}}' }, operations: common },
  { name: 'empty-events-original-order', files: { userSettings: '{"hooks":{"StopFailure":[],"Stop":[]}}', projectSettings: '{"hooks":{"SubagentStop":[],"Stop":[]}}' }, operations: common },
  { name: 'snapshot-and-settings-independent-cache', files: { userSettings: '{"hooks":{"Stop":[]}}' }, operations: [{ kind: 'snapshot' }, { kind: 'write', source: 'userSettings', raw: '{"hooks":{"StopFailure":[]}}' }, { kind: 'snapshot' }, { kind: 'reset' }, { kind: 'snapshot' }, { kind: 'update' }, { kind: 'snapshot' }, { kind: 'normal' }, { kind: 'cache' }] },
  { name: 'merged-disable-empty-policy', files: { userSettings: '{"disableAllHooks":true,"hooks":{"Stop":[]}}' }, operations: common },
  { name: 'higher-source-disable-false', files: { userSettings: '{"disableAllHooks":true,"hooks":{"Stop":[]}}', localSettings: '{"disableAllHooks":false,"hooks":{"StopFailure":[]}}' }, operations: common },
  { name: 'registered-empty-array-existence', operations: [{ kind: 'register', event: 'StopFailure', mode: 'array' }, { kind: 'register', event: 'Stop', mode: 'array' }, ...common, { kind: 'clearRegistry' }, { kind: 'normal' }] },
  { name: 'registered-empty-matcher-simple', simple: '1', operations: [{ kind: 'register', event: 'StopFailure', mode: 'matcher' }, { kind: 'register', event: 'Stop', mode: 'matcher' }, ...common] },
  { name: 'registered-empty-matcher-full-resolver', simple: '0', operations: [{ kind: 'register', event: 'StopFailure', mode: 'matcher' }, { kind: 'register', event: 'Stop', mode: 'matcher' }, ...common] },
  { name: 'session-empty-array-is-truthy', simple: '1', operations: [{ kind: 'session', event: 'Stop', sid: 'hook-main', mode: 'array' }, { kind: 'session', event: 'StopFailure', sid: 'hook-main', mode: 'array' }, ...common, { kind: 'clearSession', sid: 'hook-main' }, { kind: 'normal' }] },
  { name: 'session-empty-matcher-full-resolver', simple: '0', operations: [{ kind: 'session', event: 'Stop', sid: 'hook-main', mode: 'matcher' }, ...common] },
  { name: 'subagent-stop-and-main-failure-sid', simple: '1', agentID: 'hook-agent', operations: [{ kind: 'session', event: 'SubagentStop', sid: 'hook-agent', mode: 'array' }, { kind: 'session', event: 'StopFailure', sid: 'hook-agent', mode: 'array' }, ...common] },
  { name: 'interactive-untrusted-empty-matcher', simple: '0', interactive: true, trust: false, operations: [{ kind: 'register', event: 'Stop', mode: 'matcher' }, { kind: 'register', event: 'StopFailure', mode: 'matcher' }, ...common] },
  { name: 'interactive-trusted-empty-matcher', simple: '0', interactive: true, trust: true, operations: [{ kind: 'register', event: 'Stop', mode: 'matcher' }, ...common] },
  { name: 'non-main-query-does-not-replace-cache', querySource: 'agent', operations: common },
  { name: 'original-empty-utf16-unknown-metadata', files: { userSettings: '{"hooks":{},"future":"\\ud800", "model":"deepseek-flash"}' }, operations: common },
  { name: 'registered-active-suppressed-by-simple', simple: '1', operations: [{ kind: 'register', event: 'StopFailure', mode: 'active' }, { kind: 'register', event: 'Stop', mode: 'active' }, ...common] },
  { name: 'simple-trims-ascii-and-unicode-whitespace', simple: ' \t YeS\u00a0', operations: [{ kind: 'register', event: 'StopFailure', mode: 'active' }, { kind: 'register', event: 'Stop', mode: 'active' }, ...common] },
  { name: 'non-managed-disable-suppresses-active-session', simple: '0', files: { userSettings: '{"disableAllHooks":true}' }, operations: [{ kind: 'session', event: 'Stop', sid: 'hook-main', mode: 'active' }, ...common] },
  { name: 'untrusted-skips-active-runtime-callbacks', simple: '0', interactive: true, trust: false, operations: [{ kind: 'register', event: 'StopFailure', mode: 'active' }, { kind: 'register', event: 'Stop', mode: 'active' }, ...common] },
  { name: 'registration-append-session-overwrite-and-reinsert', simple: '0', operations: [{ kind: 'register', event: 'Stop', mode: 'array' }, { kind: 'register', event: 'Stop', mode: 'matcher' }, { kind: 'session', event: 'Stop', sid: 'hook-main', mode: 'matcher' }, { kind: 'session', event: 'SubagentStop', sid: 'hook-agent', mode: 'array' }, { kind: 'session', event: 'Stop', sid: 'hook-main', mode: 'array' }, ...common, { kind: 'clearSession', sid: 'hook-main' }, { kind: 'session', event: 'Stop', sid: 'hook-main', mode: 'array' }, { kind: 'clearRegistry' }, { kind: 'normal' }] },
  { name: 'dormant-caller-abort-cleanup', operations: [{ kind: 'cleanup' }, { kind: 'cleanup', value: true }, ...common] },
  { name: 'subagent-skips-process-cleanup', agentID: 'hook-agent', operations: [{ kind: 'cleanup', value: true }, ...common] },
  { name: 'snapshot-reset-clears-sdk-registry-preserves-session', simple: '1', operations: [{ kind: 'register', event: 'Stop', mode: 'active' }, { kind: 'register', event: 'StopFailure', mode: 'active' }, { kind: 'session', event: 'Stop', sid: 'hook-main', mode: 'array' }, { kind: 'snapshot' }, { kind: 'reset' }, ...common] },
  { name: 'active-registered-is-explicit-pending', simple: '0', supported: false, operations: [{ kind: 'register', event: 'StopFailure', mode: 'active' }, { kind: 'failure' }] },
  { name: 'active-session-is-explicit-pending', simple: '0', supported: false, operations: [{ kind: 'session', event: 'Stop', sid: 'hook-main', mode: 'active' }, { kind: 'normal' }] },
  { name: 'active-programmatic-is-explicit-pending', supported: false, operations: [{ kind: 'programmatic' }, { kind: 'post' }] },
]
export const hookHistorySources = ['{"type":"user","uuid":"original-user","message":{"role":"user","content":"original \\ud800 input"}}']
export const hookAssistantSource = '{"type":"assistant","uuid":"original-assistant","message":{"id":"original-message","role":"assistant","content":[{"type":"text","text":"original terminal text"}]},"error":"unknown","isApiErrorMessage":true}'

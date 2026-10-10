export type SettingsStateOperation = { kind: string; key?: string; ref?: string | null; now?: number; window?: number; value?: string }
export type SettingsStateCase = { name: string; operations: SettingsStateOperation[] }
const snapshot = { kind: 'snapshot' }
export const settingsStateCases: SettingsStateCase[] = [
  { name: 'cache miss cached null and reset keeps plugin base', operations: [
    snapshot, { kind: 'setSource', key: 'userSettings', ref: null }, { kind: 'setSource', key: 'localSettings', ref: 'a' },
    { kind: 'setParsed', key: '/private/a', ref: 'p' }, { kind: 'setSession', ref: 'p' }, { kind: 'setPlugin', ref: 'b' }, snapshot,
    { kind: 'reset' }, snapshot, { kind: 'clearPlugin' }, snapshot,
  ] },
  { name: 'cache reference identity and byte intact unknown fields', operations: [
    { kind: 'setSource', key: 'userSettings', ref: 'a' }, { kind: 'setSource', key: 'projectSettings', ref: 'a' },
    { kind: 'setParsed', key: '/private/a', ref: 'p' }, { kind: 'setParsed', key: '/private/b', ref: 'p' },
    { kind: 'setSession', ref: 'p' }, { kind: 'setPlugin', ref: 'a' }, snapshot,
    { kind: 'mutateValue', ref: 'a', key: 'model', value: '"changed\\ud800"' }, { kind: 'mutateErrors', ref: 'p', value: '{"message":"later\\udfff"}' }, snapshot,
    { kind: 'setSource', key: 'userSettings', ref: 'b' }, { kind: 'setParsed', key: '/private/b', ref: 'q' }, { kind: 'setSession', ref: 'q' }, snapshot,
  ] },
  { name: 'source map and path map remain distinct and exact', operations: [
    { kind: 'setSource', key: 'unknown', ref: 'b' }, { kind: 'setParsed', key: '/private/a', ref: 'q' },
    { kind: 'setParsed', key: '/private/A', ref: 'p' }, { kind: 'setPlugin', ref: 'a' }, snapshot,
    { kind: 'reset' }, snapshot, { kind: 'setPlugin' }, snapshot,
  ] },
  { name: 'internal write strict window single consumption and stale retention', operations: [
    { kind: 'consume', key: '/private/a', window: 50 }, { kind: 'mark', key: '/private/a' },
    { kind: 'time', now: 1050 }, { kind: 'consume', key: '/private/a', window: 50 },
    { kind: 'time', now: 1049 }, { kind: 'consume', key: '/private/a', window: 50 }, { kind: 'consume', key: '/private/a', window: 50 },
    { kind: 'mark', key: '/private/a' }, { kind: 'time', now: 999 }, { kind: 'consume', key: '/private/a', window: 0 },
    { kind: 'mark', key: '/private/a' }, { kind: 'mark', key: '/private/b' }, { kind: 'clearWrites' },
    { kind: 'consume', key: '/private/a', window: 100 }, { kind: 'consume', key: '/private/b', window: 100 },
  ] },
  { name: 'write marks independent of settings reset plugin clear and overwrite', operations: [
    { kind: 'mark', key: '/private/a' }, { kind: 'time', now: 1010 }, { kind: 'mark', key: '/private/a' },
    { kind: 'setSource', key: 'userSettings', ref: 'a' }, { kind: 'setPlugin', ref: 'b' }, { kind: 'reset' }, { kind: 'clearPlugin' },
    { kind: 'time', now: 1014.5 }, { kind: 'consume', key: '/private/a', window: 5 },
    { kind: 'mark', key: '/private/b' }, { kind: 'time', now: 1015 }, { kind: 'consume', key: '/private/B', window: 5 },
    { kind: 'consume', key: '/private/b', window: 0.5 }, { kind: 'consume', key: '/private/b', window: 1 }, snapshot,
  ] },
]

export const settingsStateValues = {
  a: '{"model":"original","\\ud800":1,"�":2,"2":"two","1":"one","future":{"n":1e400,"zero":-0,"text":"<>&\\udfff"}}',
  b: '{"empty":[],"flag":false,"future":null}',
}
export const settingsStateRecords = {
  p: { settings: 'a', errors: ['{"path":"permissions","message":"warning\\ud800","future":1}'] },
  q: { settings: null, errors: [] },
}

export const settingsStateSourceKeys = ['userSettings', 'projectSettings', 'localSettings', 'policySettings', 'flagSettings', 'unknown']
export const settingsStatePathKeys = ['/private/a', '/private/b', '/private/A', '/private/missing']

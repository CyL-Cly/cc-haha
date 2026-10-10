import { join } from 'node:path'
export const controllerSID = '11111111-1111-4111-8111-111111111111'
export const controllerTemporary = '22222222-2222-4222-8222-222222222222'
export type ControllerCase = {
  name: string
  pending?: boolean
  input: Record<string, unknown>
  files: Record<string, string>
}
export function controllerCases(home: string): ControllerCase[] {
  const path = join(home, '.claude', 'projects', home.replace(/[^a-zA-Z0-9]/g, '-'), controllerSID + '.jsonl')
  const user = (uuid: string, content: unknown, parentUuid: unknown = null, extra: Record<string, unknown> = {}) => ({ parentUuid, isSidechain: false, sessionId: controllerSID, type: 'user', uuid, timestamp: '2025-01-01T00:00:00Z', message: { role: 'user', content }, ...extra })
  const assistant = { parentUuid: 'old-user', isSidechain: false, sessionId: controllerSID, type: 'assistant', uuid: 'old-assistant', timestamp: '2025-01-01T00:00:01Z', message: { id: 'msg_old', type: 'message', role: 'assistant', model: 'fixture', content: [{ type: 'text', text: 'old answer' }], stop_reason: 'end_turn', stop_sequence: null, usage: { input_tokens: 1, output_tokens: 1 } } }
  const sdk = (uuid: string, content = 'next original prompt') => ({ type: 'user', uuid, message: { role: 'user', content } })
  const base = { home, temporary: controllerTemporary, sid: controllerSID, continue: false, forkSession: false, checkpointing: false, disabled: false, resumeInterrupted: '', resumeAt: '', model: 'fixture-model', sdk: [sdk('input-uuid')] }
  const source = [user('old-user', 'original user'), assistant].map(value => JSON.stringify(value) + '\n').join('')
  const files = { [path]: source, [join(home, 'policy', 'managed-settings.json')]: '{}', [join(home, '.claude', '.claude.json')]: '{"userID":"fixture-controller-device"}' }
  const toolHistory = (name: string, toolInput: unknown, result: unknown, timestamp = '2025-01-01T00:00:02Z', extra: Record<string, unknown> = {}) => [
    user('old-user', 'original user'),
    { ...assistant, uuid: 'tool-assistant', message: { ...assistant.message, content: [{ type: 'tool_use', id: 'tool-id', name, input: toolInput }], stop_reason: 'tool_use' } },
    user('tool-result-user', [{ type: 'tool_result', tool_use_id: 'tool-id', content: result, ...extra }], 'tool-assistant', { timestamp }),
    { ...assistant, parentUuid: 'tool-result-user' },
  ].map(value => JSON.stringify(value) + '\n').join('')
  const hostedReadHistory = (count: number) => {
    const entries: unknown[] = [user('read-root-user', 'original user')]
    let parentUuid = 'read-root-user'
    for (let index = 0; index < count; index++) {
      const uuid = `read-assistant-${index}`, toolId = `read-id-${index}`
      entries.push({ ...assistant, parentUuid, uuid, message: { ...assistant.message, id: `msg_${uuid}`, content: [{ type: 'tool_use', id: toolId, name: 'Read', input: { file_path: `file-${index}.txt` } }], stop_reason: 'tool_use' } })
      parentUuid = `read-result-${index}`
      entries.push(user(parentUuid, [{ type: 'tool_result', tool_use_id: toolId, content: `1→original file ${index}` }], uuid, { timestamp: '2025-01-01T00:00:02Z' }))
    }
    entries.push({ ...assistant, parentUuid, uuid: 'read-final-assistant', timestamp: '2025-01-01T00:00:03Z' })
    return entries.map(value => JSON.stringify(value) + '\n').join('')
  }
  return [
    { name: 'sid-actual-headless-first-http', input: base, files },
    { name: 'continue-actual-original-selection', input: { ...base, continue: true }, files },
    { name: 'fork-original-retained-history-new-writer', input: { ...base, forkSession: true }, files },
    { name: 'historical-and-runtime-sdk-duplicates', input: { ...base, sdk: [sdk('old-user', 'historical retry'), sdk('input-uuid'), sdk('input-uuid', 'runtime retry')] }, files },
    { name: 'resume-at-before-assistant', input: { ...base, resumeAt: 'old-user' }, files },
    { name: 'multiple-sdk-original-priority-batch-and-last-uuid', pending: true, input: { ...base, sdk: [{ ...sdk('later-id', 'later original'), priority: 'later' }, { ...sdk('now-id', 'now original'), priority: 'now' }, sdk('next-id', 'next original')] }, files },
    { name: 'missing-sdk-uuid-real-factory-allocation', input: { ...base, sdk: [{ type: 'user', message: { role: 'user', content: 'original missing uuid' } }] }, files },
    { name: 'read-cache-hosted-12-original-history', input: base, files: { ...files, [path]: hostedReadHistory(12) } },
    { name: 'read-cache-hosted-101-original-history', input: base, files: { ...files, [path]: hostedReadHistory(101) } },
    { name: 'read-cache-original-blank-lines-cr-unicode-regex', input: base, files: { ...files, [path]: toolHistory('Read', { file_path: 'model-file.txt' }, '\n\n  1→a\r\n2→b\u2028end\n3→c') } },
    { name: 'restored-legacy-empty-todos-original-array', input: base, files: { ...files, [path]: toolHistory('TodoWrite', { todos: [] }, 'ok') } },
    { name: 'read-cache-original-lines-lone-and-reminder', input: base, files: { ...files, [path]: toolHistory('Read', { file_path: 'model-file.txt' }, '  1→first\ud800\n2\tsecond<system-reminder>drop</system-reminder>') } },
    { name: 'read-cache-ranged-input-is-not-seeded', input: base, files: { ...files, [path]: toolHistory('Read', { file_path: 'model-file.txt', limit: null }, '1→skip') } },
    { name: 'write-cache-seeds-despite-result-error', input: base, files: { ...files, [path]: toolHistory('Write', { file_path: 'written.txt', content: 'original\ud800write' }, 'error', '2025-01-01T00:00:02Z', { is_error: true }) } },
    { name: 'edit-cache-actual-disk-crlf', input: base, files: { ...files, [path]: toolHistory('Edit', { file_path: 'edit.txt' }, 'done'), [join(home, 'edit.txt')]: 'actual\r\ndisk' } },
    { name: 'edit-cache-missing-private-file-is-skipped', input: base, files: { ...files, [path]: toolHistory('Edit', { file_path: 'missing.txt' }, 'done') } },
    { name: 'restored-legacy-todos-validated-and-unknown-stripped', input: base, files: { ...files, [path]: toolHistory('TodoWrite', { todos: [{ content: 'task', status: 'in_progress', activeForm: 'working', future: 'strip' }] }, 'ok') } },
    { name: 'restored-legacy-todos-malformed-last-clears', input: base, files: { ...files, [path]: toolHistory('TodoWrite', { todos: [{ content: '', status: 'pending', activeForm: 'working' }] }, 'ok') } },
    { name: 'disabled-original-no-session-persistence', input: { ...base, disabled: true }, files },
    { name: 'zero-cleanup-original-settings', input: base, files: { ...files, [join(home, '.claude', 'settings.json')]: '{"cleanupPeriodDays":0}' } },
    { name: 'continue-restored-metadata-zero-pr-worktree-null', input: { ...base, continue: true }, files: { ...files, [path]: source + [{ type: 'custom-title', customTitle: 'restored\ud800 title', sessionId: controllerSID }, { type: 'tag', tag: 'fixture-tag', sessionId: controllerSID }, { type: 'mode', mode: 'normal', sessionId: controllerSID }, { type: 'worktree-state', worktreeSession: null, sessionId: controllerSID }, { type: 'pr-link', prNumber: 0, prUrl: 'fixture-pr', prRepository: 'fixture-repo', sessionId: controllerSID }].map(value => JSON.stringify(value) + '\n').join('') } },
    { name: 'interrupted-original-user-requeued-before-sdk', input: { ...base, resumeInterrupted: '0' }, files: { ...files, [path]: JSON.stringify(user('pending-user', 'original pending')) + '\n' } },
    { name: 'checkpoint-restore-with-no-turn-does-not-materialize-new-writer', input: { ...base, checkpointing: true, holdCopies: true, sdk: [] }, files: { ...files, [path]: source + JSON.stringify({ type: 'file-history-snapshot', messageId: 'old-user', snapshot: { messageId: 'old-user', schemaVersion: 1, trackedFileBackups: { [join(home, 'tracked.txt')]: { backupFileName: 'backup', version: 1 } }, completedFileBackups: { [join(home, 'completed.txt')]: { backupFileName: null, version: 0 } }, timestamp: '2025-01-01T00:00:00Z' }, isSnapshotUpdate: false }) + '\n', [join(home, '.claude', 'file-history', controllerSID, 'backup')]: 'original backup' } },
    { name: 'original-lone-prompt-content', input: { ...base, sdk: [sdk('input-uuid', 'next\ud800😀')] }, files },
  ]
}

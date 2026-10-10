import { join } from 'node:path'
export type ResumeCase = { name: string, sources: string[] }
export function resumeCases(home: string): ResumeCase[] {
  const user = (uuid: string, content: unknown, extra: Record<string, unknown> = {}) => ({ type: 'user', uuid, timestamp: '2025-01-01T00:00:00Z', message: { role: 'user', content }, ...extra })
  const assistant = (uuid: string, content: unknown, extra: Record<string, unknown> = {}) => ({ type: 'assistant', uuid, timestamp: '2025-01-01T00:00:01Z', message: { id: uuid, role: 'assistant', content }, ...extra })
  const noise = { type: 'system', subtype: 'local_command', content: 'status', uuid: 'system' }
  const toolUse = (id: unknown, name: unknown = 'Read') => ({ type: 'tool_use', id, name, input: {} })
  const toolResult = (id: unknown) => ({ type: 'tool_result', tool_use_id: id, content: 'read' })
  return [
    { name: 'empty', messages: [] },
    { name: 'plain-user-and-trailing-noise', messages: [user('u1', 'question'), noise, { type: 'progress', uuid: 'p1' }] },
    { name: 'unresolved-all-including-text', messages: [user('u1', 'question'), assistant('a1', [toolUse('t1'), { type: 'text', text: 'after' }])] },
    { name: 'mixed-resolved-and-unresolved-use', messages: [user('u1', 'question'), assistant('a1', [toolUse('t1'), toolUse('t2')]), user('u2', [toolResult('t1')])] },
    { name: 'orphan-thinking-does-not-merge-user', messages: [user('u1', 'one'), assistant('a1', [{ type: 'thinking', thinking: 'secret', signature: 'fixture' }]), user('u2', 'two')] },
    { name: 'whitespace-merges-all-adjacent-user', messages: [user('u1', 'one', { future: 1 }), user('u2', 'two'), assistant('a1', [{ type: 'text', text: '\ufeff\u00a0\u2028' }]), user('u3', 'three', { future: 2 })] },
    { name: 'whitespace-merge-retains-first-meta', messages: [user('u1', 'context', { isMeta: true }), assistant('a1', [{ type: 'text', text: ' ' }]), user('u2', 'question')] },
    { name: 'api-error-detection-vs-sentinel', messages: [user('u1', 'question'), assistant('a1', [{ type: 'text', text: 'error' }], { isApiErrorMessage: true })] },
    { name: 'attachment-continuation', messages: [{ type: 'attachment', attachment: { type: 'future_attachment', future: true }, uuid: 'attachment' }, noise] },
    { name: 'legacy-attachment-and-backfill', messages: [{ type: 'attachment', attachment: { type: 'new_file', filename: join(home, 'f.txt'), displayPath: 'old', future: 1 } }, { type: 'attachment', attachment: { type: 'future_attachment', filename: 3, path: join(home, 'd'), skillDir: join(home, 'skills') } }, { type: 'attachment', attachment: { type: 'future_attachment', skillDir: join(home, 'skills') } }, { type: 'attachment', attachment: { type: 'future_attachment', path: join(home, 'd'), displayPath: null } }] },
    { name: 'malformed-drop-and-permission-mutation', messages: [{ type: 'attachment', attachment: null }, user('u1', 'question', { permissionMode: 'bubble' }), user('u2', 'more', { permissionMode: 'auto' })] },
    { name: 'toolUseResult-truthy-string', messages: [user('u1', 'question', { toolUseResult: [] })] },
    { name: 'null-name-terminal-with-features-off', messages: [assistant('a1', [toolUse('t1', null)]), user('u1', [toolResult('t1')])] },
    { name: 'missing-name-not-terminal-with-features-off', messages: [assistant('a1', [{ type: 'tool_use', id: 't1', input: {} }]), user('u1', [toolResult('t1')])] },
    { name: 'empty-assistant-array-survives', messages: [user('u1', 'question'), assistant('a1', [])] },
    { name: 'known-attachment-with-malformed-block', messages: [assistant('a1', [null])] },
    { name: 'thinking-id-sibling-across-noise', messages: [assistant('a1', [{ type: 'thinking', thinking: 'keep', signature: 'fixture' }], { message: { id: 'shared', content: [{ type: 'thinking', thinking: 'keep', signature: 'fixture' }] } }), noise, assistant('a2', [{ type: 'text', text: 'done' }], { message: { id: 'shared', content: [{ type: 'text', text: 'done' }] } })] },
    { name: 'text-block-missing-text-is-whitespace', messages: [assistant('a1', [{ type: 'text' }])] },
    { name: 'text-block-null-text-fails', messages: [assistant('a1', [{ type: 'text', text: null }])] },
    { name: 'system-progress-only', messages: [noise, { type: 'progress', uuid: 'p1' }] },
    { name: 'meta-user-still-gets-sentinel', messages: [user('u1', 'context', { isMeta: true }), noise] },
    { name: 'compact-summary-still-gets-sentinel', messages: [user('u1', 'summary', { isCompactSummary: true }), noise] },
    { name: 'second-tool-result-does-not-trigger-detection', messages: [assistant('a1', [toolUse('t1')]), user('u1', [{ type: 'text', text: 'prompt' }, toolResult('t1')])] },
    { name: 'terminal-name-string-is-not-enabled', messages: [assistant('a1', [toolUse('t1', 'SendUserMessage')]), user('u1', [toolResult('t1')])] },
    { name: 'missing-use-and-result-id-pair', messages: [assistant('a1', [{ type: 'tool_use', name: 'Read' }]), user('u1', [{ type: 'tool_result', content: 'read' }])] },
    { name: 'object-ids-are-reference-unequal', messages: [assistant('a1', [toolUse({ future: 1 })]), user('u1', [toolResult({ future: 1 })])] },
    { name: 'all-runtime-permissions-with-features-off', messages: ['acceptEdits', 'bypassPermissions', 'default', 'dontAsk', 'plan', 'auto', 'bubble', null, 3].map((permissionMode, i) => user('u' + i, 'p', { permissionMode })) },
    { name: 'attachment-empty-and-wrong-known-shapes', messages: [undefined, null, [], {}, { type: 3 }, { type: 'hook_success', content: [] }, { type: 'invoked_skills', skills: [null] }, { type: 'deferred_tools_delta', addedNames: [], addedLines: [], removedNames: [3] }].map(attachment => ({ type: 'attachment', attachment })) },
    { name: 'attachment-known-valid-and-forward-fields', messages: [
      { type: 'new_directory', path: join(home, 'd'), displayPath: 'overwrite' },
      { type: 'file', filename: join(home, 'f') }, { type: 'directory', path: join(home, 'd') },
      { type: 'selected_lines_in_ide', content: '' }, { type: 'hook_success', content: '' }, { type: 'skill_listing', content: '' },
      { type: 'hook_additional_context', content: [] }, { type: 'invoked_skills', skills: [{}, []] },
      { type: 'deferred_tools_delta', addedNames: [], addedLines: [], removedNames: [] },
      { type: 'mcp_instructions_delta', addedNames: [], addedBlocks: [], removedNames: [] },
      { type: 'agent_listing_delta', addedTypes: [], addedLines: [], removedTypes: [] },
    ].map(attachment => ({ type: 'attachment', attachment: { ...attachment, future: { unknown: true } } })) },
    { name: 'message-null-fails-before-drop', messages: [null] },
    { name: 'message-missing-user-envelope-fails', messages: [{ type: 'user' }] },
    { name: 'text-block-number-trim-fails', messages: [assistant('a1', [{ type: 'text', text: 3 }])] },
  ].map(row => ({ name: row.name, sources: row.messages.map(value => JSON.stringify(value)) }))
}

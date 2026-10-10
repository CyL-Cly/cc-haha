import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { join, relative, isAbsolute } from 'node:path'
import { mock } from 'bun:test'
import * as originalCrypto from 'node:crypto'

const input = await Bun.stdin.json()
const home = input.homeDir
const root = process.env.CC_HAHA_FIXTURE_TEMP_ROOT
if (process.env.CC_HAHA_NATIVE_COLLABORATION_FIXTURE !== '1' || !home || !root || !isAbsolute(home) ||
  process.env.HOME !== home || process.env.USERPROFILE !== home || process.env.CLAUDE_CONFIG_DIR !== join(home, '.claude') ||
  !relative(root, home) || relative(root, home).startsWith('..') || isAbsolute(relative(root, home))) throw new Error('Private fixture paths required')
await mkdir(join(home, '.claude'), { recursive: true })
let uuidOrdinal = 0
mock.module('node:crypto', () => ({ ...originalCrypto, randomUUID: () => `00000000-0000-4000-8000-${String(++uuidOrdinal).padStart(12, '0')}` }))
const { SessionCollaborationService, migrateCollaborationStore, projectCollaborationHistory } = await import('../../../src/server/services/sessionCollaborationService.js')
const { handleSessionCollaborationApi, handleSessionCollaborationUiApi } = await import('../../../src/server/api/sessionCollaboration.js')
const results: unknown[] = []
const catchError = (error: any) => ({ name: error?.name ?? 'Error', message: error?.message ?? String(error), ...(error?.code !== undefined ? { code: error.code } : {}), ...(error?.statusCode !== undefined ? { status: error.statusCode } : {}) })
for (const [index, scenario] of input.cases.entries()) {
  uuidOrdinal = 0
  if (scenario.kind === 'migration' || scenario.kind === 'projection') {
    try { results.push({ result: scenario.kind === 'migration' ? migrateCollaborationStore(scenario.value) : projectCollaborationHistory(scenario.value, scenario.options) }) }
    catch (error) { results.push({ error: catchError(error) }) }
    continue
  }
  const directory = join(home, `collaboration-${index}`)
  await mkdir(directory, { recursive: true })
  const statePath = join(directory, 'state.json')
  if (scenario.initial !== undefined) await writeFile(statePath, JSON.stringify(scenario.initial), { mode: 0o600 })
  const started: unknown[] = [], injected: unknown[] = [], stopped: string[] = [], reads: unknown[] = []
  let childOrdinal = 0
  const live = { ...(scenario.live ?? {}) }
  let service: InstanceType<typeof SessionCollaborationService>
  const dependencies = {
    statePath,
    now: () => new Date('2026-01-02T03:04:05.006Z'),
    sessions: {
      list: async (options: any) => scenario.sessionList ?? { sessions: [{ id: 'root', title: 'Main', workDir: '/fixture', modifiedAt: 'now' }] },
      read: async (sessionId: string, options: any) => { reads.push({ sessionId, options: Object.fromEntries(Object.entries(options).filter(([key]) => key !== 'signal')) }); return scenario.history ?? { messages: [], page: { historyComplete: true } } },
      exists: async (id: string) => id !== 'missing',
      create: async () => { if (scenario.createFailure) throw new Error(scenario.createFailure); return { sessionId: `child-${++childOrdinal}`, workDir: '/fixture/worktree' } },
      ...(scenario.titles ? { titles: () => scenario.titles } : {}),
    },
    runtime: {
      ...(scenario.live ? { getState: (id: string) => live[id] } : {}),
      start: async (_: string, message: any) => { started.push(structuredClone(message)); if (scenario.startFailure) throw new Error(scenario.startFailure) },
      enqueue: async (_: string, message: any) => { injected.push(structuredClone(message)); if (scenario.enqueueFailure) throw new Error(scenario.enqueueFailure) },
      stop: async (id: string) => { stopped.push(id); if (scenario.stopFailure) throw new Error(scenario.stopFailure) },
    },
  }
  service = new SessionCollaborationService(dependencies)
  const observations: unknown[] = []
  let lastCursor: string | undefined
  for (const op of scenario.operations) {
    try {
      let result: unknown
      switch (op.action) {
        case 'api': {
          const method = op.method ?? 'POST'
          const request = new Request(`http://127.0.0.1/api/session-collaboration/${op.route}`, { method, ...(method === 'GET' ? {} : { body: op.bodyText ?? JSON.stringify(op.body) }) })
          const response = await handleSessionCollaborationApi(request, op.route, op.caller ?? 'root', service)
          result = { status: response.status, body: await response.json() }
          break
        }
        case 'ui': {
          const url = new URL(`http://127.0.0.1/api/session-collaboration${op.parts.length ? '/' + op.parts.join('/') : ''}`)
          if (op.query !== undefined) url.searchParams.set('query', op.query)
          const request = new Request(url, { method: op.method ?? 'GET' })
          const response = await handleSessionCollaborationUiApi(request, url, service)
          result = { status: response.status, body: await response.json() }
          break
        }
        case 'create': result = await service.create(op.caller ?? 'root', op.input); break
        case 'send': result = await service.send(op.caller ?? 'root', op.target, op.content, op.messageId); break
        case 'status': result = await service.status(op.ids); break
        case 'group': result = await service.groupStatus(op.id); break
        case 'candidates': result = await service.candidates(op.query); break
        case 'state': result = await service.onSessionState(op.id, op.state, op.result, op.eventId); break
        case 'consumed': result = await service.onMessageConsumed(op.messageId, op.target); break
        case 'stop': result = await service.stop(op.id); break
        case 'stopped': result = await service.onStopped(op.id); break
        case 'stopGroup': result = await service.stopGroup(op.id); break
        case 'resume': result = await service.resume(op.id); break
        case 'input': result = await service.onUserInput(op.id, { dispatch: op.dispatch }); break
        case 'recover': result = await service.recover(); break
        case 'restart': service = new SessionCollaborationService(dependencies); result = await service.recover(); break
        case 'historyCommit': scenario.history = op.history; result = null; break
        case 'read': result = await service.read(op.id, op.options); lastCursor = (result as any).page?.nextCursor ?? undefined; break
        case 'readNext': result = await service.read(op.id, { ...op.options, cursor: lastCursor }); lastCursor = (result as any).page?.nextCursor ?? undefined; break
        case 'wait': result = await service.wait(op.after, op.ids, op.timeout, undefined, op.caller); break
        case 'canDeliver': result = service.canDeliver(op.messageId); break
        default: throw new Error(`Unknown fixture operation ${op.action}`)
      }
      observations.push({ result: result ?? null })
    } catch (error) { observations.push({ error: catchError(error) }) }
  }
  const snapshot = await service.status().catch(error => ({ error: catchError(error) }))
  const persisted = await readFile(statePath, 'utf8').then(JSON.parse).catch(error => error.code === 'ENOENT' ? null : { error: catchError(error) })
  results.push({ observations, snapshot, persisted, started, injected, stopped, reads })
}
process.stdout.write(JSON.stringify({ results }) + '\n')

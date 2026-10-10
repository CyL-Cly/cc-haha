import { mock } from 'bun:test'
import { mkdir, writeFile } from 'node:fs/promises'
import { join, dirname } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
const home = assertDisposableFixtureHome(process.env.HOME, 'File Read analytics actual Source')
const input = await Bun.stdin.json()
await import('../../../preload')
let events: unknown[] = []
const originalAnalytics = await import('../../../src/services/analytics/index')
mock.module('../../../src/services/analytics/index', () => ({ ...originalAnalytics, logEvent: (name: string, metadata: unknown) => events.push({ name, metadata }) }))
mock.module('../../../src/utils/secureStorage/index', () => ({ getSecureStorage: () => ({ read: () => null, readAsync: async () => null }) }))
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
process.env.CLAUDE_CODE_SIMPLE = '1'
const { FileReadTool } = await import('../../../src/tools/FileReadTool/FileReadTool')
const output = []
for (const row of input.cases) {
  const file = join(home, row.path)
  if (!file.startsWith(home + '/')) throw new Error('Owned path required')
  await mkdir(dirname(file), { recursive: true })
  await writeFile(file, Buffer.from(row.base64, 'base64'))
  const readFileState = new Map()
  events = []
  let result: unknown
  for (const read of row.reads) {
    result = await FileReadTool.call({ file_path: file, ...read }, {
      readFileState, abortController: new AbortController(), nestedMemoryAttachmentTriggers: new Set(),
      options: {}, getAppState: () => ({ toolPermissionContext: { mode: 'bypassPermissions', additionalWorkingDirectories: new Map() } }),
    } as any, undefined, { message: { id: 'private-assistant-message' } } as any)
  }
  // Normalize the hash of the independently allocated absolute path only.
  const { createHash } = await import('node:crypto')
  const pathHash = createHash('sha256').update(file).digest('hex').slice(0, 16)
  output.push({ events: events.map((event: any) => event.name === 'tengu_file_operation' ? { ...event, metadata: { ...event.metadata, filePathHash: event.metadata.filePathHash === pathHash ? '$PATH_HASH' : event.metadata.filePathHash } } : event), result })
}
process.stdout.write(JSON.stringify(output))

import { assertDisposableFixtureHome } from './fixture-guard'
import { mock } from 'bun:test'
import * as physical from 'node:fs'
import * as path from 'node:path'
const io = { ...physical }

assertDisposableFixtureHome(process.env.HOME, 'Native CLI OpenAI storage')
if (process.env.CC_HAHA_CLI_OPENAI_STORAGE_FIXTURE !== '1') throw new Error('Unowned CLI OAuth fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const originalNow = Date.now
Date.now = () => input.now
let secure: any = null
let logs = 0
let calls: any[] = []
let current: any
mock.module('../../../src/utils/secureStorage/index', () => ({ getSecureStorage: () => ({
  read: () => { calls.push('sync'); if (current.readError) throw new Error(current.readError); return structuredClone(secure) },
  readAsync: async () => { calls.push('async'); if (current.readError) throw new Error(current.readError); return structuredClone(secure) },
  update: (value: any) => {
    calls.push({ update: structuredClone(value) })
    if (current.updateError) throw new Error(current.updateError)
    if (current.updateSuccess !== false) secure = structuredClone(value)
    return { success: current.updateSuccess !== false, ...(current.warning !== undefined ? { warning: current.warning } : {}) }
  },
}) }))
mock.module('../../../src/utils/log', () => ({ logError: () => { logs++ } }))
mock.module('../../../src/utils/browser', () => ({ openBrowser: async () => { throw new Error('Unowned browser') } }))
mock.module('../../../src/services/oauth/auth-code-listener', () => ({ AuthCodeListener: class {} }))
const originalClient = await import('../../../src/services/openaiAuth/client')
mock.module('../../../src/services/openaiAuth/client', () => ({ ...originalClient,
  refreshOpenAITokens: async (token: any) => {
    calls.push({ refresh: token })
    if (current.refreshError) throw new Error(current.refreshError)
    return current.response
  },
}))
mock.module('fs', () => ({ ...physical,
  mkdirSync: (...args: any[]) => {
    if (current.fault === 'mkdir') throw new Error('private mkdir fault')
    return (io.mkdirSync as any)(...args)
  },
  writeFileSync: (...args: any[]) => {
    const marker = String(args[0]).endsWith('openai-oauth-file-backed')
    if (current.fault === 'write' && !marker || current.fault === 'marker' && marker) throw new Error('private write fault')
    return (io.writeFileSync as any)(...args)
  },
  renameSync: (...args: any[]) => {
    if (current.fault === 'rename') throw new Error('private rename fault')
    return (io.renameSync as any)(...args)
  },
}))
const storage = await import('../../../src/services/openaiAuth/storage')
const { ensureFreshOpenAITokens } = await import('../../../src/services/openaiAuth/index')
const rows = []
try {
  for (const [index, row] of input.rows.entries()) {
    current = row
    const root = path.join(process.env.HOME!, `row${index}`)
    io.mkdirSync(root, { recursive: true })
    process.chdir(root)
    process.env.CLAUDE_CONFIG_DIR = path.join(root, '.claude')
    const file = path.join(root, 'tokens.json')
    const marker = path.join(root, '.claude', 'cc-haha', 'openai-oauth-file-backed')
    secure = row.secure ?? null
    logs = 0
    calls = []
    if (row.file !== undefined) io.writeFileSync(file, row.file, { mode: 0o600 })
    if (row.marker) { io.mkdirSync(path.dirname(marker), { recursive: true }); io.writeFileSync(marker, '1\n', { mode: 0o600 }) }
    if (row.env === null) delete process.env.OPENAI_CODEX_OAUTH_FILE
    else process.env.OPENAI_CODEX_OAUTH_FILE = row.env ?? file
    storage.clearOpenAIOAuthTokenCache()
    const values: any[] = []
    let last: any = null
    for (const action of row.actions) {
      switch (action.op) {
        case 'get': last = storage.getOpenAIOAuthTokens(); values.push(last); break
        case 'async': last = await storage.getOpenAIOAuthTokensAsync(); values.push(last); break
        case 'save': values.push(storage.saveOpenAIOAuthTokens(action.value)); break
        case 'delete': values.push(storage.deleteOpenAIOAuthTokens()); break
        case 'clear': storage.clearOpenAIOAuthTokenCache(); break
        case 'file': io.writeFileSync(file, action.value, { mode: 0o600 }); break
        case 'env': if (action.value === null) delete process.env.OPENAI_CODEX_OAUTH_FILE; else process.env.OPENAI_CODEX_OAUTH_FILE = action.value; break
        case 'secure': secure = action.value; break
        case 'mutate': if (last) last.accessToken = action.value; break
        case 'ensure': last = await ensureFreshOpenAITokens(); values.push(last); break
        default: throw new Error('Unowned action')
      }
    }
    rows.push({ values, calls, logs, secure, marker: physical.existsSync(marker), file: physical.existsSync(file) ? physical.readFileSync(file, 'utf8') : null, mode: physical.existsSync(file) ? physical.statSync(file).mode & 0o777 : null, temporaries: physical.readdirSync(root).filter(name => name.includes('.tmp.')) })
  }
  process.stdout.write(JSON.stringify(rows))
} finally { Date.now = originalNow }

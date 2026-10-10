import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

if (process.env.CC_HAHA_SESSION_PERMISSION_FIXTURE !== '1') throw new Error('fixture only')
const source = readFileSync(resolve(import.meta.dir, '../../../src/server/services/conversationService.ts'), 'utf8')
const start = source.indexOf('function normalizeSessionPermissionUpdates(')
const end = source.indexOf('\nexport const conversationService', start)
if (start < 0 || end < 0) throw new Error('Source function anchors missing')
const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(source.slice(start, end))
const run = new Function('rows', code + '\nreturn rows.map(row => normalizeSessionPermissionUpdates(row.suggestions, row.toolName));')
console.log(JSON.stringify(run(await Bun.stdin.json())))

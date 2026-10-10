import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const original = readFileSync(resolve(import.meta.dir, '../../../../src/server/ws/handler.ts'), 'utf8')
const start = original.indexOf('function normalizeSessionSlashCommand(')
const end = original.indexOf('export function closeSessionConnection(', start)
if (start < 0 || end < 0) throw new Error('Original slash-command normalization boundary moved')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(original.slice(start, end))
const normalize = new Function(`${source}\nreturn normalizeSessionSlashCommand`)()
const input = JSON.parse(await Bun.stdin.text())
console.log(JSON.stringify(input.map(normalize).filter(item => item !== null)))

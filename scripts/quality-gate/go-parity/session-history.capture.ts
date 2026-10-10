// Explicit offline capture only. This module is never imported by the corpus.
import { readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

if (process.argv.slice(2).join(' ') !== '--capture-frozen-native-golden') throw new Error('Explicit --capture-frozen-native-golden is required; do not run after source freeze')
if (Bun.version !== '1.3.14') throw new Error('Source capture requires exact Bun 1.3.14')
const root = fileURLToPath(new URL('../../../', import.meta.url))
const child = Bun.spawn([process.execPath, 'test', join(root, 'scripts/quality-gate/go-parity/session-history.test.ts')], { cwd: root, stdout: 'inherit', stderr: 'inherit', stdin: 'ignore' })
const code = await child.exited
if (code !== 0) throw new Error('Actual Source replay/differential failed; native golden remains unchanged')
const raw = await readFile(join(root, 'artifacts/go-parity/session-history/source-contract.json'), 'utf8')
const contract = JSON.parse(raw) as { source?: string, cases?: unknown[] }
if (!contract.source?.startsWith('Bun 1.3.14 actual ') || (contract.cases?.length ?? 0) < 20) throw new Error('Missing actual Source capture evidence')
await writeFile(join(root, 'backend/internal/sessionhistory/testdata/source_contract.json'), raw)
console.log('Captured ' + contract.cases!.length + ' actual Source contracts before source freeze')

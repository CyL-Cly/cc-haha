import { readFile, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'
import { stateCases } from './resume-state-fixtures'

// The ordinary corpus writes ignored evidence only. Updating native Source
// contracts requires this separately invoked offline capture command.
if (Bun.version !== '1.3.14' || process.argv.slice(2).join(' ') !== '--capture-original-source') throw new Error('Use pinned Bun and explicit --capture-original-source after the complete focused suite')
const root = fileURLToPath(new URL('../../../', import.meta.url))
const data = await readFile(join(root, 'artifacts/go-parity/resume-state/source-contract.json'), 'utf8')
const parsed = JSON.parse(data) as { source: string, contracts: Array<{ name: string, expected: unknown }>, helperContracts: Array<{ mode: string, expected: unknown }> }
const names = stateCases('/fixture').map(row => row.name)
if (parsed.source !== 'actual Source loadConversationForResume; release TRANSCRIPT_CLASSIFIER' || parsed.contracts.length !== names.length || parsed.contracts.some((row, index) => row.name !== names[index] || row.expected === undefined)) throw new Error('Capture requires the complete ordered actual Source self-replay and independent Go corpus')
if (parsed.helperContracts?.length !== 3 || parsed.helperContracts.some((row, index) => row.mode !== ['tail-read', 'close', 'zero-head'][index] || row.expected === undefined)) throw new Error('Capture requires actual portable reader fault helper replay')
await writeFile(join(root, 'backend/internal/resumestate/testdata/source_contract.json'), data)
console.log(JSON.stringify({ captured: parsed.contracts.length, destination: 'backend/internal/resumestate/testdata/source_contract.json' }))

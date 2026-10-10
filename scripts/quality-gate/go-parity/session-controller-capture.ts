import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { controllerCases } from './session-controller-fixtures'

// Explicit offline capture only. Ordinary suites write ignored artifacts and
// never refresh the backend replay contract while a unified tree is frozen.
if (process.env.CC_HAHA_SESSION_CONTROLLER_CAPTURE !== '1') {
  throw new Error('Explicit session-controller capture flag required')
}
const root = process.cwd()
const values = []
for (const row of controllerCases('/placeholder').filter(row => !row.pending)) {
  const path = join(root, 'artifacts/go-parity/session-controller', row.name + '-source-raw.json')
  const result = JSON.parse(await readFile(path, 'utf8'))
  const home = result.original.input.home as string
  const replacement = '/__fixture_home__'
  const encoded = JSON.stringify({ name: row.name, input: result.original.input, files: result.original.files, expected: result.result.observation })
    .replaceAll(home.replace(/[^a-zA-Z0-9]/g, '-'), replacement.replace(/[^a-zA-Z0-9]/g, '-'))
    .replaceAll(home, replacement)
  values.push(JSON.parse(encoded))
}
const directory = join(root, 'backend/internal/sessioncontroller/testdata')
await mkdir(directory, { recursive: true })
await writeFile(join(directory, 'source_contract.json'), JSON.stringify(values, null, 2) + '\n')
console.log(`Captured ${values.length} actual Source headless profiles`)

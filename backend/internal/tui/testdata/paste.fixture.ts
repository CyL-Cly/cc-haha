import fs from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PASTE_FIXTURE !== '1'
  || Object.keys(input).some(key => !['home', 'text', 'contents', 'formats'].includes(key))) throw new Error('Paste comparison requires original private inputs and Bun 1.3.14')
const home = assertDisposableFixtureHome(input.home, 'Paste references')
fs.mkdirSync(join(home, '.claude'), { recursive: true })
fs.writeFileSync(join(home, '.claude/.claude.json'), JSON.stringify({ customApiKeyResponses: { approved: ['fixture'], rejected: [] } }))
Object.assign(globalThis, { MACRO: { VERSION: 'fixture', PACKAGE_URL: 'fixture', NATIVE_PACKAGE_URL: 'fixture', BUILD_TIME: 'fixture', FEEDBACK_CHANNEL: 'local', VERSION_CHANGELOG: '', ISSUES_EXPLAINER: '' } })
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
const { expandPastedTextRefs, formatPastedTextRef, getPastedTextRefNumLines } = await import('../../../../src/history')
const expanded = expandPastedTextRefs(input.text, input.contents)
const formats = input.formats.map((content: string, index: number) => formatPastedTextRef(index + 1, getPastedTextRefNumLines(content)))
process.stdout.write(JSON.stringify({ expanded, formats, capabilities }))

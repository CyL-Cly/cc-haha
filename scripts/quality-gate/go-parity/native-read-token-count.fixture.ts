import { parse } from 'acorn'
import { join } from 'node:path'
import { mkdir, writeFile } from 'node:fs/promises'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Actual large Read token count')
if (process.env.CC_HAHA_READ_TOKEN_COUNT_FIXTURE !== '1') throw new Error('Unowned large Read token count fixture')
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: new URL(input.url).origin, absentPaths: absentManagedSettingsFixturePaths() })
await mkdir(join(home, '.claude'), { recursive: true })
await writeFile(join(home, '.claude', 'settings.json'), JSON.stringify(input.settings ?? {}))
await import('../../../preload')
for (const [key, value] of Object.entries(input.env ?? {})) process.env[key] = String(value)
process.env.ANTHROPIC_BASE_URL = input.url
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
state.setIsInteractive(false)
state.switchSession(input.session)
state.setMainLoopModelOverride(input.model)
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: input.device }))
const counter = await import('../../../src/services/tokenEstimation')
// Keep the original validation/error bodies. The only injected policy primitive
// is the default cap; the token estimate and count API execute actual Source.
const text = await Bun.file(new URL('../../../src/tools/FileReadTool/FileReadTool.ts', import.meta.url)).text()
const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(text)
const ast = parse(code, { ecmaVersion: 'latest', sourceType: 'module' }) as any
const nodes = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
const declarations = ['MaxFileReadTokenExceededError', 'validateContentTokens'].map(name => {
  const node = nodes.find((node: any) => (node?.type === 'ClassDeclaration' || node?.type === 'FunctionDeclaration') && node.id?.name === name)
  if (!node) throw new Error(`Missing actual Source Read ${name}`)
  return code.slice(node.start, node.end)
}).join('\n')
const validate = new Function('roughTokenCountEstimationForFileType', 'countTokensWithAPI', 'getDefaultFileReadingLimits', `${declarations};return validateContentTokens`)(counter.roughTokenCountEstimationForFileType, counter.countTokensWithAPI, () => ({ maxTokens: 25000 }))
const rows = []
for (const row of input.rows) {
  try {
    await validate(row.content, row.extension, row.maxTokens)
    rows.push({ error: null })
  } catch (error) {
    rows.push({ error: error instanceof Error ? error.message : String(error) })
  }
}
if (capabilities.blocked.length) throw new Error('Blocked large Read capability: ' + capabilities.blocked.join(', '))
if (input.includeBetas) {
  const { getModelBetas } = await import('../../../src/utils/betas')
  process.stdout.write(JSON.stringify({ rows, betas: getModelBetas(input.model) }))
} else {
  process.stdout.write(JSON.stringify(rows))
}

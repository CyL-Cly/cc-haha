import fs from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MEMORY_FIXTURE !== '1' || process.env.USER_TYPE !== 'external') throw new Error('Normal memory fixture requires pinned isolated Source runtime')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (Object.keys(input).some(key => !['home', 'cwd', 'mode', 'files', 'sources', 'additional', 'memory'].includes(key))) throw new Error('Only original native memory inputs are admitted')
const home = assertDisposableFixtureHome(input.home, 'Normal memory')
const inside = (path: string) => {
  const resolved = resolve(home, path)
  if (!resolved.startsWith(home + '/')) throw new Error('Original fixture file is outside private home')
  return resolved
}
const cwd = inside(input.cwd)
fs.mkdirSync(cwd, { recursive: true })
for (const file of input.files) {
  const path = inside(file.path)
  fs.mkdirSync(dirname(path), { recursive: true })
  fs.writeFileSync(path, file.content)
}
const config = join(home, '.claude')
fs.mkdirSync(config, { recursive: true })
fs.writeFileSync(join(config, '.claude.json'), JSON.stringify({ customApiKeyResponses: { approved: ['fixture'], rejected: [] } }))
fs.writeFileSync(join(config, 'settings.json'), JSON.stringify({ autoMemoryEnabled: !!input.memory, pluginConfigs: { 'agents-md@builtin': { options: { instructionFiles: input.mode } } } }))
Object.assign(globalThis, { MACRO: { VERSION: 'fixture', PACKAGE_URL: 'fixture', NATIVE_PACKAGE_URL: 'fixture', BUILD_TIME: 'fixture', FEEDBACK_CHANNEL: 'local', VERSION_CHANGELOG: '', ISSUES_EXPLAINER: '' } })
const absent = absentManagedSettingsFixturePaths()
const managed = dirname(absent[0]!)
absent.push(join(managed, 'CLAUDE.md'), join(managed, '.claude/rules'))
for (let path = dirname(home); ; path = dirname(path)) {
  for (const leaf of ['.git', 'CLAUDE.md', '.claude/CLAUDE.md', '.claude/rules', 'CLAUDE.local.md', 'AGENTS.md', '.claude/AGENTS.md']) absent.push(join(path, leaf))
  if (dirname(path) === path) break
}
absent.push('/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token')
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent })
const bootstrap = await import('../../../../src/bootstrap/state')
const configAPI = await import('../../../../src/utils/config')
bootstrap.setOriginalCwd(cwd)
bootstrap.setCwdState(cwd)
bootstrap.setProjectRoot(cwd)
bootstrap.setIsInteractive(true)
bootstrap.setAllowedSettingSources(input.sources)
bootstrap.setFlagSettingsInline({ pluginConfigs: { 'agents-md@builtin': { options: { instructionFiles: input.mode } } } })
bootstrap.setAdditionalDirectoriesForClaudeMd((input.additional ?? []).map(inside))
configAPI.enableConfigs()
process.env.CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD = '1'
process.env.CLAUDE_COWORK_MEMORY_PATH_OVERRIDE = join(home, 'memory')
const memory = await import('../../../../src/utils/claudemd')
const files = await memory.getMemoryFiles()
process.stdout.write(JSON.stringify({ files, claudeMd: memory.getClaudeMds(files), capabilities }))

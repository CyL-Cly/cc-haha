import fs from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
if (Bun.version !== '1.3.14') throw new Error('System prompt comparison requires pinned Bun 1.3.14')
if (Object.keys(input).some(key => !['home', 'model', 'tools', 'language', 'simple', 'interactive', 'autoMemory', 'style', 'files', 'sources', 'agentEnvironment', 'additional', 'baseURL', 'agentCwdOverride', 'sequenceModel'].includes(key)) || !Array.isArray(input.tools) || process.env.CC_HAHA_SYSTEM_PROMPT_FIXTURE !== '1' || process.env.USER_TYPE !== 'external') throw new Error('System prompt requires declared original private inputs')
const home = assertDisposableFixtureHome(input.home, 'System prompt')
const project = join(home, 'project'), config = join(home, '.claude')
fs.mkdirSync(project, { recursive: true })
fs.mkdirSync(config, { recursive: true })
fs.writeFileSync(join(config, '.claude.json'), JSON.stringify({ customApiKeyResponses: { approved: ['fixture'], rejected: [] } }))
fs.writeFileSync(join(config, 'settings.json'), JSON.stringify({ language: input.language, autoMemoryEnabled: input.autoMemory ?? true, outputStyle: input.style }))
for (const file of input.files ?? []) {
  const path = resolve(home, file.path)
  if (!path.startsWith(home + '/') || !path.includes('/output-styles/')) throw new Error('Only original private output style files')
  fs.mkdirSync(dirname(path), { recursive: true })
  fs.writeFileSync(path, file.content)
}
Object.assign(globalThis, { MACRO: { VERSION: 'fixture', PACKAGE_URL: 'fixture', NATIVE_PACKAGE_URL: 'fixture', BUILD_TIME: 'fixture', FEEDBACK_CHANNEL: 'local', VERSION_CHANGELOG: '', ISSUES_EXPLAINER: '' } })
const absent = absentManagedSettingsFixturePaths()
const managed = dirname(absent[0]!)
for (const leaf of ['CLAUDE.md', '.claude/rules', '.claude/skills', '.claude/commands', '.claude/output-styles', 'skills', 'commands', 'output-styles']) absent.push(join(managed, leaf))
absent.push(join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token')
for (const directory of (process.env.PATH ?? '').split(':')) absent.push(join(directory, 'rg'))
for (let path = dirname(home); ; path = dirname(path)) {
  for (const leaf of ['.git', '.claude/skills', '.claude/commands', '.claude/output-styles', 'CLAUDE.md', '.claude/CLAUDE.md', 'CLAUDE.local.md', 'AGENTS.md', '.claude/AGENTS.md']) absent.push(join(path, leaf))
  if (dirname(path) === path) break
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absent })
if (input.baseURL) process.env.ANTHROPIC_BASE_URL = input.baseURL
const bootstrap = await import('../../../../src/bootstrap/state')
const configAPI = await import('../../../../src/utils/config')
const common = await import('../../../../src/constants/common')
bootstrap.setOriginalCwd(project)
bootstrap.setCwdState(project)
bootstrap.setProjectRoot(project)
bootstrap.setIsInteractive(input.interactive)
bootstrap.setAllowedSettingSources(input.sources ?? ['userSettings'])
configAPI.enableConfigs()
process.env.CLAUDE_CODE_SIMPLE = input.simple ? '1' : '0'
process.env.CLAUDE_CODE_OVERRIDE_DATE = '2026-10-05'
process.env.CLAUDE_CODE_USE_NATIVE_FILE_SEARCH = '1'
common.getSessionStartDate.cache.clear()
const prompts = await import('../../../../src/constants/prompts')
const { runWithCwdOverride } = await import('../../../../src/utils/cwd')
const sections = input.agentEnvironment ? [await runWithCwdOverride(input.agentCwdOverride ? join(project, 'child') : project, () => prompts.computeEnvInfo(input.model, input.additional ?? []))] : await prompts.getSystemPrompt(input.tools.map((name: string) => ({ name })), input.model, [], [])
const sequence = []
if (input.sequenceModel) {
  sequence.push(await prompts.getSystemPrompt(input.tools.map((name: string) => ({ name })), input.sequenceModel, [], []))
  const { clearSystemPromptSections } = await import('../../../../src/constants/systemPromptSections')
  clearSystemPromptSections()
  sequence.push(await prompts.getSystemPrompt(input.tools.map((name: string) => ({ name })), input.sequenceModel, [], []))
}
const { getAllOutputStyles } = await import('../../../../src/constants/outputStyles')
const styles = Object.entries(await getAllOutputStyles(project)).map(([key, style]) => ({ name: style?.name ?? key, description: style?.description ?? '', source: style?.source ?? 'built-in', prompt: style?.prompt ?? '', keep: style?.keepCodingInstructions === true, nil: style === null }))
process.stdout.write(JSON.stringify({ sections, sequence, styles, capabilities }))

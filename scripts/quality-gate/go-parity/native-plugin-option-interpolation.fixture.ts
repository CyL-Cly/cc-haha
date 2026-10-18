import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { createRequire } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const home = assertDisposableFixtureHome(process.env.HOME, 'Actual saved plugin option interpolation')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_PLUGIN_OPTION_INTERPOLATION !== '1') throw new Error('Unowned interpolation Source')
const ts = createRequire(new URL('../../../desktop/package.json', import.meta.url))('typescript')
const caps = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
const input = await Bun.stdin.json()
function compile(path: string, names: string[], deps: Record<string, unknown>, returns: string) {
  const source = ts.createSourceFile(path, fs.readFileSync(new URL('../../../src/' + path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  const nodes = source.statements.filter((node: any) => ts.isFunctionDeclaration(node) && names.includes(node.name?.text))
  if (nodes.length !== names.length) throw new Error('Original interpolation declarations changed')
  const code = ts.transpileModule(nodes.map((node: any) => node.getText(source).replace(/^export /, '')).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
  return new Function(...Object.keys(deps), code + '\nreturn ' + returns)(...Object.values(deps))
}
const directories: string[] = []
const options = compile('utils/plugins/pluginOptionsStorage.ts', ['substitutePluginVariables', 'substituteUserConfigVariables', 'substituteUserConfigInContent'], {
  getPluginDataDir: () => { directories.push(input.dataDirectory); fs.mkdirSync(input.dataDirectory, { recursive: true }); return input.dataDirectory }
}, '({substitutePluginVariables,substituteUserConfigVariables,substituteUserConfigInContent})')
const environment = compile('services/mcp/envExpansion.ts', ['expandEnvVarsInString'], {}, 'expandEnvVarsInString')
const resolve = compile('utils/plugins/mcpPluginIntegration.ts', ['resolvePluginMcpEnvironment'], {
  ...options, expandEnvVarsInString: environment,
  getPluginDataDir: () => { directories.push(input.dataDirectory); fs.mkdirSync(input.dataDirectory, { recursive: true }); return input.dataDirectory }
}, 'resolvePluginMcpEnvironment')
for (const [key, value] of Object.entries(input.environment ?? {})) {
  if (!key.startsWith('CC_HAHA_INTERPOLATION_')) throw new Error('Unowned fixture environment key')
  process.env[key] = value as string
}
const results = []
for (const row of input.rows) {
  const originalPlatform = Object.getOwnPropertyDescriptor(process, 'platform')!
  const errors: any[] = []
  Object.defineProperty(process, 'platform', { ...originalPlatform, value: row.platform ?? originalPlatform.value })
  try {
    let value
    if (row.prompt) {
      const path = 'utils/plugins/loadPluginCommands.ts'
      const source = ts.createSourceFile(path, fs.readFileSync(new URL('../../../src/' + path, import.meta.url), 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
      const methods: any[] = []
      const visit = (node: any) => {
        if (ts.isMethodDeclaration(node) && node.name.getText(source) === 'getPromptForCommand') methods.push(node)
        ts.forEachChild(node, visit)
      }
      visit(source)
      if (methods.length !== 1) throw new Error('Original plugin prompt method changed')
      const method = methods[0].getText(source).replace(/^async getPromptForCommand/, 'async function getPromptForCommand')
      const code = ts.transpileModule(method, { compilerOptions: { target: ts.ScriptTarget.ESNext } }).outputText
      ;(globalThis as any).MACRO = { BUILD_TIME: '' }
      const { substituteArguments } = await import('../../../src/utils/argumentSubstitution')
      const { executeShellCommandsInPrompt } = await import('../../../src/utils/promptShellExecution')
      const deps = {
        ...options, dirname, substituteArguments, executeShellCommandsInPrompt,
        config: { isSkillMode: row.skillMode }, content: row.content,
        file: { filePath: join(row.directory, 'SKILL.md') }, argumentNames: [],
        pluginPath: input.plugin.path, sourceName: input.plugin.source,
        pluginManifest: { userConfig: row.schema },
        loadPluginOptions: () => row.values, getSessionId: () => row.sessionID,
        allowedTools: [], commandName: 'unicode:actual', shell: 'bash'
      }
      const prompt = new Function(...Object.keys(deps), code + '\nreturn getPromptForCommand')(...Object.values(deps))
      value = await prompt('', { getAppState: () => ({ toolPermissionContext: { alwaysAllowRules: {} } }) })
    } else {
      value = row.config ? resolve(row.config, input.plugin, row.values, errors, 'unicode', 'server') : options.substituteUserConfigInContent(row.content, row.values, row.schema)
    }
    results.push({ value, errors, failure: null })
  } catch (error) {
    results.push({ value: null, errors, failure: (error as Error).message })
  } finally {
    Object.defineProperty(process, 'platform', originalPlatform)
  }
}
if (caps.blocked.length || caps.secureReads.length) throw new Error('Interpolation Source escaped private boundary: ' + JSON.stringify(caps))
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ results, directories }), error => error ? reject(error) : resolve()))

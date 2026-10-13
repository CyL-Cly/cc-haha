import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { exposeSourceBindings } from './source-exports'
import { mock } from 'bun:test'
import { createHash } from 'node:crypto'
import { realpathSync, statSync } from 'node:fs'
import { resolve, sep } from 'node:path'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Actual native PowerShell AST policy')
if (input.cwd) {
  const ownedHome = realpathSync(home), cwd = realpathSync(input.cwd)
  if (!statSync(cwd).isDirectory() || !resolve(input.cwd).startsWith(resolve(home) + sep) || !cwd.startsWith(ownedHome + sep)) throw new Error('PowerShell fixture working directory must remain inside disposable HOME')
  process.chdir(cwd)
}
if (process.env.CC_HAHA_NATIVE_POWERSHELL_POLICY_FIXTURE !== '1') throw new Error('Unowned native PowerShell policy fixture')
const spawnNativeAST = Bun.spawn.bind(Bun)
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.cwd ?? home)
bootstrap.setOriginalCwd(input.cwd ?? home)
bootstrap.setProjectRoot(input.cwd ?? home)
const detectionPath = new URL('../../../src/utils/shell/powershellDetection.ts', import.meta.url).pathname
let selectedExecutable = input.executable ?? null
let controlledProcesses: any[] | undefined
let observedParserTimeouts: number[] = []
// Native executable discovery is the explicit platform seam. Both sides
// execute this exact pwsh binary; the AST parser and validators remain Source.
mock.module(detectionPath, () => ({ getCachedPowerShellPath: async () => selectedExecutable }))
// No sandbox is configured in the disposable empty-settings profile. Keep
// its native availability service outside this policy contract; otherwise
// Source probes the host's packaged rg/macOS dependencies before reading the
// disabled setting. The permission/path validators themselves remain intact.
let sandboxProfile: any
mock.module(new URL('../../../src/utils/sandbox/sandbox-adapter.ts', import.meta.url).pathname, () => ({ SandboxManager: {
  isSandboxingEnabled: () => !!sandboxProfile?.enabled,
  getFsWriteConfig: () => ({ allowOnly: sandboxProfile?.allowOnly ?? [], denyWithinAllow: sandboxProfile?.denyWithinAllow ?? [] }),
} }))
const parserPath = new URL('../../../src/utils/powershell/parser.ts', import.meta.url).pathname
await Bun.plugin({ name: 'actual-powershell-private-transform', setup(builder) {
  builder.onLoad({ filter: /\/utils\/powershell\/parser\.ts$/ }, async args => ({
    contents: exposeSourceBindings(await Bun.file(args.path).text(), ['transformRawOutput', 'buildParseScript', 'getParseTimeoutMs']), loader: 'ts',
  }))
  builder.onLoad({ filter: /\/PowerShellTool\/pathValidation\.ts$/ }, async args => ({
    contents: exposeSourceBindings(await Bun.file(args.path).text(), ['CMDLET_PATH_CONFIG', 'extractPathsFromCommand', 'validatePath']), loader: 'ts',
  }))
  builder.onLoad({ filter: /\/PowerShellTool\/powershellPermissions\.ts$/ }, async args => ({
    contents: exposeSourceBindings(await Bun.file(args.path).text(), ['filterRulesByContentsMatchingInput']), loader: 'ts',
  }))
  builder.onLoad({ filter: /\/PowerShellTool\/gitSafety\.ts$/ }, async args => ({
    contents: exposeSourceBindings(await Bun.file(args.path).text(), ['normalizeGitPathArg']), loader: 'ts',
  }))
  builder.onLoad({ filter: /\/utils\/permissions\/permissions\.ts$/ }, async args => ({
    contents: exposeSourceBindings(await Bun.file(args.path).text(), ['hasPermissionsToUseToolInner']), loader: 'ts',
  }))
} })
// The fixture's only subprocess is a data-only native Parser.ParseInput job.
// Guard every argv before invoking the saved physical primitive; the ordinary
// Source graph still has the fail-closed process/network capability boundary.
let scriptBody = ''
mock.module('execa', () => ({ execaSync: () => { throw new Error('Unowned synchronous process in AST fixture') }, execa: async (executable: string, args: string[], options: any) => {
  if (executable !== input.executable || !/(?:^|[\\/])pwsh(?:\.exe)?$/.test(executable)
      || args.length !== 5 || args.slice(0, 4).join('\0') !== '-NoProfile\0-NonInteractive\0-NoLogo\0-EncodedCommand') throw new Error('Unowned AST process')
  const script = Buffer.from(args[4]!, 'base64').toString('utf16le')
  if (!scriptBody || !script.endsWith(scriptBody) || !/^\$EncodedCommand = '[A-Za-z0-9+/=]*'\n$/.test(script.slice(0, -scriptBody.length))) throw new Error('Non-parser PowerShell execution refused')
  observedParserTimeouts.push(options.timeout)
  if (input.parserUserTypeAfterSpawn !== undefined) process.env.USER_TYPE = input.parserUserTypeAfterSpawn
  if (controlledProcesses) {
    const result = controlledProcesses.shift()
    if (!result) throw new Error('Unexpected additional parser process')
    if (result.error) throw new Error(result.error)
    return result
  }
  const child = spawnNativeAST([executable, ...args], { cwd: home, env: { HOME: home, USERPROFILE: home, PATH: '/usr/bin:/bin', DOTNET_CLI_HOME: home }, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  let timedOut = false
  const timer = setTimeout(() => { timedOut = true; child.kill() }, options.timeout)
  try {
    const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    return { exitCode, stdout, stderr, timedOut, failed: exitCode !== 0 || timedOut }
  } finally { clearTimeout(timer) }
} }))
const parser: any = await import(parserPath)
scriptBody = parser.PARSE_SCRIPT_BODY
const { execa } = await import('execa')
const { powershellCommandIsSafe } = await import('../../../src/tools/PowerShellTool/powershellSecurity')
const { isClmAllowedType } = await import('../../../src/tools/PowerShellTool/clmTypes')
const readOnly = await import('../../../src/tools/PowerShellTool/readOnlyValidation')
const { checkPermissionMode } = await import('../../../src/tools/PowerShellTool/modeValidation')
const pathPolicy: any = await import('../../../src/tools/PowerShellTool/pathValidation')
const permissionPolicy: any = await import('../../../src/tools/PowerShellTool/powershellPermissions')
const { isPathInSandboxWriteAllowlist } = await import('../../../src/utils/permissions/pathValidation')
const gitSafety: any = await import('../../../src/tools/PowerShellTool/gitSafety')
const prefixes = await import('../../../src/utils/powershell/staticPrefix')
const { NEVER_SUGGEST } = await import('../../../src/utils/powershell/dangerousCmdlets')
const { getCommandSpec } = await import('../../../src/utils/bash/registry')
const { default: builtInSpecs } = await import('../../../src/utils/bash/specs/index')
const { parseToolListFromCLI } = await import('../../../src/utils/permissions/permissionSetup')
const permissionCore: any = await import('../../../src/utils/permissions/permissions')
const { generateShellSuggestionsLabel } = await import('../../../src/components/permissions/shellPermissionHelpers')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const rows = []
for (const command of input.commands ?? []) {
  const encoded = Buffer.from(parser.buildParseScript(command), 'utf16le').toString('base64')
  const processResult = await execa(input.executable, ['-NoProfile', '-NonInteractive', '-NoLogo', '-EncodedCommand', encoded], { timeout: 5000, reject: false })
  if (processResult.exitCode !== 0 || processResult.timedOut) throw new Error('Actual native parser failed: ' + processResult.stderr)
  const raw = JSON.parse(processResult.stdout)
  const parsed = parser.transformRawOutput(raw)
  rows.push({ command, raw, parsed, security: powershellCommandIsSafe(command, parsed), flags: parser.deriveSecurityFlags(parsed), readOnly: readOnly.isReadOnlyCommand(command, parsed),
    extractedPaths: parser.getAllCommands(parsed).map(pathPolicy.extractPathsFromCommand),
    modes: (input.modes ?? []).map((mode: any) => checkPermissionMode({ command }, parsed, { mode } as any)),
    allNames: parser.getAllCommandNames(parsed), allCommands: parser.getAllCommands(parsed), redirections: parser.getAllRedirections(parsed), fileRedirections: parser.getFileRedirections(parsed), single: parser.isSingleCommand(parsed), directoryChange: parser.hasDirectoryChange(parsed) })
}
const transformed = (input.raw ?? []).map((raw: any) => parser.transformRawOutput(raw))
function labelText(value: any): string {
  if (value === null || value === undefined || typeof value === 'boolean') return ''
  if (typeof value === 'string' || typeof value === 'number') return String(value)
  if (Array.isArray(value)) return value.map(labelText).join('')
  return labelText(value.props?.children)
}
const shellLabelRows = (input.shellLabelRows ?? []).map((row: any) => labelText(generateShellSuggestionsLabel(row.suggestions, row.shell)))
const timeoutValues = (input.timeoutValues ?? []).map((value: string) => { process.env.CLAUDE_CODE_PWSH_PARSE_TIMEOUT_MS = value; return String(parser.getParseTimeoutMs()) })
delete process.env.CLAUDE_CODE_PWSH_PARSE_TIMEOUT_MS
const toolLists = (input.toolLists ?? []).map(parseToolListFromCLI)
const gitRows = (input.gitRows ?? []).map((value: string) => ({ normalized: gitSafety.normalizeGitPathArg(value), internal: gitSafety.isGitInternalPathPS(value), dotGit: gitSafety.isDotGitPathPS(value) }))
const failures = []
for (const command of input.failures ?? []) failures.push(await parser.parsePowerShellCommand(command))
const ruleRows = (input.ruleRows ?? []).map((row: any) => ({ parsed: permissionPolicy.powershellPermissionRule(row.content), matched: permissionPolicy.filterRulesByContentsMatchingInput({ command: row.command }, new Map([[row.content, row.rule]]), row.matchMode, row.behavior) }))
const pathRows = (input.pathRows ?? []).map((row: any) => {
  const context = { ...getEmptyToolPermissionContext(), ...row.context, additionalWorkingDirectories: new Map((row.context?.directories ?? []).map((dir: string) => [dir, {}])) }
  return { path: pathPolicy.validatePath(row.path, input.cwd ?? home, context, row.operation) }
})
const evaluations = []
for (const row of input.evaluations ?? []) {
  const context = { ...getEmptyToolPermissionContext(), ...row.context, additionalWorkingDirectories: new Map((row.context?.directories ?? []).map((dir: string) => [dir, {}])) }
  const parsed = await parser.parsePowerShellCommand(row.input.command.trim())
  const result = await permissionPolicy.powershellToolHasPermission(row.input, { getAppState: () => ({ toolPermissionContext: context }) } as any)
  evaluations.push({ parsed, result })
}
const normalizedEvaluations = []
for (const row of input.normalizedEvaluations ?? []) {
  if (row.userType === undefined) delete process.env.USER_TYPE
  else process.env.USER_TYPE = row.userType
  const context = { ...getEmptyToolPermissionContext(), ...row.context, additionalWorkingDirectories: new Map((row.context?.directories ?? []).map((dir: string) => [dir, {}])) }
  const tool = { name: 'PowerShell', inputSchema: { parse: (value: any) => value }, checkPermissions: permissionPolicy.powershellToolHasPermission }
  const result = await permissionCore.hasPermissionsToUseToolInner(tool, row.input, { abortController: new AbortController(), getAppState: () => ({ toolPermissionContext: context }) })
  normalizedEvaluations.push({ parsed: await parser.parsePowerShellCommand(row.input.command.trim()), result })
}
delete process.env.USER_TYPE
const prefixRows = []
for (const command of input.prefixCommands ?? []) {
  prefixRows.push({ parsed: await parser.parsePowerShellCommand(command), first: await prefixes.getCommandPrefixStatic(command), compound: await prefixes.getCompoundCommandPrefixesStatic(command), filtered: await prefixes.getCompoundCommandPrefixesStatic(command, element => readOnly.isAllowlistedCommand(element, element.text)) })
}
const prefixSpecs = await Promise.all((input.prefixSpecNames ?? []).map(async (name: string) => [name, await getCommandSpec(name)]))
const sandboxRows = []
for (const row of input.sandboxRows ?? []) {
  sandboxProfile = row.config
  sandboxRows.push({ allowed: isPathInSandboxWriteAllowlist(row.path) })
}
sandboxProfile = undefined
const failureProfiles = []
for (const profile of input.failureProfiles ?? []) {
  parser.parsePowerShellCommand.cache.clear()
  selectedExecutable = profile.noPowerShell ? null : input.executable
  controlledProcesses = [...(profile.results ?? [])]
  const results = []
  for (let i = 0; i < (profile.calls ?? 1); i++) results.push(await parser.parsePowerShellCommand(profile.command))
  failureProfiles.push({ results, remainingProcesses: controlledProcesses.length })
}
const cacheRows = []
const parserEnvironmentRows = []
if (input.parserEnvironmentRows) {
  parser.parsePowerShellCommand.cache.clear()
  selectedExecutable = input.executable
  for (const row of input.parserEnvironmentRows) {
    process.env.CLAUDE_CODE_PWSH_PARSE_TIMEOUT_MS = row.timeout
    controlledProcesses = row.results === null ? undefined : [...row.results]
    observedParserTimeouts = []
    const result = await parser.parsePowerShellCommand(row.command)
    parserEnvironmentRows.push({ result, timeouts: observedParserTimeouts, remainingProcesses: controlledProcesses?.length ?? 0 })
  }
  delete process.env.CLAUDE_CODE_PWSH_PARSE_TIMEOUT_MS
}
if (input.cacheCommands) {
  parser.parsePowerShellCommand.cache.clear()
  selectedExecutable = null
  controlledProcesses = undefined
  for (const command of input.cacheCommands) {
    const result = await parser.parsePowerShellCommand(command)
    cacheRows.push({ result, size: parser.parsePowerShellCommand.cache.size(), has: (input.cacheProbes ?? []).map((key: string) => parser.parsePowerShellCommand.cache.has(key)) })
  }
}
if (capabilities.blocked.length) throw new Error('Blocked native PowerShell policy fixture: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ rows, transformed, failures, ruleRows, pathRows, evaluations, normalizedEvaluations, sandboxRows, gitRows, prefixRows, prefixSpecs, builtInSpecs, neverSuggest: [...NEVER_SUGGEST], toolLists, shellLabelRows, timeoutValues, failureProfiles, parserEnvironmentRows, cacheRows, aliases: parser.COMMON_ALIASES,
  clm: (input.types ?? []).map((type: string) => isClmAllowedType(type)), script: parser.PARSE_SCRIPT_BODY,
  allowlist: Object.fromEntries(Object.entries(readOnly.CMDLET_ALLOWLIST).map(([name, value]) => [name, { safeFlags: value.safeFlags, allowAllFlags: value.allowAllFlags, callback: value.additionalCommandIsDangerousCallback?.name, regex: value.regex?.source }])),
  pathConfigs: pathPolicy.CMDLET_PATH_CONFIG,
  windowsMaxCommandLength: parser.WINDOWS_MAX_COMMAND_LENGTH, maxCommandLength: parser.MAX_COMMAND_LENGTH,
  authority: createHash('sha256').update(await Bun.file(parserPath).text()).digest('hex'), capabilities: { blocked: capabilities.blocked } }) + '\n')

import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { basename, dirname, join, resolve, win32 } from 'node:path'
import { existsSync } from 'node:fs'
import { mock } from 'bun:test'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Native Windows shell')
if (process.env.CC_HAHA_NATIVE_WINDOWS_SHELL_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned Windows shell fixture')
const executable = process.env.CLAUDE_CODE_POWERSHELL_PATH
const guardianText = input.execute?.length ? await Bun.file(new URL('../../../src/utils/shell/parentProcessGuard.ts', import.meta.url)).text() : ''
const guardian = guardianText.match(/const PARENT_PROCESS_GUARD = `([\s\S]*?)`/)?.[1]
const originalEnvironment = { ...process.env }
let approvedCommand: ((command: string) => boolean) | undefined
const approvalFailures: unknown[] = []
const capabilities = await installClientFixtureCapabilities(home, {
  absentPaths: [...absentManagedSettingsFixturePaths(), ...(input.execute?.length ? [join(dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg'] : [])],
  readonlyExternalFiles: input.execute?.length && executable ? [executable] : [],
  approvedSpawn: input.execute?.length ? request => {
    if (request.command === '/usr/bin/pgrep') return request.args.length === 2 && request.args[0] === '-P' && request.options.detached === false
    const direct = request.command === executable
    const wrapped = request.command === '/bin/sh' && request.args[0] === '-c' && request.args[1] === guardian && request.args[2] === 'cc-haha-shell-lifetime' && request.args[3] === String(process.pid) && request.args[4] === executable
    const args = wrapped ? request.args.slice(5) : request.args
    const expectedEnvironment = { ...originalEnvironment, CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: originalEnvironment.CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH ?? '1', SHELL: undefined, GIT_EDITOR: 'true', CLAUDECODE: '1' }
    const foreignEnvironment = Object.entries(request.options.env ?? {}).filter(([key, value]) => value !== expectedEnvironment[key as keyof typeof expectedEnvironment]).map(([key]) => key)
    const commandApproved = args.length === 4 && approvedCommand?.(args[3]) === true
    const allowed = (direct || wrapped) && request.options.detached === wrapped && args.length === 4 && args[0] === '-NoProfile' && args[1] === '-NonInteractive' && args[2] === '-Command' && commandApproved && foreignEnvironment.length === 0
    if (!allowed) approvalFailures.push({ direct, wrapped, detached: request.options.detached, argumentCount: args.length, commandApproved, foreignEnvironment })
    return allowed
  } : undefined,
})
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setOriginalCwd(input.cwd ?? home)
state.setCwdState(input.cwd ?? home)
if (input.sessionId) state.switchSession(input.sessionId)
const { parse } = await import('acorn')
const originalText = await Bun.file(new URL('../../../src/utils/windowsPaths.ts', import.meta.url)).text()
const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(originalText)
const ast = parse(code, { ecmaVersion: 'latest', sourceType: 'module' }) as any
const functions = new Map<string, string>()
let gitResolver = ''
for (const top of ast.body) {
  const node = top.type === 'ExportNamedDeclaration' ? top.declaration : top
  if (node?.type === 'FunctionDeclaration') functions.set(node.id.name, code.slice(node.start, node.end))
  if (node?.type === 'VariableDeclaration') for (const declaration of node.declarations) {
    if (declaration.id?.name === 'tryFindGitBashPath') gitResolver = code.slice(declaration.init.arguments[0].start, declaration.init.arguments[0].end)
  }
}
if (!gitResolver || !functions.has('checkPathExists') || !functions.has('findExecutable')) throw new Error('Frozen Source Git Bash resolver is unavailable')
const git = (input.git ?? []).map((row: any) => {
  const calls: string[] = []
  const exec = (command: string) => {
    calls.push(command)
    if (command === 'where.exe git') {
      if (row.whereError) throw new Error('Native where probe unavailable')
      return row.where ?? ''
    }
    if (command.startsWith('dir "') && command.endsWith('"')) {
      if (!(row.exists ?? []).includes(command.slice(5, -1))) throw new Error('Native path unavailable')
      return ''
    }
    throw new Error('Unexpected Source native probe: ' + command)
  }
  const source = new Function('process', 'execSync_DEPRECATED', 'getCwd', 'path', 'pathWin32', 'logForDebugging', `${functions.get('checkPathExists')}\n${functions.get('findExecutable')}\nreturn (${gitResolver})()`)
  const path = source({ env: row.environment ?? {} }, exec, () => row.cwd, win32, win32, () => {})
  return { path, calls }
})
const detection = await import('../../../src/utils/shell/powershellDetection')
const overrides = []
for (const row of input.overrides ?? []) {
  const calls: any[] = []
  const path = await detection.resolvePowerShellPathOverride(row.override, {
    getPlatform: () => row.platform ?? 'windows',
    probePath: async candidate => { calls.push(['probe', candidate]); return (row.files ?? []).includes(candidate) ? candidate : null },
    readWindowsPwshHome: async () => { calls.push(['home']); return row.psHome ?? null },
    which: async candidate => { calls.push(['which', candidate]); return row.which?.[candidate] ?? null },
  })
  overrides.push({ path, calls })
}
const tools = await import('../../../src/utils/shell/shellToolUtils')
const defaults = await import('../../../src/utils/shell/resolveDefaultShell')
const availability = (input.availability ?? []).map((row: any) => {
  const available = tools.resolveShellToolAvailability({ platform: row.platform, gitBashPath: row.gitBashPath ?? null, userType: row.userType, powerShellSetting: row.setting })
  return { available, shell: defaults.resolveDefaultShellFromAvailability(row.configured, available) }
})
const { createPowerShellProvider } = await import('../../../src/utils/shell/powershellProvider')
const providers = []
for (const row of input.providers ?? []) {
  const provider = createPowerShellProvider(row.path)
  const prepared = await provider.buildExecCommand(row.command, { id: row.id, useSandbox: false })
  providers.push({ ...prepared, args: provider.getSpawnArgs(prepared.commandString), detached: provider.detached })
}
const { PowerShellTool } = await import('../../../src/tools/PowerShellTool/PowerShellTool')
const { zodToJsonSchema } = await import('../../../src/utils/zodToJsonSchema')
const metadata = { name: PowerShellTool.name, searchHint: PowerShellTool.searchHint, maxResultSizeChars: PowerShellTool.maxResultSizeChars, strict: PowerShellTool.strict }
const outputSchema = zodToJsonSchema(PowerShellTool.outputSchema)
const mcpInputSchema = zodToJsonSchema(PowerShellTool.inputSchema)
const { toolToAPISchema } = await import('../../../src/utils/api')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const schema = await toolToAPISchema(PowerShellTool, { tools: [PowerShellTool], agents: [], model: 'deepseek-flash', getToolPermissionContext: async () => getEmptyToolPermissionContext() })
const parsers = (input.parsers ?? []).map((value: any) => {
  const parsed = PowerShellTool.inputSchema.safeParse(value)
  return parsed.success ? { data: parsed.data } : { issues: parsed.error.issues }
})
const results = (input.results ?? []).map((row: any) => PowerShellTool.mapToolResultToToolResultBlockParam(row.data, row.id))
const shellImages = []
if (input.shellImages?.length) {
  const { isImageOutput, resizeShellImageOutput } = await import('../../../src/tools/BashTool/utils')
  const { BashTool } = await import('../../../src/tools/BashTool/BashTool')
  for (const row of input.shellImages) {
    let stdout = row.stdout
    let isImage = isImageOutput(stdout)
    if (isImage) {
      const normalized = await resizeShellImageOutput(stdout, row.path, row.size)
      if (normalized) stdout = normalized
      else isImage = false
    }
    shellImages.push({ stdout, isImage, mapped: BashTool.mapToolResultToToolResultBlockParam({ ...row.data, stdout, isImage }, row.id) })
  }
}
const executions = []
if (input.execute?.length) {
  const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
  const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
  for (const row of input.execute) {
    const provider = createPowerShellProvider(executable!)
    const prepared = await provider.buildExecCommand(row.input.command, { id: 'FIXTURE_APPROVAL_MARKER', useSandbox: false })
    const [before, after] = prepared.commandString.split(prepared.cwdFilePath.replace(/'/g, "''"))
    approvedCommand = command => {
      if (!command.startsWith(before) || !command.endsWith(after)) return false
      const path = command.slice(before.length, command.length - after.length)
      return resolve(dirname(path)) === resolve(process.env.TMPDIR!) && /^claude-pwd-ps-[A-Za-z0-9_-]+$/.test(basename(path)) && path === join(dirname(path), basename(path))
    }
    let current = getDefaultAppState()
    const abortController = new AbortController()
    const context: any = { abortController, options: { commands: [], tools: [], mcpClients: [], mcpResources: {}, mainLoopModel: 'deepseek-flash', verbose: false, debug: false, isNonInteractiveSession: true, thinkingConfig: { type: 'disabled' }, agentDefinitions: { activeAgents: [], allAgents: [] } }, readFileState: createFileStateCacheWithSizeLimit(), getAppState: () => current, setAppState: (update: any) => { current = update(current) }, setAppStateForTasks: (update: any) => { current = update(current) }, messages: [] }
    state.setCwdState(input.cwd ?? home)
    let timer: ReturnType<typeof setTimeout> | undefined
    const timerStarted = Date.now()
    if (row.abortAfterFile) {
      timer = setInterval(() => {
        if (existsSync(row.abortAfterFile)) { clearInterval(timer); abortController.abort(row.abortReason) }
        else if (Date.now() - timerStarted > 5_000) { clearInterval(timer); abortController.abort('fixture-owned-start-timeout') }
      }, 20)
    } else if (row.abortMs) timer = setTimeout(() => abortController.abort(row.abortReason), row.abortMs)
    try {
      if (row.prompt) {
        const { executeShellCommandsInPrompt } = await import('../../../src/utils/promptShellExecution')
        const availability = tools.resolveShellToolAvailability({ platform: 'windows', gitBashPath: row.prompt.gitBashPath ?? null, powerShellSetting: row.prompt.powerShellSetting })
        const checked: unknown[] = []
        const text = await executeShellCommandsInPrompt(row.prompt.text, context, row.prompt.name ?? '/fixture', row.prompt.shell, {
          availability,
          // The original prompt utility exposes this permission dependency.
          // Bind only the corpus' explicit whole-tool grant. The actual tool,
          // shell process, result persistence and replacement remain Source.
          checkPermission: async (tool, command) => {
            if (tool.name !== 'PowerShell' || (command as any).command !== row.input.command) throw new Error('Unexpected Source prompt permission request')
            checked.push({ name: tool.name, input: command })
            return { behavior: 'allow', updatedInput: command }
          },
        })
        executions.push({ promptOutput: text, checked, cwd: state.getCwdState() })
        continue
      }
      if (row.terminal) {
        mock.module(new URL('../../../src/utils/shell/shellToolUtils.ts', import.meta.url).pathname, () => ({ ...tools, getShellToolAvailability: () => tools.resolveShellToolAvailability({ platform: 'windows', gitBashPath: null }), isPowerShellToolEnabled: () => true }))
        const { processBashCommand } = await import('../../../src/utils/processUserInput/processBashCommand')
        const processed = await processBashCommand(row.input.command, [], [], context, () => {})
        executions.push({ messages: processed.messages.map((message: any) => ({ type: message.type, role: message.message?.role, content: message.message?.content, isMeta: message.isMeta === true })), shouldQuery: processed.shouldQuery, cwd: state.getCwdState() })
        continue
      }
      const result = await PowerShellTool.call(row.input, context)
      const execution: any = { data: result.data, mapped: PowerShellTool.mapToolResultToToolResultBlockParam(result.data, row.id), cwd: state.getCwdState() }
      if (result.data.backgroundTaskId && row.joinBackground) {
        const task = current.tasks[result.data.backgroundTaskId]
        if (!task?.shellCommand) throw new Error('Source background task did not retain its actual process')
        const shell = task.shellCommand
        let joinTimer: ReturnType<typeof setTimeout> | undefined
        try {
          const completed = await Promise.race([shell.result, new Promise<never>((_, reject) => { joinTimer = setTimeout(() => reject(new Error('Source owned PowerShell did not join')), 8_000) })])
          execution.background = { type: task.type, isBackgrounded: task.isBackgrounded, code: completed.code, interrupted: completed.interrupted }
        } finally {
          if (joinTimer) clearTimeout(joinTimer)
          if (shell.status === 'running' || shell.status === 'backgrounded') shell.kill()
          await shell.result
          shell.cleanup()
        }
      }
      executions.push(execution)
    } catch (error: any) {
      executions.push({ error: { name: error.name, message: error.message, stdout: error.stdout, stderr: error.stderr, code: error.code, interrupted: error.interrupted }, cwd: state.getCwdState() })
    } finally {
      if (timer) clearTimeout(timer)
      abortController.abort()
      // Real foreground tasks have already joined their shell result. Any
      // background corpus is admitted separately with an explicit join below.
    }
  }
}
if (capabilities.blocked.length) throw new Error('Blocked Windows shell fixture: ' + capabilities.blocked.join(', ') + '; approval=' + JSON.stringify(approvalFailures))
process.stdout.write(JSON.stringify({ git, overrides, availability, providers, schema, metadata, outputSchema, mcpInputSchema, parsers, results, shellImages, executions, capabilities: { blocked: capabilities.blocked } }) + '\n')

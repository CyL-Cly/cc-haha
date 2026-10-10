import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { probeCli } from './cliInventory'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
let home: string, executable: string
const diagnostics: unknown[] = []

beforeAll(async () => {
  home = await mkdtemp(join(tmpdir(), 'go-cliargs-parity-'))
  executable = join(home, process.platform === 'win32' ? 'cliargs-fixture.exe' : 'cliargs-fixture')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/cliargs/testdata/fixture'], {
    cwd: resolve(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, home),
    stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill(), 180_000)
  try {
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr).toBe(0)
  } finally { clearTimeout(timer) }
}, 190_000)

afterAll(async () => {
  if (diagnostics.length) {
    const directory = join(rootDir, 'artifacts/go-parity/cliargs')
    await mkdir(directory, { recursive: true })
    await writeFile(join(directory, 'pending-raw-error-diagnostics.json'), JSON.stringify({
      scope: 'Raw Error message/exit/guard only; stack/source excerpt stdio contract remains incomplete',
      fixtures: diagnostics,
    }, null, 2) + '\n')
  }
  if (home) await rm(home, { recursive: true, force: true })
})

async function go(args: string[], xaa = false, operation?: string, profile?: string) {
  const child = Bun.spawn([executable], {
    cwd: home, env: createSandboxedTestEnvironment(home), stdin: new Blob([JSON.stringify({ args, xaa, operation, profile })]),
    stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill(), 20_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, stderr).toBe(0)
    return JSON.parse(stdout)
  } finally { clearTimeout(timer) }
}

function cobraRegistration(node: any): any {
  const flags = node.options.flatMap((option: any) => {
    const names = option.long ? [option.long, ...(option.short?.startsWith('--') ? [option.short] : [])] : [option.short]
    return names.map((name: string, index: number) => ({
      name: name.replace(/^-+/, ''), short: index === 0 && option.short?.length === 2 ? option.short.slice(1) : '',
      hidden: option.hidden, description: option.description,
    }))
  }).sort((a: any, b: any) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0)
  return {
    name: node.name, aliases: node.aliases, description: node.description, hidden: node.hidden,
    hasAction: node.hasAction, use: (node.name + ' ' + node.usage).trim(), version: node.version ?? '',
    options: flags, commands: node.commands.map(cobraRegistration).sort((a: any, b: any) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0),
  }
}

function stripKinds(node: any): any {
  return {
    ...node,
    options: node.options.map(({ parserKind, ...option }: any) => option),
    arguments: node.arguments.map(({ parserKind, ...argument }: any) => argument),
    commands: node.commands.map(stripKinds),
  }
}

test('the neutral Go registration resource matches the actual production TS tree, including parser source/help/action boundaries', async () => {
  const spec = await go([], false, 'spec')
  const source = JSON.parse(await readFile(join(rootDir, 'backend/internal/cliargs/spec.json'), 'utf8'))
  expect(spec).toEqual(source)
  for (const [profile, args, env] of [
    ['normal', ['--help'], {}], ['bare', ['--bare', '--help'], {}],
    ['print', ['--print', '--help'], {}], ['xaa', ['--help'], { CLAUDE_CODE_ENABLE_XAA: '1' }],
  ] as const) {
    const reference = await probeCli([...args], { mode: 'tree', env })
    expect(reference.exitCode).toBe(0)
    expect(stripKinds(spec.profiles[profile])).toEqual(reference.registration)
    expect(await go([], false, 'tree', profile)).toEqual(cobraRegistration(reference.registration))
  }
}, 30_000)

const cases: string[][] = [
  [], ['hello'], ['--help'], ['-h'], ['--version'], ['-v'], ['-V'], ['--version', '--unknown'],
  ['-V', '--unknown'], ['--unknown'], ['--agent-teams'], ['--unknown', '--help'], ['--ouput-format', 'json'],
  ['--output-format'], ['--output-format', 'invalid'], ['--output-format', '--help'],
  ['--output-format=json'], ['--output-format=invalid'], ['--output-format', 'json', '--output-format', 'text'],
  ['one', 'two'], ['--', '--unknown'], ['-dp'], ['-pc'], ['-rname'], ['--resume'], ['--resume='],
  ['--resume', '-2'], ['--worktree', '-2'], ['--debug', 'filter'], ['--debug-file', 'path'],
  ['--debug-to-stderr'], ['--plugin-dir', 'one', '--plugin-dir=two', 'hello'],
  ['--allowedTools', 'Read', 'Write', '--disallowed-tools=Edit'], ['--tools', '-2', 'Read'],
  ['--tools=Read', 'hello'], ['--add-dir', 'one', '--add-dir', 'two'],
  ['--no-chrome'], ['--chrome', '--no-chrome'], ['--no-chrome', '--chrome'],
  ['--no-session-persistence', '--no-computer-use'], ['--max-turns', 'NaN'], ['--max-turns', 'Infinity'],
  ['--max-turns', '0x10'], ['--max-turns', '-0'], ['--max-turns', '\u00851'],
  ['--max-turns', '\ufeff7\u00a0'], ['--deep-link-last-fetch', 'invalid'],
  ['--deep-link-last-fetch', '7'], ['--deep-link-last-fetch', 'Infinity'], ['--effort', ' XHIGH '], ['--effort', 'ULTRACODE'],
  ['--effort', '-7'], ['--effort', '1.5'], ['--effort', 'invalid'],
  ['--max-budget-usd', '2.5'], ['--max-budget-usd', 'Infinity'], ['--task-budget', '0x10'],
  ['mcp'], ['mcp', '--help'], ['mcp', 'help'], ['mcp', 'help', 'get'], ['mcp', 'help', 'unknown'],
  ['mcp', 'unknown'], ['mcp', 'gett'], ['mcp', 'get'], ['mcp', 'get', 'name'], ['-vignored'],
  ['mcp', 'get', 'one', 'two'], ['mcp', 'get', 'name', '--verbose'],
  ['--verbose', 'mcp', 'serve', '--debug'], ['mcp', 'add', 'name', 'command', '--', '--flag', 'arg'],
  ['mcp', 'add', 'name', 'command', 'one', 'two'], ['mcp', 'add', '--scope', 'user', 'name', 'command'],
  ['mcp', 'add', '-suser', 'name', 'command'], ['mcp', 'add', '--scope'],
  ['plugins', '--help'], ['plugins', 'i', 'example'], ['plugins', 'install', 'example'],
  ['plugin', 'install', '--scope', 'project', 'example'], ['plugin', 'install', 'one', 'two'],
  ['auto-mode'], ['auto-mode', '--help'], ['auth', 'status'], ['--print', 'mcp'], ['plugin', 'unknown'],
  ['--unknown', '--', 'literal'],
]

for (const args of cases) {
  test('actual Commander/Cobra standard parse/stdio parity: ' + JSON.stringify(args), async () => {
    const reference = await probeCli(args)
    const candidate = await go(args)
    expect({ exitCode: candidate.exitCode, stdout: candidate.stdout, stderr: candidate.stderr, parsed: candidate.parsed })
      .toEqual({ exitCode: reference.exitCode, stdout: reference.stdout, stderr: reference.stderr, parsed: reference.parsed })
    expect(candidate.diagnostic).toBeUndefined()
  }, 25_000)
}

test('all registered command help paths and aliases retain the actual TS pipe output', async () => {
  const source = await probeCli(['--help'], { mode: 'tree', env: { CLAUDE_CODE_ENABLE_XAA: '1' } })
  const paths = (node: any, prefix: string[] = []): string[][] => [
    ...[node.name, ...node.aliases].map(name => [...prefix, name]),
    ...node.commands.flatMap((child: any) => paths(child, [...prefix, node.name])),
  ]
  for (const path of paths(source.registration)) {
    const args = [...path.slice(1), '--help']
    const reference = await probeCli(args, { env: { CLAUDE_CODE_ENABLE_XAA: '1' } })
    const candidate = await go(args, true)
    expect({ exitCode: candidate.exitCode, stdout: candidate.stdout, stderr: candidate.stderr })
      .toEqual({ exitCode: reference.exitCode, stdout: reference.stdout, stderr: reference.stderr })
  }
}, 90_000)

test('XAA required flags/defaults are checked by the actual TS oracle before handler guards', async () => {
  for (const args of [
    ['mcp', 'xaa', 'setup'], ['mcp', 'xaa', 'setup', '--issuer', 'https://issuer.invalid'],
    ['mcp', 'xaa', 'setup', '--issuer', 'https://issuer.invalid', '--client-id', 'client'],
  ]) {
    const reference = await probeCli(args, { env: { CLAUDE_CODE_ENABLE_XAA: '1' } })
    const candidate = await go(args, true)
    expect({ exitCode: candidate.exitCode, stdout: candidate.stdout, stderr: candidate.stderr, parsed: candidate.parsed })
      .toEqual({ exitCode: reference.exitCode, stdout: reference.stdout, stderr: reference.stderr, parsed: reference.parsed })
  }
}, 30_000)

test('raw parser errors preserve message/exit/guard semantics while retaining the unresolved TS diagnostic stack separately', async () => {
  for (const args of [
    ['--max-budget-usd', '0'], ['--max-budget-usd', 'NaN'],
    ['--task-budget', '-1'], ['--task-budget', '1.5'], ['--task-budget', 'Infinity'],
  ]) {
    const reference = await probeCli(args)
    const candidate = await go(args)
    expect(candidate.exitCode).toBe(reference.exitCode)
    expect(candidate.stdout).toBe(reference.stdout)
    expect(candidate.parsed).toBeNull()
    expect(reference.parsed).toBeNull()
    expect(candidate.diagnostic).toMatchObject({ kind: 'raw-parser-error', stdioComplete: false })
    expect(reference.stderr.split(/\r?\n/).find(line => line.startsWith('error: '))).toBe(candidate.stderr.trimEnd())
    diagnostics.push({ args, reference: { exitCode: reference.exitCode, stdout: reference.stdout, stderr: reference.stderr }, candidate })
  }
}, 40_000)

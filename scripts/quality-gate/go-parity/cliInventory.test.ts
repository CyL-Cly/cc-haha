import { Command, Option } from '@commander-js/extra-typings'
import { beforeAll, expect, test } from 'bun:test'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  assertCliReferenceFeatures, captureCliInventory, cliInventoryContract, inventoryBootstrap, probeCli, snapshotCommand,
} from './cliInventory'

test('snapshots hidden syntax, aliases, registered defaults, negation, choices, and parsers', () => {
  const command = new Command('fixture').alias('f').argument('<input>', 'Input').argument('[rest...]')
    .option('--no-cache').option('-o, --optional [value]', 'Optional', 'seed')
    .addOption(new Option('--private <mode>').choices(['one', 'two']).default('one').hideHelp())
    .option('--item <items...>', 'Items', (value: string, previous: string[]) => [...previous, value], [])
  command.command('hidden', { hidden: true }).alias('h')
  const tree = snapshotCommand(command) as any
  expect(tree.aliases).toEqual(['f'])
  expect(tree.commands[0].hidden).toBe(true)
  expect(tree.commands[0].aliases).toEqual(['h'])
  expect(tree.arguments.map((argument: any) => [argument.name, argument.required, argument.variadic]))
    .toEqual([['input', true, false], ['rest', false, true]])
  expect(tree.options.find((option: any) => option.flags === '--private <mode>'))
    .toMatchObject({ hidden: true, choices: ['one', 'two'], hasDefault: true, default: 'one' })
  expect(tree.options.find((option: any) => option.flags === '--no-cache')).toMatchObject({ negate: true, hasDefault: false })
  expect(tree.options.find((option: any) => option.flags.startsWith('-o'))).toMatchObject({ optional: true, default: 'seed' })
  expect(tree.options.find((option: any) => option.flags.startsWith('--item'))).toMatchObject({ parser: true, variadic: true, default: [] })
})

test('bootstrap inventory evaluates the production build feature set and retains disabled route evidence', () => {
  const routes = inventoryBootstrap(`import { feature } from 'bun:bundle'
    async function main(): Promise<void> {
      const args = process.argv.slice(2)
      if (feature('TRANSCRIPT_CLASSIFIER') && args[0] === '--enabled') await import('./enabled')
      if (feature('DAEMON') && args[0] === '--disabled') { if (args.includes('--nested')) await import('./nested') }
      if (args[0] === '--native') await import('./native')
    }`)
  expect(routes.find(route => route.tokens.includes('--enabled'))).toMatchObject({ enabledByBuild: true, imports: ['./enabled'] })
  expect(routes.find(route => route.tokens.includes('--disabled'))?.enabledByBuild).toBe(false)
  expect(routes.find(route => route.tokens.includes('--nested'))?.enabledByBuild).toBe(false)
  expect(routes.find(route => route.tokens.includes('--native'))).toMatchObject({ enabledByBuild: true, imports: ['./native'] })
  expect(() => inventoryBootstrap('const notMain = 1')).toThrow('main function')
  expect(() => assertCliReferenceFeatures(['TRANSCRIPT_CLASSIFIER'])).not.toThrow()
  expect(() => assertCliReferenceFeatures(['DAEMON'])).toThrow('differs')
  expect(() => assertCliReferenceFeatures([])).toThrow('differs')
  expect(() => assertCliReferenceFeatures(['TRANSCRIPT_CLASSIFIER', 'DAEMON'])).toThrow('differs')
  expect(() => assertCliReferenceFeatures(['TRANSCRIPT_CLASSIFIER', 'TRANSCRIPT_CLASSIFIER'])).toThrow('differs')
  expect(() => assertCliReferenceFeatures('TRANSCRIPT_CLASSIFIER')).toThrow('differs')
  expect(() => assertCliReferenceFeatures([null])).toThrow('differs')
})

let inventory: Awaited<ReturnType<typeof captureCliInventory>>
beforeAll(async () => { inventory = await captureCliInventory() }, 25_000)

test('actual production Commander registration and stdio match the trusted language-neutral fixture', async () => {
  const fixture = JSON.parse(await readFile(new URL('./cli-inventory.fixture.json', import.meta.url), 'utf8'))
  expect(cliInventoryContract(inventory)).toEqual(fixture)
  expect(inventory.profiles.normal.options.length).toBe(81)
  expect(inventory.profiles.normal).toEqual(inventory.profiles.bare)
  expect(inventory.profiles.print.commands).toEqual([])
  expect(inventory.profiles.print.options).toEqual(inventory.profiles.normal.options)
  expect(inventory.profiles.xaa.commands.find((command: any) => command.name === 'mcp').commands.some((command: any) => command.name === 'xaa')).toBe(true)
  expect(inventory.observations.unknownOption.exitCode).toBe(1)
  expect(inventory.observations.versionUpper).toMatchObject({ exitCode: 0, stdout: '999.0.0-local (Claude Code)\n', stderr: '' })
})

test('actual TS registration self replay is stable before any handler executes', async () => {
  const repeat = await probeCli(['--help'], { mode: 'tree' })
  expect(repeat.exitCode).toBe(0)
  expect(repeat.tree).toEqual(inventory.profiles.normal)
  expect(repeat.blocked.every(operation => operation.startsWith('subprocess:'))).toBe(true)
  const action = await probeCli(['-p', 'must never reach a model'])
  expect(action.exitCode).toBe(86)
  expect(action.tree.commands).toEqual([])
  expect(action.stdout).toBe('')
  expect(action.stderr).toBe('')
  const literal = await probeCli(['--', '--inventory-literal'])
  expect(literal.exitCode).toBe(86)
  expect(literal.parsed).toMatchObject({ args: ['--inventory-literal'], processedArgs: ['--inventory-literal'] })
  const group = await probeCli(['mcp'])
  expect(group.exitCode).toBe(1)
  expect(group.stderr).toContain('help [command]')
  expect(group.parsed).toBeNull()
}, 15_000)

test('child guard denies network, native processes, and reads/writes outside the disposable home', async () => {
  const temporary = await mkdtemp(join(tmpdir(), 'cli-inventory-guard-regression-'))
  try {
    const protectedPath = join(temporary, 'protected.json')
    const entrypoint = join(temporary, 'guard.ts')
    await writeFile(protectedPath, 'must remain unchanged')
    await writeFile(entrypoint, `
      import fs from 'node:fs'
      import { execFile } from 'node:child_process'
      import { connect } from 'node:net'
      const target = ${JSON.stringify(protectedPath)}
      for (const operation of [
        () => fetch('https://must-not-resolve.invalid'),
        () => connect(443, 'must-not-resolve.invalid'),
        () => Bun.spawn(['security', 'find-generic-password']),
        () => fs.readFileSync(target),
        () => fs.writeFileSync(target, 'unsafe'),
        () => fs.createWriteStream(target),
        () => fs.promises.readFile(target),
        () => fs.promises.writeFile(target, 'unsafe'),
        () => Bun.file(target),
        () => Bun.write(target, 'unsafe'),
      ]) {
        try { await operation(); throw new Error('guard failed open') }
        catch (error) { if (!error.message.startsWith('CLI inventory blocked ')) throw error }
      }
      await new Promise(resolve => fs.writeFile(target, 'unsafe', error => {
        if (!error?.message.startsWith('CLI inventory blocked ')) throw new Error('callback filesystem guard failed open')
        resolve()
      }))
      await new Promise(resolve => execFile('security', ['find-generic-password'], error => {
        if (error?.code !== 'ENOENT') throw new Error('subprocess guard failed open')
        resolve()
      }))
      fs.writeFileSync(process.env.HOME + '/safe.json', 'sandbox write works')
      console.log('isolated')
    `)
    const result = await probeCli([], { entrypoint })
    expect(result.exitCode).toBe(0)
    expect(result.stdout).toBe('isolated\n')
    expect(result.stderr).toBe('')
    expect(result.blocked).toEqual([
      'network:fetch', 'network:node:net:connect', 'native:spawn',
      'filesystem:readFileSync', 'filesystem:writeFileSync', 'filesystem:createWriteStream',
      'filesystem:readFile', 'filesystem:writeFile', 'filesystem:Bun.file', 'filesystem:Bun.write',
      'filesystem:writeFile', 'subprocess:execFile:security',
    ])
    expect(await readFile(protectedPath, 'utf8')).toBe('must remain unchanged')
    await expect(probeCli(['--help'], { env: { HOME: '/must-not-use' } })).rejects.toThrow('Unsupported')
  } finally { await rm(temporary, { recursive: true, force: true }) }
}, 15_000)

test('caller-owned oracle homes stay private, reject repository/shared roots, and are retained for differential file checks', async () => {
  const home = await mkdtemp(join(tmpdir(), 'cli-inventory-owned-home-'))
  try {
    const preserved = join(home, 'caller-owned.txt')
    await writeFile(preserved, 'must stay available after a probe')
    await expect(probeCli([], { home: fileURLToPath(new URL('../../../', import.meta.url)) })).rejects.toThrow('real disposable temporary HOME')
    await expect(probeCli([], { home: '../relative-home' })).rejects.toThrow('absolute disposable temporary HOME')
    await expect(probeCli(['impossible\0OSargv'], { home })).rejects.toThrow('NUL-free strings')
    const result = await probeCli(['--help'], { home, mode: 'tree' })
    expect(result.exitCode).toBe(0)
    expect(await readFile(preserved, 'utf8')).toBe('must stay available after a probe')
  } finally { await rm(home, { recursive: true, force: true }) }
}, 15_000)

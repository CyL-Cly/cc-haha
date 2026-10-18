import { afterEach, expect, spyOn, test } from 'bun:test'
import { existsSync, mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve, sep } from 'node:path'
import { prepareGoBackend } from './goBackend'
import { BACKEND_TARGET_ENV } from './backendLaunch'
import { goPersistenceReceipts, needsGoPersistence, requiredGoPersistenceTests, runGoPersistence } from './go-persistence'

const directories: string[] = []
afterEach(() => { for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true }) })

test('a Source target cannot make the Go Agent gate pass without exercising Go', async () => {
  const root = mkdtempSync(join(tmpdir(), 'go-lane-target-'))
  directories.push(root)
  const executable = join(root, 'native runtime')
  writeFileSync(executable, 'fixture', { mode: 0o700 })
  await expect(prepareGoBackend(root, join(root, 'artifacts'), {
    [BACKEND_TARGET_ENV]: JSON.stringify({ kind: 'typescript', command: executable }),
  })).rejects.toThrow('requires a Go backend target')
  const result = await prepareGoBackend(root, join(root, 'artifacts'), {
    [BACKEND_TARGET_ENV]: JSON.stringify({ kind: 'go', command: executable, args: ['server'] }),
  })
  expect(result.target.command).toBe(executable)
  expect(result.target.args).toEqual(['server'])
  result.cleanup()
})

test('an explicit Go target cannot replace the guarded home or host/port', async () => {
  const root = mkdtempSync(join(tmpdir(), 'go-lane-guard-'))
  directories.push(root)
  const executable = join(root, 'native')
  writeFileSync(executable, 'fixture', { mode: 0o700 })
  for (const extra of [{ env: { HOME: '/real-user' } }, { args: ['server', '--port', '1234'] }]) {
    await expect(prepareGoBackend(root, join(root, 'artifacts'), {
      [BACKEND_TARGET_ENV]: JSON.stringify({ kind: 'go', command: executable, ...extra }),
    })).rejects.toThrow()
  }
})

test('native persistence is selected for actual Go changes without requiring Go for documentation or renderer-only changes', () => {
  expect(needsGoPersistence(['backend/internal/whatsappcompat/device_state.go'])).toBe(true)
  expect(needsGoPersistence(['backend/internal/whatsappcompat/device_appstate_contract_test.go'])).toBe(true)
  expect(needsGoPersistence(['backend/go.mod'])).toBe(true)
  expect(needsGoPersistence(['scripts/quality-gate/go-persistence.ts'])).toBe(true)
  expect(needsGoPersistence(['scripts/quality-gate/persistence-upgrade.ts'])).toBe(true)
  expect(needsGoPersistence(['backend/internal/whatsappcompat/testdata/node_appstate_persistence.fixture.ts'])).toBe(true)
  expect(needsGoPersistence(['backend/internal/sessionmetadata/testdata/old-session.json'])).toBe(true)
  expect(needsGoPersistence(['README.md', 'docs/internals/contributing.md', 'backend/AGENTS.md'])).toBe(false)
  expect(needsGoPersistence(['desktop/src/pages/Settings.tsx', 'desktop/src/lib/persistenceMigrations.ts'])).toBe(false)
})

test('native persistence requires every named physical test receipt and rejects skipped or unexecuted Source cases', () => {
  const pkg = 'github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat'
  const receipt = (test: string, action = 'pass') => JSON.stringify({ Action: action, Package: pkg, Test: test })
  const packageReceipt = JSON.stringify({ Action: 'pass', Package: pkg })
  const good = [...requiredGoPersistenceTests.map(name => receipt(name)), packageReceipt].join('\n')
  expect(goPersistenceReceipts(good).passedTests).toBe(requiredGoPersistenceTests.length)
  expect(() => goPersistenceReceipts([...requiredGoPersistenceTests.slice(1).map(name => receipt(name)), packageReceipt].join('\n'))).toThrow('lack actual passing receipts')
  expect(() => goPersistenceReceipts([receipt(requiredGoPersistenceTests[0], 'skip'), ...requiredGoPersistenceTests.slice(1).map(name => receipt(name)), packageReceipt].join('\n'))).toThrow('skipped')
  expect(() => goPersistenceReceipts([receipt(`${requiredGoPersistenceTests[1]}/old-json-fixture`, 'skip'), good].join('\n'))).toThrow('skipped')
  expect(() => goPersistenceReceipts([receipt(requiredGoPersistenceTests[0], 'fail'), packageReceipt].join('\n'))).toThrow('Go test failed')
  expect(() => goPersistenceReceipts(packageReceipt)).toThrow('no passing tests')
})

test('persistence forwards its declared artifact root under a narrow lane TMPDIR without changing the test window', async () => {
  const artifacts = resolve(import.meta.dir, '../../artifacts')
  mkdirSync(artifacts, { recursive: true })
  const root = mkdtempSync(join(artifacts, 'persistence-poppler-root-regression-'))
  const bin = join(root, 'artifacts/poppler/bin')
  const lane = mkdtempSync(join(tmpdir(), 'persistence-poppler-narrow-temp-'))
  mkdirSync(join(root, 'backend'), { recursive: true })
  mkdirSync(bin, { recursive: true })
  writeFileSync(join(root, 'backend/go.mod'), 'module fixture.test/persistence-root\n\ngo 1.25.7\n')
  writeFileSync(join(root, 'package.json'), JSON.stringify({ packageManager: `bun@${process.versions.bun}` }))
  const previous = Object.fromEntries(['TMPDIR', 'TMP', 'TEMP'].map(key => [key, process.env[key]]))
  const commands: string[][] = []
  let home = ''
  const spawn = spyOn(Bun, 'spawn').mockImplementation(((command: string[], options: { env: Record<string, string>; cwd: string }) => {
    commands.push(command)
    home = options.env.HOME
    expect(options.cwd).toBe(join(root, 'backend'))
    expect(realpathSync(home).startsWith(realpathSync(lane) + sep)).toBe(true)
    expect(options.env.CC_HAHA_TEST_POPPLER_BIN).toBe(realpathSync(bin))
    expect(options.env.ANTHROPIC_API_KEY).toBeUndefined()
    expect(options.env.GOPROXY).toBe('off')
    let output = ''
    if (command[1] === 'mod') output = JSON.stringify({ Require: [] })
    else {
      expect(command[1]).toBe('test')
      expect(command).toContain('-timeout=45s')
      const pkg = 'github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat'
      output = [...requiredGoPersistenceTests.map(Test => ({ Action: 'pass', Package: pkg, Test })), { Action: 'pass', Package: pkg }].map(value => JSON.stringify(value)).join('\n')
    }
    // Fake the process primitive only. The real environment, hydration checks,
    // persistence command construction and receipt validator still execute.
    return { exited: Promise.resolve(0), stdout: new Response(output).body, stderr: new Response('').body }
  }) as any)
  try {
    Object.assign(process.env, { TMPDIR: lane, TMP: lane, TEMP: lane })
    const result = await runGoPersistence(root, { CC_HAHA_TEST_POPPLER_BIN: bin, ANTHROPIC_API_KEY: 'must-not-leak' })
    expect(result.passedTests).toBe(requiredGoPersistenceTests.length)
    expect(commands.map(command => command[1])).toEqual(['mod', 'test'])
    expect(existsSync(home)).toBe(false)
  } finally {
    spawn.mockRestore()
    for (const [key, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[key]
      else process.env[key] = value
    }
    rmSync(root, { recursive: true, force: true })
    rmSync(lane, { recursive: true, force: true })
  }
})

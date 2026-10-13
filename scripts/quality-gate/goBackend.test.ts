import { afterEach, expect, test } from 'bun:test'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { prepareGoBackend } from './goBackend'
import { BACKEND_TARGET_ENV } from './backendLaunch'
import { goPersistenceReceipts, needsGoPersistence, requiredGoPersistenceTests } from './go-persistence'

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

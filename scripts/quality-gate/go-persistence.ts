import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { hydrateGoDependencies } from '../pr/go-dependencies'
import { collectGoTestEvidence, createGoTestEnvironment } from '../pr/run-go-tests'

const target = 'github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat'
export const requiredGoPersistenceTests = [
  'TestActualOriginalAppStateKeyColdMigrationAndVersionPreservesFutureFields',
  'TestNativeAuthAppStateKeyOldJSONRepresentationsUpgradeWithoutGlobalMarker',
  'TestAppStateMalformedFingerprintAndVersionAreRejectedWithoutCommit',
  'TestActualOriginalSenderKeyColdAuthOutOfOrderNativeForwardAndReplay',
  'TestActualOriginalAuthBackupColdRecoveryAndQueuedSavePreserveFutureFields',
] as const

export function needsGoPersistence(files: string[]): boolean {
  return files.some(value => {
    const file = value.replaceAll('\\', '/').replace(/^\.\//, '')
    return file.startsWith('backend/') && (file.endsWith('.go') || file.endsWith('/go.mod') || file.endsWith('/go.sum')
      || file.endsWith('.fixture.ts') || file.includes('/testdata/') && !file.endsWith('.md'))
      || file.startsWith('scripts/quality-gate/go-persistence')
      || file.startsWith('scripts/quality-gate/persistence-upgrade')
  })
}

export function goPersistenceReceipts(stdout: string) {
  const evidence = collectGoTestEvidence(stdout, [target])
  const required = new Set<string>(requiredGoPersistenceTests)
  for (const line of stdout.split(/\r?\n/).filter(Boolean)) {
    const event = JSON.parse(line) as { Action?: string; Package?: string; Test?: string }
    if (event.Package === target && event.Test) {
      const expected = requiredGoPersistenceTests.some(name => event.Test === name || event.Test!.startsWith(`${name}/`))
      if (expected && event.Action === 'skip') throw new Error(`Native persistence case skipped: ${event.Test}`)
      if (event.Action === 'pass') required.delete(event.Test)
    }
  }
  if (required.size) throw new Error(`Native persistence cases lack actual passing receipts: ${[...required].join(', ')}`)
  return evidence
}

export async function runGoPersistence(root: string, sourceEnvironment: NodeJS.ProcessEnv = process.env) {
  const pinned = (JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')) as { packageManager?: string }).packageManager
  if (pinned !== `bun@${process.versions.bun}`) throw new Error(`Native persistence Source must use the repository pinned Bun ${pinned ?? 'missing'}, got ${process.versions.bun}`)
  const home = mkdtempSync(join(tmpdir(), 'go-persistence-upgrade-'))
  const env = createGoTestEnvironment(home, sourceEnvironment, root)
  env.CC_HAHA_SOURCE_BUN = process.execPath
  env.CC_HAHA_PINNED_BUN = process.execPath
  try {
    await hydrateGoDependencies(root, env, { sourceEnvironment })
    const command = ['go', 'test', '-json', '-count=1', '-timeout=45s', '-run', `^(?:${requiredGoPersistenceTests.join('|')})$`, './internal/whatsappcompat']
    const child = Bun.spawn(command, { cwd: join(root, 'backend'), env, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
    const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    if (exitCode !== 0) throw new Error(`Native persistence tests exited ${exitCode}: ${stderr || stdout}`)
    const evidence = goPersistenceReceipts(stdout)
    console.log(`[persistence-upgrade] native Go: ${evidence.passedTests} actual passing tests; Source Bun ${process.versions.bun}; no required-case skips`)
    return evidence
  } finally { rmSync(home, { recursive: true, force: true }) }
}

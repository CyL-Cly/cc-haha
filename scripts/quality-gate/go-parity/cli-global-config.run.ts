import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

// The parent also receives a disposable HOME/config and filtered environment.
// Only an explicit command argument enables a Source-only/capture operation.
const operation = process.argv[2] ?? 'verify'
if (!['verify', 'source', 'capture'].includes(operation)) throw new Error('Expected verify, source, or capture')
const home = await mkdtemp(join(tmpdir(), 'cli-global-config-parent-'))
try {
  const environment = createSandboxedTestEnvironment(home, {
    ...(operation === 'source' || operation === 'capture' ? { CC_HAHA_GLOBAL_CONFIG_SOURCE_ONLY: '1' } : {}),
    ...(operation === 'capture' ? { CC_HAHA_CAPTURE_GLOBAL_CONFIG: '1' } : {}),
  })
  const child = Bun.spawn([process.execPath, '--no-env-file', 'test', 'scripts/quality-gate/go-parity/cli-global-config.test.ts'], {
    cwd: resolve(import.meta.dir, '../../..'), env: environment, stdin: 'ignore', stdout: 'inherit', stderr: 'inherit',
  })
  process.exitCode = await child.exited
} finally {
  await rm(home, { recursive: true, force: true })
}

import { expect, test } from 'bun:test'
import { mkdtemp, realpath, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

const root = fileURLToPath(new URL('../../../', import.meta.url))
for (const mode of ['success', 'basename', 'outside-grant', 'directory-grant', 'foreign-path', 'wrong-home', 'wrong-cwd', 'wrong-args', 'shell', 'replace-symlink']) {
  test(`private executable fixture capability: ${mode}`, async () => {
    const home = await realpath(await mkdtemp(join(tmpdir(), 'client-private-executable-')))
    try {
      const child = Bun.spawn([process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/client-private-executable.fixture.ts')], {
        cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir() }),
        stdin: new Blob([JSON.stringify({ homeDir: home, mode })]), stdout: 'pipe', stderr: 'pipe',
      })
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, stderr).toBe(0)
      expect(stderr).toBe('')
      const result = JSON.parse(stdout)
      if (mode === 'success' || mode === 'basename') expect(result).toEqual({ code: 0, stdout: 'private-ok', blocked: [] })
      else if (mode.endsWith('-grant')) expect(result.error).toContain('requires an exact executable inside its disposable home')
      else { expect(result.error).toContain('Client options fixture blocked subprocess:spawn:'); expect(result.blocked).toHaveLength(1); expect(result.stdout).toBeUndefined() }
    } finally { await rm(home, { recursive: true, force: true }) }
  })
}

import { expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

const root = fileURLToPath(new URL('../../../', import.meta.url)), allocationRoot = tmpdir()
for (const mode of ['private', 'unknown', 'reused', 'readonly', 'symlink']) {
  test(`client fixture descriptor provenance: ${mode}`, async () => {
    const parent = await mkdtemp(join(allocationRoot, 'client-descriptor-'))
    let child: ReturnType<typeof Bun.spawn> | undefined
    try {
      const home = join(parent, 'home'), outside = join(parent, 'outside.txt')
      await mkdir(home)
      await writeFile(outside, 'protected fixture sentinel')
      await writeFile(join(home, 'transcript.jsonl'), '')
      if (mode === 'symlink') await symlink(outside, join(home, 'alias'))
      child = Bun.spawn([process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/client-descriptors.fixture.ts')], {
        cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_CLIENT_DESCRIPTORS_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: allocationRoot }),
        stdin: new Blob([JSON.stringify({ homeDir: home, outside, mode })]), stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
      try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
        if (mode === 'private') {
          expect(code, stderr).toBe(0)
          expect(stderr).toBe('')
          expect(JSON.parse(stdout)).toEqual({ text: 'first\nsecond\nthird\n', capabilities: { blocked: [], secureReads: [], absentReads: [] } })
        } else {
          expect(code).toBe(1)
          expect(stdout).toBe('')
          expect(stderr).toContain('Client options fixture blocked filesystem:' + (mode === 'symlink' ? 'openSync:' : 'appendFileSync:'))
        }
        expect(await readFile(outside, 'utf8')).toBe('protected fixture sentinel')
        expect(await readFile(join(home, 'transcript.jsonl'), 'utf8')).toBe(mode === 'private' ? 'first\nsecond\nthird\n' : '')
      } finally { clearTimeout(timer) }
    } finally { child?.kill(); if (child) await child.exited; await rm(parent, { recursive: true, force: true }) }
  })
}

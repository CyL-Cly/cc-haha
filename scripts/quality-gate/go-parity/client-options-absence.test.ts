import { expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, realpath, rm, symlink, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
test('client guard exact absent string/Buffer/fileURL preserve denied foreign and symlink paths', async () => {
  const parent = await realpath(await mkdtemp(join(tmpdir(), 'client-absence-')))
  const home = join(parent, 'home'), sentinel = join(parent, 'outside-sentinel'), absent = join(parent, 'declared-absent')
  try {
    await mkdir(home)
    await writeFile(sentinel, 'OWNED_SENTINEL')
    await symlink(sentinel, join(home, 'outside-link'))
    const child = Bun.spawn([process.execPath, '--no-env-file', join(import.meta.dir, 'client-options-absence.fixture.ts')], { cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_ABSENCE_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: parent }), stdin: new Blob([JSON.stringify({ home, sentinel, absent })]), stdout: 'pipe', stderr: 'pipe' })
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, stderr).toBe(0)
    expect(stderr).toBe('')
    const result = JSON.parse(stdout)
    expect(result.observations.slice(0, 3).map((row: any) => [row.name, row.success, row.code])).toEqual([['declared-string', false, 'ENOENT'], ['declared-buffer', false, 'ENOENT'], ['declared-file-url', false, 'ENOENT']])
    expect(result.absentReads).toEqual(Array.from({ length: 3 }, () => ({ operation: 'statSync', path: absent })))
    expect(result.observations.slice(3).every((row: any) => !row.success && row.code !== 'ENOENT')).toBe(true)
    expect(result.blocked).toHaveLength(5)
    expect(await readFile(sentinel, 'utf8')).toBe('OWNED_SENTINEL')
  }
  finally { await rm(parent, { recursive: true, force: true }) }
})

import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'plugin manifest/content validation')
if (process.env.CC_HAHA_PLUGIN_VALIDATE_FIXTURE !== '1') throw new Error('Unowned plugin validation fixture')
await import('../../preload')
const input = await Bun.stdin.json()
const original = await import('../../src/utils/plugins/validatePlugin')
if (input.schemaCases) {
  const { PluginManifestSchema } = await import('../../src/utils/plugins/schemas')
  process.stdout.write(JSON.stringify(input.schemaCases.map((value: unknown) => {
    const result = PluginManifestSchema().safeParse(value)
    return result.success ? [] : result.error.issues
  })) + '\n')
  process.exit(0)
}
const rows = []
for (const path of input.paths) {
  try {
    const manifest = await original.validateManifest(path)
    const { basename, dirname } = await import('node:path')
    const contents = manifest.fileType === 'plugin' && basename(dirname(manifest.filePath)) === '.claude-plugin'
      ? await original.validatePluginContents(dirname(dirname(manifest.filePath))) : []
    rows.push({ manifest, contents })
  } catch (error: any) { rows.push({ error: error.message }) }
}
process.stdout.write(JSON.stringify(rows) + '\n')

import { assertDisposableFixtureHome } from './fixture-guard'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'cc-switch original scanner')
await import('../../../preload')
Bun.plugin({ name: 'cc-switch-original-mapping', setup(build) {
  build.onLoad({ filter: /\/ccSwitchImport\.ts$/ }, async args => ({ contents: `${await Bun.file(args.path).text()}\nexport { toEntry, toLegacyRows, dedupeAcrossApps }`, loader: 'ts' }))
} })
const original: any = await import('../../../src/server/services/ccSwitchImport')
const output = []
for (const row of input.cases) {
  if (row.action === 'mapping') output.push(original.toEntry(row.row, { currentClaudeProviderId: row.current, existingProviders: row.existing ?? [] }))
  if (row.action === 'legacy') output.push(original.dedupeAcrossApps(original.toLegacyRows(row.config).map((value: any) => original.toEntry(value, { currentClaudeProviderId: row.current, existingProviders: row.existing ?? [] }))))
}
if (input.scan) {
  const value = await original.readCcSwitchProviders({ existingProviders: input.existing ?? [] })
  process.stdout.write(JSON.stringify({ scan: original.toCcSwitchScanResult(value), imports: original.resolveCcSwitchImports(value, input.ids ?? []), entries: value.entries }))
} else process.stdout.write(JSON.stringify(output))

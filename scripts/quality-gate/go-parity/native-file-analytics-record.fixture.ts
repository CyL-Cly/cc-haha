import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Analytics event record Source comparison')
const input = await Bun.stdin.json()
await import('../../../preload')
const { FirstPartyEventLoggingExporter } = await import('../../../src/services/analytics/firstPartyEventLoggingExporter')
// Constructor side effects are outside this pure serializer comparison. The
// method itself is the actual production proto formatter, not a test copy.
const exporter = Object.create(FirstPartyEventLoggingExporter.prototype)
const results = input.cases.map((row: any) => exporter.transformLogsToEvents([{
  body: row.event.name,
  hrTime: [Math.floor(row.timestamp / 1000), (row.timestamp % 1000) * 1_000_000],
  attributes: {
    event_name: row.event.name, event_id: row.id, user_id: row.userID || undefined,
    core_metadata: row.core, user_metadata: row.user, event_metadata: row.event.metadata,
  },
}]).events[0])
process.stdout.write(JSON.stringify(results))

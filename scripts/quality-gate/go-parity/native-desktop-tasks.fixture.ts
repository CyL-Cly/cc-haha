import { assertDisposableFixtureHome } from './fixture-guard'

const home = process.env.HOME!
assertDisposableFixtureHome(home, 'Desktop TaskService parity')
if (process.env.CC_HAHA_DESKTOP_TASKS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') {
  throw new Error('Desktop TaskService parity requires the isolated production fixture')
}

// Only the original file reader is loaded. No model, saved provider, user
// settings or runtime loop is substituted for this desktop projection.
const { TaskService } = await import('../../../src/server/services/taskService')
const service = new TaskService()
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { lists: string[] }
const lists = await service.listTaskLists()
const tasks = await service.listTasks()
const byList = Object.fromEntries(await Promise.all(input.lists.map(async id => [id, await service.getTasksForList(id)])))
process.stdout.write(JSON.stringify({ lists, tasks, byList }))

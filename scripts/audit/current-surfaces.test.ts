import { expect, test } from 'bun:test'
import { inspectTS } from './current-surfaces.mjs'

test('keeps method, exported consumer symbol and query path without executing an API', () => {
  const result = inspectTS('desktop/src/api/skills.ts', `
    import { api } from './client'
    const base = '/api/skills'
    export const skillsApi = {
      list: () => api.get(base),
      detail: (name: string) => api.get(\`\${base}/detail?name=\${name}\`),
      remove: (name: string) => api.delete(\`\${base}/\${name}\`),
    }
  `)
  expect(result.http.map(call => [call.method, call.path, call.operation])).toEqual([
    ['GET', '/api/skills', 'skillsApi.list'],
    ['GET', '/api/skills/detail?name=[name]', 'skillsApi.detail'],
    ['DELETE', '/api/skills/[name]', 'skillsApi.remove'],
  ])
})

test('resolves Source path-builder parameters and nested function calls structurally', () => {
  const result = inspectTS('desktop/src/api/trajectory.ts', `
    function sessionPath(id: string) { return \`/api/sessions/\${id}/trajectory\` }
    function reviewPath(id: string, suffix = '') { return \`/api/sessions/\${id}/review\${suffix}\` }
    export const trajectoryApi = {
      row: (id: string, row: string) => api.get(\`\${sessionPath(id)}/rows/\${row}\`),
      revision: (id: string) => api.get(reviewPath(id, '/revision')),
      status: (id: string) => api.get(reviewPath(id)),
    }
  `)
  expect(result.http.map(call => call.path)).toEqual([
    '/api/sessions/[id]/trajectory/rows/[row]',
    '/api/sessions/[id]/review/revision',
    '/api/sessions/[id]/review',
  ])
  expect(result.http.every(call => call.family === 'sessions')).toBe(true)
})

test('retains unresolved dynamic input instead of inventing a route', () => {
  const result = inspectTS('desktop/src/api/dynamic.ts', 'export const client = { call: (dynamic: string) => api.get(dynamic) }')
  expect(result.http[0].path).toBe('[dynamic]')
  expect(result.http[0].pathResolution).toBe('unresolved')
  expect(result.http[0].family).toBeNull()
})

test('matches IPC constants, registerHandler and bridge call without treating comments as code', () => {
  const result = inspectTS('desktop/electron/main.ts', `
    export const ELECTRON_IPC_CHANNELS = { settings: 'desktop:settings' } as const
    registerHandler(ELECTRON_IPC_CHANNELS.settings, () => service.read())
    bridge.invoke(ELECTRON_IPC_CHANNELS.settings)
    // registerHandler(ELECTRON_IPC_CHANNELS.missing, () => 'fake')
  `)
  expect(result.constants).toHaveLength(1)
  expect(result.ipc.map(call => [call.call, call.channelKey, call.handler !== null])).toEqual([
    ['registerHandler', 'ELECTRON_IPC_CHANNELS.settings', true],
    ['bridge.invoke', 'ELECTRON_IPC_CHANNELS.settings', false],
  ])
})

test('captures actual socket discriminator and special file URL producers', () => {
  const result = inspectTS('desktop/src/api/websocket.ts', `
    socket.send({ type: 'stop', sessionId: id })
    const path = \`/preview-fs/\${id}/\${filename}\`
    fetch(getApiUrl('/api/voice/transcribe'), { method: 'POST' })
  `)
  expect(result.socket[0].type).toBe('stop')
  expect(result.specialURLs[0].path).toBe('/preview-fs/[id]/[filename]')
  expect(result.http[0].method).toBe('POST')
  expect(result.http[0].path).toBe('/api/voice/transcribe')
})

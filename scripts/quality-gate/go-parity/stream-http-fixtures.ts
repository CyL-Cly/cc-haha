import { streamHistoryScenarios } from './stream-history-fixtures'

// The entire established envelope corpus now traverses an actual HTTP socket.
// These cases end with clean transport EOF or Source controller errors. Network
// resets, SDK JSON/SSE errors, retry/watchdog and caller abort remain separate.
export const streamHttpScenarios = streamHistoryScenarios

export const streamHttpHeaderNames = [
  'accept', 'content-type', 'anthropic-version', 'x-api-key', 'authorization',
  'anthropic-beta', 'x-stainless-helper',
] as const

export type StreamHTTPRequest = {
  method: string
  path: string
  bodyBytes: string
  headers: Record<string, string | null>
}

export type StreamHTTPPreparedRequest = {
  path: string
  bodyBytes: string
  headers: Record<string, string>
}

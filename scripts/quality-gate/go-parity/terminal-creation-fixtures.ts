export type TerminalError = { kind: 'connection' | 'timeout' | 'error' | 'plain' | 'abort' | 'api'; message?: string; code?: unknown; cause?: TerminalError; selfCause?: boolean; status?: number }
export type TerminalCase = { name: string; error?: TerminalError; factoryOptions?: Record<string, unknown>; priorUUIDs?: number; wrapRetry?: boolean; supported?: boolean }
export const terminalOriginalEnv = { ANTHROPIC_BASE_URL: 'https://127.0.0.1:1', ANTHROPIC_API_KEY: 'offline-original-env-key', CLAUDE_CODE_DISABLE_FAST_MODE: '1' }
const connection = (code?: unknown): TerminalError => ({ kind: 'connection', cause: code === undefined ? undefined : { kind: 'error', code, message: 'original private socket diagnostic' } })
const chain = (error: TerminalError, count: number): TerminalError => count ? { kind: 'error', message: 'original outer cause', cause: chain(error, count - 1) } : error
const sslCodes = ['UNABLE_TO_VERIFY_LEAF_SIGNATURE', 'UNABLE_TO_GET_ISSUER_CERT', 'UNABLE_TO_GET_ISSUER_CERT_LOCALLY', 'CERT_SIGNATURE_FAILURE', 'CERT_NOT_YET_VALID', 'CERT_HAS_EXPIRED', 'CERT_REVOKED', 'CERT_REJECTED', 'CERT_UNTRUSTED', 'DEPTH_ZERO_SELF_SIGNED_CERT', 'SELF_SIGNED_CERT_IN_CHAIN', 'CERT_CHAIN_TOO_LONG', 'PATH_LENGTH_EXCEEDED', 'ERR_TLS_CERT_ALTNAME_INVALID', 'HOSTNAME_MISMATCH', 'ERR_TLS_HANDSHAKE_TIMEOUT', 'ERR_SSL_WRONG_VERSION_NUMBER', 'ERR_SSL_DECRYPTION_FAILED_OR_BAD_RECORD_MAC']
export const terminalCreationCases: TerminalCase[] = [
  ...['handshake-timeout', 'malformed-timeout-zero', 'malformed-timeout-one', 'large-malformed-timeout-one'].map(name => ({ name: 'original creation ' + name, error: { kind: 'timeout' as const }, priorUUIDs: 2, wrapRetry: true })),
  { name: 'original creation malformed', error: connection('FailedToOpenSocket'), priorUUIDs: 2, wrapRetry: true },
  ...['wrong', 'missing', 'default'].map(name => ({ name: 'original creation ' + name, error: connection('DEPTH_ZERO_SELF_SIGNED_CERT'), priorUUIDs: 2, wrapRetry: true })),
  ...sslCodes.map(code => ({ name: 'original SSL code ' + code, error: connection(code) })),
  ...['ETIMEDOUT', '', 'UnknownOriginalCode', '\ud800', '\udc00', '\ufffd'].map(code => ({ name: 'original nonSSL code ' + JSON.stringify(code), error: connection(code) })),
  { name: 'no original cause', error: connection() },
  { name: 'first code wins', error: { kind: 'connection', cause: { kind: 'error', code: 'FailedToOpenSocket', cause: { kind: 'error', code: 'CERT_HAS_EXPIRED' } } } },
  { name: 'fifth Error link accepted', error: { kind: 'connection', cause: chain({ kind: 'error', code: 'CERT_HAS_EXPIRED' }, 3) } },
  { name: 'sixth Error link ignored', error: { kind: 'connection', cause: chain({ kind: 'error', code: 'CERT_HAS_EXPIRED' }, 4) } },
  { name: 'non-string code continues', error: { kind: 'connection', cause: { kind: 'error', code: 123, cause: { kind: 'error', code: 'CERT_REVOKED' } } } },
  { name: 'plain object code ignored', error: { kind: 'connection', cause: { kind: 'plain', code: 'CERT_HAS_EXPIRED', cause: { kind: 'error', code: 'CERT_REVOKED' } } } },
  { name: 'self cause without code', error: { kind: 'connection', cause: { kind: 'error', selfCause: true } } },
  { name: 'self cause with code', error: { kind: 'connection', cause: { kind: 'error', code: 'CERT_HAS_EXPIRED', selfCause: true } } },
  { name: 'ASCII custom timeout precedence', error: { kind: 'connection', message: 'Original TIMEOUT diagnostic', cause: { kind: 'error', code: 'CERT_HAS_EXPIRED' } } },
  { name: 'timeout subclass custom UTF16 ignores cause', error: { kind: 'timeout', message: 'original \ud800 timeout \udc00' } },
  { name: 'factory original UTF16 and explicit null', factoryOptions: { content: 'original \ud800 text \udc00', error: 'unknown', errorDetails: null, usage: { ignored: 1 } } },
  { name: 'factory empty content and all metadata', factoryOptions: { content: '', apiError: 'fixture', error: 'unknown', errorDetails: 'original details', businessErrorCode: 'code', sourceModel: 'deepseek-flash', isVirtual: true } },
  { name: 'pending custom dotted-I lowercase', error: { kind: 'connection', message: 'Original TİMEOUT diagnostic' }, supported: false },
  { name: 'pending custom Unicode message', error: { kind: 'connection', message: 'original \ud800 diagnostic' }, supported: false },
  { name: 'pending non-timeout custom message precedence', error: { kind: 'connection', message: 'Original custom diagnostic' }, supported: false },
  { name: 'caller owns abort suppression', error: { kind: 'abort' }, supported: false },
  { name: 'pending HTTP API presentation', error: { kind: 'api', status: 503, message: 'Original HTTP error' }, supported: false },
  { name: 'pending ordinary Error presentation', error: { kind: 'error', message: 'original generic diagnostic' }, supported: false },
]

// Reuse the real production query/SDK/history oracle and its pre-import guards.
// Its HTTP profile forwards fetch to the exact parent-owned loopback origin;
// it does not return a fabricated Response or replace the SDK/parser.
await import('./stream-history.fixture')

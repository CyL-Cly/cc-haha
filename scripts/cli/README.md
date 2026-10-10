# Go CLI entry and installation

`bin/claude-haha` executes the native Go CLI directly on macOS and Linux. The npm entry, `scripts/cli/launch.cjs`, locates the same binary on macOS, Linux and Windows. It forwards arguments, standard input/output and process signals; CLI, TUI and server execution belong to Go. Neither entry falls back to the TypeScript CLI when the binary is missing.

From a source checkout, use Go 1.25.7 and Bun 1.3.14 to prepare dependencies and build:

```sh
bun run prepare:go
bun run build:cli
./bin/claude-haha --version
```

Dependency preparation is the explicit network step. Building uses a verified dependency seed with networking disabled, a disposable private compiler HOME, and physically disabled Go telemetry. An existing seed can be selected with `CC_HAHA_GO_DEPENDENCY_SEED=/absolute/path`.

Build artifacts are platform specific, for example `bin/native/claude-haha-darwin-arm64` or `bin/native/claude-haha-windows-amd64.exe`. To build another supported target:

```sh
bun run build:cli -- --target windows/amd64
```

The six supported build targets are macOS, Linux and Windows, each with amd64 and arm64. Cross-compilation does not establish that terminal interactions pass on the target OS.

Install the native binary into an explicit directory:

```sh
bun run install:cli -- --prefix /absolute/path/to/installation
/absolute/path/to/installation/bin/claude-haha --version
```

On Windows, the installed executable is `bin\claude-haha.exe`. Installed native binaries require neither Node nor Bun. Source builds use Bun as a development tool; npm's packaging launcher requires Node. For a separately built executable, set `CC_HAHA_GO_BINARY` to its absolute path.

Run the deterministic launcher and installation regressions with `bun run test:cli-launch`. These compile a real Go fixture in a private HOME and verify argument, Unicode input, caller directory, exit, signal and installation boundaries. Product `--version` smoke establishes executable wiring only. Full CLI interaction parity, native Windows ConPTY and desktop Agent e2e remain separate migration acceptance requirements.

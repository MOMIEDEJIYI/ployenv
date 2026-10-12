# dsh-plugin-ployenv

DeepSeek Harness (DSH) host plugin that drives [ployenv](../README.md) - the local runtime version
manager for Node / Java / Go / Python - from the chat.

It contributes:

- a human slash command **`/ployenv`**,
- a model-callable tool **`ployenv`** (same actions),

both of which shell out to the ployenv CLI with `node:child_process`, plus a
`DSH_PLOYENV_*` environment fact triple for session shell tools.

Written against **ployenv 0.3.3** (the 0.2.0 session model is unchanged: no shim executables, no shim/root
commands, and nothing is ever written to PATH or the registry). Since 0.3.0 the package is **self-contained** -
it ships a ployenv binary for its platform, so installing the plugin is enough (REQ-003). Since **REQ-003
FR-303** it can also **install the released binary itself** through its own `setup` action (SHA-256 verified,
into `data/ployenv/`); that action is plugin-side and is never passed to the CLI.

## Layout

| File | Role |
|---|---|
| `package.json` | DSH bundle and plugin manifest (`dsh.bundle.patch`) |
| `cordis.patch.yml` | Bundle layer: inserts the `ployenv` Loader row and documents its config |
| `index.js` | The plugin: `export apply(ctx, config)`, no build step, no dependencies |
| `stub-smoke.mjs` | Hermetic contract smoke test (no binary, no subprocess, no network) |

`cordis.patch.yml` is **required** - `package.json` declares it as the bundle's patch, and a package
that declares no `dsh.bundle` installs as a plain dependency instead of an active layer.

## Requirements

> ⚠️ **Windows x64 only for the bundled binary.** The package ships `bin/win32-x64/ployenv.exe` and
> therefore declares `"os": ["win32"]` / `"cpu": ["x64"]`, so **npm refuses to install it on macOS or
> Linux**. There, install the plugin from a local directory instead and point `PLOYENV_BIN` (or
> `PLOYENV_ROOT`) at a ployenv you provide; the resolution order below then finds it.

1. **Node.js >= 20** for the host (DSH already requires this).
2. **The ployenv CLI**, resolved in this order (REQ-003 §3.2); `status` and `bin` always name the
   stage that won, so you can tell which one is in use:
   1. row config `bin` - an explicit choice always wins,
   2. the `PLOYENV_BIN` environment variable of the DSH process,
   3. `$PLOYENV_ROOT\ployenv.exe` when that file exists (reuse a ployenv you installed yourself),
   4. **the copy installed by `setup`**: `data/ployenv/bin/ployenv(.exe)` inside this package - running
      `setup` is a deliberate choice by the operator, so it outranks the bundled copy,
   5. **the bundled copy**: `bin/<platform>-<arch>/ployenv(.exe)` inside this package,
   6. the bare name `ployenv`, resolved through `PATH`.
3. No package manager, no install scripts, no build: the plugin is plain ESM JavaScript.

## Install

Two ways in, both end the same way: pnpm runs inside the target profile directory and the package
name lands in the profile's `dsh.profile.bundles` list.

| Path | Use it when |
|---|---|
| **npm package** `dsh-plugin-ployenv` | you just want the plugin (published package, binary included) |
| **local directory** (Options A-C below) | you are developing the plugin itself |

### Option 0 - from npm (published package)

```powershell
dsh plugin --profile desktop add dsh-plugin-ployenv
```

The published tarball ships the platform binary, so no separate ployenv download is needed
(REQ-003). Quit the Desktop application before touching the reserved `desktop` profile.

### Option A - Web GUI (this machine's Desktop app)

1. Open the sidebar **Plugins** page.
2. Choose **Install**, and give the absolute path of your checkout's `dsh-plugin` directory:

   ```text
   <path to your checkout>\dsh-plugin
   ```

3. Confirm. A local directory install links the checkout, so edits to `index.js` are picked up by
   the next load (a fresh JavaScript module generation needs a restart; see *Caveats*).

### Option B - the agent

Ask the agent to call the `plugin_manager` tool:

```json
{ "action": "install_bundle", "target": "<path to your checkout>\\dsh-plugin" }
```

### Option C - the `dsh` CLI

`dsh` forwards its remaining arguments to pnpm in the profile directory. A throwaway profile keeps
the experiment away from your daily desktop profile:

```powershell
dsh plugin --profile ployenv-test add <path to your checkout>\dsh-plugin
```

`--profile <name>` is required and the profile is initialized on first use. The reserved `desktop`
profile must first be initialized by the Desktop application, and the application must be fully
quit before running package operations against it.

### What success looks like

The profile's `package.json` gains a dependency on `dsh-plugin-ployenv` and lists it in
`dsh.profile.bundles`; the profile's `cordis.patch.yml` is untouched. In the running app the
Plugins page shows the bundle with no load error, and `/ployenv status` answers.

Rollback: remove the bundle from the profile and delete the linked directory under the profile's
`node_modules` (nothing else is touched - the plugin writes no PATH, registry, or config).

## Uninstall

- Web GUI: **Plugins** page -> remove the `dsh-plugin-ployenv` bundle.
- Agent: `plugin_manager` with `{ "action": "remove_bundle", "target": "dsh-plugin-ployenv" }`.
- CLI: `dsh plugin --profile <name> remove dsh-plugin-ployenv`.

Removal deselects the bundle, unloads its runtime contributions, then runs pnpm remove. Nothing else
needs undoing: the plugin never writes PATH, the registry, or any file outside its own logs.

## Configuration

Row config in the profile's own `cordis.patch.yml` overrides the bundle's defaults. The plugin
exports no `Config` schema, so the config object is passed to `apply` unchanged - every field is
optional and unknown fields are ignored.

```yaml
- id: ployenv
  name: "dsh-plugin-ployenv"
  config:
    bin: "D:\\tools\\ployenv.exe"
    commandName: ployenv
    toolName: ployenv
    timeoutMs: 180000
    injectPathAtLoad: false
    args:
      list: ["list"]
      use: ["use", "{runtime}@{version}", "--global"]
      env: ["env", "--shell", "json"]
```

| Field | Default | Meaning |
|---|---|---|
| `bin` | unset | ployenv executable (absolute path or PATH name). Set it to pin your own build; it overrides the bundled copy. |
| `root` | unset | ployenv data root passed to the child as `PLOYENV_ROOT`. When the bundled binary is used the default is `<package>/data/ployenv`, so uninstalling the plugin removes the runtimes with it. Set it to keep data outside the package (it survives plugin updates). |
| `commandName` | `ployenv` | Slash-command name; must match `^[a-z][a-z0-9_-]*$`. |
| `toolName` | `ployenv` | Model tool name. |
| `timeoutMs` | `120000` | Per-invocation deadline. Install downloads can exceed it; raise it if needed. |
| `injectPathAtLoad` | `false` | Prepend the `env --shell json` path list to the harness process `PATH` as soon as it is known. |
| `args` | see below | argv templates per action; `{runtime}`, `{version}`, `{tool}` and `{filter}` are substituted, and a token that becomes empty is dropped. |

Default argv templates, matching the 0.2.0 CLI (`internal/cli/cli.go`):

```yaml
list:      ["list"]
available: ["available", "{runtime}"]
install:   ["install", "{runtime}@{version}"]
uninstall: ["uninstall", "{runtime}@{version}"]
use:       ["use", "{runtime}@{version}"]
current:   ["current"]
which:     ["which", "{tool}"]
env:       ["env", "--shell", "json"]
config:    ["config", "--json"]
doctor:    ["doctor", "--json"]
version:   ["--version"]
```

## Usage

### Slash command

```text
/ployenv                                  list installed runtimes and the current one
/ployenv list node                        only node
/ployenv available node lts               list installable versions (filter optional)
/ployenv install node@22.11.0             install an exact version
/ployenv install node@22                  a major resolves to the newest match
/ployenv install node@22 --from D:\node   take over an existing install instead of downloading
/ployenv install node@22 --force          reinstall
/ployenv uninstall node@20.11.1           uninstall
/ployenv use node@22.11.0                 set the global default
/ployenv use node@22.11.0 --project       pin the current project (.ployenv)
/ployenv current node                     effective version and its source
/ployenv which node                       the executable a tool would run
/ployenv env --shell json                 the 0.2.0 JSON contract (path + vars)
/ployenv env --shell pwsh                 paste-ready PATH export for this shell
/ployenv config                           root / defaults / mirrors as JSON
/ployenv status                           bin + data root + injectable directories + PATH state
/ployenv path on                          prepend those directories to the harness process PATH
/ployenv path off                         restore the PATH captured before the first injection
/ployenv doctor                           ployenv self-check as JSON
/ployenv bin                              the resolved executable and whether it exists
/ployenv raw doctor --json                run an arbitrary ployenv argument vector
/ployenv help                             usage
```

### Model tool

```json
{ "action": "list" }
{ "action": "available", "runtime": "node", "filter": "lts" }
{ "action": "install", "runtime": "node", "version": "22.11.0" }
{ "action": "install", "runtime": "node", "version": "22", "flags": ["--force"] }
{ "action": "use", "runtime": "node", "version": "22.11.0", "flags": ["--project"] }
{ "action": "current" }
{ "action": "which", "tool": "npm" }
{ "action": "env" }
{ "action": "config" }
{ "action": "status" }
{ "action": "path_on" }
{ "action": "path_off" }
{ "action": "raw", "raw": ["doctor", "--json"] }
```

The tool returns one JSON string: `{ action, bin, runtime?, version?, argv?, exitCode, stdout, stderr,
timedOut, aborted, spawnFailure?, note?, environment?, paths?, pathInjected?, ... }`. A non-zero
exit, spawn failure, timeout, or abort is reported through those fields, never as a thrown tool error.
An unknown action reports `exitCode: 1` with an explanatory `error`.

### Environment facts for shell tools

When the profile mounts the shell-env registry (`ctx.shellEnv`, part of the base bundle), the plugin
declares three managed facts. Session shell tools rebuild the managed namespace for every call, so
these are always current:

| Variable | Value |
|---|---|
| `DSH_PLOYENV_BIN` | The resolved ployenv executable. |
| `DSH_PLOYENV_ROOT` | The ployenv data root, from `config --json`, once known. |
| `DSH_PLOYENV_PATHS` | Comma separated directories from `env --shell json` (`path`), once known. |

```powershell
echo $env:DSH_PLOYENV_PATHS
```

These are *facts*, not a PATH change: they tell the shell where ployenv lives. To make bare
`node` / `java` / `go` / `python` resolve through ployenv, run `/ployenv path on` (or the
`path_on` tool action). It prepends exactly the directories from `env --shell json`, is
**idempotent** (a directory already present is never duplicated), and `path off` restores the
PATH captured before the first injection, byte for byte. Injection is process-wide: it affects every
session in the same harness process until `path off` or a restart.

## Tests

```powershell
npm run check   # node --check index.js
npm run test    # node stub-smoke.mjs
```

`stub-smoke.mjs` is **hermetic**: it patches `node:child_process` before importing the plugin, answers
every CLI invocation from a canned 0.2.0-shaped table and asserts the REQ-002 contract in thirteen
clauses (A1 apply/registration, A2 action list, argv, facts and flag set, path on/off semantics, unknown
action exit code, A4 no installer, A5 help, FR-305 status echo, and FR-303 setup failing loudly offline
without spawning the CLI). No binary, no subprocess, no network - so it gives the
same verdict in a confined session (where piped spawn is denied with EPERM), on a developer machine
and in CI (`.github/workflows/ci.yml`, job `plugin-smoke`).

## Caveats

- **PATH injection is process-wide and in-memory.** DSH's only plugin-facing per-execution shell
  environment extension point accepts `DSH_*` keys exclusively, so a plugin cannot contribute
  `PATH` to one session's shell calls. `path on` therefore mutates the harness process environment,
  which every spawned child inherits, and nothing is written to the machine.
- **Machine-wide PATH is never touched.** The plugin contains no registry, `setx` or machine PATH
  writer, and exposes no such action.
- **`--dry-run` is rejected.** ployenv 0.3.0 and later refuse the flag (BUG-020) instead of pretending to
  dry-run while installing for real; older 0.2.0 builds accepted and ignored it. Either way the plugin passes the
  flag through verbatim and never treats it as a safety switch - confirm the runtime and version before installing.
- **Replacing the installed directory requires a restart** to load a fresh JavaScript module
  generation. Newly installed bundles can activate through HMR.
- The plugin calls `node:child_process` directly rather than a harness subprocess seam. Inside a
  session that runs under a confined sandbox, piped-stdio spawns are denied by the platform with
  `EPERM`; the DSH host process itself is normally unconfined.
- `install` can download hundreds of megabytes; `timeoutMs` applies to the whole call.

## Self-contained install (REQ-003)

> Shipped in **v0.3.3**. The bundled binary is built from the release commit (`-ldflags "-s -w"`).
> **v0.3.1** fixed the shell-script shapes (**I-07** pwsh single-quoted literals, **I-08** PATH separator
> follows the target shell) and stopped injecting a python `Scripts` directory that does not exist (**I-09**).
> **v0.3.2 is a breaking change**: ployenv now reads a single project file, `.ployenv` -
> `.ployenv.local` is no longer read at all.

Installing this package is meant to be enough - no separate ployenv download:

- a ployenv binary ships in the package (`bin/win32-x64/ployenv.exe` today; other platforms use the same
  `bin/<platform>-<arch>/` layout, or a `ployenv-<platform>-<arch>` package later);
- when that bundled binary is used, its data root is `<package>/data/ployenv` (passed to the child as
  `PLOYENV_ROOT`), so **uninstalling the plugin removes the installed runtimes too**;
- if you already have your own ployenv (row `bin` / `PLOYENV_BIN` / `PLOYENV_ROOT`), the plugin
  **reuses it and never touches the bundled copy** - the plugin package and the ployenv distribution stay
  independent;
- on a platform without a bundled binary, or when you want a *newer* release than the package ships, run
  the plugin-side `setup` action - see the next section;
> ⚠️ **Upgrade caveat.** A package update replaces the whole package directory, so a `data/` inside it goes
> away with it: the plugin recreates the directory on the next run, but the runtimes have to be installed
> again (`NFR-307` self-heal). Point row config `root` at a directory **outside** the package (for
> example `D:\ployenv-data`) when the runtimes must survive plugin updates.

## setup - install the released binary (REQ-003 FR-303)

`setup` fetches the released ployenv asset, verifies its SHA-256 and installs it under this package's
own data root. It is **plugin-side**: nothing is passed to the CLI, and no file is written outside
`data/` (NFR-301).

```text
/ployenv setup                                        zero config: newest release, checksum from SHA256SUMS.txt
/ployenv setup --url <url>                             a mirror or a pinned release
/ployenv setup --url <url> --sha256 <hex>              skip the checksum file entirely
```

```json
{ "action": "setup" }
{ "action": "setup", "url": "http://mirror/ployenv-windows-amd64.exe", "sha256": "d95377df…" }
```

| Item | Value |
|---|---|
| Default URL | `https://github.com/MOMIEDEJIYI/ployenv/releases/latest/download/ployenv-windows-amd64.exe` - the fixed-name alias asset, so no version is hard-coded |
| Default digest | the entry for that file name in the `SHA256SUMS.txt` next to the URL; pass `sha256` to skip that fetch |
| Writes | `data/ployenv/bin/ployenv.exe` and the provenance record `data/ployenv/setup.json` |
| Verify order | the digest is checked **before** anything is written; a mismatch exits non-zero and leaves no file (NFR-302) |
| Platforms | `win32-x64` only (the only released asset today); other platforms fail loudly |
| After setup | `bin`/`status` report `bin source: setup`, and `status` also prints `setup: <url> @ <time>` |
| Data root | still `<package>/data/ployenv` unless row config `root` overrides it, so **uninstalling the plugin removes both the binary and the runtimes** |

## Verified / unverified

- `node --check index.js` - passes (Node 22.23.3).
- `node stub-smoke.mjs` - **13/13 clauses** (hermetic contract check).
- `setup` end-to-end against a local HTTP source - 14/14 checks: install + provenance + resolution flip +
  `status` echo, wrong digest (non-zero, nothing written, no temp left), missing checksum file, name missing
  from the checksum file, and an explicit `sha256` that skips the checksum fetch.
- An independent acceptance harness (a fake CLI recording every argv, kept outside this
directory) - re-run after any migration.
- The CLI grammar above was read from the 0.2.0 sources and confirmed by live runs of
  `env --shell json`, `current --json`, `config --json` and `doctor --json`.
- **No real-machine load through DSH has been performed yet.** Load it once in a throwaway
  profile and record the outcome in your own notes.

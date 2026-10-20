# ployenv manual

**English** | [简体中文](manual.zh-CN.md)

Complete command reference for ployenv. For a quick overview see
[README](../README.md).

- [Concepts](#concepts)
- [Data directory](#data-directory)
- [Commands](#commands)
- [Version resolution](#version-resolution)
- [Session activation](#session-activation)
- [Configuration](#configuration)
- [Mirrors](#mirrors)
- [Scripting and encoding](#scripting-and-encoding)
- [Exit codes](#exit-codes)
- [Troubleshooting](#troubleshooting)

---

## Concepts

ployenv manages **language runtimes**. A runtime is identified by a short name:

| Name | Aliases | Tools placed on `PATH` |
|---|---|---|
| `node` | `nodejs`, `node.js` | `node`, `npm`, `npx`, `corepack` |
| `java` | `jdk`, `openjdk`, `temurin` | `java`, `javac`, `jar`, `jshell`, `keytool` |
| `go` | `golang` | `go`, `gofmt` |
| `python` | `py`, `cpython` | `python`, `python3`, `pip`, `pip3` |
| `maven` | `mvn` | `mvn`, `mvnDebug` |

Every runtime is installed into its own directory under
`<data dir>/versions/<name>/<version>/`. ployenv never installs anything
system-wide and never rewrites files it does not own.

**Maven is a tool, not a language.** ployenv only puts `<ver>/bin` on `PATH` —
it does not set `MAVEN_HOME`/`M2_HOME` (`mvn.cmd` derives its own home) and it
never touches `settings.xml` or `~/.m2`. Maven needs a JDK: either activate a
ployenv-managed `java` or keep your own `JAVA_HOME` — both work. `ployenv doctor`
prints a hint on stderr when neither is reachable.

A version specifier is one of:

| Specifier | Meaning | Example |
|---|---|---|
| Exact | A full version string | `ployenv install node@20.20.2` |
| Partial | Highest matching version | `ployenv install node@20` -> `20.20.2` |
| Channel | Resolved against the version index | `ployenv install node@lts`, `node@latest` |

Maven channels follow the 3.x line only: `latest` / `stable` / no filter pick the
highest **stable 3.x** (currently 3.10.0), `@release` is refused while upstream
`<release>` is a 4.0 pre-release, `maven@lts` reports that Maven has no LTS
channel, and `ployenv available maven all` lists pre-releases marked `preview`.

## Data directory

The data directory holds everything ployenv owns:

```text
<data dir>\
├─ ployenv.exe                     the executable (when portable)
├─ config.json                     configuration
├─ versions\<name>\<version>\      installed runtimes
├─ cache\                          downloaded archives
└─ logs\                           diagnostic logs
```

It is resolved in this order:

1. `PLOYENV_ROOT`, if set and non-empty.
2. The executable's own directory, if it is writable.
3. `%LOCALAPPDATA%\Programs\PloyEnv` (Windows) or `$XDG_DATA_HOME` /
   `~/.local/share/ployenv` (elsewhere), as a fallback.

`ployenv doctor` prints the directory in use, the reason it was chosen, and
whether it is writable.

**First run.** When `config.json` does not exist, ployenv performs one base
configuration step: it prints the detected data directory and asks for
confirmation before writing the config. On a pipe/non-TTY, or with `--yes`, it
proceeds automatically.

## Sharing one ployenv with the DSH plugin

The `dsh-plugin-ployenv` plugin ships its own ployenv copy and keeps its data inside
the plugin package (`<package>/data/ployenv`), so uninstalling or upgrading the plugin
also removes the runtimes installed there. If you already installed ployenv yourself
(for example under `E:\PloyEnv`), you can make the plugin and your shell **share one
binary and one data root**:

1. Set a **user** environment variable so the plugin uses your binary:

   ```text
   PLOYENV_BIN = E:\PloyEnv\ployenv.exe
   ```

2. Keep the data root in the same place (either is enough):

   - **leave** `PLOYENV_ROOT` unset: the CLI defaults to the executable's own directory
     (here `E:\PloyEnv`);
   - or set `PLOYENV_ROOT = E:\PloyEnv` explicitly.

3. To run `ployenv` from a shell too (optional), add `E:\PloyEnv` to the user `PATH`.

4. **Quit DSH completely and reopen it** (the environment is read at process start;
   only programs started afterwards see a new variable).

5. Verify with `/ployenv status` inside DSH:

   ```text
   bin:        E:\PloyEnv\ployenv.exe
   bin source: PLOYENV_BIN
   root:       E:\PloyEnv
   ```

From then on the shell and DSH share the same runtimes, and **uninstalling or upgrading
the plugin never touches the data under `E:\PloyEnv`**.

> To go back to the plugin's own copy, delete `PLOYENV_BIN` (and `PLOYENV_ROOT`) and
> restart DSH. Resolution order - binary: row `bin` > `PLOYENV_BIN` >
> `PLOYENV_ROOT\ployenv.exe` > setup copy > bundled copy > PATH; data root: row `root` >
> `PLOYENV_ROOT` > package `data/ployenv`.

## Commands

### Inspect

| Command | Description |
|---|---|
| `ployenv support` | List the runtimes ployenv knows about, with aliases, tools, mirror and installed versions. |
| `ployenv list [lang]` | Installed versions, marking the effective one. Without a language argument, only runtimes that have something installed are shown. |
| `ployenv available <lang> [filter]` | Installable versions. `filter` may be `22`, `lts`, `3.12`, … Shows the newest 20 by default; use `--limit N` or `--all`. |
| `ployenv current [lang]` | The effective version for each configured runtime and where it comes from (`project`, `global` or `env`). |
| `ployenv which <tool>` | The absolute path of the program a tool would actually run. |
| `ployenv doctor` | Self-check: paths, writability, catalogue state and the resolution report. |

### Install and remove

| Command | Description |
|---|---|
| `ployenv install <lang>@<version>` | Resolve, download, verify, extract. |
| `ployenv install <lang>@<version> --force` | Reinstall even if the version is already present. |
| `ployenv install <lang>@<version> --from <dir>` | Register an existing installation instead of downloading. |
| `ployenv adopt <lang>[@version] --root <dir>` | Explicitly register a directory. Without `@version` the version is inferred from the directory name. |
| `ployenv uninstall <lang>@<version>` | Remove a version. Adopted directories are only unregistered, never deleted. |
| `ployenv uninstall <lang> --all` | Remove every installed version of a runtime. |

Installation steps:

1. Resolve the request to one exact version.
2. Download the archive to `cache/` (skipped if a cached copy exists).
3. Verify the checksum when the upstream provides one.
4. Extract into `versions/<lang>/<version>/` and flatten the top-level
   directory if the archive has one.
5. Check that the primary binary exists; roll back on failure.
6. Delete the archive unless `cache.keep` is `true`.

### Choose a version

| Command | Description |
|---|---|
| `ployenv use <lang>@<version>` | Write the version into `config.json` as the global default. |
| `ployenv use <lang>@<version> -p` | Write it into `.ployenv` in the current directory (`--project` is the long form). |
| `ployenv use <lang>@none [-p]` | Remove that default / that `.ployenv` entry. `off` and `default` mean the same. |

`-p`/`--project` selects the project file, `-g`/`--global` states the global default
explicitly. A flag the command does not accept is refused rather than ignored, so
`--project` can no longer change the global default by accident.

`use` only records a version. To make it take effect in a terminal, activate it
(see below).

### Session activation

| Command | Description |
|---|---|
| `ployenv shell [--shell pwsh\|cmd\|bash]` | Open a child shell **in the same window** with the environment applied and the window title set to `[ployenv] <lang>@<version> - <dir>` (terminals that ignore console titles will not show it). The child has its **own command history**; `exit` returns to the parent shell with its history and environment intact. |
| `ployenv env [--shell pwsh\|cmd\|bash\|json]` | Print a script that applies the environment; eval it in the current shell. |
| `ployenv env --json` | Print the same information as JSON. |

For `maven` the script only prepends `<ver>/bin` to `PATH`; no `MAVEN_HOME`,
`M2_HOME` or Maven configuration is involved.

Examples:

```powershell
# PowerShell
ployenv env | Invoke-Expression
```

```bash
# bash / zsh
eval "$(ployenv env --shell bash)"
```

```bat
:: cmd has no eval; open an activated child shell instead (run in cmd, not PowerShell)
ployenv shell
```

`ployenv shell` starts a **child** shell, so its command history starts empty. To apply
the environment to the current `cmd` window instead (keeping its history):

```bat
:: run these two lines in cmd.exe - PowerShell's `>` writes UTF-16LE and cmd cannot run that file
ployenv env --shell cmd > %TEMP%\ployenv-env.bat && call %TEMP%\ployenv-env.bat
```

That keeps the history, but nothing is restored automatically when you leave the window.

### Cache

| Command | Description |
|---|---|
| `ployenv cache` | List cached archives and their total size. |
| `ployenv cache --clean` | Delete every cached archive. |

Partial downloads (`.part` files) are removed on `Ctrl+C` and swept on the next
run if a previous process was killed.

## Version resolution

Resolution has exactly two levels, so the result is always predictable:

1. **Environment override** — `PLOYENV_<LANG>_VERSION`, for example
   `PLOYENV_NODE_VERSION=20.20.2`. Highest priority, intended for CI.
2. **Project file** — `.ployenv` found by walking up from the current directory.
   `.nvmrc`, `.node-version`, `.java-version`, `.python-version`, `.go-version`,
   `.tool-versions` and `go.mod` are also read (read-only).
3. **Global default** — the `defaults` section of `config.json`.

Notes:

- Only configured runtimes appear in `list` / `current` / `env` / `shell`.
- A version declared in a project file but not installed is an **error**, not a
  silent skip: `ployenv` tells you which file asked for it.
- The project file format is `key=value`, one entry per line:

  ```ini
  node=20.20.2
  java=21
  go=1.24
  python=3.12
  ```

## Configuration

`ployenv config` prints every setting:

```console
$ ployenv config
root     C:\tools\PloyEnv
config   C:\tools\PloyEnv\config.json
defaults:
  node     (not set)
  java     (not set)
  go       1.27.2
  python   (not set)
mirrors:
  node     https://nodejs.org/dist (official)
  java     https://mirrors.tuna.tsinghua.edu.cn/Adoptium
  go       https://go.dev/dl (official)
  python   https://api.nuget.org/v3-flatcontainer (official)
logs:
  mode     on-error   (off | on-error | always)
  dir      logs
  keep     20
cache:
  keep     false   (true = keep downloaded archives)

change: config --set-default | --set-mirror | --set-log-mode | --set-cache-keep
        or edit config.json directly.
```

| Flag | Effect |
|---|---|
| `--set-default <lang> <version>` | Set the global default version. `none`, `off` or `default` clears it. |
| `--set-mirror <lang> <url>` | Use a mirror for a runtime. `none`, `off` or `default` restores the official source. |
| `--set-log-mode off\|on-error\|always` | When diagnostics are written to `logs/`: never, only on failure, or always. |
| `--set-cache-keep true\|false` | Keep downloaded archives after a successful install. |

`config.json` is plain JSON and can be edited by hand. Reading tolerates a UTF-8 BOM (Windows editors add one) and writing back normalises to UTF-8 without a BOM; a file that cannot be parsed is reported once and never overwritten.

```powershell
ployenv config --set-default node 22.23.3    # set the global default
ployenv config --set-default node none       # clear it again (also: off / default)
```

## Mirrors

- The official source is used by default.
- When a mirror is configured it is tried first; on failure ployenv falls back
  to the official source, then to the system `curl` as a last resort.
- Version listings and downloads may use different endpoints: Java and Python
  list versions through official APIs while the archive download can use a
  mirror. Maven lists through a repository-shaped base (Central by default) and
  downloads from the mirror first; a distribution-only mirror is still used for
  the archive.
- ployenv never changes the npm/pip configuration or the environment variables
  of your system.

Layouts:

| Runtime | Mirror base expected |
|---|---|
| `node` | A Node.js dist mirror, `<base>/v<version>/<file>`. |
| `go` | A Go download mirror, `<base>/<file>`. |
| `java` | An Adoptium-style mirror, `<base>/<feature>/jdk/<arch>/<os>/<file>`. |
| `python` | Windows: a NuGet flat-container base. Otherwise a GitHub-release mirror, `<base>/<tag>/<file>`. |
| `maven` | A **repository-shaped** base (same layout as Central), `<base>/<version>/apache-maven-<version>-bin.<ext>`. Apache distribution mirrors (dlcdn, TUNA, Aliyun, USTC) carry the archives but not `maven-metadata.xml`: listing tries the configured base once and then Central, while downloads follow the mirror-first order. |

```powershell
ployenv config --set-mirror node https://registry.npmmirror.com/-/binary/node
ployenv config --set-mirror java https://mirrors.tuna.tsinghua.edu.cn/Adoptium
ployenv config --set-mirror java none        # back to the official source
```

## Scripting and encoding

- On Windows the console output code page is switched to UTF-8 for the duration
  of the run and restored afterwards, so non-ASCII paths print correctly in a
  default GBK console.
- **Use `--json` in scripts.** It emits pure ASCII; non-ASCII characters are
  escaped as `\uXXXX`, so the output is independent of the terminal encoding.
- Results go to **stdout**; progress, warnings and errors go to **stderr**, so
  `ployenv ... | some-tool` stays clean.

Global options: `--json`, `--verbose`, `--debug`, `-q`, `--log`, `--no-color`,
`--yes`.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success. |
| `1` | Any error (usage, network, filesystem). The message goes to stderr. |
| `130` | Interrupted with `Ctrl+C`; partial downloads are cleaned up. |

## Troubleshooting

**`ployenv: command not found` / not on `PATH`.**
`ployenv doctor` prints a hint with the directory to add to `PATH`.

**`error: <tool> has no version configured`.**
No project file or global default exists yet: `ployenv use <lang>@<version>`,
then activate the session.

**A project file wants a version that is not installed.**
The error names the file. Install it with
`ployenv install <lang>@<version>`, or `adopt` an existing directory.

**Downloads fail behind a proxy.**
Set the standard `HTTP_PROXY` / `HTTPS_PROXY` variables, or point
`PLOYENV_CURL` at a `curl` binary that already honors your proxy settings.

**A version list is stale or empty.**
Version indexes are always fetched live; check the network and `--verbose` for
the failing endpoint.

**Logs.**
When `logs.mode` is `on-error`, a log file is written to `logs/` only after a
failure. `--log` forces logging for a single run; `--debug` raises the level.

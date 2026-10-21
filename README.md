# ployenv

**English** | [简体中文](README.zh-CN.md)

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A portable, single-binary version manager for local runtimes —
**Node.js, Java, Go, Python, Maven and adb (Android platform-tools)** — with
nothing to install and nothing to configure.

No installer, no account, no licence checks, and **no changes to your system
environment**. Drop one executable anywhere; its runtimes and settings live next
to it.

```console
$ ployenv install node@20
$ ployenv use node@20
$ ployenv shell
```

## Why ployenv

- **Non-invasive** — never edits the system `PATH`, `.npmrc`, `pip.ini` or the
  registry. Activating a version affects one terminal window only.
- **Explicit** — official sources by default. ployenv adopts an existing
  installation only when you ask it to, and never guesses.
- **Predictable** — the effective version comes from a project file (`.ployenv`,
  plus `.nvmrc`, `.tool-versions` and friends) or from one global default, and
  `PLOYENV_<LANG>_VERSION` overrides both. Nothing ever falls back to "some
  installed version".
- **Portable** — a single ~7.5 MB static binary with zero third-party
  dependencies. Move the folder, keep everything.
- **Scriptable** — `--json` output is pure ASCII (non-ASCII characters are
  escaped as `\uXXXX`), so it survives any console code page.

## Install

Windows 10/11 is the primary target; Linux and macOS (amd64/arm64) are built and
validated on native runners by CI. The macOS binaries are unsigned and unnotarized,
so the first run may need `xattr -d com.apple.quarantine`. Building needs **Go 1.22+**.

```powershell
go build -ldflags "-s -w" -o dist/ployenv.exe ./cmd/ployenv
```

Or, if script execution is allowed:

```powershell
powershell -ExecutionPolicy Bypass -File .\build.ps1 -Test
```

Then put the binary on your `PATH`, for example `D:\PloyEnv\ployenv.exe`.

## Quick start

```powershell
# see what is available
ployenv available node --limit 5

# install a runtime (22, lts and latest all work)
ployenv install node@20
ployenv install java@21
ployenv install go@1.24
ployenv install python@3.12

# record a default, then inspect
ployenv use node@20
ployenv current
ployenv list

# activate it in this terminal only
ployenv shell
```

Pin a project by adding a `.ployenv` file next to its source:

```ini
node=20.20.2
java=21
go=1.24
python=3.12
```

`ployenv use node@20 --project` writes or updates this file for you.

## Documentation

The full command reference, configuration and troubleshooting guide lives in
**[docs/manual.md](docs/manual.md)** ([简体中文](docs/manual.zh-CN.md)).

Highlights:

| Command | Description |
|---|---|
| `ployenv support` | List supported runtimes |
| `ployenv available <lang>` | Installable versions |
| `ployenv doctor` | Paths, writability and resolution report |
| `ployenv config` | Show or change every setting |

Run `ployenv --help` for the built-in summary.

## Roadmap

**Done**

- [x] Portable data directory, one-time base configuration
- [x] Node.js, Java, Go, Python and Maven, each described by a built-in Spec
- [x] Mirror fallback, and interrupt-safe cleanup (`Ctrl+C` leaves no partial files)
- [x] Checksum verification where upstream publishes one: SHA-256 for Go archives,
      SHA-512 for Maven (node / Java / Python checksums still to come)
- [x] Session activation without touching the system environment
- [x] `--json` machine interface
- [x] Prebuilt archives published to GitHub Releases (windows / linux / darwin on
      amd64 / arm64, with checksums)

**Next**

- [ ] Configurable runtime definitions (FR-11). Today runtime definitions are
      compiled-in Go code; `config.json` cannot add or override one yet
- [ ] Checksums for node, Java and Python, and a `doctor` connectivity check
      against every configured download source
- [ ] Remaining runtimes on the same `Spec` definition, including the database
      and middleware kinds (`MySQL`, `Redis`, `Nginx`) that already have a
      reserved `kind` field
- [ ] shell completion scripts; `--dry-run` is rejected until it is implemented (it used to be accepted and silently ignored, BUG-020)
- [ ] Log rotation honoring `logs.keep`

**Not planned**

- Automatic switching on `cd` — it needs a global shell hook, which would break
  the non-invasive promise. Use `ployenv shell` in each project terminal.

## License

[MIT](LICENSE) © MOMIEDEJIYI

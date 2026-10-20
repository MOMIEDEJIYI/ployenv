/**
 * DSH host plugin for ployenv, the local runtime version manager
 * (node / java / go / python / maven).
 *
 * It contributes one human slash command and one model-callable tool that both
 * shell out to the ployenv CLI with node:child_process, and it publishes the
 * ployenv facts (binary, data root, injectable PATH directories) to the
 * session's shell tools through the harness managed DSH_* environment namespace.
 *
 * ployenv is session scoped and non invasive: the CLI ships no shims and no
 * root / shims / setup commands, and nothing is ever written to PATH or the
 * registry.  'setup' here is a *plugin-side* action (REQ-003 FR-303): it
 * downloads the released binary into this package's own data directory and is
 * never passed to the CLI.  The plugin mirrors the CLI grammar (internal/cli/cli.go):
 *   list [runtime] | available <runtime> [filter] | install <runtime>@<version> [--from <dir>] [--force]
 *   uninstall <runtime>@<version> | use <runtime>@<version> [--global|--project]
 *   current [--json] | which <tool> | env --shell json | config --json | doctor --json
 * and derives the directories it injects from 'env --shell json' (the 'path'
 * array), never from a shims directory.
 *
 * Loaded as a Cordis function plugin: the Loader calls apply(ctx, config),
 * where config is this row's config from cordis.patch.yml, unchanged, because
 * this module exports no Config schema.
 *
 * @module dsh-plugin-ployenv
 */
import { spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
import { chmodSync, existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

/** Optional display metadata used in Cordis diagnostics. */
export const name = 'dsh-plugin-ployenv'

/** Services that must exist before apply runs; ctx.shellEnv is read optionally. */
export const inject = ['commands', 'tools']

const WINDOWS = process.platform === 'win32'
const PATH_SEPARATOR = WINDOWS ? ';' : ':'

/** This plugin's own directory (the installed package root). */
const PLUGIN_DIR = dirname(fileURLToPath(import.meta.url))
/** Platform tag used for bundled binaries, e.g. win32-x64. */
const PLATFORM_TAG = process.platform + '-' + process.arch
/** Where a plugin-owned ployenv keeps its data (removed with the plugin). */
const PLUGIN_DATA_ROOT = join(PLUGIN_DIR, 'data', 'ployenv')
/** The copy 'setup' installs, inside the data root (REQ-003 FR-303). */
const SELF_INSTALLED_BIN = join(PLUGIN_DATA_ROOT, 'bin', WINDOWS ? 'ployenv.exe' : 'ployenv')
/** Provenance of the last successful 'setup' (URL, digest, time). */
const SETUP_RECORD = join(PLUGIN_DATA_ROOT, 'setup.json')
/** Checksum file shipped next to every release asset. */
const SETUP_SUMS_NAME = 'SHA256SUMS.txt'
/** Release assets use Go's OS/arch words, not Node's (REQ-008 §3.1). */
const RELEASE_OS = { win32: 'windows', linux: 'linux', darwin: 'darwin' }
const RELEASE_ARCH = { x64: 'amd64', arm64: 'arm64' }
/** The release target of a Node platform/arch pair, e.g. windows-amd64. */
function releaseTargetFor(platform, arch) {
  const osName = RELEASE_OS[platform]
  const archName = RELEASE_ARCH[arch]
  return osName === undefined || archName === undefined ? undefined : osName + '-' + archName
}
/** The fixed-name alias asset of a release target (uploaded for every release). */
function assetNameFor(target) {
  return 'ployenv-' + target + (target.startsWith('windows-') ? '.exe' : '')
}
/** The targets the release pipeline publishes assets for. */
const SETUP_TARGETS = ['windows-amd64', 'windows-arm64', 'linux-amd64', 'linux-arm64', 'darwin-amd64', 'darwin-arm64']
/** This host's release target, or undefined when no asset matches this host. */
const SETUP_TARGET = releaseTargetFor(process.platform, process.arch)
/** Zero-config default: the fixed-name alias asset of the newest release. */
const DEFAULT_SETUP_URL = SETUP_TARGET === undefined || !SETUP_TARGETS.includes(SETUP_TARGET)
  ? undefined
  : 'https://github.com/MOMIEDEJIYI/ployenv/releases/latest/download/' + assetNameFor(SETUP_TARGET)

/** Bundled binary candidates, most specific first (REQ-003 §3.1). */
function bundledCandidates() {
  const exe = WINDOWS ? 'ployenv.exe' : 'ployenv'
  return [
    join(PLUGIN_DIR, 'bin', PLATFORM_TAG, exe),
    join(PLUGIN_DIR, 'bin', exe),
    join(PLUGIN_DIR, 'vendor', PLATFORM_TAG, exe),
    join(PLUGIN_DIR, 'vendor', exe),
    join(PLUGIN_DIR, 'node_modules', '@local', 'ployenv-' + PLATFORM_TAG, 'bin', exe),
  ]
}

/**
 * Resolve the ployenv executable (REQ-003 §3.2). Order: explicit configuration
 * wins (so an operator can point at their own build), then the bundled copy,
 * then the PATH name as a last resort. Every stage is named so status/bin can
 * report which one was used.
 * @returns {{ bin: string, source: string, exists: boolean, bundled: boolean }}
 */
function resolveBinary(configuredBin) {
  const configured = optionalText(configuredBin)
  if (configured !== undefined) {
    return { bin: configured, source: 'row config bin', exists: existsSync(configured), bundled: false }
  }
  const envBin = optionalText(process.env.PLOYENV_BIN)
  if (envBin !== undefined) {
    return { bin: envBin, source: 'PLOYENV_BIN', exists: existsSync(envBin), bundled: false }
  }
  const root = optionalText(process.env.PLOYENV_ROOT)
  if (root !== undefined) {
    const candidate = join(root, WINDOWS ? 'ployenv.exe' : 'ployenv')
    if (existsSync(candidate)) return { bin: candidate, source: 'PLOYENV_ROOT', exists: true, bundled: false }
  }
  // REQ-003 FR-303: a copy the operator installed with 'setup' outranks the
  // shipped copy.  Running setup is a deliberate choice, and the acceptance
  // test asserts that the chain resolves to this stage after a setup run.
  if (existsSync(SELF_INSTALLED_BIN)) return { bin: SELF_INSTALLED_BIN, source: 'setup', exists: true, bundled: false }
  for (const candidate of bundledCandidates()) {
    if (existsSync(candidate)) return { bin: candidate, source: 'bundled', exists: true, bundled: true }
  }
  return { bin: 'ployenv', source: 'PATH', exists: false, bundled: false }
}

const DEFAULT_TIMEOUT_MS = 120000

/**
 * Default argv templates, one per action. {runtime}, {version}, {tool} and
 * {filter} are substituted; a token that becomes empty is dropped.
 */
const DEFAULT_ARGS = Object.freeze({
  list: ['list'],
  available: ['available', '{runtime}'],
  install: ['install', '{runtime}@{version}'],
  uninstall: ['uninstall', '{runtime}@{version}'],
  use: ['use', '{runtime}@{version}'],
  current: ['current'],
  which: ['which', '{tool}'],
  env: ['env', '--shell', 'json'],
  config: ['config', '--json'],
  doctor: ['doctor', '--json'],
  version: ['--version'],
})

/** Actions accepted by the model tool (0.2.0 grammar; no shims, no root). */
const TOOL_ACTIONS = [
  'list', 'available', 'install', 'uninstall', 'use', 'current', 'which', 'env',
  'config', 'status', 'doctor', 'bin', 'raw', 'path_on', 'path_off', 'setup',
]

/** Boolean flags ployenv itself recognizes (internal/cli/args.go boolFlags). */
const BOOL_FLAGS = new Set(['force', 'global', 'project', 'dry-run', 'json', 'verbose', 'quiet', 'debug', 'log', 'no-color', 'help', 'all', 'yes', 'clean'])

const TOOL_DESCRIPTION = 'Manage local language runtimes (node, java, go, python, maven) through ployenv. '
  + 'list shows installed versions; available lists installable versions; '
  + 'install downloads and installs <runtime>@<version> (version may be exact, a major like 22, lts, or latest); '
  + 'uninstall removes a version; use switches the global default, or pins the current project with flag --project; '
  + 'current reports the effective version and its source; which reports the executable a tool would run; '
  + 'env prints ployenv environment setup (json by default); config prints root/defaults/mirrors as json; '
  + 'path_on prepends the directories reported by env --shell json to this harness process PATH so later shell '
  + 'commands resolve managed runtimes, and path_off restores the original PATH; status reports the resolved binary, '
  + 'data root, active directories, and PATH injection state; doctor prints ployenv diagnosis as json; bin reports the '
  + 'resolved executable; raw runs an arbitrary ployenv argument vector. '
  + 'setup downloads the released ployenv binary for this platform, verifies its SHA-256 against the release SHA256SUMS.txt and installs it under the plugin data root (default URL: the fixed-name alias asset of the newest release); it is the plugin-side fallback when no bundled or configured ployenv can be used. '
  + 'PATH injection only affects this harness process and is never written to the machine.'

const USAGE_HINT = 'list [runtime] | available <runtime> [filter] | install <runtime>@<version> [--force|--from <dir>] | '
  + 'uninstall <runtime>@<version> | use <runtime>@<version> [--global|--project] | current [runtime] | which <tool> | '
  + 'env [--shell json|pwsh|cmd|bash] | config | status | path on|off | doctor | bin | setup [--url <u>] [--sha256 <h>] | raw <args>'

const USAGE = 'Usage: /ployenv ' + USAGE_HINT
  + '\n  Version specs accept <runtime>@<version> or <runtime>=<version>; <version> may be exact, a major (22), lts, or latest.'
  + '\n  path on prepends the directories from env --shell json to this harness process PATH; path off restores it.'
  + '\n  setup downloads the released binary (SHA-256 verified) into this plugin\'s own data directory; it never touches PATH or the registry.'
  + '\n  ployenv is non invasive: nothing is written to PATH or the registry, and no machine wide setup is exposed here.'

/** Coerce one unknown value into a non-empty string or undefined. */
function optionalText(value) {
  return typeof value === 'string' && value.length > 0 ? value : undefined
}

/** Merge caller argv templates over the defaults, ignoring malformed entries. */
function normalizeArgs(raw) {
  const merged = {}
  for (const key of Object.keys(DEFAULT_ARGS)) merged[key] = [...DEFAULT_ARGS[key]]
  if (raw !== undefined && raw !== null && typeof raw === 'object') {
    for (const [key, value] of Object.entries(raw)) {
      if (Array.isArray(value) && value.every(token => typeof token === 'string')) merged[key] = [...value]
    }
  }
  return merged
}

/** Read the row config into a complete options record; no Config schema validates it. */
function normalizeConfig(raw) {
  const source = raw !== undefined && raw !== null && typeof raw === 'object' ? raw : {}
  const configuredBin = optionalText(source.bin)
  const commandName = optionalText(source.commandName)
  const toolName = optionalText(source.toolName)
  return {
    bin: configuredBin,
    root: optionalText(source.root),
    commandName: commandName !== undefined && /^[a-z][a-z0-9_-]*$/u.test(commandName) ? commandName : 'ployenv',
    toolName: toolName !== undefined ? toolName : 'ployenv',
    timeoutMs: Number.isFinite(source.timeoutMs) && source.timeoutMs > 0 ? source.timeoutMs : DEFAULT_TIMEOUT_MS,
    injectPathAtLoad: source.injectPathAtLoad === true,
    args: normalizeArgs(source.args),
  }
}

/** Render an arbitrary thrown value without trusting its string coercion. */
function describeError(error) {
  if (error instanceof Error) return error.message
  try { return String(error) } catch { return '<unprintable error>' }
}

/** Substitute {runtime} / {version} / {tool} / {filter} into one argv template. */
function expand(template, vars) {
  const argv = []
  for (const token of template) {
    const replaced = token.replace(/\{(\w+)\}/gu, function (_match, key) {
      return typeof vars[key] === 'string' ? vars[key] : ''
    })
    if (replaced.length > 0) argv.push(replaced)
  }
  return argv
}

/** Append flags that are not already part of the argv verbatim. */
function appendFlags(argv, flags) {
  const out = [...argv]
  for (const token of flags) {
    if (typeof token === 'string' && token.length > 0 && !out.includes(token)) out.push(token)
  }
  return out
}

/** Whether the configured executable is a path rather than a bare PATH name. */
function looksLikePath(bin) {
  return bin.includes('/') || bin.includes('\\')
}

/**
 * Run the ployenv CLI once and capture both streams.
 * Resolves for every outcome, including spawn failure and timeout, so callers
 * never need a try/catch around process management.
 * @param bin absolute path or PATH name of the ployenv executable.
 * @param argv argument vector after the executable.
 * @param options optional { timeoutMs, signal }.
 * @returns the captured outcome.
 */
function runPloyenv(bin, argv, options) {
  const settings = options !== undefined && options !== null ? options : {}
  const timeoutMs = Number.isFinite(settings.timeoutMs) && settings.timeoutMs > 0 ? settings.timeoutMs : DEFAULT_TIMEOUT_MS
  const signal = settings.signal
  const env = settings.env
  return new Promise(function (resolve) {
    let stdout = ''
    let stderr = ''
    let settled = false
    let child
    let timer
    const conclude = function (outcome) {
      if (settled) return
      settled = true
      if (timer !== undefined) clearTimeout(timer)
      if (signal !== undefined) signal.removeEventListener('abort', onAbort)
      resolve(Object.assign({ bin: bin, argv: argv, stdout: stdout, stderr: stderr }, outcome))
    }
    const onAbort = function () {
      conclude({ exitCode: null, timedOut: false, aborted: true, spawnFailure: undefined })
      try { if (child !== undefined) child.kill() } catch (error) { void error }
    }
    if (signal !== undefined) {
      if (signal.aborted === true) {
        conclude({ exitCode: null, timedOut: false, aborted: true, spawnFailure: undefined })
        return
      }
      signal.addEventListener('abort', onAbort, { once: true })
    }
    try {
      child = spawn(bin, argv, Object.assign({ stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true }, env !== undefined ? { env: env } : {}))
    } catch (error) {
      conclude({ exitCode: null, timedOut: false, aborted: false, spawnFailure: describeError(error) })
      return
    }
    if (child.stdout !== null && child.stdout !== undefined) {
      child.stdout.setEncoding('utf8')
      child.stdout.on('data', function (chunk) { stdout += chunk })
    }
    if (child.stderr !== null && child.stderr !== undefined) {
      child.stderr.setEncoding('utf8')
      child.stderr.on('data', function (chunk) { stderr += chunk })
    }
    child.on('error', function (error) {
      conclude({ exitCode: null, timedOut: false, aborted: false, spawnFailure: describeError(error) })
    })
    child.on('close', function (code) {
      conclude({ exitCode: code, timedOut: false, aborted: false, spawnFailure: undefined })
    })
    timer = setTimeout(function () {
      conclude({ exitCode: null, timedOut: true, aborted: false, spawnFailure: undefined })
      try { child.kill() } catch (error) { void error }
    }, timeoutMs)
    if (typeof timer.unref === 'function') timer.unref()
  })
}

/** Parse the first JSON object or array ployenv prints on stdout. */
function parseJsonValue(text) {
  if (typeof text !== 'string') return undefined
  const trimmed = text.trim()
  const start = Math.min(...['{', '['].map(function (brace) { return trimmed.indexOf(brace) }).filter(function (at) { return at >= 0 }))
  if (!Number.isFinite(start)) return undefined
  const opener = trimmed[start]
  const end = opener === '{' ? trimmed.lastIndexOf('}') : trimmed.lastIndexOf(']')
  if (end <= start) return undefined
  try {
    const value = JSON.parse(trimmed.slice(start, end + 1))
    return value !== null && typeof value === 'object' ? value : undefined
  } catch (error) { void error; return undefined }
}

/**
 * The directories 'env --shell json' says belong on PATH. ployenv lists one
 * entry per resolvable runtime directory (python contributes two), which is
 * exactly what a session shell needs in order to resolve managed runtimes.
 */
function environmentPaths(environment) {
  if (environment === null || typeof environment !== 'object') return []
  const raw = environment.path
  if (!Array.isArray(raw)) return []
  const dirs = []
  for (const entry of raw) {
    const text = optionalText(entry)
    if (text !== undefined && !dirs.includes(text)) dirs.push(text)
  }
  return dirs
}

/** Run one ployenv argument vector for an action (with the pinned data root). */
function runAction(state, argv, timeoutMs, signal) {
  return runPloyenv(state.options.bin, argv, { timeoutMs: timeoutMs, signal: signal, env: state.childEnv })
}

/** Read 'ployenv --version' once and cache it; undefined when the CLI cannot run. */
function probeCliVersion(state, timeoutMs) {
  if (state.cliVersion !== undefined) return Promise.resolve(state.cliVersion)
  if (state.cliProbe === undefined) {
    state.cliProbe = runAction(state, ['--version'], timeoutMs).then(function (result) {
      if (result.exitCode !== 0) return undefined
      const match = /ployenv\s+([0-9][0-9A-Za-z.+-]*)/u.exec(String(result.stdout !== undefined ? result.stdout : ''))
      if (match === null) return undefined
      state.cliVersion = match[1]
      return state.cliVersion
    }, function () { return undefined })
  }
  return state.cliProbe
}

/** Read 'config --json' once and cache it; a failure leaves the cache empty so it can be retried. */
function probeConfig(state, timeoutMs) {
  if (state.config !== undefined) return Promise.resolve(state.config)
  if (state.configProbe === undefined) {
    state.configProbe = runAction(state, expand(state.options.args.config, {}), timeoutMs).then(function (result) {
      if (result.exitCode !== 0) return undefined
      const value = parseJsonValue(result.stdout)
      if (value === undefined || Array.isArray(value)) return undefined
      state.config = value
      return value
    }, function () { return undefined })
    state.configProbe.then(function (value) {
      if (value === undefined) state.configProbe = undefined
    }, function () { state.configProbe = undefined })
  }
  return state.configProbe
}

/**
 * Read 'env --shell json' once and cache it; refresh=true re-reads it so a
 * fresh 'path on' reflects runtimes installed since the last probe.
 */
function probeEnvironment(state, timeoutMs, refresh) {
  if (refresh !== true && state.environment !== undefined) return Promise.resolve(state.environment)
  if (refresh === true || state.environmentProbe === undefined) {
    const probe = runAction(state, expand(state.options.args.env, {}), timeoutMs).then(function (result) {
      if (result.exitCode !== 0) return undefined
      const value = parseJsonValue(result.stdout)
      if (value === undefined || Array.isArray(value)) return undefined
      state.environment = value
      return value
    }, function () { return undefined })
    state.environmentProbe = probe
    probe.then(function (value) {
      if (value === undefined && state.environmentProbe === probe) state.environmentProbe = undefined
    }, function () {
      if (state.environmentProbe === probe) state.environmentProbe = undefined
    })
  }
  return state.environmentProbe
}

/** ployenv's own trailing-separator and case rules, for PATH comparisons. */
function stripTrailingSeparator(value) {
  let text = value
  while (text.length > 1 && (text.endsWith('/') || text.endsWith('\\'))) text = text.slice(0, -1)
  return text
}

/** A comparable key for one PATH entry. */
function pathKey(value) {
  const text = stripTrailingSeparator(value)
  return WINDOWS ? text.toLowerCase() : text
}

/**
 * Prepend directories to this process PATH. Idempotent: a directory that is
 * already present is never duplicated, and the value seen before the first
 * injection is remembered so path_off can restore it byte for byte.
 * @returns {{ changed: boolean, added: string[], skipped: string[], dirs: string[] }}
 */
function injectPaths(state, dirs) {
  const wanted = []
  const seen = new Set()
  for (const entry of dirs) {
    const text = optionalText(entry)
    if (text === undefined) continue
    const key = pathKey(text)
    if (seen.has(key)) continue
    seen.add(key)
    wanted.push(text)
  }
  if (wanted.length === 0) return { changed: false, added: [], skipped: [], dirs: [] }
  if (state.originalPath === undefined) state.originalPath = process.env.PATH !== undefined ? process.env.PATH : ''
  const entries = (process.env.PATH !== undefined ? process.env.PATH : '').split(PATH_SEPARATOR).filter(function (entry) { return entry.length > 0 })
  const present = new Set(entries.map(pathKey))
  const added = []
  const skipped = []
  for (const dir of wanted) {
    const key = pathKey(dir)
    if (present.has(key)) { skipped.push(dir); continue }
    present.add(key)
    added.push(dir)
  }
  if (added.length > 0) process.env.PATH = added.concat(entries).join(PATH_SEPARATOR)
  state.pathInjected = true
  state.injectedDirs = wanted
  return { changed: added.length > 0, added: added, skipped: skipped, dirs: wanted }
}

/** Restore the PATH captured before the first injection. */
function restorePath(state) {
  if (state.originalPath === undefined) return false
  const changed = process.env.PATH !== state.originalPath
  process.env.PATH = state.originalPath
  state.pathInjected = false
  state.injectedDirs = []
  return changed
}

/** Split '<runtime>@<version>' or '<runtime>=<version>' exactly as ployenv's parseSpec does. */
function splitRuntimeSpec(token) {
  if (typeof token !== 'string') return { runtime: undefined, version: undefined }
  for (const separator of ['@', '=']) {
    const at = token.indexOf(separator)
    if (at > 0) return { runtime: optionalText(token.slice(0, at)), version: optionalText(token.slice(at + 1)) }
  }
  return { runtime: optionalText(token), version: undefined }
}

/**
 * Separate positional arguments from flags the way ployenv's splitFlags does
 * (internal/cli/args.go): a --name consumes the next non-flag token as its
 * value unless it is a known boolean flag.
 */
function splitFlags(tokens) {
  const positional = []
  const flags = []
  for (let index = 0; index < tokens.length; index++) {
    const token = tokens[index]
    if (typeof token !== 'string' || token.length === 0) continue
    if (token.slice(0, 2) !== '--') { positional.push(token); continue }
    flags.push(token)
    const name = token.slice(2)
    if (BOOL_FLAGS.has(name)) continue
    const next = tokens[index + 1]
    if (typeof next === 'string' && next.slice(0, 2) !== '--') { flags.push(next); index++ }
  }
  return { positional: positional, flags: flags }
}

/** Lowercase hex SHA-256 of one buffer. */
function sha256Hex(buffer) {
  return createHash('sha256').update(buffer).digest('hex')
}

/** Where the checksum file lives for a download URL (same directory, fixed name). */
function checksumsUrl(url) {
  const cut = url.lastIndexOf('/')
  return cut < 0 ? undefined : url.slice(0, cut + 1) + SETUP_SUMS_NAME
}

/** Read one URL into memory; any non-200 status is an error. */
async function fetchBytes(url, timeoutMs) {
  const controller = new AbortController()
  const timer = setTimeout(function () { controller.abort() }, timeoutMs)
  if (typeof timer.unref === 'function') timer.unref()
  try {
    const response = await fetch(url, { redirect: 'follow', signal: controller.signal })
    if (response.status !== 200) throw new Error('GET ' + url + ' returned HTTP ' + String(response.status))
    return Buffer.from(await response.arrayBuffer())
  } finally {
    clearTimeout(timer)
  }
}

/** The SHA-256 a SHA256SUMS.txt line declares for one file name. */
function checksumFor(text, name) {
  for (const line of String(text).split(/\r?\n/u)) {
    if (line.length === 0 || line.startsWith('#')) continue
    const match = /^([0-9A-Fa-f]{64})\s+(.+?)\s*$/u.exec(line)
    if (match !== null && match[2] === name) return match[1].toLowerCase()
  }
  return undefined
}

/** Provenance of the last successful setup, if a record exists. */
function readSetupRecord() {
  try {
    if (!existsSync(SETUP_RECORD)) return undefined
    const value = JSON.parse(readFileSync(SETUP_RECORD, 'utf8'))
    return value !== null && typeof value === 'object' && !Array.isArray(value) ? value : undefined
  } catch (error) { void error; return undefined }
}

/** One '--name value' / '--name=value' lookup in a flag list. */
function flagValue(flags, name) {
  for (let index = 0; index < flags.length; index++) {
    const token = flags[index]
    if (token === '--' + name) return flags[index + 1]
    if (token.slice(0, name.length + 3) === '--' + name + '=') return token.slice(name.length + 3)
  }
  return undefined
}

/**
 * REQ-003 FR-303: download the released ployenv binary, verify its SHA-256 and
 * install it under this package's own data root.  Nothing is written until the
 * digest matches (NFR-302), and every write stays inside data/ (NFR-301).
 * @param state mutable plugin state.
 * @param request { url?, sha256? } - both default to the zero-config source.
 * @param timeoutMs per-download deadline.
 * @returns a losslessly JSON-serializable outcome record.
 */
async function runSetup(state, request, timeoutMs) {
  const base = { action: 'setup', bin: state.options.bin }
  const requested = optionalText(request.url)
  const url = requested !== undefined ? requested : DEFAULT_SETUP_URL
  if (url === undefined) {
    const reason = 'setup has no released asset for ' + process.platform + '-' + process.arch
      + '; published targets: ' + SETUP_TARGETS.join(', ')
      + '. Pass --url to install a build from a mirror or a custom source.'
    return Object.assign(base, { exitCode: 1, stdout: '', stderr: 'error: ' + reason + '\n', error: reason })
  }
  const name = url.slice(url.lastIndexOf('/') + 1)
  if (name.length === 0) {
    const reason = 'setup needs a URL that ends in a file name: ' + JSON.stringify(url)
    return Object.assign(base, { exitCode: 1, stdout: '', stderr: 'error: ' + reason + '\n', error: reason })
  }
  const given = optionalText(request.sha256)
  let expected = given !== undefined ? given.trim().toLowerCase() : undefined
  let checksumsFrom
  try {
    if (expected === undefined) {
      const sumsUrl = checksumsUrl(url)
      if (sumsUrl === undefined) throw new Error('cannot derive a ' + SETUP_SUMS_NAME + ' URL from ' + url)
      const declared = checksumFor((await fetchBytes(sumsUrl, timeoutMs)).toString('utf8'), name)
      if (declared === undefined) throw new Error(SETUP_SUMS_NAME + ' does not list ' + name)
      expected = declared
      checksumsFrom = sumsUrl
    }
    const bytes = await fetchBytes(url, timeoutMs)
    const actual = sha256Hex(bytes)
    if (actual !== expected) {
      throw new Error('SHA-256 mismatch for ' + name + ': expected ' + expected + ', got ' + actual + ' (nothing was written)')
    }
    const at = new Date().toISOString()
    mkdirSync(dirname(SELF_INSTALLED_BIN), { recursive: true })
    const staged = SELF_INSTALLED_BIN + '.tmp-' + String(process.pid)
    writeFileSync(staged, bytes)
    renameSync(staged, SELF_INSTALLED_BIN)
    // A downloaded asset carries no mode of its own: without this the copy is not
    // executable on the Unix-likes and every later spawn fails with EACCES.
    if (!WINDOWS) chmodSync(SELF_INSTALLED_BIN, 0o755)
    const record = { url: url, checksums: checksumsFrom, sha256: actual, bytes: bytes.length, at: at, target: SELF_INSTALLED_BIN }
    writeFileSync(SETUP_RECORD, JSON.stringify(record, null, 2) + '\n')
    return Object.assign(base, {
      exitCode: 0, stdout: SELF_INSTALLED_BIN, stderr: '', argv: undefined,
      source: 'setup', installed: true, setup: record,
      note: 'installed ' + name + ' (' + String(bytes.length) + ' bytes) into the plugin data root; bin now resolves to source "setup"',
    })
  } catch (error) {
    const reason = describeError(error)
    return Object.assign(base, { exitCode: 1, stdout: '', stderr: 'error: setup failed: ' + reason + '\n', error: reason })
  }
}

/**
 * Execute one requested operation against the ployenv CLI.
 * @param state mutable plugin state (options, cached config and environment, PATH bookkeeping).
 * @param request { action, runtime?, version?, filter?, tool?, flags?, args?, timeoutMs? }.
 * @param signal optional caller cancellation signal.
 * @returns a losslessly JSON-serializable outcome record.
 */
async function invoke(state, request, signal) {
  const action = typeof request.action === 'string' ? request.action : ''
  const runtime = optionalText(request.runtime)
  const version = optionalText(request.version)
  const filter = optionalText(request.filter)
  const tool = optionalText(request.tool)
  const flags = Array.isArray(request.flags) ? request.flags.filter(function (token) { return typeof token === 'string' }) : []
  const url = optionalText(request.url)
  const sha256 = optionalText(request.sha256)
  const timeoutMs = Number.isFinite(request.timeoutMs) && request.timeoutMs > 0 ? request.timeoutMs : state.options.timeoutMs
  const base = { action: action, bin: state.options.bin, runtime: runtime, version: version }
  const templates = state.options.args

  if (action === 'bin') {
    const resolved = resolveBinary(state.configuredBin)
    const pathLike = looksLikePath(resolved.bin)
    const ok = !pathLike || resolved.exists === true
    return Object.assign(base, {
      exitCode: ok ? 0 : 1,
      stdout: resolved.bin,
      stderr: ok ? '' : 'error: resolved ployenv executable does not exist: ' + resolved.bin + '\n',
      argv: undefined, pathLike: pathLike, exists: pathLike ? resolved.exists : undefined, source: resolved.source,
      note: pathLike
        ? (ok ? 'resolved executable exists (source: ' + resolved.source + ')' : 'resolved executable does not exist (source: ' + resolved.source + '); set bin/PLOYENV_BIN or reinstall the plugin bundle')
        : 'resolved as a bare name through PATH',
    })
  }

  if (action === 'setup') {
    return await runSetup(state, {
      url: url !== undefined ? url : flagValue(flags, 'url'),
      sha256: sha256 !== undefined ? sha256 : flagValue(flags, 'sha256'),
    }, timeoutMs)
  }

  if (action === 'status') {
    const resolved = resolveBinary(state.configuredBin)
    const environment = await probeEnvironment(state, timeoutMs, false)
    const config = await probeConfig(state, timeoutMs)
    const root = state.root !== undefined
      ? state.root
      : (config !== undefined && typeof config.root === 'string' && config.root.length > 0 ? config.root : undefined)
    const cli = await probeCliVersion(state, timeoutMs)
    const dirs = environmentPaths(environment)
    const pathHead = (process.env.PATH !== undefined ? process.env.PATH : '').split(PATH_SEPARATOR).slice(0, 3).join(PATH_SEPARATOR)
    const resolvedOk = resolved.exists !== false || !looksLikePath(resolved.bin)
    const ok = resolvedOk && typeof cli === 'string'
    return Object.assign(base, {
      exitCode: ok ? 0 : 1, stdout: '', stderr: ok ? '' : 'error: ployenv is not usable here: ' + (resolvedOk ? 'version probe failed' : 'resolved executable is missing (' + resolved.bin + ')') + '\n',
      argv: undefined, root: root, cli: cli, binSource: resolved.source,
      setup: readSetupRecord(),
      environment: environment, paths: dirs,
      pathInjected: state.pathInjected === true, pathHead: pathHead,
    })
  }

  if (action === 'path_on') {
    const environment = await probeEnvironment(state, timeoutMs, true)
    const argv = expand(templates.env, {})
    const dirs = environmentPaths(environment)
    if (dirs.length === 0) {
      return Object.assign(base, {
        exitCode: 1, stdout: '', stderr: 'error: ployenv env --shell json returned no path entries\n', argv: argv,
        error: 'env --shell json returned no path entries; is a default version set? (ployenv use <runtime>@<version>)',
      })
    }
    const outcome = injectPaths(state, dirs)
    return Object.assign(base, {
      exitCode: 0, stdout: outcome.dirs.join('\n'), stderr: '', argv: argv,
      pathInjected: true, pathAdded: outcome.changed, paths: outcome.dirs, pathSkipped: outcome.skipped,
      note: outcome.changed
        ? 'injected ' + String(outcome.added.length) + ' director' + (outcome.added.length === 1 ? 'y' : 'ies') + ' into this harness process PATH'
        : 'every reported directory was already on this harness process PATH',
    })
  }

  if (action === 'path_off') {
    const removed = restorePath(state)
    return Object.assign(base, {
      exitCode: 0, stdout: '', stderr: '', argv: undefined, pathInjected: false, pathRemoved: removed,
      note: removed ? 'this harness process PATH was restored to its value before the first path on' : 'no path injection to undo',
    })
  }

  if (action === 'raw') {
    const raw = Array.isArray(request.args) ? request.args.filter(function (token) { return typeof token === 'string' }) : []
    if (raw.length === 0) return Object.assign(base, { error: 'raw needs at least one argument' })
    return Object.assign(base, { argv: raw }, await runAction(state, raw, timeoutMs, signal))
  }

  if (action === 'env') {
    const hasShell = flags.some(function (token) { return token === '--shell' || token.slice(0, 8) === '--shell=' })
    const argv = hasShell ? ['env'].concat(flags) : appendFlags(expand(templates.env, {}), flags)
    const result = Object.assign(base, { argv: argv }, await runAction(state, argv, timeoutMs, signal))
    if (result.exitCode === 0) {
      const value = parseJsonValue(result.stdout)
      if (value !== undefined && !Array.isArray(value)) {
        result.environment = value
        state.environment = value
      }
    }
    return result
  }

  if (action === 'config') {
    const argv = appendFlags(expand(templates.config, {}), flags)
    const result = Object.assign(base, { argv: argv }, await runAction(state, argv, timeoutMs, signal))
    if (result.exitCode === 0) {
      const value = parseJsonValue(result.stdout)
      if (value !== undefined && !Array.isArray(value)) {
        result.config = value
        state.config = value
      }
    }
    return result
  }

  if (action === 'doctor') {
    const argv = appendFlags(expand(templates.doctor, {}), flags)
    return Object.assign(base, { argv: argv }, await runAction(state, argv, timeoutMs, signal))
  }

  if (action === 'list') {
    const argv = expand(templates.list, {}).concat(runtime === undefined ? [] : [runtime]).concat(flags)
    return Object.assign(base, { argv: argv }, await runAction(state, argv, timeoutMs, signal))
  }

  if (action === 'available') {
    if (runtime === undefined) return Object.assign(base, { error: 'available needs a runtime (for example: available node lts)' })
    const argv = expand(templates.available, { runtime: runtime }).concat(filter === undefined ? [] : [filter]).concat(flags)
    return Object.assign(base, { argv: argv }, await runAction(state, argv, timeoutMs, signal))
  }

  if (action === 'current') {
    const argv = expand(templates.current, {}).concat(runtime === undefined ? [] : [runtime]).concat(flags)
    return Object.assign(base, { argv: argv }, await runAction(state, argv, timeoutMs, signal))
  }

  if (action === 'which') {
    if (tool === undefined) return Object.assign(base, { error: 'which needs a tool name (for example: which node)' })
    const argv = expand(templates.which, { tool: tool }).concat(flags)
    return Object.assign(base, { argv: argv }, await runAction(state, argv, timeoutMs, signal))
  }

  if (action === 'install' || action === 'uninstall' || action === 'use') {
    if (runtime === undefined) {
      return Object.assign(base, { error: action + ' needs a runtime, as <runtime>@<version>' })
    }
    if (version === undefined && action !== 'install') {
      return Object.assign(base, { error: action + ' needs an exact version (for example: ' + action + ' node@22.11.0)' })
    }
    const argv = expand(templates[action], {
      runtime: runtime,
      version: version !== undefined ? version : 'latest',
    }).concat(flags)
    return Object.assign(base, { argv: argv }, await runAction(state, argv, timeoutMs, signal))
  }

  if (action === 'version') {
    const argv = expand(templates.version, {}).concat(flags)
    return Object.assign(base, { argv: argv }, await runAction(state, argv, timeoutMs, signal))
  }

  return Object.assign(base, {
    exitCode: 1, stdout: '', stderr: 'error: unknown action ' + JSON.stringify(action) + '\n', argv: undefined,
    error: 'unknown action ' + JSON.stringify(action),
  })
}

/** Render the path/vars summary of an 'env --shell json' payload. */
function renderEnvironment(environment) {
  const lines = []
  const dirs = environmentPaths(environment)
  if (dirs.length > 0) {
    lines.push('path:')
    for (const dir of dirs) lines.push('  ' + dir)
  }
  const vars = environment !== null && typeof environment === 'object' ? environment.vars : undefined
  if (vars !== null && typeof vars === 'object' && !Array.isArray(vars)) {
    for (const [key, value] of Object.entries(vars)) lines.push(key + '=' + String(value))
  }
  return lines
}

/** Render one outcome for a human command result. */
function renderText(result) {
  if (typeof result.error === 'string') return 'ployenv: ' + result.error
  const lines = []
  if (result.action === 'status') lines.push('bin: ' + result.bin)
  else if (Array.isArray(result.argv) && result.argv.length > 0) lines.push('$ ' + [result.bin].concat(result.argv).join(' '))
  if (typeof result.note === 'string' && result.note.length > 0) lines.push(result.note)
  if (result.action === 'status' && typeof result.root === 'string' && result.root.length > 0) lines.push('root: ' + result.root)
  if (result.action === 'status' && typeof result.cli === 'string') lines.push('cli: ' + result.cli)
  if (result.action === 'status' && typeof result.binSource === 'string') lines.push('bin source: ' + result.binSource)
  if (result.action === 'status' && result.setup !== null && typeof result.setup === 'object' && typeof result.setup.url === 'string') {
    lines.push('setup: ' + result.setup.url + ' @ ' + String(result.setup.at))
  }
  if (result.environment !== null && typeof result.environment === 'object') {
    const rendered = renderEnvironment(result.environment)
    for (const line of rendered) lines.push(line)
  }
  if (result.action === 'status' && Array.isArray(result.paths) && result.paths.length > 0) {
    lines.push('injectable directories: ' + String(result.paths.length))
  }
  if (typeof result.pathInjected === 'boolean' && result.action === 'status') lines.push('PATH injected: ' + String(result.pathInjected))
  if (typeof result.pathHead === 'string' && result.pathHead.length > 0) lines.push('PATH head: ' + result.pathHead)
  if (typeof result.stdout === 'string' && result.stdout.trim().length > 0) {
    const rendered = result.action === 'status' || (result.action === 'env' && result.environment !== undefined)
    if (!rendered) lines.push(result.stdout.trimEnd())
  }
  if (typeof result.stderr === 'string' && result.stderr.trim().length > 0) lines.push('[stderr] ' + result.stderr.trimEnd())
  if (typeof result.spawnFailure === 'string') lines.push('[spawn failed] ' + result.spawnFailure)
  if (result.timedOut === true) lines.push('[timed out]')
  if (result.aborted === true) lines.push('[aborted]')
  if (typeof result.exitCode === 'number' && result.exitCode !== 0) lines.push('[exit code ' + String(result.exitCode) + ']')
  if (lines.length === 0) lines.push('ployenv: no output')
  return lines.join('\n')
}

/** Whether an outcome should settle a command as an error. */
function failed(result) {
  if (typeof result.error === 'string') return true
  if (typeof result.spawnFailure === 'string') return true
  if (result.timedOut === true || result.aborted === true) return true
  return typeof result.exitCode === 'number' && result.exitCode !== 0
}

/** Parse slash-command tokens into an invoke() request. */
function parseTokens(tokens) {
  if (tokens.length === 0 || tokens[0] === 'help' || tokens[0] === '--help' || tokens[0] === '-h') return { help: true }
  const head = tokens[0]
  const { positional, flags } = splitFlags(tokens.slice(1))
  const first = positional[0]
  if (head === 'list' || head === 'ls') return { action: 'list', runtime: optionalText(first), flags: flags }
  if (head === 'available' || head === 'list-remote' || head === 'ls-remote') {
    return { action: 'available', runtime: optionalText(first), filter: optionalText(positional[1]), flags: flags }
  }
  if (head === 'install' || head === 'add') {
    const spec = splitRuntimeSpec(first)
    return { action: 'install', runtime: spec.runtime, version: spec.version, flags: flags }
  }
  if (head === 'uninstall' || head === 'remove' || head === 'rm') {
    const spec = splitRuntimeSpec(first)
    return { action: 'uninstall', runtime: spec.runtime, version: spec.version, flags: flags }
  }
  if (head === 'use' || head === 'switch') {
    const spec = splitRuntimeSpec(first)
    return { action: 'use', runtime: spec.runtime, version: spec.version, flags: flags }
  }
  if (head === 'current') return { action: 'current', runtime: optionalText(first), flags: flags }
  if (head === 'which') return { action: 'which', tool: optionalText(first), flags: flags }
  if (head === 'env') return { action: 'env', flags: flags }
  if (head === 'config') return { action: 'config', flags: flags }
  if (head === 'path' || head === 'paths') {
    if (first === 'on' || first === 'enable') return { action: 'path_on' }
    if (first === 'off' || first === 'disable') return { action: 'path_off' }
    return { action: 'status' }
  }
  if (head === 'status') return { action: 'status' }
  if (head === 'doctor') return { action: 'doctor', flags: flags }
  if (head === 'version' || head === '--version' || head === '-v') return { action: 'version' }
  if (head === 'bin' || head === 'where') return { action: 'bin' }
  if (head === 'setup') return { action: 'setup', flags: flags }
  if (head === 'raw' || head === 'exec') return { action: 'raw', args: tokens.slice(1) }
  return { error: 'unknown subcommand ' + JSON.stringify(head) }
}

/** Run one slash-command invocation. */
async function commandHandler(state, invocation) {
  const tokens = invocation.rawInput.trim().split(/\s+/u).filter(function (token) { return token.length > 0 })
  const parsed = parseTokens(tokens)
  if (parsed.help === true) return { kind: 'success', text: USAGE }
  if (typeof parsed.error === 'string') return { kind: 'error', text: parsed.error + '\n' + USAGE }
  const result = await invoke(state, parsed, invocation.signal)
  return failed(result) ? { kind: 'error', text: renderText(result) } : { kind: 'success', text: renderText(result) }
}

/** Register the DSH_PLOYENV_* facts when the shell-env registry is mounted. */
function registerShellEnvFacts(ctx, state) {
  const shellEnv = typeof ctx.get === 'function' ? ctx.get('shellEnv') : undefined
  if (shellEnv === undefined || shellEnv === null || typeof shellEnv.register !== 'function') return
  shellEnv.register({
    name: 'ployenv',
    variables: {
      DSH_PLOYENV_BIN: { description: 'ployenv CLI executable or PATH name, from the ployenv DSH plugin.' },
      DSH_PLOYENV_ROOT: { description: 'ployenv data root (the directory holding versions and config.json).' },
      DSH_PLOYENV_PATHS: { description: 'Comma separated directories from ployenv env --shell json, resolved from the 0.2.0 path list.' },
    },
    resolve: function () {
      const values = {}
      if (state.options.bin.length > 0) values.DSH_PLOYENV_BIN = state.options.bin
      if (state.config !== undefined && typeof state.config.root === 'string' && state.config.root.length > 0) {
        values.DSH_PLOYENV_ROOT = state.config.root
      }
      const dirs = environmentPaths(state.environment)
      if (dirs.length > 0) values.DSH_PLOYENV_PATHS = dirs.join(',')
      return values
    },
  })
}

/** Register the slash command. */
function registerCommand(ctx, state) {
  ctx.commands.register({
    name: state.options.commandName,
    description: 'Manage local runtimes through ployenv (list, available, install, use, and inject the 0.2.0 path list into shell tools).',
    input: { hint: USAGE_HINT },
    handler: function (invocation) { return commandHandler(state, invocation) },
  })
}

/** Register the model tool. */
function registerTool(ctx, state) {
  ctx.tools.register({
    name: state.options.toolName,
    description: TOOL_DESCRIPTION,
    parameters: {
      type: 'object',
      properties: {
        action: { type: 'string', enum: TOOL_ACTIONS, description: 'Operation to perform.' },
        runtime: { type: 'string', description: 'Runtime family: node (nodejs), java (jdk), go (golang), python (py), or maven (mvn).' },
        version: { type: 'string', description: 'Version for install/use/uninstall: exact (22.11.0), a major (22), lts, or latest.' },
        filter: { type: 'string', description: 'For action=available: a version filter such as 22 or lts.' },
        tool: { type: 'string', description: 'For action=which: the tool name, for example node or npm.' },
        flags: { type: 'array', items: { type: 'string' }, description: 'Extra ployenv flags appended verbatim, for example ["--force"] or ["--project"].' },
        raw: { type: 'array', items: { type: 'string' }, description: 'For action=raw: the exact ployenv arguments.' },
        url: { type: 'string', description: 'For action=setup: download URL; defaults to the newest release alias asset for this host (ployenv-<os>-<arch>[.exe]).' },
        sha256: { type: 'string', description: 'For action=setup: expected SHA-256; defaults to the entry in the SHA256SUMS.txt next to the URL.' },
        timeoutMs: { type: 'number', description: 'Optional per-call timeout in milliseconds.' },
      },
      required: ['action'],
    },
    output: {
      schema: { type: 'string' },
      render: function (_args, value) { return [{ type: 'text', text: value }] },
    },
    execute: async function (args, exec) {
      const source = args !== undefined && args !== null && typeof args === 'object' ? args : {}
      const request = {
        action: typeof source.action === 'string' ? source.action : '',
        runtime: source.runtime,
        version: source.version,
        filter: source.filter,
        tool: source.tool,
        flags: source.flags,
        args: source.raw,
        url: source.url,
        sha256: source.sha256,
        timeoutMs: source.timeoutMs,
      }
      const result = await invoke(state, request, exec !== undefined ? exec.signal : undefined)
      return JSON.stringify(result)
    },
  })
}

/**
 * Load the plugin: register the slash command and tool, publish DSH_PLOYENV_*
 * facts to session shell tools, and warm the config/environment cache.
 * @param ctx Cordis context owning the registrations (disposed with the plugin).
 * @param config this row's config from cordis.patch.yml, unvalidated.
 */
export function apply(ctx, config) {
  const options = normalizeConfig(config)
  const configuredBin = optionalText(options.bin)
  const resolved = resolveBinary(configuredBin)
  options.bin = resolved.bin
  const ownsRoot = resolved.bundled || resolved.source === 'setup'
  // A bundled or setup-installed copy carries its own portable data root, but an
  // explicit PLOYENV_ROOT still wins: the operator asked for that root globally,
  // and sharing it keeps runtimes across plugin reinstalls.
  // Read the operator's root with the same trim the CLI applies; a blank value
  // must not create a whitespace-named directory or be echoed as the root.
  const ambientRaw = process.env.PLOYENV_ROOT
  const ambientRoot = typeof ambientRaw === 'string' && ambientRaw.trim() !== '' ? ambientRaw.trim() : undefined
  const defaultRoot = ambientRoot !== undefined ? ambientRoot : PLUGIN_DATA_ROOT
  const root = ownsRoot ? (options.root !== undefined ? options.root : defaultRoot) : options.root
  if (root !== undefined) {
    try { mkdirSync(root, { recursive: true }) } catch (error) { void error }
  }
  const state = {
    options: options,
    configuredBin: configuredBin,
    binSource: resolved.source,
    root: root,
    childEnv: root !== undefined ? Object.assign({}, process.env, { PLOYENV_ROOT: root }) : undefined,
    cliVersion: undefined,
    cliProbe: undefined,
    config: undefined,
    configProbe: undefined,
    environment: undefined,
    environmentProbe: undefined,
    originalPath: undefined,
    pathInjected: false,
    injectedDirs: [],
  }
  registerShellEnvFacts(ctx, state)
  registerCommand(ctx, state)
  registerTool(ctx, state)
  const warm = Promise.all([probeConfig(state, options.timeoutMs), probeEnvironment(state, options.timeoutMs, false)])
  if (options.injectPathAtLoad === true) {
    void warm.then(function (results) {
      const dirs = environmentPaths(results[1])
      if (dirs.length > 0) injectPaths(state, dirs)
    }, function () { return undefined })
  } else {
    void warm.then(function () { return undefined }, function () { return undefined })
  }
}

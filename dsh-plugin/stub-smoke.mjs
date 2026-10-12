// dsh-plugin/stub-smoke.mjs - plugin contract smoke test (hermetic).
//
// The plugin drives the ployenv CLI through child_process.spawn.  A confined DSH
// session denies piped spawn (spawn EPERM) and a CI runner has no ployenv binary,
// so this test replaces spawn BEFORE the plugin is imported: every CLI invocation
// is recorded and answered from a canned table.  The contract is then checked the
// same way inside a sandbox, on a developer machine and on CI.
//
// Run:  node dsh-plugin/stub-smoke.mjs
// Exit: 0 = every clause passed, 1 = at least one clause failed.

import assert from 'node:assert'
import { EventEmitter } from 'node:events'
import { createRequire } from 'node:module'
import { Readable } from 'node:stream'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const pluginPath = process.env.PLOYENV_PLUGIN || fileURLToPath(new URL('./index.js', import.meta.url))
const sourceText = readFileSync(pluginPath, 'utf8')

const work = mkdtempSync(join(tmpdir(), 'ployenv-smoke-'))
const dataRoot = join(work, 'root')
const injected = [join(dataRoot, 'versions', 'node', '22.23.3'), join(dataRoot, 'versions', 'go', '1.27.2', 'bin')]
const fakeBin = join(work, process.platform === 'win32' ? 'ployenv.exe' : 'ployenv')
writeFileSync(fakeBin, '')

const calls = []

function answer(args) {
  const head = args.find((a) => !a.startsWith('-')) || ''
  if (args.includes('--version')) return { code: 0, out: 'ployenv 0.3.0 (windows/amd64)\n' }
  switch (head) {
    case 'list': return { code: 0, out: 'node (Node.js) 22.23.3\ngo (Go) 1.27.2\n' }
    case 'current': return { code: 0, out: JSON.stringify([{ lang: 'node', version: '22.23.3', source: 'global', dir: injected[0] }], null, 2) }
    case 'env': return { code: 0, out: JSON.stringify({ path: injected, vars: { GOROOT: join(dataRoot, 'versions', 'go', '1.27.2') } }, null, 2) }
    case 'config': return { code: 0, out: JSON.stringify({ root: dataRoot, defaults: { node: '22.23.3' }, mirrors: {} }, null, 2) }
    case 'doctor': return { code: 0, out: JSON.stringify({ version: '0.2.0', exe: fakeBin, data_dir: dataRoot, writable: true }, null, 2) }
    case 'which': return { code: 0, out: join(injected[0], 'node.exe') + '\n' }
    case 'status': return { code: 0, out: 'ployenv 0.2.0 (windows/amd64)\n' }
    case 'support': return { code: 0, out: 'ployenv manages 4 runtimes\n' }
    case 'cache': return { code: 0, out: 'cache dir  ' + join(dataRoot, 'cache') + '\n' }
    default: return { code: 1, err: 'error: unknown command "' + head + '"\n' }
  }
}

// Patch the CJS export before the plugin's ESM import snapshots it.
const childProcess = createRequire(import.meta.url)('node:child_process')
childProcess.spawn = function fakeSpawn(bin, argv) {
  const args = Array.isArray(argv) ? argv.map(String) : []
  calls.push({ bin, argv: args })
  const reply = answer(args)
  const child = new EventEmitter()
  child.stdout = new Readable({ read() {} })
  child.stderr = new Readable({ read() {} })
  child.stdin = { end() {} }
  child.kill = () => {}
  setImmediate(() => {
    if (reply.err) child.stderr.push(reply.err)
    if (reply.out) child.stdout.push(reply.out)
    child.stdout.push(null)
    child.stderr.push(null)
    child.emit('close', reply.code)
  })
  return child
}

const registered = { commands: [], tools: [], shellEnv: [] }
const ctx = {
  get(name) {
    if (name === 'shellEnv') return { register: (c) => { registered.shellEnv.push(c); return () => {} } }
    return undefined
  },
  commands: { register: (c) => { registered.commands.push(c); return () => {} } },
  tools: { register: (t) => { registered.tools.push(t); return () => {} } },
}

const results = []
async function clause(id, title, fn) {
  try {
    const detail = await fn()
    results.push({ id, title, ok: true, detail: detail === undefined ? '' : String(detail) })
  } catch (error) {
    results.push({ id, title, ok: false, detail: String(error.message).split('\n')[0] })
  }
}

const FORBIDDEN_HEADS = ['root', 'shims', 'setup']
const FORBIDDEN_TOKENS = ['apply', 'prune-legacy', 'remove-flyenv']
const KNOWN_HEADS = new Set(['--version', 'list', 'available', 'install', 'uninstall', 'use', 'current', 'which', 'env', 'config', 'doctor', 'support', 'cache', 'shell'])

const mod = await import(pathToFileURL(pluginPath).href)
let tool
let command
const actionEnum = () => (tool && tool.parameters && tool.parameters.properties && tool.parameters.properties.action && tool.parameters.properties.action.enum) || []
const run = async (args) => JSON.parse(await tool.execute(args, { signal: undefined }))

await clause('A1-apply', 'apply(ctx, config) registers one tool and one slash command', () => {
  assert.strictEqual(typeof mod.apply, 'function', 'apply must be exported')
  mod.apply(ctx, { bin: fakeBin })
  tool = registered.tools[0]
  command = registered.commands[0]
  assert.ok(tool, 'no tool registered')
  assert.ok(command, 'no slash command registered')
  return 'tool=' + tool.name
})

await clause('A2-1-actions', 'action enum exposes path_on/path_off and no root/shims action', () => {
  const list = actionEnum()
  assert.ok(list.includes('path_on'), 'action enum must contain path_on; got: ' + list.join(','))
  assert.ok(list.includes('path_off'), 'action enum must contain path_off; got: ' + list.join(','))
  const stale = list.filter((a) => FORBIDDEN_HEADS.includes(String(a)) || String(a).startsWith('shims'))
  assert.deepStrictEqual(stale, [], 'stale actions still advertised: ' + stale.join(','))
  return list.join(',')
})

await clause('A2-2-argv', 'every CLI call uses a known command and never root/shims/setup', async () => {
  calls.length = 0
  for (const args of [{ action: 'list' }, { action: 'current' }, { action: 'env' }, { action: 'doctor' }, { action: 'status' }, { action: 'which', tool: 'node' }, { action: 'config' }, { action: 'bin' }]) {
    await tool.execute(args, { signal: undefined })
  }
  assert.ok(calls.length > 0, 'no CLI call recorded: the plugin must invoke the resolved binary')
  const badHeads = calls.map((c) => c.argv[0]).filter((h) => FORBIDDEN_HEADS.includes(h) || !KNOWN_HEADS.has(h))
  assert.deepStrictEqual(badHeads, [], 'unexpected argv head: ' + badHeads.join(','))
  const badTokens = calls.filter((c) => c.argv.some((t) => FORBIDDEN_TOKENS.includes(t)))
  assert.deepStrictEqual(badTokens, [], 'forbidden flag in argv: ' + JSON.stringify(badTokens.map((c) => c.argv)))
  return calls.length + ' calls, none forbidden'
})

await clause('A2-3-facts', 'shellEnv declares BIN/ROOT/PATHS and no SHIMS', () => {
  assert.strictEqual(registered.shellEnv.length, 1, 'exactly one shellEnv contribution')
  const keys = Object.keys(registered.shellEnv[0].variables || {}).sort()
  assert.deepStrictEqual(keys, ['DSH_PLOYENV_BIN', 'DSH_PLOYENV_PATHS', 'DSH_PLOYENV_ROOT'], 'facts were: ' + keys.join(','))
  return keys.join(',')
})

await clause('A2-4-bool-flags', 'BOOL_FLAGS keeps only flags the 0.2.0 CLI accepts', () => {
  const match = /BOOL_FLAGS\s*=\s*new Set\(\[([^\]]*)\]\)/.exec(sourceText)
  assert.ok(match, 'BOOL_FLAGS not found in the plugin source')
  const flags = match[1].split(',').map((s) => s.trim().replace(/^['"]|['"]$/g, '')).filter(Boolean)
  const stale = flags.filter((f) => FORBIDDEN_TOKENS.includes(f))
  assert.deepStrictEqual(stale, [], 'stale flags still declared: ' + stale.join(','))
  return flags.join(',')
})

const originalPath = process.env.PATH || ''
let pathModified = false

await clause('D4-path-on', 'path_on prepends the env --shell json path list exactly once', async () => {
  calls.length = 0
  const first = await run({ action: 'path_on' })
  assert.strictEqual(typeof first.exitCode, 'number', 'path_on must report a numeric exit code, got ' + JSON.stringify(first.exitCode))
  assert.strictEqual(first.exitCode, 0, 'path_on failed: ' + String(first.stderr || '').slice(0, 120))
  assert.ok(calls.some((c) => c.argv[0] === 'env' && c.argv.includes('json')), 'path_on must read the PATH list from env --shell json')
  const sep = process.platform === 'win32' ? ';' : ':'
  const entries = (process.env.PATH || '').split(sep).filter(Boolean)
  for (const dir of injected) {
    const hits = entries.filter((e) => e.toLowerCase() === dir.toLowerCase()).length
    assert.strictEqual(hits, 1, 'injected dir appears ' + hits + ' times after one path_on: ' + dir)
  }
  assert.deepStrictEqual(entries.slice(0, injected.length), injected, 'injected dirs must be prepended in order')
  pathModified = true
  await run({ action: 'path_on' })
  const after = (process.env.PATH || '').split(sep).filter(Boolean)
  for (const dir of injected) {
    const hits = after.filter((e) => e.toLowerCase() === dir.toLowerCase()).length
    assert.strictEqual(hits, 1, 'second path_on duplicated ' + dir)
  }
  return 'injected=' + injected.length + ', idempotent'
})

await clause('D4-path-off', 'path_off restores the original PATH byte-for-byte', async () => {
  assert.ok(pathModified, 'path_on never changed PATH: the restore check would pass vacuously')
  const off = await run({ action: 'path_off' })
  assert.strictEqual(process.env.PATH, originalPath, 'PATH was not restored byte-for-byte')
  return 'restored, exit=' + JSON.stringify(off.exitCode)
})

await clause('D6-unknown-action', 'an unknown action reports a numeric non-zero exit code', async () => {
  const res = await run({ action: 'definitely-not-an-action' })
  assert.strictEqual(typeof res.exitCode, 'number', 'unknown action must report a numeric exit code, got ' + JSON.stringify(res.exitCode))
  assert.notStrictEqual(res.exitCode, 0, 'unknown action must not report success')
  return 'exitCode=' + res.exitCode
})

await clause('A4-no-installer', 'no registry / machine PATH / setx call in the plugin source', () => {
  const bad = ['reg add', 'setx ', '[Environment]::SetEnvironmentVariable', 'HKEY_LOCAL_MACHINE']
  const found = bad.filter((needle) => sourceText.includes(needle))
  assert.deepStrictEqual(found, [], 'forbidden source fragment: ' + found.join(' | '))
  return 'clean'
})

await clause('NFR-306-bin-missing', 'bin fails loudly when the resolved executable is missing', async () => {
  const reg = { tools: [], commands: [], shellEnv: [] }
  const ctx2 = { get: () => undefined, commands: { register: (c) => reg.commands.push(c) }, tools: { register: (t) => reg.tools.push(t) } }
  const mod2 = await import(pathToFileURL(pluginPath).href + '?missing=1')
  mod2.apply(ctx2, { bin: join(work, 'does-not-exist.exe') })
  const missing = JSON.parse(await reg.tools[0].execute({ action: 'bin' }, { signal: undefined }))
  assert.strictEqual(missing.exitCode, 1, 'a missing resolved binary must fail; got exitCode ' + JSON.stringify(missing.exitCode))
  return 'exitCode=1'
})

await clause('FR-305-status-echoes-cli', 'status echoes cli version and the resolution source', async () => {
  const st = JSON.parse(await tool.execute({ action: 'status' }, { signal: undefined }))
  assert.strictEqual(typeof st.cli, 'string', 'status must echo cli; got ' + JSON.stringify(st.cli))
  assert.strictEqual(typeof st.binSource, 'string', 'status must echo binSource; got ' + JSON.stringify(st.binSource))
  return 'cli=' + st.cli + ' source=' + st.binSource
})

await clause('A5-help', 'the slash command answers help without calling the CLI', async () => {
  calls.length = 0
  const help = await command.handler({ rawInput: 'help', signal: undefined })
  assert.strictEqual(help.kind, 'success', 'help must succeed')
  assert.strictEqual(calls.length, 0, 'help must not call the CLI')
  return String(help.text || '').split('\n')[0].slice(0, 60)
})

process.env.PATH = originalPath
rmSync(work, { recursive: true, force: true })

const failed = results.filter((r) => !r.ok)
console.log('=== dsh-plugin contract smoke ===')
console.log('plugin: ' + pluginPath)
console.log('')
for (const r of results) console.log((r.ok ? 'PASS  ' : 'FAIL  ') + r.id.padEnd(22) + r.detail)
console.log('')
console.log((results.length - failed.length) + '/' + results.length + ' clauses passed')
if (failed.length > 0) {
  console.log('note: FAIL before a migration is the expected RED baseline.')
  process.exit(1)
}
console.log('SMOKE OK')

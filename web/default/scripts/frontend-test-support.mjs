// Read-only hook simulation. The stdin runner can supply precompiled sources
// to keep Node and esbuild memory use separate. No DOM or network.
import { readFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { createRequire } from 'node:module'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)
const buildRequire = createRequire(require.resolve('@rsbuild/core'))
const esbuild = resolve(dirname(buildRequire.resolve('esbuild/package.json')), 'bin/esbuild')
export const webRoot = fileURLToPath(new URL('../../', import.meta.url))
const compiled = new Map(Object.entries(globalThis.__frontendTestSource || {}))

export function loadSource(path, imports, globals = {}) {
  const absolute = resolve(webRoot, path)
  let code = compiled.get(absolute)
  if (!code) {
    code = execFileSync(esbuild, [
      `--loader=${path.endsWith('.tsx') ? 'tsx' : path.endsWith('.ts') ? 'ts' : 'jsx'}`,
      '--format=cjs', '--jsx=automatic', '--log-level=error',
    ], { input: readFileSync(absolute), encoding: 'utf8', maxBuffer: 1 << 20 })
    compiled.set(absolute, code)
  }
  const module = { exports: {} }
  const localRequire = (name) => {
    if (Object.hasOwn(imports, name)) return imports[name]
    throw new Error(`Unmocked import ${name} in ${path}`)
  }
  new Function('require', 'module', 'exports', ...Object.keys(globals), code)(
    localRequire, module, module.exports, ...Object.values(globals)
  )
  return module.exports
}

export function hookHarness(context = () => undefined) {
  let active
  const equal = (left, right) => left && right && left.length === right.length &&
    left.every((value, index) => Object.is(value, right[index]))
  const effect = (callback, deps) => {
    const host = active
    const index = host.cursor++
    const previous = host.cells[index]
    if (!previous || !equal(previous.deps, deps)) {
      host.effects.push(() => {
        previous?.cleanup?.()
        host.cells[index] = { deps, cleanup: callback() }
      })
    }
  }
  const react = {
    useRef(value) {
      const index = active.cursor++
      return active.cells[index] ??= { current: value }
    },
    useState(initial) {
      const host = active
      const index = host.cursor++
      const cell = host.cells[index] ??= { value: typeof initial === 'function' ? initial() : initial }
      return [cell.value, (next) => {
        cell.value = typeof next === 'function' ? next(cell.value) : next
      }]
    },
    useCallback(callback, deps) {
      const index = active.cursor++
      const previous = active.cells[index]
      if (!previous || !equal(previous.deps, deps)) active.cells[index] = { deps, callback }
      return active.cells[index].callback
    },
    useLayoutEffect: effect,
    // UI list loads are tested separately from draft ownership.
    useEffect: () => {},
    useContext: () => context(),
    useId: () => 'test',
    useSyncExternalStore(subscribe, snapshot) {
      const host = active
      effect(() => subscribe(() => { host.notifications++ }), [subscribe])
      return snapshot()
    },
  }
  react.default = react
  return {
    react,
    host(render) {
      const host = { cells: [], cursor: 0, effects: [], notifications: 0 }
      return Object.assign(host, {
        render(...args) {
          host.cursor = 0
          host.effects = []
          active = host
          const result = render(...args)
          active = undefined
          host.effects.forEach((run) => run())
          return result
        },
        unmount() { host.cells.forEach((cell) => cell?.cleanup?.()) },
      })
    },
  }
}

export function accountEnvironment() {
  class Storage {
    values = new Map()
    getItem(key) { return this.values.get(key) ?? null }
    setItem(key, value) { this.values.set(key, String(value)) }
    removeItem(key) { this.values.delete(key) }
    clear() { this.values.clear() }
  }
  const localStorage = new Storage()
  let state = { auth: { user: { id: 1, role: 100 } } }
  const subscribers = new Set()
  const events = new Set()
  const useAuthStore = (selector) => selector(state)
  useAuthStore.getState = () => state
  useAuthStore.subscribe = (listener) => {
    subscribers.add(listener)
    return () => subscribers.delete(listener)
  }
  const window = {
    localStorage,
    addEventListener: (name, listener) => { if (name === 'storage') events.add(listener) },
    removeEventListener: (name, listener) => events.delete(listener),
  }
  const rawUser = (id) => id === null ? null : JSON.stringify({ id, role: 100 })
  const remote = (id, notify = false) => {
    const oldValue = localStorage.getItem('user')
    const newValue = rawUser(id)
    if (newValue === null) localStorage.values.delete('user')
    else localStorage.values.set('user', newValue)
    localStorage.values.set('uid', String(id ?? ''))
    if (notify) events.forEach((listener) => listener({ key: 'user', oldValue, newValue, storageArea: localStorage }))
  }
  remote(1)
  return {
    globals: { Storage, localStorage, window }, useAuthStore, remote,
    context: () => [{ user: state.auth.user }],
    setLocalUser(user) {
      const previous = state
      state = { auth: { user } }
      subscribers.forEach((listener) => listener(state, previous))
    },
    event(oldId, newId) {
      events.forEach((listener) => listener({ key: 'user', oldValue: rawUser(oldId), newValue: rawUser(newId), storageArea: localStorage }))
    },
  }
}

export const jsx = (type, props, key) => ({ type, props: props || {}, key })
export const jsxRuntime = { jsx, jsxs: jsx, Fragment: 'Fragment' }
export function nodes(tree, predicate) {
  if (Array.isArray(tree)) return tree.flatMap((node) => nodes(node, predicate))
  if (!tree || typeof tree !== 'object') return []
  return [...(predicate(tree) ? [tree] : []), ...nodes(tree.props?.children, predicate)]
}
export const deferred = () => {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

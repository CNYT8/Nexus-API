import assert from 'node:assert/strict'
import { test } from 'node:test'
import axios from 'axios/dist/browser/axios.cjs'
import {
  accountEnvironment, deferred, hookHarness, jsxRuntime, loadSource, nodes,
} from './frontend-test-support.mjs'

const errors = {
  isServerErrorCancelled: axios.isCancel,
  getServerErrorMessage: (cause, fallback) => cause?.message || fallback,
}
const t = (key) => key
const i18next = { t, language: 'en' }

function setup(kind) {
  const env = accountEnvironment()
  const harness = hookHarness(env.context)
  const common = {
    react: harness.react, axios, 'react/jsx-runtime': jsxRuntime,
    'react-i18next': { useTranslation: () => ({ t }) },
    '@/stores/auth-store': { useAuthStore: env.useAuthStore },
    '../../../context/User': { UserContext: {} },
    '@/lib/server-error-message': errors,
    '../../../helpers/server-error-message': errors,
  }
  const sessions = loadSource(kind === 'default'
    ? 'default/src/features/playground/hooks/use-account-session.ts'
    : 'classic/src/hooks/playground/accountSession.js', common, env.globals)
  const observer = harness.host(sessions.useAccountSession)
  const session = observer.render()
  const hookModule = loadSource(kind === 'default'
    ? 'default/src/features/system-settings/operations/use-script-plugin-request.ts'
    : 'classic/src/pages/Setting/Operation/useScriptPluginRequests.js', common)
  const hook = kind === 'default' ? hookModule.useScriptPluginRequest : hookModule.default
  const hosts = []
  const lane = (write = true) => {
    const host = harness.host(() => hook(session, !write))
    hosts.push(host)
    const result = host.render()
    return {
      host,
      run(action, apply = () => {}, reject = () => {}) {
        return kind === 'default' ? result.run(action, apply, 'failed')
          : result.run(write ? 'mutation' : 'list', action, apply, reject)
      },
    }
  }
  return {
    env, harness, common, sessions, observer, session, hookModule, lane,
    cleanup() { hosts.forEach((host) => host.unmount()); observer.unmount() },
  }
}

for (const kind of ['default', 'classic']) {
  test(`${kind}: submit rereads storage before the storage event`, async () => {
    const fixture = setup(kind)
    try {
      const lane = fixture.lane()
      fixture.env.remote(2) // The old Zustand/Context user is deliberately still A.
      let calls = 0
      await lane.run(async () => { calls++ })
      assert.equal(calls, 0)
      assert.equal(fixture.session.isCurrent(), false)
      assert.ok(fixture.observer.notifications > 0)
      // Returning to the same ID cannot revive the discarded session.
      fixture.env.remote(1)
      await lane.run(async () => { calls++ })
      assert.equal(calls, 0)
    } finally { fixture.cleanup() }
  })

  test(`${kind}: invalidation aborts every lane and ignores late success/error`, async () => {
    const fixture = setup(kind)
    try {
      const read = fixture.lane(false)
      const write = fixture.lane()
      const first = deferred(), second = deferred()
      const signals = []
      let applied = 0, rejected = 0
      const a = read.run((config) => { signals.push(config.signal); return first.promise }, () => applied++, () => rejected++)
      const b = write.run((config) => { signals.push(config.signal); return second.promise }, () => applied++, () => rejected++)
      fixture.env.remote(2, true)
      assert.ok(signals.every((signal) => signal.aborted))
      first.resolve('late read')
      second.reject(new Error('late failure'))
      await Promise.all([a, b])
      assert.equal(applied, 0)
      assert.equal(rejected, 0)
      for (const lane of [read, write]) {
        const state = lane.host.render()
        if (kind === 'default') { assert.equal(state.error, ''); assert.equal(state.busy, false) }
        else assert.deepEqual(state.pending, {})
      }
    } finally { fixture.cleanup() }
  })

  test(`${kind}: late reply discovers an eventless account change`, async () => {
    const fixture = setup(kind)
    try {
      const response = deferred()
      let applied = false, signal
      const pending = fixture.lane().run((config) => { signal = config.signal; return response.promise }, () => { applied = true })
      fixture.env.remote(null)
      response.resolve('old result')
      await pending
      assert.equal(applied, false)
      assert.equal(signal.aborted, true)
    } finally { fixture.cleanup() }
  })

  test(`${kind}: Axios dispatch rechecks the session and pins the owner header`, async () => {
    const fixture = setup(kind)
    try {
      let api
      if (kind === 'default') {
        api = loadSource('default/src/lib/api.ts', {
          ...fixture.common, i18next,
          '@/i18n/languages': { normalizeInterfaceLanguage: (value) => value },
          '@/lib/server-error-message': { ...errors, getServerMessage: () => '', notifyServerError() {} },
          '@/lib/web-risk': { isWebRiskVerificationRequired: () => false },
        }, fixture.env.globals).api
      } else {
        api = axios.create()
        api.interceptors.request.use((config) => config)
      }
      const sent = []
      api.defaults.adapter = async (config) => {
        sent.push(config)
        return { data: { success: true }, status: 200, statusText: 'OK', headers: {}, config }
      }
      // Default's shared interceptor reads uid=B while user still belongs to A.
      fixture.env.globals.localStorage.setItem('uid', '2')
      const lane = fixture.lane()
      await lane.run((config) => api.post('/api/task/script_plugin', { source: 'A draft' }, config))
      assert.equal(sent.length, 1)
      assert.equal(sent[0].headers.get('New-Api-User'), '1')
      assert.deepEqual(JSON.parse(sent[0].data), { source: 'A draft' })
      // Change user after run() accepted the action, before Axios dispatch.
      const pending = lane.run((config) => {
        const result = api.post('/api/task/script_plugin', { source: 'old draft' }, config)
        fixture.env.remote(2)
        return result
      })
      await pending
      assert.equal(sent.length, 1)
      assert.equal(fixture.session.isCurrent(), false)
    } finally { fixture.cleanup() }
  })

  test(`${kind}: serialized writes, replaced reads, and unmount stay safe`, async () => {
    const fixture = setup(kind)
    try {
      const write = fixture.lane()
      const response = deferred()
      let calls = 0
      const pending = write.run(() => { calls++; return response.promise })
      await write.run(async () => { calls++ })
      assert.equal(calls, 1)
      response.resolve()
      await pending
      const read = fixture.lane(false), old = deferred(), next = deferred()
      let oldSignal, applied = ''
      const a = read.run((config) => { oldSignal = config.signal; return old.promise }, () => { applied = 'old' })
      const b = read.run(() => next.promise, () => { applied = 'new' })
      assert.equal(oldSignal.aborted, true)
      old.resolve(); next.resolve()
      await Promise.all([a, b])
      assert.equal(applied, 'new')
      write.host.unmount()
      await write.run(async () => { calls++ })
      assert.equal(calls, 1)
    } finally { fixture.cleanup() }
  })

  test(`${kind}: root role is still required`, async () => {
    const fixture = setup(kind)
    try {
      const lane = fixture.lane()
      fixture.env.setLocalUser({ id: 1, role: 10 })
      lane.host.render()
      let calls = 0
      await lane.run(async () => { calls++ })
      assert.equal(calls, 0)
    } finally { fixture.cleanup() }
  })

  test(`${kind}: logout/login remount clears source, bindings, and dialogs`, () => {
    const fixture = setup(kind)
    const uiHosts = []
    try {
      const { common, harness, sessions, hookModule } = fixture
      let Panel, Binding
      if (kind === 'default') {
        const componentImports = {
          ...common,
          '@/features/playground/hooks/use-account-session': sessions,
          './use-script-plugin-request': hookModule,
        }
        for (const name of ['badge', 'button', 'label', 'input', 'textarea', 'table']) {
          const names = name === 'table' ? ['Table', 'TableBody', 'TableCell', 'TableHead', 'TableHeader', 'TableRow']
            : [name === 'textarea' ? 'Textarea' : name[0].toUpperCase() + name.slice(1)]
          componentImports[`@/components/ui/${name}`] = Object.fromEntries(names.map((value) => [value, value]))
        }
        componentImports['@/components/confirm-dialog'] = { ConfirmDialog: 'ConfirmDialog' }
        componentImports['./script-plugins-api'] = loadSource(
          'default/src/features/system-settings/operations/script-plugins-api.ts', { '@/lib/api': { api: {} } }
        )
        Binding = loadSource('default/src/features/system-settings/operations/script-plugin-bindings.tsx', componentImports).ScriptPluginBindings
        componentImports['./script-plugin-bindings'] = { ScriptPluginBindings: Binding }
        Panel = loadSource('default/src/features/system-settings/operations/script-plugins-panel.tsx', componentImports).ScriptPluginsPanel
      } else {
        const names = ['Button', 'Card', 'Input', 'Modal', 'Popconfirm', 'Table', 'Tag', 'TextArea']
        Panel = loadSource('classic/src/pages/Setting/Operation/SettingsScriptPlugins.jsx', {
          ...common,
          '@douyinfe/semi-ui': { ...Object.fromEntries(names.map((name) => [name, name])), Typography: { Text: 'Text', Title: 'Title' } },
          '../../../helpers': { API: {} },
          '../../../hooks/playground/accountSession': sessions,
          './useScriptPluginRequests': hookModule,
        }).default
      }
      const wrapper = harness.host(Panel)
      uiHosts.push(wrapper)
      let owner = wrapper.render()
      const firstKey = owner.key
      const content = harness.host(() => owner.type(owner.props))
      uiHosts.push(content)
      let tree = content.render()
      const field = (value, suffix) => nodes(value, (node) => node.props.id?.endsWith(suffix))[0]
      const change = (node, value) => node.props.onChange(kind === 'default' ? { target: { value } } : value)
      change(field(tree, '-source'), 'private source')
      tree = content.render()
      assert.equal(field(tree, '-source').props.value, 'private source')
      let bindingHost = content
      if (kind === 'default') {
        nodes(tree, (node) => node.type === 'Button' && node.props.children === 'Create immutable revision')[0].props.onClick()
        tree = content.render()
        assert.equal(nodes(tree, (node) => node.type === 'ConfirmDialog')[0].props.open, true)
        const binding = nodes(tree, (node) => node.type === Binding)[0]
        bindingHost = harness.host(() => binding.type(binding.props))
        uiHosts.push(bindingHost)
      }
      let bindingTree = bindingHost.render()
      change(field(bindingTree, '-channel'), '17')
      bindingTree = bindingHost.render()
      change(field(bindingTree, '-model'), 'private-model')
      bindingTree = bindingHost.render()
      assert.equal(field(bindingTree, '-model').props.value, 'private-model')
      const unbindLabel = kind === 'default' ? 'Unbind' : '确认解绑'
      nodes(bindingTree, (node) => node.type === 'Button' && node.props.children === unbindLabel)[0].props.onClick()
      bindingTree = bindingHost.render()
      const dialogType = kind === 'default' ? 'ConfirmDialog' : 'Modal'
      assert.equal(nodes(bindingTree, (node) => node.type === dialogType)[0].props[kind === 'default' ? 'open' : 'visible'], true)

      // Same-ID login can be batched: old/new event values still mark a boundary.
      fixture.env.remote(1)
      fixture.env.event(1, null)
      owner = wrapper.render()
      assert.notEqual(owner.key, firstKey)
      content.unmount()
      if (bindingHost !== content) bindingHost.unmount()
      const fresh = harness.host(() => owner.type(owner.props))
      uiHosts.push(fresh)
      tree = fresh.render()
      assert.equal(field(tree, '-source').props.value, '')
      if (kind === 'default') {
        assert.equal(nodes(tree, (node) => node.type === 'ConfirmDialog')[0].props.open, false)
        const binding = nodes(tree, (node) => node.type === Binding)[0]
        const freshBinding = harness.host(() => binding.type(binding.props))
        uiHosts.push(freshBinding)
        tree = freshBinding.render()
      }
      assert.equal(field(tree, '-channel').props.value, '')
      assert.equal(field(tree, '-model').props.value, '')
      assert.equal(nodes(tree, (node) => node.type === dialogType)[0].props[kind === 'default' ? 'open' : 'visible'], false)
      fixture.env.remote(2, true)
      const blocked = wrapper.render()
      assert.ok(blocked === null || blocked.type === 'p')
    } finally {
      uiHosts.reverse().forEach((host) => host.unmount())
      fixture.cleanup()
    }
  })
}

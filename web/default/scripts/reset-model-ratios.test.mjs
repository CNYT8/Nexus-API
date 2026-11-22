import assert from 'node:assert/strict'
import { test } from 'node:test'
import { deferred, loadSource } from './frontend-test-support.mjs'

function setup({ write, read, pricing } = {}) {
  const calls = []
  const getSystemOptions = async () => { calls.push('read'); return read?.() ?? [] }
  const { resetModelRatiosAndRefresh } = loadSource(
    'default/src/features/system-settings/models/reset-model-ratios.ts', {
      i18next: { t: (key) => key },
      '../api': {
        resetModelRatios: async () => { calls.push('write'); return write?.() ?? { success: true } },
        getSystemOptions,
      },
    }
  )
  const client = {
    async cancelQueries({ queryKey }) { calls.push(`cancel:${queryKey[0]}`) },
    async invalidateQueries(options, behavior) {
      calls.push(`invalidate:${options.queryKey[0]}`)
      if (options.queryKey[0] === 'system-options') assert.equal(options.refetchType, 'none')
      else {
        assert.equal(behavior.throwOnError, true)
        await pricing?.()
      }
    },
    async fetchQuery(options) {
      assert.deepEqual(options.queryKey, ['system-options'])
      assert.equal(options.staleTime, 0)
      assert.equal(options.retry, false)
      assert.equal(options.queryFn, getSystemOptions)
      return options.queryFn()
    },
  }
  return { calls, run: () => resetModelRatiosAndRefresh(client) }
}

const expected = ['write', 'cancel:system-options', 'invalidate:system-options', 'read', 'cancel:pricing', 'invalidate:pricing']

test('ratio reset waits for confirmed options and pricing refresh', async () => {
  const read = deferred(), pricing = deferred()
  const fixture = setup({ read: () => read.promise, pricing: () => pricing.promise })
  let settled = false
  const pending = fixture.run().then(() => { settled = true })
  // Drain a bounded number of microtasks; no clock/timer dependency.
  for (let index = 0; index < 12; index++) await Promise.resolve()
  assert.equal(settled, false)
  assert.deepEqual(fixture.calls, expected.slice(0, 4))
  read.resolve([])
  for (let index = 0; index < 12; index++) await Promise.resolve()
  assert.equal(settled, false)
  assert.deepEqual(fixture.calls, expected)
  pricing.resolve()
  await pending
  assert.equal(settled, true)
})

test('ratio write business failure still reconciles both caches and rejects', async () => {
  const fixture = setup({ write: () => ({ success: false, message: 'write rejected' }) })
  await assert.rejects(fixture.run(), /write rejected/)
  assert.deepEqual(fixture.calls, expected)
})

test('ratio write transport failure still reconciles both caches and rejects', async () => {
  const fixture = setup({ write: () => { throw new Error('write disconnected') } })
  await assert.rejects(fixture.run(), /write disconnected/)
  assert.deepEqual(fixture.calls, expected)
})

test('ratio refresh failure invalidates pricing and never reports success', async () => {
  const fixture = setup({ read: () => { throw new Error('read disconnected') } })
  await assert.rejects(fixture.run(), /Failed to refresh settings/)
  assert.deepEqual(fixture.calls, expected)
})

test('ratio pricing failure rejects even after confirmed options', async () => {
  const fixture = setup({ pricing: () => { throw new Error('pricing disconnected') } })
  await assert.rejects(fixture.run(), /Failed to refresh settings/)
  assert.deepEqual(fixture.calls, expected)
})

test('ratio errors retain write failure and deduplicate refresh failures', async () => {
  const fixture = setup({
    write: () => ({ success: false, message: 'write rejected' }),
    read: () => { throw new Error('read disconnected') },
    pricing: () => { throw new Error('pricing disconnected') },
  })
  await assert.rejects(fixture.run(), { message: 'write rejected\nFailed to refresh settings' })
  assert.deepEqual(fixture.calls, expected)
})

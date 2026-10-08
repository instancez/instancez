import { test } from 'node:test'
import assert from 'node:assert/strict'
import worker from './index.js'

function run(path, { method = 'GET', headers = {}, fail = false } = {}) {
  const calls = []
  const waits = []
  globalThis.fetch = async (url, init) => {
    calls.push({ url, body: JSON.parse(init.body), headers: init.headers })
    if (fail) throw new Error('umami down')
    return new Response('ok')
  }
  const req = new Request('https://get.instancez.ai' + path, { method, headers })
  const ctx = { waitUntil: (p) => waits.push(p) }
  return worker.fetch(req, {}, ctx).then(async (res) => {
    await Promise.all(waits)
    return { res, calls }
  })
}

test('installer GET sends one sh event with ref and UA', async () => {
  const { res, calls } = await run('/?ref=reddit-selfhosted', { headers: { 'user-agent': 'curl/8.0', referer: 'https://reddit.com/' } })
  assert.equal(res.status, 200)
  assert.equal(calls.length, 1)
  const p = calls[0].body.payload
  assert.equal(p.name, 'install_script')
  assert.equal(p.url, '/?ref=reddit-selfhosted')
  assert.equal(p.data.os, 'sh')
  assert.equal(p.data.ua, 'curl/8.0')
  assert.equal(p.referrer, 'https://reddit.com/')
  assert.match(calls[0].headers['user-agent'], /Mozilla/)
})

test('windows path tags os=ps1', async () => {
  const { calls } = await run('/windows')
  assert.equal(calls[0].body.payload.data.os, 'ps1')
})

test('404 and HEAD are not tracked', async () => {
  assert.equal((await run('/nope')).calls.length, 0)
  const head = await run('/', { method: 'HEAD' })
  assert.equal(head.calls.length, 0)
})

test('umami failure never breaks the installer', async () => {
  const { res } = await run('/', { fail: true })
  assert.equal(res.status, 200)
  assert.match(await res.text(), /\S/)
})

test('missing ctx still serves', async () => {
  globalThis.fetch = async () => new Response('ok')
  const res = await worker.fetch(new Request('https://get.instancez.ai/'), {}, undefined)
  assert.equal(res.status, 200)
})

test('very long user agent is truncated', async () => {
  const { calls } = await run('/', { headers: { 'user-agent': 'x'.repeat(500) } })
  assert.equal(calls[0].body.payload.data.ua.length, 100)
})

import { test } from 'node:test'
import assert from 'node:assert/strict'
import worker from './index.js'

function run(path, { method = 'GET', headers = {}, fail = false } = {}) {
  const calls = []
  const waits = []
  globalThis.fetch = async (url, init) => {
    calls.push({ url, body: JSON.parse(init.body), headers: init.headers })
    if (fail) throw new Error('posthog down')
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
  const b = calls[0].body
  assert.equal(calls[0].url, 'https://us.i.posthog.com/capture/')
  assert.equal(b.event, 'install_script')
  assert.match(b.api_key, /^phc_/)
  assert.ok(b.distinct_id)
  assert.equal(b.properties.os, 'sh')
  assert.equal(b.properties.ref, 'reddit-selfhosted')
  assert.equal(b.properties.$current_url, 'https://get.instancez.ai/?ref=reddit-selfhosted')
  assert.equal(b.properties.client_ua, 'curl/8.0')
  assert.equal(b.properties.$referrer, 'https://reddit.com/')
  assert.equal(b.properties.$process_person_profile, false)
})

test('windows path tags os=ps1', async () => {
  const { calls } = await run('/windows')
  assert.equal(calls[0].body.properties.os, 'ps1')
})

test('404 and HEAD are not tracked', async () => {
  assert.equal((await run('/nope')).calls.length, 0)
  const head = await run('/', { method: 'HEAD' })
  assert.equal(head.calls.length, 0)
})

test('posthog failure never breaks the installer', async () => {
  const { res } = await run('/', { fail: true })
  assert.equal(res.status, 200)
  assert.match(await res.text(), /\S/)
})

test('each request gets a fresh distinct_id', async () => {
  const a = (await run('/')).calls[0].body.distinct_id
  const b = (await run('/')).calls[0].body.distinct_id
  assert.notEqual(a, b)
})

test('missing ctx still serves', async () => {
  globalThis.fetch = async () => new Response('ok')
  const res = await worker.fetch(new Request('https://get.instancez.ai/'), {}, undefined)
  assert.equal(res.status, 200)
})

test('very long user agent is truncated', async () => {
  const { calls } = await run('/', { headers: { 'user-agent': 'x'.repeat(500) } })
  assert.equal(calls[0].body.properties.client_ua.length, 100)
})

test('installers tell users how to opt out of telemetry', async () => {
  const { readFile } = await import('node:fs/promises')
  for (const f of ['install.sh', 'install.ps1']) {
    const src = await readFile(new URL(`../${f}`, import.meta.url), 'utf8')
    assert.match(src, /anonymous usage and error data/, f)
    assert.match(src, /inz telemetry disable/, f)
    assert.match(src, /INSTANCEZ_TELEMETRY_DISABLED=1/, f)
  }
})

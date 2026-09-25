import assert from 'node:assert/strict'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { test } from 'node:test'
import { pathToFileURL } from 'node:url'

import ts from 'typescript'

async function loadClientModule() {
  const sourceURL = new URL('../src/api/client.ts', import.meta.url)
  const source = await readFile(sourceURL, 'utf8')
  const compiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.ES2022,
      target: ts.ScriptTarget.ES2020,
      moduleResolution: ts.ModuleResolutionKind.Bundler,
    },
  })
  const dir = path.join(tmpdir(), 'budgie-web-api-client-tests')
  await mkdir(dir, { recursive: true })
  const output = path.join(dir, `client-${Date.now()}-${Math.random().toString(16).slice(2)}.mjs`)
  await writeFile(output, compiled.outputText)
  return import(pathToFileURL(output).href)
}

function jsonResponse(status, body) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function installFetchStub(t, handlers) {
  const originalFetch = globalThis.fetch
  const requests = []
  globalThis.fetch = async (input, init = {}) => {
    const url = typeof input === 'string' ? input : input.url
    const headers = new Headers(init.headers ?? (typeof input === 'string' ? undefined : input.headers))
    const request = { url, init, headers }
    requests.push(request)
    const handler = handlers.shift()
    assert.ok(handler, `unexpected fetch ${url}`)
    return handler(request)
  }
  t.after(() => {
    globalThis.fetch = originalFetch
    assert.equal(handlers.length, 0, 'all expected fetch handlers should run')
  })
  return requests
}

function installImmediateTimers(t) {
  const originalSetTimeout = globalThis.setTimeout
  globalThis.setTimeout = (callback, _delay, ...args) => {
    queueMicrotask(() => callback(...args))
    return 0
  }
  t.after(() => {
    globalThis.setTimeout = originalSetTimeout
  })
}

test('fresh projection reads use the latest durable ack seq and retry projection_stale', async (t) => {
  installImmediateTimers(t)
  const client = await loadClientModule()
  client.rememberDurableAck({ seq: 42 })
  assert.equal(client.latestDurableWriteSeq(), 42)

  const requests = installFetchStub(t, [
    (request) => {
      assert.equal(request.url, '/api/v1/rankings/boards?limit=20')
      assert.equal(request.headers.get('Authorization'), 'Bearer token-a')
      assert.equal(request.headers.get('X-Budgie-Min-Seq'), '42')
      return jsonResponse(425, {
        error: {
          code: 'projection_stale',
          message: 'ranking projection is behind',
          retryable: true,
          retryAfterMs: 1,
        },
      })
    },
    (request) => {
      assert.equal(request.url, '/api/v1/rankings/boards?limit=20')
      assert.equal(request.headers.get('Authorization'), 'Bearer token-a')
      assert.equal(request.headers.get('X-Budgie-Min-Seq'), '42')
      return jsonResponse(200, { boards: [{ board: 'general', score: 1 }] })
    },
  ])

  const result = await client.listBoardRankings('token-a', 20, { projectionRetryLimit: 1 })
  assert.equal(result.error, undefined)
  assert.equal(result.data.length, 1)
  assert.equal(requests.length, 2)
})

test('resolved command status advances durable seq for following canonical reads', async (t) => {
  const client = await loadClientModule()
  const pendingAck = {
    id: 'cmd-1',
    commandId: 'cmd-1',
    status: 'pending',
    commandPartitionKind: 'thread',
    commandPartitionKey: 'thr-1',
    commandOffset: 4,
  }

  installFetchStub(t, [
    (request) => {
      assert.match(request.url, /^\/api\/v1\/commands\/cmd-1\?/)
      const params = new URLSearchParams(request.url.split('?')[1])
      assert.equal(params.get('commandPartitionKind'), 'thread')
      assert.equal(params.get('commandPartitionKey'), 'thr-1')
      assert.equal(params.get('commandOffset'), '4')
      assert.equal(request.headers.get('Authorization'), 'Bearer token-b')
      return jsonResponse(200, {
        commandId: 'cmd-1',
        status: 'applied',
        commandPartitionKind: 'thread',
        commandPartitionKey: 'thr-1',
        commandOffset: 4,
        result: { id: 'post-1', seq: 77 },
      })
    },
    (request) => {
      assert.equal(request.url, '/api/v1/threads/thr-1/posts?limit=50&offset=0')
      assert.equal(request.headers.get('Authorization'), 'Bearer token-b')
      assert.equal(request.headers.get('X-Budgie-Min-Seq'), '77')
      return jsonResponse(200, { posts: [] })
    },
  ])

  const resolved = await client.resolveCommandResult('token-b', pendingAck, { intervalMs: 1, timeoutMs: 100 })
  assert.equal(resolved.error, undefined)
  assert.equal(resolved.data.seq, 77)
  assert.equal(client.latestDurableWriteSeq(), 77)

  const posts = await client.listPosts('token-b', 'thr-1')
  assert.equal(posts.error, undefined)
  assert.deepEqual(posts.data, [])
})

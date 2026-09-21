#!/usr/bin/env bun

const argv = process.argv.slice(2)
const argOf = (name, fallback) => {
  const index = argv.indexOf(name)
  return index >= 0 && argv[index + 1] ? argv[index + 1] : fallback
}

const stub = argOf('--stub', 'http://stub:8100')
const status = Number(argOf('--status', '0'))
if (!Number.isInteger(status) || status < 0 || status > 599) {
  throw new Error('--status must be an integer from 0 to 599')
}

const reset = await fetch(`${stub}/_control/reset`, { method: 'POST' })
if (!reset.ok) throw new Error(`stub reset failed: HTTP ${reset.status}`)

const response = await fetch(`${stub}/_control/profile`, {
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify({
    status,
    statusPlan: [],
    delayMs: 0,
    hang: false,
    statusBody: null,
  }),
})
if (!response.ok) throw new Error(`stub profile update failed: HTTP ${response.status}`)
console.log(JSON.stringify(await response.json()))

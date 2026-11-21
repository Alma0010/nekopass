import assert from 'node:assert/strict'
import { test } from 'node:test'
import { parseBatchNodes } from '../web/src/node-batch-create.ts'

test('accepts comma-separated rows and two spreadsheet columns', () => {
  const input = '东京入口, tokyo.example.com\r\n\r\n备用,\r\n东区,机房\t2001:db8::1'
  const rows = parseBatchNodes(input)
  assert.deepEqual(rows.map(({ line, name, address, error }) => [line, name, address, error]), [
    [1, '东京入口', 'tokyo.example.com', ''],
    [3, '备用', '', ''],
    [4, '东区,机房', '2001:db8::1', ''],
  ])
  assert.equal(rows[1].raw, '备用,')
})

test('validates individual rows and only treats matching names and addresses as duplicates', () => {
  const rows = parseBatchNodes([
    'same,EXAMPLE.com',
    'same , example.COM',
    'same,another.example.com',
    'other,example.com',
    ',example.com',
    'missing comma',
    'too,many,columns',
    '名'.repeat(27) + ',example.com',
  ].join('\n'))
  assert.deepEqual(rows.map(row => Boolean(row.error)), [false, true, false, false, true, true, true, true])
})

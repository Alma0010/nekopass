import assert from 'node:assert/strict'
import test from 'node:test'
import { defaultTableSettings, normalizeTableSettings, orderedRows } from '../web/src/table-layout.ts'

const columns = [
  { id: 'name', label: '名称', width: 140, sortable: true },
  { id: 'status', label: '状态', width: 110, sortable: true },
  { id: 'address', label: '地址', width: 160 },
]

test('saved layout ignores removed columns and adds new columns to the end', () => {
  assert.deepEqual(normalizeTableSettings({
    order: ['status', 'removed', 'status', 'name'],
    widths: { status: 150.7, name: 2, address: 5000, removed: 150 },
    sort: { key: 'name', direction: 'descending' },
  }, columns), {
    order: ['status', 'name', 'address'],
    widths: { status: 151, name: 72, address: 720 },
    sort: { key: 'name', direction: 'descending' },
  })
})

test('invalid saved widths and non-sortable fields restore defaults', () => {
  const defaults = defaultTableSettings(columns)
  assert.deepEqual(normalizeTableSettings(null, columns), defaults)
  assert.deepEqual(normalizeTableSettings({
    widths: { status: NaN, name: '160' },
    sort: { key: 'address', direction: 'ascending' },
  }, columns), defaults)
})

test('sorting is natural for names, numeric for counts, and keeps missing values last', () => {
  const rows = [
    { id: 1, name: 'Node 10', count: 15 },
    { id: 2, name: 'Node 2', count: 3 },
    { id: 3, name: 'Node 9' },
    { id: 4, name: 'Node 2', count: 3 },
  ]
  const name = { ...columns[0], sortValue: row => row.name }
  const count = { ...columns[0], sortValue: row => row.count }
  assert.deepEqual(orderedRows(rows, name, 'ascending').map(row => row.id), [2, 4, 3, 1])
  assert.deepEqual(orderedRows(rows, count, 'descending').map(row => row.id), [1, 2, 4, 3])
  assert.deepEqual(orderedRows(rows, count, 'ascending').map(row => row.id), [2, 4, 1, 3])
  assert.deepEqual(rows.map(row => row.id), [1, 2, 3, 4])
})

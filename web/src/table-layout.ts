import { computed, shallowRef, watch, type Ref } from 'vue'

export const minColumnWidth = 72
export const maxColumnWidth = 720

export type SortDirection = 'ascending' | 'descending'
type SortValue = string | number | undefined
export type LayoutColumn = {
  id: string
  label: string
  width: number
  fluid?: boolean
  sortable?: boolean
}
export type DataColumn<Row> = Omit<LayoutColumn, 'sortable'> & {
  sortValue?: (row: Row) => SortValue
}
export type TableSettings = {
  order: string[]
  widths: Record<string, number>
  sort: { key: string; direction: SortDirection } | null
}
export type SortableTable = {
  sort: (prop: string, order: SortDirection) => void
  clearSort: () => void
}

export function defaultTableSettings(columns: readonly LayoutColumn[]): TableSettings {
  return { order: columns.map(column => column.id), widths: {}, sort: null }
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

export function clampColumnWidth(width: number) {
  return Math.round(Math.max(minColumnWidth, Math.min(width, maxColumnWidth)))
}

export function normalizeTableSettings(value: unknown, columns: readonly LayoutColumn[]): TableSettings {
  const saved = record(value)
  const valid = new Set(columns.map(column => column.id))
  const order = new Set<string>()
  if (Array.isArray(saved.order)) {
    for (const id of saved.order) {
      if (typeof id === 'string' && valid.has(id)) order.add(id)
    }
  }
  for (const id of valid) order.add(id)
  const savedWidths = record(saved.widths)
  const widths: Record<string, number> = {}
  for (const id of valid) {
    const width = savedWidths[id]
    if (typeof width === 'number' && Number.isFinite(width)) widths[id] = clampColumnWidth(width)
  }
  const { key, direction } = record(saved.sort)
  const sort: TableSettings['sort'] = typeof key === 'string' && columns.some(column => column.id === key && column.sortable)
    && (direction === 'ascending' || direction === 'descending') ? { key, direction } : null
  return { order: [...order], widths, sort }
}

const collator = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' })

export function orderedRows<Row>(rows: Row[], column: DataColumn<Row> | undefined, direction?: SortDirection) {
  const getValue = column?.sortValue
  if (!getValue || !direction) return rows
  const items = rows.map(row => ({ row, value: getValue(row) }))
  const sign = direction === 'ascending' ? 1 : -1
  return items.sort((a, b) => {
    if (a.value === undefined) return b.value === undefined ? 0 : 1
    if (b.value === undefined) return -1
    const result = typeof a.value === 'number' && typeof b.value === 'number'
      ? a.value - b.value : collator.compare(String(a.value), String(b.value))
    return result * sign
  }).map(item => item.row)
}

export function useTableLayout<Row>(
  key: () => string,
  fields: readonly DataColumn<Row>[],
  table: Readonly<Ref<SortableTable | undefined>>,
) {
  const columns = fields.map(column => ({ ...column, sortable: !!column.sortValue }))
  const byID = new Map(columns.map(column => [column.id, column]))
  const settings = shallowRef(defaultTableSettings(columns))

  watch(key, storageKey => {
    try {
      settings.value = normalizeTableSettings(JSON.parse(localStorage.getItem(storageKey) ?? 'null'), columns)
    } catch {
      settings.value = defaultTableSettings(columns)
    }
  }, { immediate: true })

  const displayColumns = computed(() => settings.value.order.map(id => {
    const column = byID.get(id)!
    const width = settings.value.widths[id]
    return {
      ...column,
      width: width ?? (column.fluid ? undefined : column.width),
      minWidth: column.fluid ? column.width : undefined,
    }
  }))
  const sortKey = computed(() => settings.value.sort?.key)
  const sortDirection = computed(() => settings.value.sort?.direction)

  function saveSettings(value: TableSettings) {
    settings.value = normalizeTableSettings(value, columns)
    try { localStorage.setItem(key(), JSON.stringify(settings.value)) } catch { /* Keep session preferences. */ }
  }

  function sortChanged({ prop, order }: { prop: string; order: SortDirection | null }) {
    const sort = order && byID.get(prop)?.sortValue ? { key: prop, direction: order } : null
    if (sort?.key !== sortKey.value || sort?.direction !== sortDirection.value) {
      saveSettings({ ...settings.value, sort })
    }
  }

  watch([table, sortKey, sortDirection], ([instance, prop, direction]) => {
    if (prop && direction) instance?.sort(prop, direction)
    else instance?.clearSort()
  }, { flush: 'post' })

  function resizeColumn(width: number, _previous: number, column: { columnKey?: string; width?: string | number; realWidth?: number }) {
    const id = column.columnKey
    if (!id || !byID.has(id) || !Number.isFinite(width)) return
    const size = clampColumnWidth(width)
    // Element Plus has already written the dragged width into its column state.
    column.width = column.realWidth = size
    saveSettings({ ...settings.value, widths: { ...settings.value.widths, [id]: size } })
  }

  function sortRows(rows: Row[]) {
    return orderedRows(rows, sortKey.value ? byID.get(sortKey.value) : undefined, sortDirection.value)
  }

  return { columns, displayColumns, settings, sortKey, sortDirection, saveSettings, sortChanged, resizeColumn, sortRows }
}

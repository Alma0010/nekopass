<script setup lang="ts">
import { computed, ref } from 'vue'
import Icon from './Icon.vue'
import {
  defaultTableSettings, minColumnWidth, maxColumnWidth, normalizeTableSettings,
  type LayoutColumn, type SortDirection, type TableSettings,
} from './table-layout'

const props = defineProps<{ columns: LayoutColumn[]; settings: TableSettings }>()
const emit = defineEmits<{ save: [settings: TableSettings] }>()
const open = ref(false)
const draft = ref(defaultTableSettings(props.columns))
const sortColumn = ref('default')
const direction = ref<SortDirection>('ascending')
const sortOptions = computed(() => props.columns.filter(column => column.sortable))
const ordered = computed(() => draft.value.order.map(id => props.columns.find(column => column.id === id)!))

function edit(settings: TableSettings) {
  draft.value = normalizeTableSettings(settings, props.columns)
  sortColumn.value = draft.value.sort?.key ?? 'default'
  direction.value = settings.sort?.direction ?? 'ascending'
}

function show() {
  edit(props.settings)
  open.value = true
}

function move(index: number, step: -1 | 1) {
  const next = index + step
  if (next < 0 || next >= draft.value.order.length) return
  const [column] = draft.value.order.splice(index, 1)
  draft.value.order.splice(next, 0, column)
}

function setWidth(id: string, value: number | null | undefined) {
  if (typeof value === 'number') draft.value.widths[id] = value
  else delete draft.value.widths[id]
}

function save() {
  emit('save', {
    ...draft.value,
    sort: sortColumn.value === 'default' ? null : { key: sortColumn.value, direction: direction.value },
  })
  open.value = false
}
</script>

<template>
  <el-button @click="show"><Icon name="settings" />表格设置</el-button>
  <el-dialog v-model="open" title="表格设置" width="510px" top="6vh" class="managed-form-dialog" append-to-body>
    <div class="table-settings-sort">
      <label class="table-settings-field">
        <span>排序字段</span>
        <el-select v-model="sortColumn" aria-label="排序字段">
          <el-option value="default" label="默认顺序" />
          <el-option v-for="column in sortOptions" :key="column.id" :value="column.id" :label="column.label" />
        </el-select>
      </label>
      <label class="table-settings-field">
        <span>排序方向</span>
        <el-select v-model="direction" aria-label="排序方向" :disabled="sortColumn === 'default'">
          <el-option value="ascending" label="升序" />
          <el-option value="descending" label="降序" />
        </el-select>
      </label>
    </div>
    <div class="table-settings-columns">
      <div class="table-settings-row table-settings-labels"><span>列名</span><span>列宽（px）</span><span>列顺序</span></div>
      <div v-for="(column, index) in ordered" :key="column.id" class="table-settings-row">
        <span class="table-settings-name">{{ column.label }}</span>
        <el-input-number
          :model-value="draft.widths[column.id]"
          :placeholder="column.fluid ? '自动' : String(column.width)"
          :min="minColumnWidth"
          :max="maxColumnWidth"
          :precision="0"
          :controls="false"
          :aria-label="column.label + '列宽'"
          @update:model-value="setWidth(column.id, $event)"
        />
        <div class="table-settings-move">
          <el-button :aria-label="'上移' + column.label" :disabled="index === 0" @click="move(index, -1)">↑</el-button>
          <el-button :aria-label="'下移' + column.label" :disabled="index === ordered.length - 1" @click="move(index, 1)">↓</el-button>
        </div>
      </div>
    </div>
    <template #footer>
      <div class="layout-actions table-settings-footer">
        <el-button class="table-settings-reset" link @click="edit(defaultTableSettings(columns))">恢复默认</el-button>
        <el-button @click="open = false">取消</el-button>
        <el-button type="primary" @click="save">保存</el-button>
      </div>
    </template>
  </el-dialog>
</template>

<style scoped>
.table-settings-sort {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 120px);
  gap: var(--layout-toolbar-gap);
  margin-bottom: var(--layout-section-gap);
}

.table-settings-field {
  display: grid;
  gap: 6px;
  min-width: 0;
}

.table-settings-columns {
  border: 1px solid var(--border);
  border-radius: 8px;
}

.table-settings-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(62px, 96px) auto;
  align-items: center;
  gap: var(--layout-toolbar-gap);
  padding: 6px var(--layout-inset);
}

.table-settings-row + .table-settings-row { border-top: 1px solid var(--border); }
.table-settings-labels { background: var(--el-fill-color-light); color: var(--el-text-color-secondary); }
.table-settings-labels span:last-child { min-width: 84px; }
.table-settings-name { overflow-wrap: anywhere; }
.table-settings-row .el-input-number { width: 100%; }
.table-settings-move { display: flex; gap: 4px; }
.table-settings-move .el-button { inline-size: 40px; block-size: 40px; margin: 0; padding: 0; }
.table-settings-footer { justify-content: flex-end; }
.table-settings-footer .table-settings-reset { margin-inline-end: auto; }

@media (max-width: 440px) {
  .table-settings-sort { grid-template-columns: minmax(0, 1fr) minmax(0, 96px); }
  .table-settings-row { grid-template-columns: minmax(0, 1fr) minmax(60px, 74px) auto; padding-inline: 8px; }
}
</style>

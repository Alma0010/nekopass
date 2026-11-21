<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api, APIError, type Node, type NodeGroup, type NodeInput } from './api'
import { maxBatchNodes, parseBatchNodes } from './node-batch-create'

const props = defineProps<{
  modelValue: boolean
  groups: NodeGroup[]
  makeDefaults: () => NodeInput
}>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; created: [] }>()

const input = ref('')
const groupIDs = ref<number[]>([])
const busy = ref(false)
const failures = ref<{ name: string; address: string; error: string }[]>([])
const summary = ref('')
const nodes = computed(() => parseBatchNodes(input.value))
const invalid = computed(() => nodes.value.filter(node => node.error))
const overLimit = computed(() => nodes.value.length > maxBatchNodes)
const retryTokens = new Map<string, string>()

function resetForm() {
  input.value = ''
  groupIDs.value = []
  failures.value = []
  summary.value = ''
  retryTokens.clear()
}

function closeDialog() {
  if (!busy.value) emit('update:modelValue', false)
}

function tokenFor(key: string) {
  const retained = retryTokens.get(key)
  if (retained) return retained
  // A retry uses the same unique token, even if a successful response was lost.
  const bytes = crypto.getRandomValues(new Uint8Array(32))
  const token = Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
  retryTokens.set(key, token)
  return token
}

async function createdAfterError(token: string) {
  try {
    const list = await api<Pick<Node, 'token'>[]>('admin/nodes')
    return list.some(node => node.token === token)
  } catch {
    return false
  }
}

async function createNodes() {
  if (busy.value || !nodes.value.length || invalid.value.length || overLimit.value) return
  const batch = nodes.value
  const group_ids = [...groupIDs.value]
  const created = new Set<string>()
  let attempted = 0
  busy.value = true
  failures.value = []
  summary.value = ''
  try {
    for (const node of batch) {
      const token = tokenFor(node.key)
      attempted++
      try {
        await api('admin/nodes', 'POST', {
          ...props.makeDefaults(),
          name: node.name,
          public_address: node.address,
          group_ids,
          token
        })
      } catch (error) {
        const rejected = error instanceof APIError && error.status >= 400 && error.status < 500
        if ((rejected && error.status !== 409) || !await createdAfterError(token)) {
          failures.value.push({
            name: node.name,
            address: node.address,
            error: rejected ? error.message : '无法确认是否创建成功，可以重试本批次'
          })
          if (!rejected || [401, 403, 429].includes(error.status)) break
          continue
        }
      }
      created.add(node.key)
      retryTokens.delete(node.key)
    }
  } finally {
    busy.value = false
    if (created.size) emit('created')
    if (created.size === batch.length) {
      ElMessage.success(`已创建 ${created.size} 个节点`)
      closeDialog()
    } else {
      input.value = batch.filter(node => !created.has(node.key)).map(node => node.raw).join('\n')
      summary.value = `已创建 ${created.size} 个，失败 ${failures.value.length} 个，未提交 ${batch.length - attempted} 个。`
    }
  }
}
</script>

<template>
  <el-dialog
    :model-value="props.modelValue"
    title="批量创建节点"
    width="680px"
    class="managed-form-dialog"
    :show-close="!busy"
    :close-on-click-modal="!busy"
    :close-on-press-escape="!busy"
    @update:model-value="closeDialog"
    @closed="resetForm"
  >
    <el-form label-position="top" :disabled="busy" @submit.prevent="createNodes">
      <el-form-item label="节点列表">
        <el-input
          v-model="input"
          type="textarea"
          :rows="7"
          placeholder="东京入口,tokyo.example.com&#10;香港入口,hongkong.example.com"
        />
        <p class="field-tip">每行填名称和公网 IP 或域名，用半角逗号分隔，也可以粘贴表格中的两列。地址可留空，每次最多 {{ maxBatchNodes }} 个。</p>
      </el-form-item>
      <el-form-item label="所属节点组">
        <el-select v-model="groupIDs" multiple filterable placeholder="可选，应用到本批全部节点">
          <el-option v-for="group in groups" :key="group.id" :label="group.name" :value="group.id" />
        </el-select>
      </el-form-item>
    </el-form>

    <el-alert v-if="overLimit" :title="`每次最多创建 ${maxBatchNodes} 个节点`" type="warning" :closable="false" />
    <el-table v-else-if="invalid.length" :data="invalid" max-height="180" aria-label="输入有误的行">
      <el-table-column prop="line" label="行号" width="74" />
      <el-table-column prop="error" label="请修改" />
    </el-table>
    <template v-if="summary">
      <el-alert :title="summary" type="warning" :closable="false" />
      <el-table v-if="failures.length" :data="failures" max-height="180" aria-label="创建失败的节点">
        <el-table-column label="节点" min-width="150">
          <template #default="{ row }">{{ row.name }}<br v-if="row.address" /><small v-if="row.address">{{ row.address }}</small></template>
        </el-table-column>
        <el-table-column prop="error" label="失败原因" min-width="240" />
      </el-table>
    </template>

    <template #footer>
      <el-button :disabled="busy" @click="closeDialog">关闭</el-button>
      <el-button
        type="primary"
        :loading="busy"
        :disabled="!nodes.length || !!invalid.length || overLimit"
        @click="createNodes"
      >创建 {{ nodes.length }} 个节点</el-button>
    </template>
  </el-dialog>
</template>

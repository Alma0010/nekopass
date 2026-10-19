<script setup lang="ts">
withDefaults(defineProps<{ ids: number[]; groups: {id: number; name: string}[]; emptyText?: string }>(), { emptyText: '未分组' })
</script>
<template>
 <div class="group-list">
  <span v-if="!ids.length" class="subtle">{{emptyText}}</span>
  <el-tag v-for="id in ids.slice(0, 2)" :key="id" class="group-chip">{{groups.find(g => g.id === id)?.name || id}}</el-tag>
  <el-popover v-if="ids.length > 2" trigger="click" placement="bottom" :width="260">
   <template #reference><el-button class="group-more" :aria-label="`查看全部 ${ids.length} 个节点组`">+{{ids.length - 2}}</el-button></template>
   <div class="group-popover"><el-tag v-for="id in ids" :key="id" class="group-chip">{{groups.find(g => g.id === id)?.name || id}}</el-tag></div>
  </el-popover>
 </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { Me } from './api'
const props = defineProps<{ me: Me | null; loading: boolean; error: string; active: string; admin?: boolean }>()
const allowed = computed(() => !!props.me && (!props.admin || props.me.is_admin))
</script>
<template>
 <div class="page-content" tabindex="-1">
  <el-alert v-if="error" class="page-load-alert" :title="error" type="error" :closable="false" />
  <div v-if="loading" class="page-content-loading surface" role="status" aria-live="polite"><el-skeleton :rows="5" animated /><p>正在加载…</p></div>
  <div v-else-if="!allowed" class="page-load-error surface"><h2>暂时无法加载页面</h2><p>{{ error || '当前账号无法访问此页面' }}</p><a href="/login">返回登录页</a></div>
  <slot v-else />
 </div>
</template>

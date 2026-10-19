import { nextTick, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { type Me } from './api'
import {loadSession} from './session'

export function usePage(load: (me: Me) => Promise<void>, options: { admin?: boolean; interval?: number } = {}) {
 const me = ref<Me | null>(null), loading = ref(true), error = ref(''), busy = ref(false)
 let timer: ReturnType<typeof setInterval> | undefined
 let stopped = false, refreshing: Promise<void> | undefined
 const pageKey = location.pathname + location.search
 async function refresh() {
  if (!me.value || stopped) return
  if (refreshing) return refreshing
  refreshing = (async () => { try { await load(me.value!); if (!stopped) error.value = '' } catch (e) { if (!stopped) error.value = (e as Error).message } })().finally(() => { refreshing = undefined })
  return refreshing
 }
 async function run(task: () => Promise<void>) { busy.value = true; try { await task() } catch (e) { ElMessage.error((e as Error).message) } finally { busy.value = false } }
 onMounted(async () => { try { me.value = await loadSession(); if(stopped)return; if (options.admin && !me.value.is_admin) { location.replace('/'); return }; await refresh() } catch (e) { error.value = (e as Error).message } finally { loading.value = false; await nextTick(); if (!stopped) window.dispatchEvent(new CustomEvent('nekopass:page-ready', { detail: pageKey })) }; if (options.interval && !stopped) timer = setInterval(() => { if (!document.hidden) void refresh() }, options.interval) })
 onUnmounted(() => { stopped = true; if (timer) clearInterval(timer) })
 return { me, loading, error, busy, refresh, run }
}

export function safeReturnPath(raw: string | null, isAdmin: boolean) {
 if (!raw || !raw.startsWith('/') || raw.startsWith('//') || /[\\\r\n]/.test(raw)) return '/'
 try { const u = new URL(raw, location.origin); if (u.origin !== location.origin || u.hash || u.username || u.password) return '/'; const path = u.pathname; const user = ['/', '/shop', '/orders', '/profile', '/tickets', '/referrals', '/forward_rules', '/node_status'].includes(path); const admin = ['/admin', '/admin/orders', '/admin/tickets', '/admin/payment_gateways', '/admin/announcements', '/admin/users', '/admin/plans', '/admin/nodes', '/admin/node_groups', '/admin/settings'].includes(path) || /^\/admin\/users\/[1-9]\d*\/forward_rules$/.test(path) || /^\/admin\/nodes\/[1-9]\d*\/(ddns|protocols|ip-recovery)$/.test(path); return user || isAdmin && admin ? path + u.search : '/' } catch { return '/' }
}

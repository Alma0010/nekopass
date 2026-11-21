<script setup lang="ts">
import { computed, markRaw, nextTick, onMounted, onUnmounted, ref, shallowRef, type Component } from 'vue'
import ShellLayout from './ShellLayout.vue'
import { resolveRoute, routeEnabled } from './navigation'
import { loadSession, sessionUser } from './session'
import { loadSite, site } from './site'
const props = defineProps<{ initial: Component; initialProps: Record<string, unknown> }>()
const current = shallowRef(markRaw(props.initial)), route = shallowRef(resolveRoute(location.pathname)!)
const pageProps = shallowRef(props.initialProps), pageKey = ref(location.pathname + location.search), pending = ref(false)
const enabled = computed(() => routeEnabled(route.value, site.features))
const sessionError = ref('')
const positions = new Map<string, number>()
let sequence = 0, restoreTop: number | undefined
let scrollObserver: ResizeObserver | undefined, scrollTimer: ReturnType<typeof setTimeout> | undefined
function stopScrollRestore() { scrollObserver?.disconnect(); scrollObserver = undefined; if (scrollTimer) clearTimeout(scrollTimer) }
function restoreScroll() {
 if (restoreTop === undefined) return
 const top = restoreTop
 const apply = (final = false) => {
  if (!final && document.documentElement.scrollHeight - innerHeight < top) return
  window.scrollTo({ top, behavior: 'instant' }); restoreTop = undefined; stopScrollRestore()
 }
 apply()
 if (restoreTop === undefined) return
 // Table layout may finish after Vue's DOM update. Observe its size before restoring.
 scrollObserver = new ResizeObserver(() => apply())
 const content = document.querySelector('.page-content')
 if (content) scrollObserver.observe(content)
 scrollTimer = setTimeout(() => apply(true), 300)
}

function link(event: Event): URL | undefined {
 const target = event.target instanceof Element ? event.target.closest('a[href]') : null
 if (!(target instanceof HTMLAnchorElement) || target.hasAttribute('download') || target.hasAttribute('data-full-navigation') || target.target && target.target !== '_self') return
 const url = new URL(target.href, location.href)
 if (url.origin !== location.origin || url.hash || !resolveRoute(url.pathname)) return
 if (resolveRoute(url.pathname)?.admin && !sessionUser.value?.is_admin) return
 return url
}
async function navigate(url: URL, pop = false) {
 const target = resolveRoute(url.pathname)
 if (!target) { location.assign(url.href); return }
 const key = url.pathname + url.search
 const attempt = ++sequence
 if (key === pageKey.value) { pending.value = false; return }
 pending.value = true
 try {
  const [module, me] = await Promise.all([target.load(), loadSession(true), loadSite()])
  if (attempt !== sequence) return
  if (target.admin && !me.is_admin) { location.assign(url.href); return }
  positions.set(pageKey.value, window.scrollY)
  if (!pop) history.pushState(null, '', key)
  stopScrollRestore()
  restoreTop = pop ? positions.get(key) || 0 : 0
  route.value = target
  pageProps.value = target.props || {}
  current.value = markRaw(module.default)
  pageKey.value = key
  document.title = target.title + ' · ' + site.name
  await nextTick()
  if (!pop) window.scrollTo({ top: 0, behavior: 'instant' })
 } catch {
  // A deployment may retire an old chunk. A normal navigation obtains its new HTML.
  if (attempt === sequence) location.assign(url.href)
 } finally { if (attempt === sequence) pending.value = false }
}
function click(event: MouseEvent) {
 if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
 const url = link(event)
 if (!url) return
 event.preventDefault()
 void navigate(url)
}
function prefetch(event: Event) {
 const connection = (navigator as Navigator & { connection?: { saveData?: boolean } }).connection
 if (connection?.saveData) return
 const url = link(event)
 const target = url && resolveRoute(url.pathname)
 if (target && routeEnabled(target, site.features)) void target.load().catch(() => {})
}
function popstate() { void navigate(new URL(location.href), true) }
function focusContent() { document.querySelector<HTMLElement>('.page-content')?.focus({ preventScroll: true }) }
function pageReady(event: Event) {
 if ((event as CustomEvent<string>).detail !== pageKey.value) return
 restoreScroll()
 focusContent()
}
onMounted(() => {
 if (!enabled.value) void loadSession().catch(e => { sessionError.value = (e as Error).message })
 document.addEventListener('click', click)
 document.addEventListener('pointerover', prefetch, { passive: true })
 document.addEventListener('focusin', prefetch)
 window.addEventListener('popstate', popstate)
 window.addEventListener('nekopass:page-ready', pageReady)
 history.scrollRestoration = 'manual'
})
onUnmounted(() => {
 sequence++
 stopScrollRestore()
 document.removeEventListener('click', click)
 document.removeEventListener('pointerover', prefetch)
 document.removeEventListener('focusin', prefetch)
 window.removeEventListener('popstate', popstate)
 window.removeEventListener('nekopass:page-ready', pageReady)
 history.scrollRestoration = 'auto'
})
</script>
<template>
 <ShellLayout :me="sessionUser" :loading="!sessionUser && !sessionError" :error="sessionError" :admin="route.admin" :active="route.active" :pending="pending">
  <Transition name="page-switch" mode="out-in" @after-enter="focusContent">
   <component v-if="enabled" :is="current" v-bind="pageProps" :key="pageKey" />
   <section v-else-if="sessionUser" class="page-content page-load-error surface" tabindex="-1">
    <h2>{{ !route.admin && !site.features.users ? '用户端已关闭' : '此功能已关闭' }}</h2>
    <a v-if="sessionUser.is_admin" href="/admin">返回管理后台</a>
    <a v-else href="/">返回主页</a>
   </section>
  </Transition>
 </ShellLayout>
</template>

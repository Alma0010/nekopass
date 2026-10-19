import { createApp, type Component } from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import './style.css'
import {loadSite} from './site'
import NavigationRoot from './NavigationRoot.vue'
import {resolveRoute} from './navigation'
import {applyTheme} from './theme'
export {applyTheme} from './theme'

export function mount(component: Component, props: Record<string, unknown> = {}) {
 applyTheme(localStorage.getItem('nekopass-theme-v3') !== 'light')
 window.addEventListener('pageshow', e => { if (e.persisted) location.reload() })
 const app = resolveRoute(location.pathname) ? createApp(NavigationRoot, { initial: component, initialProps: props }) : createApp(component, props)
 app.use(ElementPlus).mount('#app')
 void loadSite()
}

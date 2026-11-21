import {reactive} from 'vue'
import {withFeatureDefaults, type SiteFeatures} from './features'

export const site = reactive({name: 'Nekopass', features: withFeatureDefaults()})
let request = 0

export async function loadSite() {
 const attempt = ++request
 try {
  const response = await fetch('/api/v1/site', {cache: 'no-store'})
  if (!response.ok) return
  const data: {site_name?: string; features?: Partial<SiteFeatures>} = await response.json()
  if (attempt !== request) return
  const old = site.name
  site.name = data.site_name || 'Nekopass'
  site.features = withFeatureDefaults(data.features)
  if (document.title.endsWith(' · ' + old)) document.title = document.title.slice(0, -old.length) + site.name
  else if (document.title.endsWith(' · Nekopass')) document.title = document.title.replace(/Nekopass$/, site.name)
 } catch {/* Keep the last site settings available. */}
}

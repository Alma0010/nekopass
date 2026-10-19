import { shallowRef } from 'vue'
import { api, type Me } from './api'

export const sessionUser = shallowRef<Me | null>(null)
let checkedAt = 0
let pending: Promise<Me> | undefined

// Share only a short-lived identity check; page data always comes from its API.
export function loadSession(force = false): Promise<Me> {
 if (pending) return pending
 if (!force && sessionUser.value && Date.now() - checkedAt < 2000) return Promise.resolve(sessionUser.value)
 pending = api<Me>('me').then(user => {
  sessionUser.value = user
  checkedAt = Date.now()
  return user
 }).catch(error => { sessionUser.value = null; checkedAt = 0; throw error })
 .finally(() => { pending = undefined })
 return pending
}

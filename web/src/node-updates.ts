export type UpdateCandidate = {
 id: number; name: string; online: boolean; update_supported: boolean; protocol_version: number;
 agent_version: string; update_status: {state: string} | null
}
export type UpdateSkip = 'offline' | 'unsupported' | 'updating' | 'current'

export function newerVersion(target: string, current: string): boolean {
 const pattern = /^v([0-9]{1,8})\.([0-9]{1,8})\.([0-9]{1,8})$/
 const a = pattern.exec(target), b = pattern.exec(current)
 if (!a) return false
 if (!b) return true
 for (let i = 1; i <= 3; i++) if (Number(a[i]) !== Number(b[i])) return Number(a[i]) > Number(b[i])
 return false
}

export function planNodeUpdates<T extends UpdateCandidate>(nodes: readonly T[], version: string) {
 if (!/^v[0-9]{1,8}\.[0-9]{1,8}\.[0-9]{1,8}$/.test(version)) throw new Error('最新正式版本无效，请稍后重试')
 const candidates: T[] = [], skipped: Record<UpdateSkip, number> = {offline: 0, unsupported: 0, updating: 0, current: 0}
 for (const node of nodes) {
  if (!node.online) skipped.offline++
  else if (!node.update_supported || node.protocol_version < 9) skipped.unsupported++
  else if (['queued', 'running'].includes(node.update_status?.state || '')) skipped.updating++
  else if (!newerVersion(version, node.agent_version)) skipped.current++
  else candidates.push(node)
 }
 return {candidates, skipped}
}

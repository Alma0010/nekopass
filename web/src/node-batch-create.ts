export const maxBatchNodes = 100

export type BatchNode = {
  line: number
  raw: string
  name: string
  address: string
  key: string
  error: string
}

export function parseBatchNodes(text: string): BatchNode[] {
  const seen = new Set<string>()
  const nodes: BatchNode[] = []
  const encoder = new TextEncoder()
  for (const [index, raw] of text.split(/\r?\n/).entries()) {
    if (!raw.trim()) continue
    const columns = raw.split(raw.includes('\t') ? '\t' : ',')
    const [name = '', address = ''] = columns.map(value => value.trim())
    const key = JSON.stringify([name, address.toLowerCase()])
    let error = ''
    if (columns.length !== 2) error = '需要两列：节点名称、公网 IP 或域名'
    else if (!name) error = '请填写节点名称'
    else if (encoder.encode(name).length > 80) error = '节点名称最多 80 字节'
    else if (seen.has(key)) error = '本批次已有相同名称和地址'
    if (!error) seen.add(key)
    nodes.push({ line: index + 1, raw, name, address, key, error })
  }
  return nodes
}

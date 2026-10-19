import type {RuleNode,RuleEgressGroup} from './api'
export type EgressOption={value:string;name:string;kind:'node'|'group'|'direct'}
export const egressValue=(node=0,group=0)=>group?`group:${group}`:`node:${node}`
export function splitEgress(value:string){const [kind,id]=value.split(':');return {egress_node_id:kind==='node'?Number(id)||0:0,egress_group_id:kind==='group'?Number(id)||0:0}}
export function egressChoices(nodes:RuleNode[],groups:RuleEgressGroup[],ingress:number,protocol:string):EgressOption[]{
 const available=nodes.filter(n=>n.id!==ingress&&n.tunnel_exit_enabled&&n.allowed_ingress_ids.includes(ingress)&&(!n.tunnel_protocol.endsWith('_udp')||protocol==='udp'))
 const ids=new Set(available.map(n=>n.id))
 return [
  ...(nodes.some(n=>n.id===ingress&&n.allow_direct)?[{value:'node:0',name:'#0 不使用隧道，直接转发',kind:'direct' as const}]:[]),
  ...available.map(n=>({value:egressValue(n.id),name:n.name,kind:'node' as const})),
  ...groups.filter(g=>g.node_ids.some(id=>ids.has(id))).map(g=>({value:egressValue(0,g.id),name:g.name,kind:'group' as const})),
 ]
}

export function commonEgressChoices(nodes:RuleNode[],groups:RuleEgressGroup[],ingresses:{id:number;allow_direct:boolean}[],protocol:string):EgressOption[]{
 if(!ingresses.length)return []
 const choices=ingresses.map(i=>egressChoices(nodes,groups,i.id,protocol))
 const result=choices[0]!.filter(o=>o.kind!=='direct'&&choices.every(c=>c.some(v=>v.value===o.value)))
 if(ingresses.every(i=>i.allow_direct))result.unshift({value:'node:0',name:'#0 不使用隧道，直接转发',kind:'direct'})
 return result
}

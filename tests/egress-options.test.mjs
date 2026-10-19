import assert from 'node:assert/strict'
import {test} from 'node:test'
import {commonEgressChoices,splitEgress} from '../web/src/egress-options.ts'
import {parseRuleImport,prepareRuleImport,encodeNyanpassRules} from '../web/src/rule-transfer.ts'

const nodes=[
 {id:1,allow_direct:false,tunnel_exit_enabled:false,tunnel_protocol:'plain_tcp',allowed_ingress_ids:[]},
 {id:2,allow_direct:true,tunnel_exit_enabled:false,tunnel_protocol:'plain_tcp',allowed_ingress_ids:[]},
 {id:11,tunnel_exit_enabled:true,tunnel_protocol:'plain_tcp',allowed_ingress_ids:[1],name:'exit A'},
 {id:12,tunnel_exit_enabled:true,tunnel_protocol:'plain_tcp',allowed_ingress_ids:[2],name:'exit B'},
 {id:13,tunnel_exit_enabled:true,tunnel_protocol:'plain_udp',allowed_ingress_ids:[1,2],name:'UDP exit'},
]
const groups=[{id:11,name:'group with separate routes',node_ids:[11,12]},{id:12,name:'only one ingress',node_ids:[11]}]
test('each ingress can use a different permitted member of one exit group',()=>{
 const choices=commonEgressChoices(nodes,groups,nodes.slice(0,2),'tcp')
 assert.deepEqual(choices.map(c=>c.value),['group:11'])
 assert.deepEqual(splitEgress(choices[0].value),{egress_node_id:0,egress_group_id:11})
 assert.deepEqual(commonEgressChoices(nodes,groups,[],'tcp'),[])
})
test('all ingress protocols and direct permissions constrain shared choices',()=>{
 const choices=commonEgressChoices(nodes,groups,nodes.slice(0,2),'udp')
 assert.deepEqual(choices.map(c=>c.value),['node:13','group:11'])
 assert.equal(choices.some(c=>c.kind==='direct'),false)
})
test('both group bindings survive a native backup and imports stay one rule',()=>{
 const data=parseRuleImport(JSON.stringify({name:'both groups',listen_port:24005,dest:['example.com:443']}))
 const [rule]=prepareRuleImport(data,{userID:7,nodeID:0,ingressGroupID:42,egressNodeID:0,egressGroupID:11,groupID:0,trustedCIDRs:[],randomPorts:false})
 assert.equal(rule.ingress_group_id,42);assert.equal(rule.egress_group_id,11)
 const restored=prepareRuleImport(parseRuleImport(JSON.stringify({rules:[rule]})),{userID:9,nodeID:0,egressNodeID:0,groupID:0,trustedCIDRs:[],randomPorts:false})
 assert.equal(restored.length,1);assert.equal(restored[0].ingress_group_id,42);assert.equal(restored[0].egress_group_id,11)
 assert.throws(()=>encodeNyanpassRules(restored),/完整备份/)
})

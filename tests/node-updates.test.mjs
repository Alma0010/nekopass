import assert from 'node:assert/strict'
import {test} from 'node:test'
import {newerVersion,planNodeUpdates} from '../web/src/node-updates.ts'
const node=(id,patch={})=>({id,name:'node-'+id,online:true,update_supported:true,protocol_version:25,agent_version:'v0.19.4',update_status:null,...patch})

test('stable versions compare numerically and unknown installed versions can upgrade',()=>{
 assert.equal(newerVersion('v0.19.10','v0.19.9'),true)
 assert.equal(newerVersion('v1.0.0','v0.99.99'),true)
 for(const current of ['v0.19.5','v0.19.6','v1.0.0'])assert.equal(newerVersion('v0.19.5',current),false)
 for(const current of ['dev','','unknown'])assert.equal(newerVersion('v0.19.5',current),true)
 assert.equal(newerVersion('latest','v0.19.4'),false)
})

test('batch skips offline, unsupported, active tasks and already-current nodes',()=>{
 const nodes=[node(1),node(2,{online:false}),node(3,{update_supported:false}),node(4,{protocol_version:8}),node(5,{update_status:{state:'queued'}}),node(6,{update_status:{state:'running'}}),node(7,{agent_version:'v0.19.5'}),node(8,{agent_version:'v0.20.0'}),node(9,{update_status:{state:'failed'}}),node(10,{enabled:false}),node(11,{agent_version:'dev'})]
 const {candidates,skipped}=planNodeUpdates(nodes,'v0.19.5')
 assert.deepEqual(candidates.map(n=>n.id),[1,9,10,11])
 assert.deepEqual(skipped,{offline:1,unsupported:2,updating:2,current:2})
 assert.equal(nodes[0].agent_version,'v0.19.4')
 assert.equal(nodes[8].update_status.state,'failed')
})

test('empty batches do nothing and invalid target versions fail before submissions',()=>{
 assert.deepEqual(planNodeUpdates([],'v0.19.5').candidates,[])
 for(const version of ['latest','v0.19','v0.19.5-beta','v0.19.123456789'])assert.throws(()=>planNodeUpdates([node(1)],version),/版本无效/)
})

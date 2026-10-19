<script setup lang="ts">
import {computed,reactive,ref,watch} from 'vue'
import {ElMessage} from 'element-plus'
import Icon from './Icon.vue'
import type {Rule,RuleNode,RuleBatchPatch,RuleNodeGroup} from './api'

const props=defineProps<{modelValue:boolean;rules:Rule[];nodes:RuleNode[];nodeGroups?:RuleNodeGroup[];busy:boolean;limitsLabel:string}>()
const emit=defineEmits<{
 'update:modelValue':[value:boolean]
 save:[value:{ids:number[];patch:RuleBatchPatch}]
 toggle:[value:{ids:number[];enabled:boolean}]
}>()
const form=reactive<RuleBatchPatch>({})
const advanced=ref<string[]>([])
watch(()=>props.modelValue,open=>{if(open){for(const key of Object.keys(form) as (keyof RuleBatchPatch)[])delete form[key];advanced.value=[]}})
function clearField(key:keyof RuleBatchPatch){delete form[key]}
const hasSharedTLS=computed(()=>props.rules.some(r=>!!r.tls_inbound_id))
const hasUDP=computed(()=>props.rules.some(r=>r.protocol==='udp'))
const ingressNodes=computed(()=>props.nodes.filter(n=>n.ingress_enabled))
const ingressIDs=computed(()=>[...new Set(form.ingress_group_id ? props.nodeGroups?.find(g=>g.id===form.ingress_group_id)?.node_ids||[] : props.rules.flatMap(r=>form.node_id?[form.node_id]:r.ingress_group_id?(r.ingress_nodes||[]).map(n=>n.node_id):[r.node_id]))])
const ingressChoice=computed({get:()=>form.ingress_group_id?'g:'+form.ingress_group_id:form.node_id?'n:'+form.node_id:undefined,set:(value:string|undefined)=>{delete form.node_id;delete form.ingress_group_id;if(value){const [kind,id]=value.split(':');if(kind==='g')form.ingress_group_id=Number(id);else form.node_id=Number(id)}}})
const directAllowed=computed(()=>ingressIDs.value.length>0&&ingressIDs.value.every(id=>props.nodes.some(n=>n.id===id&&n.allow_direct)||props.nodeGroups?.some(g=>g.members.some(n=>n.id===id&&n.allow_direct))))
const exitNodes=computed(()=>props.nodes.filter(n=>n.tunnel_exit_enabled&&
 ingressIDs.value.every(id=>id!==n.id&&n.allowed_ingress_ids.includes(id))&&
 (!n.tunnel_protocol.endsWith('_udp')||props.rules.every(r=>r.protocol==='udp'))))
watch([exitNodes,directAllowed],()=>{
 if(form.egress_node_id===0&&!directAllowed.value||form.egress_node_id!=null&&form.egress_node_id!==0&&!exitNodes.value.some(n=>n.id===form.egress_node_id))delete form.egress_node_id
})
function submit(){
 if(props.busy)return
 const patch:RuleBatchPatch={}
 for(const key of ['node_id','ingress_group_id','egress_node_id','balance','proxy_accept_enabled','proxy_send_enabled'] as const){
  const value=form[key]
  if(value!==undefined&&value!==null&&value!=='')Object.assign(patch,{[key]:value})
 }
 if(!Object.keys(patch).length){ElMessage.warning('请选择至少一个需要更新的字段');return}
 emit('save',{ids:props.rules.map(r=>r.id),patch})
}
</script>

<template>
 <el-dialog :model-value="modelValue" @update:model-value="emit('update:modelValue',$event)" :title="`批量编辑 ${rules.length} 条规则`" width="480px" top="6vh" class="managed-form-dialog" :close-on-click-modal="!busy" :close-on-press-escape="!busy" :show-close="!busy">
  <p class="subtle">留空保持原值。保存后由节点同步生效，受影响的现有连接可能断开。</p>
  <el-form label-position="top" :disabled="busy" @submit.prevent="submit">
   <el-form-item label="入口（留空不更新）">
    <el-select v-model="ingressChoice" clearable placeholder="保持原入口" :disabled="hasSharedTLS" data-testid="batch-rule-ingress">
     <el-option-group v-if="nodeGroups?.length" label="节点组"><el-option v-for="g in nodeGroups" :key="g.id" :label="g.name+' · 节点组'" :value="'g:'+g.id" :disabled="!g.node_ids.length"/></el-option-group><el-option-group label="单节点"><el-option v-for="n in ingressNodes" :key="n.id" :label="n.name" :value="'n:'+n.id"/></el-option-group>
    </el-select>
    <span class="field-tip">{{hasSharedTLS?'共享 TLS 规则切换入口时，请逐条选择新节点的共享 TLS 入口。':'更换入口会重新分配监听端口；选择原入口时保留端口。'}}</span>
   </el-form-item>
   <el-form-item label="出口（留空不更新）">
    <el-select v-model="form.egress_node_id" clearable @clear="clearField('egress_node_id')" placeholder="保持原出口" data-testid="batch-rule-egress">
     <el-option v-if="directAllowed" label="#0 不使用隧道，直接转发" :value="0"/>
     <el-option v-for="n in exitNodes" :key="n.id" :label="n.name" :value="n.id"/>
    </el-select>
   </el-form-item>
   <el-collapse v-model="advanced">
    <el-collapse-item title="高级选项（留空不更新）" name="advanced">
     <el-form-item label="负载均衡策略">
      <el-select v-model="form.balance" clearable @clear="clearField('balance')" placeholder="保持原策略" data-testid="batch-rule-balance">
       <el-option label="随机" value="random"/><el-option label="轮询" value="round_robin"/><el-option label="最少连接" value="least_connections"/>
      </el-select>
     </el-form-item>
     <el-form-item label="接受 Proxy Protocol">
      <el-select v-model="form.proxy_accept_enabled" clearable @clear="clearField('proxy_accept_enabled')" placeholder="保持原设置" :disabled="hasSharedTLS" data-testid="batch-rule-proxy-accept">
       <el-option label="关闭" :value="false"/><el-option label="开启" :value="true" :disabled="hasUDP"/>
      </el-select>
      <span v-if="hasSharedTLS" class="field-tip">共享 TLS 入站的接收设置由管理员统一配置。</span>
     </el-form-item>
     <el-form-item label="发送 Proxy Protocol">
      <el-select v-model="form.proxy_send_enabled" clearable @clear="clearField('proxy_send_enabled')" placeholder="保持原设置" data-testid="batch-rule-proxy-send">
       <el-option label="关闭" :value="false"/><el-option label="开启" :value="true" :disabled="hasUDP"/>
      </el-select>
      <span v-if="hasUDP" class="field-tip">所选规则包含 UDP，只能保持或关闭 Proxy Protocol。</span>
     </el-form-item>
     <p class="field-tip">{{limitsLabel}}</p>
    </el-collapse-item>
   </el-collapse>
  </el-form>
  <div class="batch-state-actions">
   <el-button :disabled="busy" @click="emit('toggle',{ids:rules.map(r=>r.id),enabled:false})"><Icon name="pause"/>暂停选中规则</el-button>
   <el-button :disabled="busy" @click="emit('toggle',{ids:rules.map(r=>r.id),enabled:true})"><Icon name="play"/>恢复选中规则</el-button>
  </div>
  <template #footer><el-button :disabled="busy" @click="emit('update:modelValue',false)">取消</el-button><el-button type="primary" :loading="busy" @click="submit">确定</el-button></template>
 </el-dialog>
</template>

<style scoped>
.batch-state-actions{display:flex;gap:10px;flex-wrap:wrap;margin-top:18px}
.batch-state-actions .el-button{margin-left:0}
</style>

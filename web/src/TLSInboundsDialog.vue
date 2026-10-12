<script setup lang="ts">
import {reactive,ref,watch} from 'vue'
import {ElMessage,ElMessageBox} from 'element-plus'
import {api,type SharedTLSConfig} from './api'
const props=defineProps<{modelValue:boolean;nodeID:number}>()
const emit=defineEmits<{'update:modelValue':[value:boolean];saved:[]}>()
type Entry={id:number;config:SharedTLSConfig;rule_count:number}
type Details={tls_only:boolean;inbounds:Entry[];default_rules:{id:number;name:string;tls_inbound_id:number}[]}
const entries=ref<Entry[]>([]),defaults=ref<Details['default_rules']>([]),tlsOnly=ref(false),editing=ref(false),id=ref(0),busy=ref(false),loading=ref(false),error=ref(''),suffixes=ref('')
const blank=():SharedTLSConfig=>({name:'',port:443,enabled:true,proxy_accept:'off',allowed_sni:[],reject_empty_sni:true,unknown_sni_action:'reject',default_rule_id:0,max_handshakes:128,handshake_timeout_seconds:5})
const form=reactive(blank())
let generation=0
async function load(){const current=++generation;loading.value=true;error.value='';try{const data=await api<Details>(`admin/nodes/${props.nodeID}/tls-inbounds`);if(current!==generation)return;entries.value=data.inbounds||[];defaults.value=data.default_rules||[];tlsOnly.value=data.tls_only}catch(e){if(current===generation)error.value=(e as Error).message}finally{if(current===generation)loading.value=false}}
watch(()=>props.modelValue,open=>{editing.value=false;if(open&&props.nodeID)void load();else generation++})
function edit(entry?:Entry){id.value=entry?.id||0;Object.assign(form,JSON.parse(JSON.stringify(entry?.config||blank())));suffixes.value=form.allowed_sni.join('\n');editing.value=true}
async function save(){busy.value=true;try{form.allowed_sni=suffixes.value.split(/\r?\n/).map(v=>v.trim()).filter(Boolean);await api(`admin/nodes/${props.nodeID}/tls-inbounds`,'PUT',{id:id.value,config:form});editing.value=false;await load();emit('saved');ElMessage.success('TLS 入站已保存并下发')}catch(e){ElMessage.error((e as Error).message)}finally{busy.value=false}}
async function policy(value:boolean){busy.value=true;try{await api(`admin/nodes/${props.nodeID}/tls-policy`,'PUT',{tls_only:value});tlsOnly.value=value;emit('saved')}catch(e){ElMessage.error((e as Error).message)}finally{busy.value=false}}
async function remove(entry:Entry){try{await ElMessageBox.confirm(`删除共享入口「${entry.config.name}」？`,'删除 TLS 入站',{confirmButtonText:'删除',cancelButtonText:'取消'});busy.value=true;await api(`admin/nodes/${props.nodeID}/tls-inbounds`,'DELETE',{id:entry.id});await load();emit('saved')}catch(e){if(e instanceof Error)ElMessage.error(e.message)}finally{busy.value=false}}
</script>
<template>
 <el-dialog :model-value="modelValue" @update:model-value="emit('update:modelValue',$event)" title="TLS 入站" width="780px" top="4vh" append-to-body class="managed-form-dialog" :close-on-click-modal="!busy" :show-close="!busy">
  <div v-loading="loading"><el-alert v-if="error" :title="error" type="error" :closable="false" />
   <template v-else-if="!editing">
    <div class="inbound-toolbar"><el-checkbox :model-value="tlsOnly" :disabled="busy" @update:model-value="policy(Boolean($event))">此节点的 TCP 规则仅允许 TLS 入站</el-checkbox><el-button type="primary" :disabled="busy" @click="edit()">新增共享入口</el-button><el-button :disabled="busy" @click="load">刷新</el-button></div>
    <el-table :data="entries" empty-text="暂无共享 TLS 入口"><el-table-column label="名称"><template #default="{row}">{{row.config.name}}</template></el-table-column><el-table-column prop="config.port" label="端口" width="80"/><el-table-column label="状态"><template #default="{row}">{{row.config.enabled?'启用':'停用'}}</template></el-table-column><el-table-column prop="rule_count" label="规则" width="65"/><el-table-column label="操作" width="190"><template #default="{row}"><el-button link :disabled="busy" @click="edit(row)">编辑</el-button><el-button link type="danger" :disabled="busy||row.rule_count>0" @click="remove(row)">删除</el-button></template></el-table-column></el-table>
   </template>
   <p class="field-tip">只读取 SNI 选择规则，完整 TLS 数据原样透传，证书由目标服务提供。</p>
   <el-form v-if="editing" label-position="top" @submit.prevent="save">
    <div class="form-grid"><el-form-item label="名称"><el-input v-model="form.name" maxlength="64"/></el-form-item><el-form-item label="共享监听端口"><el-input v-model.number="form.port" type="number" min="1" max="65535"/></el-form-item><el-form-item label="启用"><el-switch v-model="form.enabled"/></el-form-item></div>
    <el-form-item label="允许的 SNI 范围"><el-input v-model="suffixes" type="textarea" :rows="2" placeholder="一行一个域名，留空允许所有域名"/></el-form-item>
    <el-collapse><el-collapse-item title="高级设置" name="advanced"><div class="form-grid"><el-form-item label="接受 Proxy Protocol"><el-select v-model="form.proxy_accept"><el-option label="关闭" value="off"/><el-option label="开启" value="auto"/></el-select></el-form-item><el-form-item label="ClientHello 并发读取上限"><el-input v-model.number="form.max_handshakes" type="number"/></el-form-item><el-form-item label="ClientHello 读取超时（秒）"><el-input v-model.number="form.handshake_timeout_seconds" type="number"/></el-form-item></div><el-checkbox v-model="form.reject_empty_sni">拒绝没有 SNI 的连接</el-checkbox><el-form-item label="未知 SNI"><el-select v-model="form.unknown_sni_action"><el-option label="拒绝连接" value="reject"/><el-option label="转发至管理员默认规则" value="default_rule"/></el-select></el-form-item><el-form-item v-if="form.unknown_sni_action==='default_rule'" label="默认规则"><el-select v-model="form.default_rule_id"><el-option v-for="rule in defaults.filter(r=>r.tls_inbound_id===id)" :key="rule.id" :label="rule.name" :value="rule.id"/></el-select></el-form-item></el-collapse-item></el-collapse>
   </el-form>
  </div>
  <template #footer><template v-if="editing"><el-button :disabled="busy" @click="editing=false">返回列表</el-button><el-button type="primary" :loading="busy" @click="save">保存并下发</el-button></template><el-button v-else :disabled="busy" @click="emit('update:modelValue',false)">关闭</el-button></template>
 </el-dialog>
</template>
<style scoped>.inbound-toolbar{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin-bottom:16px}.form-grid{display:grid;grid-template-columns:1fr 1fr;gap:0 16px}@media(max-width:600px){.form-grid{grid-template-columns:1fr}}</style>

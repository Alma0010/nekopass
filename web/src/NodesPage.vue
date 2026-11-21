<script setup lang="ts">
import { copyText } from './clipboard'
import { ref, reactive, watch,computed,onMounted,onUnmounted } from 'vue'
import {consumeCreateIntent} from './page'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, APIError, type Node, type NodeDeleteResult, type NodeInput, type NodeGroup,type NodeGroupMember } from './api'
import Icon from './Icon.vue'
import GroupTags from './GroupTags.vue'
import LimitInput from './LimitInput.vue'
import NodeProtocolsDialog from './NodeProtocolsDialog.vue'
import NodeSecurityDialog from './NodeSecurityDialog.vue'
import NodeConnectionFields from './NodeConnectionFields.vue'
import NodeBatchCreateDialog from './NodeBatchCreateDialog.vue'
import TLSInboundsDialog from './TLSInboundsDialog.vue'
import {newerVersion,planNodeUpdates,type UpdateSkip} from './node-updates'
import type {NodeTransportInput,NodeSecurityInput} from './node-protocol'
import {nodeTLSDefaults as tlsDefaults,nodeTLSConfig as tlsConfig,applyNodeTransport,applyNodeSecurity} from './node-protocol'
const props=withDefaults(defineProps<{ nodes: Node[]; groups: NodeGroup[]; view?: 'nodes' | 'groups' }>(), {view:'nodes'})
const nodeScope=ref(new URLSearchParams(location.search).get('status')==='offline'?'offline':'')
const nodeSearch=ref(''), nodeGroup=ref(0), selectedIDs=ref(new Set<number>())
const visibleNodes=computed(()=>{
 const search=nodeSearch.value.trim().toLowerCase()
 return props.nodes.filter(node=>{
  if(nodeScope.value==='offline'&&!(node.enabled&&!node.online))return false
  if(nodeScope.value==='online'&&!(node.enabled&&node.online))return false
  if(nodeScope.value==='disabled'&&node.enabled)return false
  if(nodeGroup.value&& !node.group_ids.includes(nodeGroup.value))return false
  return !search||`${node.name} ${node.public_address||''}`.toLowerCase().includes(search)
 })
})
const selectedNodes=computed(()=>visibleNodes.value.filter(node=>selectedIDs.value.has(node.id)))
const allVisibleSelected=computed(()=>visibleNodes.value.length>0&&selectedNodes.value.length===visibleNodes.value.length)
let selectionAnchor=0
let dragStart:{id:number;selected:boolean}|null=null
watch([nodeScope,nodeSearch,nodeGroup],()=>{
 selectedIDs.value.clear();selectionAnchor=0;dragStart=null
 if(nodeScope.value!=='offline'&&new URLSearchParams(location.search).get('status')==='offline'){
  const url=new URL(location.href);url.searchParams.delete('status');history.replaceState(null,'',url.pathname+url.search)
 }
})
watch(visibleNodes,nodes=>{
 const ids=new Set(nodes.map(node=>node.id))
 for(const id of selectedIDs.value)if(!ids.has(id))selectedIDs.value.delete(id)
 if(!ids.has(selectionAnchor))selectionAnchor=0
})
function selectVisible(selected:boolean){
 selectedIDs.value=new Set(selected?visibleNodes.value.map(node=>node.id):[])
 selectionAnchor=0
}
function selectNode(id:number, selected:boolean){
 if(selected)selectedIDs.value.add(id)
 else selectedIDs.value.delete(id)
}
function selectRange(startID:number,endID:number,selected:boolean){
 const ids=visibleNodes.value.map(node=>node.id)
 const start=ids.indexOf(startID), end=ids.indexOf(endID)
 if(start<0||end<0)return
 for(const id of ids.slice(Math.min(start,end),Math.max(start,end)+1))selectNode(id,selected)
}
function startSelection(id:number,event:PointerEvent){
 if(event.button!==0||event.pointerType==='touch')return
 event.preventDefault()
 const label=event.currentTarget as HTMLElement
 label.querySelector('input')?.focus({preventScroll:true})
 const selected=!selectedIDs.value.has(id)
 if(event.shiftKey&&selectionAnchor)selectRange(selectionAnchor,id,selected)
 else{selectNode(id,selected);selectionAnchor=id}
 dragStart={id,selected}
}
function stopSelection(){dragStart=null}
onMounted(()=>{window.addEventListener('pointerup',stopSelection);window.addEventListener('pointercancel',stopSelection)})
onUnmounted(()=>{window.removeEventListener('pointerup',stopSelection);window.removeEventListener('pointercancel',stopSelection)})
function extendSelection(id:number,event:PointerEvent){
 if(dragStart&&(event.buttons&1)&&dragStart.id!==id){
  selectRange(dragStart.id,id,dragStart.selected)
 }
}
function preventDuplicateClick(event:MouseEvent){
 if(event.detail>0&&(event as PointerEvent).pointerType!=='touch')event.preventDefault()
}
function checkboxChanged(id:number,event:Event){
 selectNode(id,(event.target as HTMLInputElement).checked);selectionAnchor=id
}
onMounted(()=>{if(props.view==='nodes'&&consumeCreateIntent())editNode()})
const emit = defineEmits<{ refresh: [] }>()
const updatingNode=ref(0),removingNode=ref(0),bulkUpdating=ref(false),bulkRemoving=ref(false),batchDone=ref(0),batchTotal=ref(0),batchResultOpen=ref(false)
const batchCreateOpen=ref(false)
const batchInProgress=computed(()=>bulkUpdating.value||bulkRemoving.value)
const nodeActionInProgress=computed(()=>batchInProgress.value||!!updatingNode.value||!!removingNode.value)
type BatchSkip=UpdateSkip|'uninstalling'
type BatchResult={version:string;submitted:number;skipped:Record<BatchSkip,number>;failures:{name:string;error:string}[];notSubmitted:number}
const batchResult=ref<BatchResult|null>(null)
const skipLabels:Record<BatchSkip,string>={current:'已是目标版本或更新版本',offline:'离线',unsupported:'尚未安装更新服务',updating:'已有更新任务',uninstalling:'已有卸载任务'}
function skippedText(skipped:Record<BatchSkip,number>){return (Object.keys(skipLabels) as BatchSkip[]).filter(key=>skipped[key]).map(key=>`${skipLabels[key]} ${skipped[key]} 个`).join('；') || '无'}
async function latestVersion(){const data=await api<{latest:{version:string}}>('admin/updates');return data.latest.version}
async function updateNode(n:Node){
 if(nodeActionInProgress.value||n.uninstall_status)return
 if(!n.update_supported){ElMessage.warning('请先重新运行新版节点安装命令，以安装更新服务');return}
 updatingNode.value=n.id
 try{
  const version=await latestVersion()
  if(!newerVersion(version,n.agent_version)){ElMessage.info('节点已是该版本或更新版本');return}
  await ElMessageBox.confirm(`更新到 ${version} 将重启节点并中断现有转发连接。`,'更新节点',{confirmButtonText:'更新',cancelButtonText:'取消'})
  await api(`admin/nodes/${n.id}/update`,'POST',{version});ElMessage.success('更新任务已下发');emit('refresh')
 }catch(e){if(e instanceof Error)ElMessage.error(e.message)}finally{updatingNode.value=0}
}
async function updateSelectedNodes(){
 if(nodeActionInProgress.value)return
 const hasSelection=selectedNodes.value.length>0
 const toUpdate=hasSelection?selectedNodes.value:visibleNodes.value
 const ids=new Set(toUpdate.map(node=>node.id))
 const title=ids.size===props.nodes.length?'更新所有节点':'批量更新节点'
 bulkUpdating.value=true;batchDone.value=0;batchTotal.value=0
 try{
  const [version,nodes]=await Promise.all([latestVersion(),api<Node[]>('admin/nodes')])
  const scoped=nodes.filter(node=>ids.has(node.id))
  const {candidates,skipped:updateSkips}=planNodeUpdates(scoped.filter(node=>!node.uninstall_status),version)
  const skipped={...updateSkips,uninstalling:scoped.filter(node=>node.uninstall_status).length}
  if(!candidates.length){ElMessage.info(`没有需要更新的节点。跳过：${skippedText(skipped)}`);emit('refresh');return}
  const scope=hasSelection?'':'未勾选节点，将检查当前筛选结果。'
  await ElMessageBox.confirm(`${scope}将向 ${candidates.length} 个节点下发 ${version} 更新。节点会重启并中断现有转发连接。跳过：${skippedText(skipped)}。`,title,{confirmButtonText:`更新 ${candidates.length} 个节点`,cancelButtonText:'取消',type:'warning'})
  const result:BatchResult={version,submitted:0,skipped,failures:[],notSubmitted:0}
  batchTotal.value=candidates.length
  // Submit one at a time; each node still uses the existing source and update checks.
  for(const node of candidates){
   try{await api(`admin/nodes/${node.id}/update`,'POST',{version});result.submitted++}
   catch(e){
    result.failures.push({name:node.name,error:e instanceof Error?e.message:'请求失败'})
    if(e instanceof APIError && (e.status===401||e.status===403)){
     result.notSubmitted=candidates.length-batchDone.value-1;batchDone.value++;break
    }
   }
   batchDone.value++
  }
  batchResult.value=result;batchResultOpen.value=true;emit('refresh')
  const message=`更新任务已下发 ${result.submitted} 个，提交失败 ${result.failures.length} 个${result.notSubmitted ? `，未提交 ${result.notSubmitted} 个` : ''}`
  if(result.failures.length)ElMessage.warning(message);else ElMessage.success(message)
 }catch(e){if(e instanceof Error)ElMessage.error(e.message)}finally{bulkUpdating.value=false}
}
const updateStates:Record<string,string>={queued:'等待更新',running:'更新中',completed:'已更新',failed:'更新失败',unconfirmed:'更新结果未确认'}
const nodeDialog = ref(false), groupDialog = ref(false), editID = ref(0), groupID = ref(0), busy = ref(false), interfaces = ref('')
const installDialog = ref(false), installCommand = ref(''), installExisting = ref(false)
async function installation(id: number) { await act(async () => { const data = await api<{command: string; existing_node: boolean}>(`admin/nodes/${id}/install-command`, 'POST', {}); installCommand.value = data.command; installExisting.value = data.existing_node; installDialog.value = true }) }
async function copyInstall() { try { await copyText(installCommand.value); ElMessage.success('安装命令已复制') } catch { ElMessage.warning('复制失败，请手动选择复制') } }
async function copyToken(value:string) { try { await copyText(value);ElMessage.success('节点密钥已复制') } catch { ElMessage.warning('复制失败，请手动选择复制') } }
const protocolDialog=ref(false),securityDialog=ref(false),tlsInboundDialog=ref(false)
watch(nodeDialog,open=>{if(!open){protocolDialog.value=false;securityDialog.value=false;tlsInboundDialog.value=false}})
function protocolsSaved(config:NodeTransportInput){applyNodeTransport(nodeForm,config)}
function securitySaved(config:NodeSecurityInput){applyNodeSecurity(nodeForm,config)}
const defaults = (): NodeInput => ({mux:{enabled:false,max_connections:0,max_streams:128,idle_timeout_seconds:60},bandwidth_up_mbps:0,bandwidth_down_mbps:0,brutal_enabled:false,client_tcp_brutal_enabled:false,ingress_multiplier:'1',egress_multiplier:'0',random:{cipher:'chacha20-poly1305',key:''},tls:tlsDefaults(), token:'', ingress_enabled:true, deny_udp_ingress_creation:false, allow_direct:true, tunnel_exit_enabled:false, tunnel_protocol:'plain_tcp',tunnel_transport:'raw_tcp',tunnel_security:'none', tunnel_listen_host:'0.0.0.0', tunnel_listen_port:0, tunnel_public_host:'', allowed_ingress_ids:[], name: '', public_address: '', notes: '', enabled: true, group_ids: [], listen_host: '0.0.0.0', port_min: 1024, port_max: 65535, max_connections: 10000, dial_timeout_seconds: 8, idle_timeout_seconds: 300, udp_idle_timeout_seconds:60, probe_interval_seconds: 5, disk_path: '/', network_interfaces: [] })
const nodeForm = reactive(defaults()), groupForm = reactive({ name: '', description: '', enabled: true, sort_order: 0, node_ids: [] as number[],members:[] as NodeGroupMember[],strategy:'least_connections',health_interval_seconds:5,health_failures:3,health_successes:2 })
function editNode(n?: Node) { editID.value = n?.id || 0; for (const k of Object.keys(defaults()) as (keyof NodeInput)[]) (nodeForm as any)[k] = n ? JSON.parse(JSON.stringify(n[k]??defaults()[k])) : defaults()[k]; nodeForm.tls=tlsConfig(nodeForm.tls);nodeForm.group_ids = [...nodeForm.group_ids]; nodeForm.allowed_ingress_ids=[...(nodeForm.allowed_ingress_ids||[])]; nodeForm.network_interfaces = [...nodeForm.network_interfaces]; interfaces.value = nodeForm.network_interfaces.join('\n'); nodeDialog.value = true }
function editGroup(g?: NodeGroup) { groupID.value = g?.id || 0; Object.assign(groupForm, { name: g?.name || '', description: g?.description || '', enabled: g?.enabled ?? true, sort_order: g?.sort_order || 0, node_ids: [...(g?.node_ids || [])],members:JSON.parse(JSON.stringify(g?.members||[])),strategy:g?.strategy==='manual'?'least_connections':g?.strategy||'least_connections',health_interval_seconds:g?.health_interval_seconds||5,health_failures:g?.health_failures||3,health_successes:g?.health_successes||2 });syncGroupMembers(); groupDialog.value = true }
function syncGroupMembers(){groupForm.members=groupForm.node_ids.map(node_id=>groupForm.members.find(m=>m.node_id===node_id)||{node_id,role:'active',weight:1})}
watch(()=>[...groupForm.node_ids],syncGroupMembers)
async function act(f: () => Promise<void>) { busy.value = true; try { await f(); emit('refresh') } catch (e) { ElMessage.error((e as Error).message) } finally { busy.value = false } }
async function saveNode() { await act(async()=>{
 nodeForm.network_interfaces=interfaces.value.split(/[,\s]+/).filter(Boolean)
 const creating=!editID.value
 const data=await api<{id:number;token:string;random?:{cipher:string;key:string}}>(creating?'admin/nodes':`admin/nodes/${editID.value}`,creating?'POST':'PUT',nodeForm)
 editID.value=data.id;nodeForm.token=data.token;if(data.random)nodeForm.random={...data.random};ElMessage.success(creating?'节点已创建，密钥已填入下方':'节点配置已保存，密钥可在下方复制')
}) }

async function saveGroup() { await act(async () => { await api(`admin/node-groups${groupID.value ? '/' + groupID.value : ''}`, groupID.value ? 'PUT' : 'POST', groupForm); groupDialog.value = false; ElMessage.success('节点组已保存') }) }
async function removeGroup(id:number){
 try {
  await ElMessageBox.confirm('删除节点组？正在被套餐使用的组无法删除。','确认删除',{confirmButtonText:'删除',cancelButtonText:'取消'})
  await act(async()=>{await api(`admin/node-groups/${id}`,'DELETE',{});ElMessage.success('节点组已删除')})
 }catch(e){if(e instanceof Error)ElMessage.error(e.message)}
}
function deleteRequest(id:number,force=false,recordOnly=false){
 const query=new URLSearchParams()
 if(force)query.set('force','true')
 if(recordOnly)query.set('record_only','true')
 return api<NodeDeleteResult>(`admin/nodes/${id}${query.size?`?${query}`:''}`,'DELETE',{})
}
function confirmsLedger(e:unknown){return e instanceof APIError&&e.code==='node_delete_confirmation_required'}
function needsManualRemoval(e:unknown){return e instanceof APIError&&e.code==='node_uninstall_manual_required'}
function queued(result:NodeDeleteResult){return 'uninstall_state' in result&&result.uninstall_state==='queued'}
function confirmLedger(detail:string){
 return ElMessageBox.confirm(`${detail}有历史用量或额度授权。历史账本仍保留，已下发但未归还的额度不会因删除返还。是否继续？`,'确认历史用量',{type:'warning',confirmButtonText:'确认继续',cancelButtonText:'取消'})
}
function confirmRecordOnly(node:Node){
 const warning=node.uninstall_status?.state==='queued'?'已下发到服务器的卸载任务仍可能完成。':'如需卸载，请到节点服务器手动处理。'
 return ElMessageBox.confirm(`将删除「${node.name}」的面板记录和密钥。${warning}`,'仅删除面板记录',{type:'warning',confirmButtonText:'仅删除面板记录',cancelButtonText:'取消'})
}
async function removeNode(node:Node,recordOnly=false){
 if(nodeActionInProgress.value)return
 removingNode.value=node.id
 try{
  if(recordOnly)await confirmRecordOnly(node)
  else{
   const retry=node.uninstall_status?.state==='failed'
   await ElMessageBox.confirm(`${retry?'重试卸载':'卸载'}「${node.name}」？请先停用节点并移走相关转发规则。已安装的节点会在连上面板后卸载对应服务。`,retry?'重试卸载':'确认卸载',{type:'warning',confirmButtonText:retry?'重试卸载':'卸载',cancelButtonText:'取消'})
  }
  let force=false
  while(true){
   try{
    const result=await deleteRequest(node.id,force,recordOnly)
    ElMessage.success(queued(result)?'卸载任务已加入队列，节点连接后处理。':'已删除节点记录')
    emit('refresh');return
   }catch(e){
    if(!force&&confirmsLedger(e)){await confirmLedger('此节点');force=true;continue}
    if(!recordOnly&&needsManualRemoval(e)){await confirmRecordOnly(node);recordOnly=true;continue}
    throw e
   }
  }
 }catch(e){if(e instanceof Error)ElMessage.error(e.message)}finally{removingNode.value=0}
}
type RemovalRow={name:string;status:string;error:string}
const removalResults=ref<RemovalRow[]>([]),removalResultOpen=ref(false)
async function removeSelectedNodes(){
 if(nodeActionInProgress.value||!selectedNodes.value.length)return
 const nodes=[...selectedNodes.value]
 bulkRemoving.value=true;batchDone.value=0;batchTotal.value=0
 try{
  await ElMessageBox.confirm(`处理选中的 ${nodes.length} 个节点？已安装的节点会在连接后卸载对应服务，未安装的节点会直接删除。请先停用节点并移走相关规则。`,'批量卸载节点',{type:'warning',confirmButtonText:`卸载 ${nodes.length} 个节点`,cancelButtonText:'取消'})
  const withLedger:{node:Node;row:RemovalRow}[]=[]
  const result=nodes.map(node=>({name:node.name,status:'未提交',error:''}))
  async function submit(node:Node,row:RemovalRow,force:boolean){
   try{
    row.status=queued(await deleteRequest(node.id,force))?'等待卸载':'已删除';row.error=''
   }catch(e){
    if(!force&&confirmsLedger(e)){row.status='待确认';withLedger.push({node,row});return}
    row.status=needsManualRemoval(e)?'需手动卸载':'提交失败'
    row.error=needsManualRemoval(e)?'请在服务器上卸载，再从对应行删除面板记录。':e instanceof Error?e.message:'请求失败'
    if(e instanceof APIError&&(e.status===401||e.status===403))return false
   }
  }
  batchTotal.value=nodes.length
  let stopped=false
  for(let i=0;i<nodes.length;i++){
   stopped=(await submit(nodes[i],result[i],false))===false
   batchDone.value++
   if(stopped)break
  }
  if(withLedger.length&&!stopped){
   try{
    await confirmLedger(`其中 ${withLedger.length} 个节点（${withLedger.slice(0,5).map(item=>item.node.name).join('、')}${withLedger.length>5?' 等':''}）`)
    for(const {node,row} of withLedger)if((await submit(node,row,true))===false)break
   }catch(e){if(e instanceof Error)ElMessage.error(e.message)}
  }
  selectVisible(false);removalResults.value=result;removalResultOpen.value=true;emit('refresh')
 }catch(e){if(e instanceof Error)ElMessage.error(e.message)}finally{bulkRemoving.value=false}
}
function runBatch(action:string){
 if(nodeActionInProgress.value)return
 if(action==='create')batchCreateOpen.value=true
 if(action==='update')void updateSelectedNodes()
 if(action==='remove')void removeSelectedNodes()
}
</script>
<template>
 <section class="surface">
  <div class="section-header">
   <strong>{{view === 'groups' ? '节点组管理' : '节点管理'}}</strong>
   <div class="admin-actions layout-actions">
    <el-dropdown v-if="view==='nodes'" trigger="click" @command="runBatch">
     <el-button :loading="batchInProgress" :disabled="nodeActionInProgress">批量操作{{batchInProgress && batchTotal ? ` · ${batchDone}/${batchTotal}` : ''}}</el-button>
     <template #dropdown><el-dropdown-menu>
      <el-dropdown-item command="create">批量创建</el-dropdown-item>
      <el-dropdown-item command="update" :disabled="!visibleNodes.length">批量更新</el-dropdown-item>
      <el-dropdown-item command="remove" :disabled="!selectedNodes.length">批量卸载</el-dropdown-item>
     </el-dropdown-menu></template>
    </el-dropdown>
    <el-button @click="editGroup()"><Icon name="folder" />创建节点组</el-button>
    <el-button type="primary" @click="editNode()"><Icon name="add" />添加节点</el-button>
   </div>
  </div>
  <div class="section-body layout-stack">
   <nav class="admin-resource-tabs"><a href="/admin/nodes" :class="{active:view==='nodes'}">节点列表</a><a href="/admin/node_groups" :class="{active:view==='groups'}">节点组</a></nav>
   <div v-if="view==='nodes'" class="layout-stack">
    <div class="node-filters layout-toolbar">
     <el-input v-model="nodeSearch" aria-label="搜索节点" placeholder="搜索名称、公网地址" clearable />
     <el-select v-model="nodeScope" aria-label="筛选节点状态" placeholder="全部状态">
      <el-option label="全部状态" value="" /><el-option label="在线" value="online" />
      <el-option label="离线" value="offline" /><el-option label="已停用" value="disabled" />
     </el-select>
     <el-select v-model="nodeGroup" aria-label="筛选节点组" filterable>
      <el-option label="全部节点组" :value="0" />
      <el-option v-for="group in groups" :key="group.id" :label="group.name" :value="group.id" />
     </el-select>
     <span v-if="selectedNodes.length" class="subtle node-selection layout-actions">已选 {{selectedNodes.length}} / {{visibleNodes.length}}<el-button link type="primary" @click="selectVisible(false)">清空</el-button></span>
    </div>
    <p v-if="nodeScope==='offline'" class="field-tip node-filter-tip">仅显示已启用的离线节点 <el-button link type="primary" @click="nodeScope=''">显示全部节点</el-button></p>
    <el-table class="resource-table" :data="visibleNodes" row-key="id" empty-text="暂无匹配的节点">
     <el-table-column width="48" align="center">
      <template #header>
       <input type="checkbox" aria-label="全选当前筛选结果" :disabled="!visibleNodes.length" :checked="allVisibleSelected" :indeterminate.prop="selectedNodes.length>0&&!allVisibleSelected" @change="selectVisible(!allVisibleSelected)" />
      </template>
      <template #default="{row}">
       <label class="node-select-area" @pointerdown="startSelection(row.id,$event)" @pointerenter="extendSelection(row.id,$event)" @click="preventDuplicateClick">
        <input type="checkbox" :aria-label="`选择 ${row.name}`" :checked="selectedIDs.has(row.id)" @change="checkboxChanged(row.id,$event)" />
       </label>
      </template>
     </el-table-column>
     <el-table-column prop="name" label="名称" min-width="140" show-overflow-tooltip />
     <el-table-column prop="public_address" label="公网地址" min-width="155" show-overflow-tooltip><template #default="{ row }">{{ row.public_address || '未配置' }}</template></el-table-column>
     <el-table-column label="节点组" min-width="145"><template #default="{ row }"><GroupTags :ids="row.group_ids" :groups="groups" empty-text="未分组" /></template></el-table-column>
     <el-table-column label="用途" width="112"><template #default="{ row }"><el-tag v-if="row.ingress_enabled" class="group-chip">入口</el-tag><el-tag v-if="row.tunnel_exit_enabled" class="group-chip" type="success">出口</el-tag><span v-if="!row.ingress_enabled && !row.tunnel_exit_enabled" class="subtle">未启用</span></template></el-table-column>
     <el-table-column label="状态" min-width="92">
      <template #default="{row}">
       <span :class="{danger:row.uninstall_status?.state==='failed'}">{{row.uninstall_status?.state==='queued'?'等待卸载':row.uninstall_status?.state==='failed'?'卸载失败':!row.enabled?'已停用':row.online?'在线':'离线'}}</span>
       <small v-if="row.uninstall_status?.error" class="danger">{{row.uninstall_status.error}}</small>
      </template>
     </el-table-column>
     <el-table-column label="配置同步" min-width="130"><template #default="{ row }"><span :class="{ danger: row.sync_error }">{{ row.sync_error || (row.applied_revision >= row.config_revision && row.applied_revision ? '已同步' : '等待同步') }}</span></template></el-table-column>
     <el-table-column label="节点版本" min-width="145"><template #default="{row}"><div>{{row.agent_version || '未知版本'}}</div><small v-if="row.update_status" :class="{danger:row.update_status.state==='failed'}">{{updateStates[row.update_status.state]}} {{row.update_status.error}}</small><small v-else-if="!row.update_supported" class="subtle">需安装更新服务</small></template></el-table-column>
     <el-table-column label="TLS 证书" min-width="130"><template #default="{row}"><span v-if="row.tunnel_security==='tls'">{{row.tls_error || row.tls_status}}<small v-if="row.tls_not_after"> · {{new Date(row.tls_not_after).toLocaleDateString()}}</small></span><span v-else>—</span></template></el-table-column>
     <el-table-column prop="active_connections" label="转发连接" width="84" />
     <el-table-column fixed="right" class-name="table-actions" label="操作" width="196">
      <template #default="{row}">
       <div class="layout-actions node-row-actions">
        <el-button link type="primary" @click="installation(row.id)">安装命令</el-button>
        <el-button link type="primary" @click="editNode(row)">编辑</el-button>
        <el-button link type="primary" tag="a" :href="`/admin/nodes/${row.id}/ddns`">DDNS</el-button>
        <el-button link type="primary" tag="a" :href="`/admin/nodes/${row.id}/ip-recovery`">自动换 IP</el-button>
        <el-button link type="primary" :loading="updatingNode===row.id" :disabled="nodeActionInProgress || !row.online || !!row.uninstall_status || ['queued','running'].includes(row.update_status?.state || '')" @click="updateNode(row)">更新</el-button>
        <el-button v-if="row.uninstall_status?.state!=='queued'" link type="danger" :loading="removingNode===row.id" :disabled="nodeActionInProgress" @click="removeNode(row)">{{row.uninstall_status?.state==='failed'?'重试卸载':'卸载'}}</el-button>
        <el-button v-if="row.uninstall_status || (row.bound&&!row.enabled&&!row.uninstall_supported)" link type="danger" :disabled="nodeActionInProgress" @click="removeNode(row,true)">仅删除面板记录</el-button>
       </div>
      </template>
     </el-table-column>
    </el-table>
   </div>
   <div v-else><el-table class="resource-table" :data="groups" empty-text="暂无节点组"><el-table-column prop="name" label="名称" min-width="170" /><el-table-column prop="description" label="说明" min-width="200" /><el-table-column label="节点数" width="100"><template #default="{ row }">{{ row.node_ids.length }}</template></el-table-column><el-table-column label="负载策略" min-width="150"><template #default="{row}">{{({least_connections:'加权最少连接',round_robin:'加权轮询',random:'加权随机'} as Record<string,string>)[row.strategy]||'加权最少连接'}}</template></el-table-column><el-table-column prop="plan_count" label="关联套餐" width="100" /><el-table-column prop="sort_order" label="显示顺序" width="100" /><el-table-column label="状态" width="76"><template #default="{ row }">{{ row.enabled ? '启用' : '停用' }}</template></el-table-column><el-table-column fixed="right" class-name="table-actions" label="操作" width="130"><template #default="{ row }"><el-button link type="primary" @click="editGroup(row)">编辑</el-button><el-button link type="danger" :disabled="row.plan_count > 0" @click="removeGroup(row.id)">删除</el-button></template></el-table-column></el-table></div></div></section>
 <NodeBatchCreateDialog v-model="batchCreateOpen" :groups="groups" :make-defaults="defaults" @created="emit('refresh')" />
 <el-dialog v-model="removalResultOpen" title="节点批量卸载结果" width="650px" class="managed-form-dialog">
  <el-table :data="removalResults">
   <el-table-column prop="name" label="节点" min-width="130" />
   <el-table-column prop="status" label="状态" width="110" />
   <el-table-column prop="error" label="说明" min-width="210" />
  </el-table>
  <template #footer><el-button @click="removalResultOpen=false">关闭</el-button></template>
 </el-dialog>
 <el-dialog v-model="batchResultOpen" title="节点批量更新结果" width="620px" class="managed-form-dialog">
  <div v-if="batchResult" class="layout-stack node-dialog-stack">
   <p>{{batchResult.version}}：已下发 {{batchResult.submitted}} 个，提交失败 {{batchResult.failures.length}} 个。</p>
   <p class="subtle">跳过：{{skippedText(batchResult.skipped)}}。</p>
   <el-alert v-if="batchResult.notSubmitted" :title="`登录状态或权限已变化，剩余 ${batchResult.notSubmitted} 个节点未提交`" type="warning" :closable="false" />
   <el-table v-if="batchResult.failures.length" :data="batchResult.failures"><el-table-column prop="name" label="节点" min-width="140" /><el-table-column prop="error" label="提交失败原因" min-width="240" /></el-table>
   <p class="field-tip">已下发表示任务已进入更新队列，实际更新进度请查看列表中的节点版本栏。</p>
  </div>
  <template #footer><el-button @click="batchResultOpen=false">关闭</el-button></template>
 </el-dialog>
 <el-dialog v-model="nodeDialog" :title="editID ? '编辑节点' : '添加节点'" width="700px" top="4vh" class="managed-form-dialog"><el-form label-position="top" @submit.prevent="saveNode"><div class="form-grid"><el-form-item label="节点名称"><el-input v-model="nodeForm.name" maxlength="80" /></el-form-item><el-form-item label="公网 IP / 域名"><el-input v-model="nodeForm.public_address" placeholder="用于转发入口地址" /></el-form-item></div><el-form-item label="节点密钥"><el-input v-model="nodeForm.token" placeholder="留空保存后自动生成"><template #append><el-button :disabled="!nodeForm.token" @click="copyToken(nodeForm.token)">复制</el-button></template></el-input><span class="field-tip">留空保存会生成新密钥；修改已安装节点的密钥后，还需更新节点端的 NEKOPASS_NODE_TOKEN 并重启服务。</span></el-form-item><el-form-item label="备注（仅管理员可见）"><el-input v-model="nodeForm.notes" type="textarea" :rows="2" /></el-form-item><el-form-item label="所属节点组"><el-select v-model="nodeForm.group_ids" multiple filterable><el-option v-for="g in groups" :key="g.id" :label="g.name" :value="g.id" /></el-select></el-form-item><div class="form-section-title">转发设置</div><div class="form-grid"><el-form-item label="监听地址"><el-input v-model="nodeForm.listen_host" placeholder="0.0.0.0 或 ::" /></el-form-item><el-form-item label="节点总连接数上限"><LimitInput v-model="nodeForm.max_connections" unit="条" :max="1000000" /></el-form-item><el-form-item label="可分配端口起始"><el-input-number v-model="nodeForm.port_min" :min="1024" :max="65535" /></el-form-item><el-form-item label="可分配端口结束"><el-input-number v-model="nodeForm.port_max" :min="nodeForm.port_min" :max="65535" /></el-form-item><el-form-item label="目标建连超时 / 秒"><el-input-number v-model="nodeForm.dial_timeout_seconds" :min="1" :max="120" /></el-form-item><el-form-item label="UDP 会话空闲超时 / 秒"><el-input-number v-model="nodeForm.udp_idle_timeout_seconds" :min="10" :max="3600" /></el-form-item><el-form-item label="空闲连接超时 / 秒"><el-input-number v-model="nodeForm.idle_timeout_seconds" :min="10" :max="86400" /></el-form-item></div><NodeConnectionFields :form="nodeForm" :nodes="nodes" :nodeID="editID" @edit-transport="protocolDialog=true" @edit-security="securityDialog=true"><template #ingress-actions><el-form-item label="TLS 入站"><el-button :disabled="!editID" plain @click="tlsInboundDialog=true">编辑 TLS 入站</el-button></el-form-item></template></NodeConnectionFields><div class="form-section-title">探针设置</div><div class="form-grid"><el-form-item label="采样间隔 / 秒"><el-input-number v-model="nodeForm.probe_interval_seconds" :min="2" :max="60" /></el-form-item><el-form-item label="磁盘监测路径"><el-input v-model="nodeForm.disk_path" placeholder="/" /></el-form-item></div><el-form-item label="统计网卡"><el-input v-model="interfaces" type="textarea" :rows="2" placeholder="留空自动选择默认路由网卡；手动填写时一行一个，如 eth0" /></el-form-item><el-form-item label="启用节点"><el-switch v-model="nodeForm.enabled" /><span class="field-tip switch-hint">停用会关闭该节点上的转发，保留主控连接和探针上报。</span></el-form-item></el-form><template #footer><el-button @click="nodeDialog = false">关闭</el-button><el-button type="primary" :loading="busy" @click="saveNode">保存并下发</el-button></template></el-dialog>
 <NodeSecurityDialog v-model="securityDialog" :initial="nodeForm" @saved="securitySaved"/>
 <TLSInboundsDialog v-model="tlsInboundDialog" :nodeID="editID" @saved="emit('refresh')" />
 <NodeProtocolsDialog v-model="protocolDialog" :initial="nodeForm" @saved="protocolsSaved" />
 <el-dialog v-model="groupDialog" :title="groupID ? '编辑节点组' : '创建节点组'" width="720px" top="4vh" class="managed-form-dialog"><el-form label-position="top"><el-form-item label="节点组名称"><el-input v-model="groupForm.name" /></el-form-item><el-form-item label="说明"><el-input v-model="groupForm.description" type="textarea" :rows="2" /></el-form-item><el-form-item label="组内节点"><el-select v-model="groupForm.node_ids" multiple filterable><el-option v-for="n in nodes" :key="n.id" :label="n.name" :value="n.id" /></el-select></el-form-item><el-form-item label="出口负载策略"><el-select v-model="groupForm.strategy"><el-option label="加权最少连接" value="least_connections"/><el-option label="加权轮询" value="round_robin"/><el-option label="加权随机" value="random"/></el-select></el-form-item>
 <el-table v-if="groupForm.members.length" :data="groupForm.members" class="group-members-table"><el-table-column label="节点" min-width="150"><template #default="{row}">{{nodes.find(n=>n.id===row.node_id)?.name}}<small v-if="!nodes.find(n=>n.id===row.node_id)?.tunnel_exit_enabled" class="subtle"> · 未启用出口</small></template></el-table-column><el-table-column label="出口职责" width="170"><template #default="{row}"><el-select v-model="row.role"><el-option label="正常出口" value="active"/><el-option label="容灾备用" value="standby"/><el-option label="不参与出口转发" value="disabled"/></el-select></template></el-table-column><el-table-column label="权重" width="160"><template #default="{row}"><el-input-number v-model="row.weight" :min="1" :max="1000" :disabled="row.role==='disabled'"/></template></el-table-column></el-table>
 <p class="field-tip">所有正常出口不可用时才使用备用出口。职责只影响本组的出口调度，不改变套餐的节点访问权限。</p>
 <div class="form-grid group-health-fields"><el-form-item label="探测间隔（秒）"><el-input-number v-model="groupForm.health_interval_seconds" :min="2" :max="60"/></el-form-item><el-form-item label="连续失败次数"><el-input-number v-model="groupForm.health_failures" :min="1" :max="20"/></el-form-item><el-form-item label="连续恢复次数"><el-input-number v-model="groupForm.health_successes" :min="1" :max="20"/></el-form-item></div>
 <el-form-item label="显示顺序"><el-input-number v-model="groupForm.sort_order" /></el-form-item><el-form-item label="启用节点组"><el-switch v-model="groupForm.enabled" /></el-form-item></el-form><template #footer><el-button @click="groupDialog = false">取消</el-button><el-button type="primary" :loading="busy" @click="saveGroup">保存节点组</el-button></template></el-dialog>
 
<el-dialog v-model="installDialog" title="节点一键安装命令" width="780px" class="managed-form-dialog" @closed="installCommand=''">
 <div class="layout-stack node-dialog-stack">
  <p>在节点服务器上以 root 运行。命令已包含面板地址、主控端口、此节点密钥和连接方式。</p>
  <p v-if="installExisting" class="field-tip">重复执行会覆盖程序和连接配置，连接信息不变时保留原状态。服务器完全重建后，可使用原密钥恢复此节点；旧服务器应已离线，主控与节点均需支持状态恢复。无法确认用量的旧流量授权会按已消耗处理。</p>
  <p v-else class="field-tip">wget 下载脚本后由 bash 执行；命令可重复使用，更换节点密钥后重新复制。</p>
  <el-input :model-value="installCommand" type="textarea" :rows="6" readonly class="install-command" />
  <p class="field-tip">下载源、版本、主控地址和连接方式来自 <a class="text-link" href="/admin/settings">系统设置</a>。管理 API 密钥不会下发到节点。</p>
 </div>
 <template #footer><el-button @click="installDialog=false">关闭</el-button><el-button type="primary" @click="copyInstall">复制安装命令</el-button></template>
 </el-dialog>
</template>

<style scoped>
.group-health-fields {
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 128px), 1fr));
}

.node-filters > .el-input {
  flex: 1 1 220px;
  max-width: 300px;
  min-width: 0;
}

.node-filters > .el-select {
  flex: 0 1 155px;
  min-width: 0;
}

.node-selection,
.node-row-actions {
  column-gap: 6px;
}

.node-filter-tip,
.node-dialog-stack > p {
  margin: 0;
}

.node-select-area {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 28px;
  cursor: pointer;
  user-select: none;
}

input[type='checkbox'] {
  accent-color: var(--el-color-primary);
  margin: 0;
  cursor: pointer;
}

@media (max-width: 700px) {
  .resource-table :deep(.el-table__cell.el-table-fixed-column--right) {
    position: static !important;
    border-left: 0;
  }

  .resource-table :deep(.el-table__cell.el-table-fixed-column--right::before) {
    display: none;
  }
}

@media (max-width: 540px) {
  .node-filters {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .node-filters > .el-input,
  .node-selection {
    grid-column: 1 / -1;
  }

  .node-filters > .el-input,
  .node-filters > .el-select {
    width: 100%;
    max-width: none;
  }
}
</style>

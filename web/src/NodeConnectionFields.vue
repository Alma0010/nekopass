<script setup lang="ts">
import {computed,toRefs,watch} from 'vue'
import type {NodeProtocolsInput} from './node-protocol'
const props=defineProps<{form:NodeProtocolsInput;nodes:{id:number;name:string;ingress_enabled:boolean}[];nodeID:number}>()
const {form:nodeForm,nodes,nodeID}=toRefs(props)
defineEmits<{'edit-transport':[];'edit-security':[]}>()
const transportName=computed(()=>({raw_tcp:'raw(tcp)',h2:'h2',raw_udp:'raw(udp)'}[nodeForm.value.tunnel_transport]||nodeForm.value.tunnel_transport))
watch(()=>nodeForm.value.tunnel_transport,transport=>{if(transport==='raw_udp')nodeForm.value.tls.fingerprint='off'},{immediate:true})
</script>
<template>
 <div data-testid="node-connection-fields">
  <div class="form-section-title">入口配置</div>
  <el-form-item label="允许作为入口"><el-switch v-model="nodeForm.ingress_enabled"/></el-form-item>
  <el-form-item v-if="nodeForm.ingress_enabled" label="允许用户选择直转">
   <el-switch v-model="nodeForm.allow_direct"/>
   <span class="field-tip switch-hint">关闭后，此入口的规则必须选择已关联的出口节点。</span>
  </el-form-item>
  <slot name="ingress-actions"/>
  <div class="form-section-title">出口配置</div>
  <el-form-item label="作为出口节点">
   <el-switch v-model="nodeForm.tunnel_exit_enabled"/>
   <span class="field-tip switch-hint">关闭时仍可编辑并保存配置，启用出口后生效。</span>
  </el-form-item>
  <div class="form-grid">
   <el-form-item label="隧道对外地址"><el-input v-model="nodeForm.tunnel_public_host" placeholder="入口节点可访问的域名或 IP"/></el-form-item>
   <el-form-item label="出口监听地址"><el-input v-model="nodeForm.tunnel_listen_host" placeholder="0.0.0.0"/></el-form-item>
   <el-form-item label="出口监听端口"><el-input-number v-model="nodeForm.tunnel_listen_port" :min="0" :max="65535"/><span class="field-tip">0 表示未配置；启用出口时须填写端口。</span></el-form-item>
   <el-form-item label="公网隧道端口">
    <el-input-number v-model="nodeForm.tls.public_port" :min="0" :max="65535"/>
    <span class="field-tip">0 使用监听端口；支持端口映射。</span>
   </el-form-item>
  </div>
  <el-form-item label="允许哪些入口使用此出口">
   <el-select v-model="nodeForm.allowed_ingress_ids" multiple filterable placeholder="选择入口节点">
    <el-option v-for="n in nodes.filter(n=>n.id!==nodeID&&n.ingress_enabled)" :key="n.id" :label="n.name" :value="n.id"/>
   </el-select>
   <span class="field-tip">用户还需要同时获得入口和出口所属节点组的套餐权限。</span>
  </el-form-item>
  <el-form-item label="传输协议">
   <div class="admin-actions">
    <el-tag data-testid="node-transport-summary">{{transportName}}</el-tag>
    <el-tag data-testid="node-security-summary" type="info">{{nodeForm.tunnel_security==='tls'?'TLS':'不加密'}}</el-tag>
    <el-button type="primary" plain @click="$emit('edit-transport')">编辑传输协议</el-button>
    <el-button plain @click="$emit('edit-security')">安全性设置</el-button>
   </div>
  </el-form-item>
 </div>
</template>

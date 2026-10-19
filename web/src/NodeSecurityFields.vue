<script setup lang="ts">
import type {NodeProtocolsInput} from './node-protocol'
import {computed,toRefs} from 'vue'
const props=defineProps<{form:NodeProtocolsInput}>()
const {form:nodeForm}=toRefs(props)
const dnsJSON=defineModel<string>('dnsJSON',{required:true})
const paddingText=computed({get:()=> (nodeForm.value.tls.padding_scheme||[]).join('\n'),set:(value:string)=>{nodeForm.value.tls.padding_scheme=value?value.split(/\r?\n/):[]}})
const paddingPlaceholder='stop=8\n0=30-30\n1=100-400\n2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000\n3=9-9,500-1000\n4=500-1000\n5=500-1000\n6=500-1000\n7=500-1000'
</script>
<template>
<div data-testid="node-security-fields">
<el-form-item label="安全性"><el-select v-model="nodeForm.tunnel_security" data-testid="node-security"><el-option label="不加密" value="none"/><el-option label="TLS" value="tls"/></el-select></el-form-item>
    <template v-if="nodeForm.tunnel_security==='tls'">
    <div class="form-section-title">TLS 设置</div>
    <p class="field-tip">所有入口连接此出口时使用以下设置。</p>
    <div class="form-grid">
    <el-form-item label="uTLS 指纹">
    <el-select v-model="nodeForm.tls.fingerprint" :disabled="nodeForm.tunnel_transport==='raw_udp'">
    <el-option label="关闭（标准 TLS）" value="off"/>
    <el-option label="Chrome（库内预设）" value="chrome"/>
    <el-option label="Firefox（库内预设）" value="firefox"/>
    </el-select>
    </el-form-item>
    <el-form-item label="连接 SNI（可选）">
    <el-input v-model="nodeForm.tls.client_sni" placeholder="留空使用本出口的证书域名"/>
    </el-form-item>
    </div>
    <template v-if="nodeForm.tunnel_transport==='raw_tcp'">
    <el-form-item label="TLS Padding"><el-switch v-model="nodeForm.tls.padding_enabled" data-testid="tls-padding-enabled"/><span class="field-tip">仅用于 raw(tcp)＋TLS 中转隧道，入口和出口均需支持 Padding。</span></el-form-item>
    <el-form-item v-if="nodeForm.tls.padding_enabled" label="Padding 策略（AnyTLS 格式）">
    <el-input v-model="paddingText" type="textarea" :rows="9" :placeholder="paddingPlaceholder" data-testid="tls-padding-scheme"/>
    <span class="field-tip">一行一条，留空使用默认策略。stop 最大 64，认证填充最多 2048 字节，其他分片最大 16384 字节。设置由主控统一下发到两端。</span>
    </el-form-item>
    </template>
    <div class="form-section-title">证书设置</div>

    <div class="form-grid">
    <el-form-item label="证书域名 / SNI">
    <el-input v-model="nodeForm.tls.server_name" placeholder="你拥有的域名" />
    </el-form-item>

    <el-form-item label="证书模式">
    <el-select v-model="nodeForm.tls.certificate_mode">
    <el-option label="自签名（自动生成并下发信任）" value="self_signed"/>
    <el-option label="手动导入证书" value="import"/>
    <el-option label="DNS 自动申请 / 续期" value="acme_dns"/>
    <el-option label="HTTP 自动申请 / 续期" value="acme_http"/>
    </el-select>
    </el-form-item>


    </div>
<template v-if="nodeForm.tls.certificate_mode==='import'">
    <el-form-item label="证书链 PEM">
    <el-input v-model="nodeForm.tls.certificate" type="textarea" :rows="4"/>
    </el-form-item>
    <el-form-item label="匹配的私钥 PEM">
    <el-input v-model="nodeForm.tls.private_key" type="textarea" :rows="4"/>
    </el-form-item>
    <el-form-item label="可选私有 CA">
    <el-input v-model="nodeForm.tls.root_ca" type="textarea" :rows="3"/>
    </el-form-item>
    </template>
    <template v-if="nodeForm.tls.certificate_mode.startsWith('acme_')">
    <p class="field-tip">启用出口并保存自动申请配置后，会向所选 CA 申请证书并接受其服务条款。DNS 密钥只由主控使用，不下发节点。</p>
    <div class="form-grid">
    <el-form-item label="ACME 邮箱">
    <el-input v-model="nodeForm.tls.acme_email"/>
    </el-form-item>
    <el-form-item label="ACME Directory HTTPS 地址">
    <el-input v-model="nodeForm.tls.acme_directory"/>
    </el-form-item>
    <el-form-item v-if="nodeForm.tls.certificate_mode==='acme_http'" label="节点 HTTP 验证监听端口">
    <el-input-number v-model="nodeForm.tls.http_challenge_port" :min="1" :max="65535"/>
    <span class="field-tip">CA 从公网 80 验证；使用其他本地端口需配置转发。</span>
    </el-form-item>
    <el-form-item v-else label="DNS 服务商">
    <el-select v-model="nodeForm.tls.dns_provider">
    <el-option label="Cloudflare" value="cloudflare"/>
    <el-option label="阿里 DNS" value="alidns"/>
    <el-option label="DNSPod" value="dnspod"/>
    </el-select>
    </el-form-item>
    </div>
    <el-form-item label="可选 ACME 私有根 CA">
    <el-input v-model="nodeForm.tls.acme_root_ca" type="textarea" :rows="2" placeholder="公共 CA 留空；私有测试 CA 填公开证书"/>
    </el-form-item>
    <el-form-item label="可选签发根 CA">
    <el-input v-model="nodeForm.tls.root_ca" type="textarea" :rows="2" placeholder="公共 CA 留空；用于入口验证私有 CA 签发的证书"/>
    </el-form-item>
    <el-form-item v-if="nodeForm.tls.certificate_mode==='acme_dns'" label="DNS 凭据 JSON">
    <el-input v-model="dnsJSON" type="textarea" :rows="4"/>
    <span class="field-tip">Cloudflare：api_token、可选 zone_token；阿里 DNS：access_key_id、access_key_secret；DNSPod：login_token。</span>
    </el-form-item>
    </template>
    </template>
 <template v-if="nodeForm.tunnel_transport!=='raw_udp'&&(nodeForm.tunnel_transport==='h2'||nodeForm.tunnel_security==='tls')">
  <div class="form-section-title">未认证访问 / Fallback</div>
  <el-form-item label="Fallback 网站地址">
   <el-input v-model="nodeForm.tls.fallback_url" placeholder="https://www.example.com（留空返回普通 404）"/>
   <span class="field-tip">用于普通 HTTP 访问及未通过认证的 HTTP 请求；留空或网站不可用时返回普通 404。</span>
  </el-form-item>
 </template>
</div>
</template>

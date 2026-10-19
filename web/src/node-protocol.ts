import type {NodeInput,TunnelTLS} from './api'
export const nodeTLSDefaults = ():TunnelTLS => ({padding_enabled:false,padding_scheme:[],server_name:'',client_sni:'',fingerprint:'off',certificate_mode:'self_signed',certificate:'',private_key:'',root_ca:'',host:'',fallback_url:'',path:'/api/stream',public_port:0,pool_size:2,stream_window_mib:16,connection_window_mib:64,max_streams:256,site_title:'Welcome',acme_email:'',acme_directory:'https://acme-v02.api.letsencrypt.org/directory',acme_root_ca:'',http_challenge_port:80,dns_provider:'cloudflare',dns_credentials:{} as Record<string,string>})
export type NodeProtocolsInput = Pick<NodeInput,'tls'|'ingress_enabled'|'allow_direct'|'tunnel_exit_enabled'|'tunnel_protocol'|'tunnel_transport'|'tunnel_security'|'tunnel_listen_host'|'tunnel_listen_port'|'tunnel_public_host'|'allowed_ingress_ids'>
export const protocolDefaults = ():NodeProtocolsInput => ({tls:nodeTLSDefaults(),ingress_enabled:true,allow_direct:true,tunnel_exit_enabled:false,tunnel_protocol:'plain_tcp',tunnel_transport:'raw_tcp',tunnel_security:'none',tunnel_listen_host:'0.0.0.0',tunnel_listen_port:0,tunnel_public_host:'',allowed_ingress_ids:[]})
const transportFields=['host','path','pool_size','stream_window_mib','connection_window_mib','max_streams'] as const
export type NodeTransportInput={tunnel_transport:string;tls:Pick<TunnelTLS,typeof transportFields[number]>}
export function nodeTransportInput(form:NodeProtocolsInput):NodeTransportInput{
 return {tunnel_transport:form.tunnel_transport,tls:Object.fromEntries(transportFields.map(key=>[key,form.tls[key]])) as NodeTransportInput['tls']}
}
export function applyNodeTransport(form:NodeProtocolsInput,input:NodeTransportInput){form.tunnel_transport=input.tunnel_transport;Object.assign(form.tls,input.tls)}
const securityFields=['padding_enabled','padding_scheme','server_name','client_sni','fingerprint','certificate_mode','certificate','private_key','root_ca','fallback_url','site_title','acme_email','acme_directory','acme_root_ca','http_challenge_port','dns_provider','dns_credentials'] as const
export type NodeSecurityInput={tunnel_security:string;tls:Pick<TunnelTLS,typeof securityFields[number]>}
export function nodeSecurityInput(form:NodeProtocolsInput):NodeSecurityInput{
 return JSON.parse(JSON.stringify({tunnel_security:form.tunnel_security,tls:Object.fromEntries(securityFields.map(key=>[key,form.tls[key]]))}))
}
export function applyNodeSecurity(form:NodeProtocolsInput,input:NodeSecurityInput){form.tunnel_security=input.tunnel_security;Object.assign(form.tls,input.tls)}
export function nodeDNSCredentials(form:NodeProtocolsInput,text:string){
 if(form.tunnel_security!=='tls'||form.tls.certificate_mode!=='acme_dns')return form.tls.dns_credentials
 const value:unknown=JSON.parse(text)
 if(!value||typeof value!=='object'||Array.isArray(value))throw new Error('DNS 凭据须为 JSON 对象')
 return value as Record<string,string>
}

# TLS 入站

多个用户的 TCP 转发规则共用一个入口端口，节点读取客户端 ClientHello 中的 SNI，选择规则后透传完整 TLS 握手和密文。客户端与最终目标建立 TLS 连接，证书、TLS 版本和 ALPN 由两端协商，节点不解密，也不需要配置入站证书。

## 管理员配置

打开「节点管理 → 编辑节点 → TLS 入站」，新增共享入口，填写名称和端口。端口可使用 `443`，不受普通规则的可分配端口范围限制。

| 配置项 | 用途 |
| --- | --- |
| 启用 | 开关整个共享入口，保留入口及关联规则。 |
| 允许的 SNI 范围 | 一行一个域名。`example.com` 允许自身及子域名，`.example.com` 仅允许子域名；留空允许所有域名。 |
| 接受 Proxy Protocol | 默认关闭。开启后连接须在 ClientHello 前发送 PROXY v1 / v2 头，自动识别版本；可信来源网段在「系统设置 → 转发安全」配置。 |
| ClientHello 并发读取上限 | 限制正在读取 ClientHello 的连接数量，默认 128；分流完成后释放名额，不限制已建立的转发连接。 |
| ClientHello 读取超时（秒） | 等待 PROXY 头和完整 ClientHello 的时间，默认 5 秒。 |
| 拒绝没有 SNI 的连接 | 默认开启。关闭后，无 SNI 的 TLS 连接可使用管理员默认规则。 |
| 未知 SNI | 拒绝连接，或转发到本入口下的管理员默认规则；仍需满足 SNI 允许范围。 |
| 仅允许 TLS 入站 | 此节点的普通 TCP 规则停止转发，TCP 规则须选择共享 TLS 入口；UDP 不受影响。 |

同一节点的共享入口、普通 TCP 规则及 TCP 隧道监听不能占用同一端口。同一共享入口的 SNI 不可重复，暂停规则仍保留其 SNI。

## 拒绝行为

共享入口采用 Nginx 样式的拒绝回应，需要更新节点端后生效：

- 有效的 TLS ClientHello 若被 SNI 策略拒绝，返回标准的 `unrecognized_name` 致命告警后关闭连接，与 Nginx 的 `ssl_reject_handshake on` 使用相同告警；不返回 HTTP 页面或节点证书。暂停、停用或额度受限的规则也不会借此落入其他用户的默认规则。
- 误发到 TLS 端口的明文 HTTP/1.0、HTTP/1.1 请求，返回带 `Server: nginx` 的 `400 Bad Request` 页面。HEAD 只返回响应头；页面不回显请求中的域名、路径或其他内容。
- 缺少 HTTP/1.1 Host、重复 Host、无效请求头或明文 HTTP/2 前言，返回普通的 Nginx `400 Bad Request` 页面；普通无冒号标头按 Nginx 的空值标头处理。请求行过长返回 `414 Request-URI Too Large`，请求头过长返回带 `Request Header Or Cookie Too Large` 的 400 页面。HEAD 错误回应同样只返回响应头。
- 请求行及单个标头行须小于 8 KiB，整个请求头读取预算为 32 KiB，与 Nginx 默认 `large_client_header_buffers 4 8k` 的容量一致；不读取请求体，并沿用 ClientHello 的超时与并发限制。不完整、超时、超过并发限制或无法识别为 HTTP/TLS 的请求仍直接关闭。

Nginx 的 `444` 表示不发送响应头就关闭连接，因此没有可同时发送的 `444` 状态行或 `Server: nginx` 标识。尚未完成 TLS 握手时，也不能将明文 HTTP 响应塞进 TLS 连接。参见 [Nginx return](https://nginx.org/en/docs/http/ngx_http_rewrite_module.html#return) 和 [Nginx ssl_reject_handshake](https://nginx.org/en/docs/http/ngx_http_ssl_module.html#ssl_reject_handshake)。

此处只调整拒绝回应，不保证与真实 Nginx 的所有网络指纹一致，也不提供客户端身份认证或重放拦截。匹配成功的 TLS 连接仍完整透传；AnyTLS 等后端的认证失败处理由后端负责。

## 用户创建规则

在「转发规则」选择 TCP、入口和出口，将「入站方式」设为「共享 TLS 入站」，选择共享入口并填写 SNI 域名，一行一个。目标填写实际提供 TLS 服务的地址与端口，监听端口使用管理员配置的共享端口。

例如两个用户分别添加以下规则：

| 共享入口 | SNI | 目标 |
| --- | --- | --- |
| `入口IP:443` | `app.example.com` | `目标A:443` |
| `入口IP:443` | `api.example.com` | `目标B:8443` |

客户端连接同一个入口地址与端口，分别发送对应 SNI。客户端验证的是目标服务的证书；目标证书应覆盖客户端使用的域名。SNI、ALPN 和其他握手内容原样透传。

选择出口后，完整 TLS 数据经所选隧道传到目标，入口到出口的传输协议和加密方式仍在节点中配置。

## 入口节点组

选择入口节点组后，同样可以选择「共享 TLS 入站」。管理员须先在每个入口成员上配置相同端口的共享入口；规则选择这个共同端口，每台节点使用各自的共享入口 ID，入口名称可以不同。

SNI 必须同时满足所有成员的允许范围，且不能与任一成员已有规则重复。新增成员时会重新检查共享入口及域名冲突，失败则整次修改不保存；移出成员后释放该节点上的 SNI，保留历史流量。暂停规则仍保留全部成员上的 SNI。关闭某个共享入口可暂停该成员的转发，修改端口或删除仍被引用的入口前，须先迁移关联规则。

规则在完整备份中使用 `ingress_group_id` 和 `tls_inbound_port` 保存绑定；单节点继续使用 `tls_inbound_id`。nyanpass 兼容格式不包含这些设置，共享 TLS 规则请使用完整备份。

各规则独立统计流量、执行资源限制和启停。流量按双向实际转发的 TCP 字节累计，包括目标 TLS 握手和记录开销，不包含节点间隧道的额外封装。

共享 TLS 入站规则使用「Nekopass 完整备份」导入导出。升级时保留原共享端口、SNI 和用量，移除旧入站证书及共享规则的回源 TLS 设置；主控和入口节点均需更新到支持 SNI 透传的版本。

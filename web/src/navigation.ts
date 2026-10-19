import type { Component } from 'vue'
type Loader = () => Promise<{ default: Component }>
export type PageRoute = { title: string; active: string; admin: boolean; load: Loader; props?: Record<string, unknown> }
const user = (title: string, active: string, load: Loader): PageRoute => ({ title, active, admin: false, load })
const admin = (title: string, active: string, load: Loader, props?: Record<string, unknown>): PageRoute => ({ title, active, admin: true, load, props })
const routes: Record<string, PageRoute> = {
 '/': user('主页', 'home', () => import('./views/HomeView.vue')),
 '/profile': user('个人中心', 'profile', () => import('./views/ProfileView.vue')),
 '/forward_rules': user('转发规则', 'rules', () => import('./views/ForwardRulesView.vue')),
 '/shop': user('商城', 'shop', () => import('./views/ShopView.vue')),
 '/orders': user('我的订单', 'orders', () => import('./views/OrdersView.vue')),
 '/tickets': user('工单', 'tickets', () => import('./views/TicketsView.vue')),
 '/referrals': user('邀请返利', 'referrals', () => import('./views/ReferralsView.vue')),
 '/node_status': user('节点状态', 'probe', () => import('./views/NodeStatusView.vue')),
 '/admin': admin('后台首页', 'admin', () => import('./views/AdminView.vue')),
 '/admin/orders': admin('订单查询', 'orders', () => import('./views/AdminOrdersView.vue')),
 '/admin/tickets': admin('工单管理', 'tickets', () => import('./views/TicketsView.vue')),
 '/admin/announcements': admin('公告管理', 'announcements', () => import('./views/AdminAnnouncementsView.vue')),
 '/admin/users': admin('用户管理', 'users', () => import('./views/AdminUsersView.vue')),
 '/admin/plans': admin('套餐管理', 'plans', () => import('./views/AdminPlansView.vue')),
 '/admin/nodes': admin('节点管理', 'nodes', () => import('./views/AdminNodesView.vue'), { view: 'nodes' }),
 '/admin/node_groups': admin('节点组管理', 'nodes', () => import('./views/AdminNodesView.vue'), { view: 'groups' }),
 '/admin/payment_gateways': admin('支付方式', 'payment_gateways', () => import('./views/AdminPaymentGatewaysView.vue')),
 '/admin/settings': admin('系统设置', 'settings', () => import('./views/AdminSettingsView.vue')),
}
export function resolveRoute(path: string): PageRoute | undefined {
 if (routes[path]) return routes[path]
 if (/^\/admin\/users\/[1-9]\d*\/forward_rules$/.test(path)) return admin('用户转发规则', 'users', () => import('./views/AdminUserRulesView.vue'))
 if (/^\/admin\/nodes\/[1-9]\d*\/ddns$/.test(path)) return admin('节点 DDNS', 'nodes', () => import('./views/AdminNodeDDNSView.vue'))
 if (/^\/admin\/nodes\/[1-9]\d*\/ip-recovery$/.test(path)) return admin('自动换 IP', 'nodes', () => import('./views/AdminNodeIPRecoveryView.vue'))
 if (/^\/admin\/nodes\/[1-9]\d*\/protocols$/.test(path)) return admin('节点传输协议', 'nodes', () => import('./views/AdminNodeProtocolsView.vue'))
}

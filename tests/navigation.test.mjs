import test from 'node:test'
import assert from 'node:assert/strict'
import { resolveRoute } from '../web/src/navigation.ts'

test('only known application pages participate in local navigation', () => {
 for (const path of ['/', '/profile', '/forward_rules', '/shop', '/orders', '/tickets', '/referrals', '/node_status']) {
  assert.equal(resolveRoute(path)?.admin, false, path)
 }
 for (const path of ['/login', '/register', '/api/v1/me', '/assets/app.js', '/unknown', '//example.test/admin', 'https://example.test/admin']) assert.equal(resolveRoute(path), undefined, path)
})

test('admin routes retain their access and selected navigation metadata', () => {
 for (const path of ['/admin', '/admin/orders', '/admin/tickets', '/admin/announcements', '/admin/users', '/admin/plans', '/admin/nodes', '/admin/node_groups', '/admin/payment_gateways', '/admin/settings']) assert.equal(resolveRoute(path)?.admin, true, path)
 assert.equal(resolveRoute('/admin/node_groups')?.active, 'nodes')
 assert.deepEqual(resolveRoute('/admin/nodes')?.props, {view:'nodes'})
 assert.deepEqual(resolveRoute('/admin/node_groups')?.props, {view:'groups'})
})

test('dynamic detail pages accept positive IDs and reject malformed paths', () => {
 assert.equal(resolveRoute('/admin/users/123/forward_rules')?.active, 'users')
 for (const suffix of ['ddns', 'ip-recovery', 'protocols']) assert.equal(resolveRoute('/admin/nodes/123/' + suffix)?.active, 'nodes')
 for (const path of ['/admin/users/0/forward_rules', '/admin/nodes/-1/ddns', '/admin/nodes/01/ddns', '/admin/nodes/1/settings', '/admin/nodes/1/ddns/extra']) assert.equal(resolveRoute(path), undefined, path)
})

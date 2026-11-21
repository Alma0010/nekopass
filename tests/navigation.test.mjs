import test from 'node:test'
import assert from 'node:assert/strict'
import { resolveRoute, routeEnabled } from '../web/src/navigation.ts'
import { withFeatureDefaults } from '../web/src/features.ts'

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

test('a missing feature configuration keeps existing routes available', () => {
 const features = withFeatureDefaults()
 for (const path of ['/shop', '/orders', '/tickets', '/referrals', '/admin/orders', '/admin/tickets', '/admin/payment_gateways']) {
  assert.equal(routeEnabled(resolveRoute(path), features), true, path)
 }
})

test('turning off a module blocks both user and admin routes tied to it', () => {
 const modules = {
  commerce: ['/shop', '/orders', '/admin/orders', '/admin/payment_gateways'],
  invites: ['/referrals'],
  tickets: ['/tickets', '/admin/tickets'],
 }
 const all = ['/', '/forward_rules', '/admin/plans', '/admin/users', '/admin/settings', ...Object.values(modules).flat()]
 for (const [key, disabledPaths] of Object.entries(modules)) {
  const features = withFeatureDefaults({[key]: false})
  for (const path of all) {
   assert.equal(routeEnabled(resolveRoute(path), features), !disabledPaths.includes(path), `${key}: ${path}`)
  }
 }
})

test('closing the user area leaves administrator operations available', () => {
 const features = withFeatureDefaults({users: false})
 for (const path of ['/', '/profile', '/forward_rules', '/shop', '/orders', '/tickets', '/referrals', '/node_status']) {
  assert.equal(routeEnabled(resolveRoute(path), features), false, path)
 }
 for (const path of ['/admin', '/admin/users', '/admin/users/123/forward_rules', '/admin/tickets', '/admin/settings']) {
  assert.equal(routeEnabled(resolveRoute(path), features), true, path)
 }
})

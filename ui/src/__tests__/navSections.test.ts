import { describe, expect, it } from 'vitest'
import {
  PRIMARY_NAV,
  activityTabs,
  collapsesSidebar,
  connectionsTabs,
  developerTabs,
  isCanvasRoute,
  legacyRedirect,
  sectionOf,
  settingsRows,
} from '@/components/layout/nav'

describe('primary navigation', () => {
  it('has exactly five places, in order', () => {
    expect(PRIMARY_NAV.map((n) => n.label)).toEqual(['Home', 'Flows', 'Connections', 'Activity', 'Settings'])
    expect(PRIMARY_NAV.map((n) => n.to)).toEqual(['/', '/workflows', '/connections', '/activity', '/settings'])
  })

  it('files every page under one of the five places', () => {
    expect(sectionOf('/')).toBe('home')
    expect(sectionOf('/workflows')).toBe('flows')
    expect(sectionOf('/workflows/abc/edit')).toBe('flows')
    expect(sectionOf('/approvals')).toBe('flows')
    expect(sectionOf('/connections')).toBe('connections')
    expect(sectionOf('/sources/new')).toBe('connections')
    expect(sectionOf('/sinks/k1/edit')).toBe('connections')
    expect(sectionOf('/marketplace')).toBe('connections')
    expect(sectionOf('/activity')).toBe('activity')
    expect(sectionOf('/logs')).toBe('activity')
    expect(sectionOf('/dead-letters')).toBe('activity')
    expect(sectionOf('/audit-logs')).toBe('activity')
    expect(sectionOf('/health')).toBe('activity')
    expect(sectionOf('/lineage')).toBe('activity')
    expect(sectionOf('/compliance')).toBe('activity')
    expect(sectionOf('/ml/models')).toBe('activity')
    expect(sectionOf('/ai/budget')).toBe('activity')
    expect(sectionOf('/settings')).toBe('settings')
    expect(sectionOf('/users/new')).toBe('settings')
    expect(sectionOf('/vhosts')).toBe('settings')
    expect(sectionOf('/workers/w1/edit')).toBe('settings')
    expect(sectionOf('/environments')).toBe('settings')
    expect(sectionOf('/promotions')).toBe('settings')
    expect(sectionOf('/profile')).toBe('settings')
    expect(sectionOf('/schemas')).toBe('settings')
    expect(sectionOf('/secrets')).toBe('settings')
    expect(sectionOf('/developer')).toBe('settings')
    expect(sectionOf('/login')).toBeNull()
  })
})

describe('sidebar on a flow', () => {
  it('collapses to the icon rail whenever a flow is open, in any mode', () => {
    expect(collapsesSidebar('/workflows/new')).toBe(true)
    expect(collapsesSidebar('/workflows/abc')).toBe(true)
    expect(collapsesSidebar('/workflows/abc/edit')).toBe(true)
    expect(collapsesSidebar('/workflows')).toBe(false)
    expect(collapsesSidebar('/')).toBe(false)
  })

  it('goes full-bleed only on the canvas', () => {
    expect(isCanvasRoute('/workflows/new')).toBe(true)
    expect(isCanvasRoute('/workflows/abc/edit')).toBe(true)
    expect(isCanvasRoute('/workflows/abc')).toBe(false)
  })
})

describe('old links keep working', () => {
  it('sends each retired list page to its new home', () => {
    expect(legacyRedirect('/sources')).toEqual({ to: '/connections', search: { tab: 'sources' } })
    expect(legacyRedirect('/sinks')).toEqual({ to: '/connections', search: { tab: 'sinks' } })
    expect(legacyRedirect('/marketplace')).toEqual({ to: '/connections', search: { tab: 'community' } })
    expect(legacyRedirect('/logs')).toEqual({ to: '/activity', search: { tab: 'logs' } })
    expect(legacyRedirect('/dead-letters')).toEqual({ to: '/activity', search: { tab: 'dead-letters' } })
    expect(legacyRedirect('/health')).toEqual({ to: '/activity', search: { tab: 'health' } })
    expect(legacyRedirect('/audit-logs')).toEqual({ to: '/activity', search: { tab: 'audit' } })
    expect(legacyRedirect('/lineage')).toEqual({ to: '/activity', search: { tab: 'lineage' } })
    expect(legacyRedirect('/compliance')).toEqual({ to: '/activity', search: { tab: 'compliance' } })
    expect(legacyRedirect('/ml/models')).toEqual({ to: '/activity', search: { tab: 'ml' } })
    expect(legacyRedirect('/ai/budget')).toEqual({ to: '/activity', search: { tab: 'ai-budget' } })
    expect(legacyRedirect('/schemas')).toEqual({ to: '/developer', search: { tab: 'schemas' } })
    expect(legacyRedirect('/secrets')).toEqual({ to: '/developer', search: { tab: 'secrets' } })
  })

  it('leaves forms and detail pages where they are', () => {
    expect(legacyRedirect('/sources/new')).toBeNull()
    expect(legacyRedirect('/sinks/k1/edit')).toBeNull()
    expect(legacyRedirect('/workflows/abc')).toBeNull()
    expect(legacyRedirect('/settings')).toBeNull()
  })
})

describe('hub tabs follow the role', () => {
  it('shows every connection tab to everyone', () => {
    expect(connectionsTabs('Viewer').map((t) => t.value)).toEqual(['sources', 'sinks', 'community'])
  })

  it('keeps the audit log and machine learning behind their roles', () => {
    expect(activityTabs('Viewer').map((t) => t.value)).toEqual(['logs', 'dead-letters', 'approvals', 'health', 'lineage', 'compliance', 'ai-budget'])
    expect(activityTabs('Editor').map((t) => t.value)).toContain('ml')
    expect(activityTabs('Editor').map((t) => t.value)).not.toContain('audit')
    expect(activityTabs('Administrator').map((t) => t.value)).toEqual(['logs', 'dead-letters', 'approvals', 'health', 'audit', 'lineage', 'compliance', 'ml', 'ai-budget'])
  })

  it('shows secrets only to those who may change them', () => {
    expect(developerTabs('Viewer').map((t) => t.value)).toEqual(['schemas'])
    expect(developerTabs('Editor').map((t) => t.value)).toEqual(['schemas', 'secrets'])
  })

  it('offers each settings row to the roles that may use it', () => {
    const viewer = settingsRows('Viewer').map((r) => r.label)
    expect(viewer).toEqual(['Releases', 'Developer', 'My account'])
    expect(settingsRows('Editor').map((r) => r.label)).toEqual(['Releases', 'Developer', 'My account'])
    expect(settingsRows('Administrator').map((r) => r.label)).toEqual([
      'Workspace', 'People', 'Notifications', 'Security', 'Releases', 'Developer', 'Audit log', 'My account',
    ])
  })
})

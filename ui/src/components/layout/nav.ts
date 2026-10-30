/**
 * The shape of the navigation, kept apart from the components so it can be
 * tested as data.
 *
 * Everything in Hermod lives in one of five places: Home, Flows, Connections,
 * Activity and Settings. A page that does not fit one of them goes inside one
 * of them, never into the sidebar. The retired top-level pages (Sources,
 * Sinks, Logs, Dead Letters, Mesh Health, ...) still answer at their old URLs
 * and redirect into the hub that now holds them, so bookmarks and links in
 * notifications keep working.
 */

export type Section = 'home' | 'flows' | 'connections' | 'activity' | 'settings'

export type Role = string | null | undefined

export interface NavItem {
  key: Section
  to: string
  label: string
}

export const PRIMARY_NAV: readonly NavItem[] = [
  { key: 'home', to: '/', label: 'Home' },
  { key: 'flows', to: '/workflows', label: 'Flows' },
  { key: 'connections', to: '/connections', label: 'Connections' },
  { key: 'activity', to: '/activity', label: 'Activity' },
  { key: 'settings', to: '/settings', label: 'Settings' },
]

const SECTION_PREFIXES: ReadonlyArray<[Section, string[]]> = [
  ['flows', ['/workflows', '/approvals']],
  ['connections', ['/connections', '/sources', '/sinks', '/marketplace']],
  [
    'activity',
    ['/activity', '/logs', '/dead-letters', '/audit-logs', '/health', '/lineage', '/compliance', '/ml', '/ai'],
  ],
  [
    'settings',
    ['/settings', '/users', '/vhosts', '/workers', '/environments', '/promotions', '/profile', '/schemas', '/secrets', '/developer'],
  ],
]

const PUBLIC_PATHS = new Set(['/login', '/setup', '/forgot-password'])

function startsWithPath(pathname: string, prefix: string): boolean {
  return pathname === prefix || pathname.startsWith(prefix + '/')
}

/** Which of the five places a URL belongs to; null for the public pages. */
/**
 * How tall the phone's bottom tabs are: one 56px row of buttons, the 1px
 * border above it, and the home-indicator inset below. The tabs, the spacer
 * that keeps page content clear of them and the workflow editor (which sizes
 * itself to the screen) all read it, so they cannot drift apart.
 */
export const BOTTOM_TABS_HEIGHT = 'calc(3.5rem + 1px + env(safe-area-inset-bottom))'

/**
 * How tall a flow page's action bar is on a phone, where it takes the place of
 * the bottom tabs: 44px buttons with 12px above and below, the 1px border and
 * the home-indicator inset. .hermod-flow-actions draws it to this height.
 */
export const FLOW_ACTIONS_HEIGHT = 'calc(4.25rem + 1px + env(safe-area-inset-bottom))'

export function sectionOf(pathname: string): Section | null {
  if (PUBLIC_PATHS.has(pathname)) return null
  if (pathname === '/') return 'home'
  for (const [section, prefixes] of SECTION_PREFIXES) {
    if (prefixes.some((p) => startsWithPath(pathname, p))) return section
  }
  return null
}

/** The editor canvas: no page padding, no card around the content. */
export function isCanvasRoute(pathname: string): boolean {
  return /^\/workflows\/(new|[^/]+\/edit)$/.test(pathname)
}

/**
 * Every page of one flow, read only or editing: the page brings its own
 * gutter and canvas card instead of sitting in the content card, so the
 * view and the editor share one frame.
 */
export function isFlowPage(pathname: string): boolean {
  return /^\/workflows\/[^/]+(\/edit)?$/.test(pathname)
}

/**
 * Whenever a flow is open, in the read-only view or the editor, the sidebar
 * folds to its icon rail so the diagram gets the width.
 */
export function collapsesSidebar(pathname: string): boolean {
  return /^\/workflows\/[^/]+(\/edit)?$/.test(pathname)
}

export interface Redirect {
  to: string
  search?: Record<string, string>
}

const LEGACY: Record<string, Redirect> = {
  '/sources': { to: '/connections', search: { tab: 'sources' } },
  '/sinks': { to: '/connections', search: { tab: 'sinks' } },
  '/marketplace': { to: '/connections', search: { tab: 'community' } },
  '/logs': { to: '/activity', search: { tab: 'logs' } },
  '/dead-letters': { to: '/activity', search: { tab: 'dead-letters' } },
  '/health': { to: '/activity', search: { tab: 'health' } },
  '/audit-logs': { to: '/activity', search: { tab: 'audit' } },
  '/lineage': { to: '/activity', search: { tab: 'lineage' } },
  '/compliance': { to: '/activity', search: { tab: 'compliance' } },
  '/ml/models': { to: '/activity', search: { tab: 'ml' } },
  '/ai/budget': { to: '/activity', search: { tab: 'ai-budget' } },
  '/schemas': { to: '/developer', search: { tab: 'schemas' } },
  '/secrets': { to: '/developer', search: { tab: 'secrets' } },
}

/** Where a retired top-level page now lives, or null if the URL is current. */
export function legacyRedirect(pathname: string): Redirect | null {
  return LEGACY[pathname] ?? null
}

export interface HubTab {
  value: string
  label: string
}

const isAdmin = (role: Role) => role === 'Administrator'
const canEdit = (role: Role) => role === 'Administrator' || role === 'Editor'

export function connectionsTabs(_role: Role): HubTab[] {
  return [
    { value: 'sources', label: 'Sources' },
    { value: 'sinks', label: 'Sinks' },
    { value: 'community', label: 'Community' },
  ]
}

export function activityTabs(role: Role): HubTab[] {
  const tabs: HubTab[] = [
    { value: 'logs', label: 'Logs' },
    { value: 'dead-letters', label: 'Dead letters' },
    { value: 'approvals', label: 'Approvals' },
    { value: 'health', label: 'Mesh health' },
  ]
  if (isAdmin(role)) tabs.push({ value: 'audit', label: 'Audit log' })
  tabs.push({ value: 'lineage', label: 'Lineage' }, { value: 'compliance', label: 'Compliance' })
  if (canEdit(role)) tabs.push({ value: 'ml', label: 'Machine learning' })
  tabs.push({ value: 'ai-budget', label: 'AI budget' })
  return tabs
}

export function developerTabs(role: Role): HubTab[] {
  const tabs: HubTab[] = [{ value: 'schemas', label: 'Schema registry' }]
  if (canEdit(role)) tabs.push({ value: 'secrets', label: 'Secrets' })
  return tabs
}

export interface SettingsRow {
  key: string
  label: string
  detail: string
  to: string
  search?: Record<string, string>
}

export function settingsRows(role: Role): SettingsRow[] {
  const rows: SettingsRow[] = []
  if (isAdmin(role)) {
    rows.push(
      { key: 'workspace', label: 'Workspace', detail: 'Virtual hosts, workers, platform and governance', to: '/settings', search: { tab: 'vhosts' } },
      { key: 'people', label: 'People', detail: 'Who can sign in and what they may do', to: '/users' },
      { key: 'notifications', label: 'Notifications', detail: 'Alerts, channels and quiet hours', to: '/settings', search: { tab: 'observability' } },
      { key: 'security', label: 'Security', detail: 'Sessions, passwords and access policy', to: '/settings', search: { tab: 'security' } },
    )
  }
  rows.push({ key: 'releases', label: 'Releases', detail: 'Environments and promotion between them', to: '/environments' })
  rows.push({ key: 'developer', label: 'Developer', detail: 'Schema registry, secrets, API and MCP', to: '/developer' })
  if (isAdmin(role)) {
    rows.push({ key: 'audit', label: 'Audit log', detail: 'Who changed what, and when', to: '/activity', search: { tab: 'audit' } })
  }
  rows.push({ key: 'account', label: 'My account', detail: 'Your profile, password and theme', to: '/profile' })
  return rows
}

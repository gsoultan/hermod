import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  Outlet,
  redirect,
  useNavigate,
} from '@tanstack/react-router'
const SourcesPage = lazy(async () => ({ default: (await import('./pages/SourcesPage')).SourcesPage }))
const AddSourcePage = lazy(async () => ({ default: (await import('./pages/AddSourcePage')).AddSourcePage }))
const EditSourcePage = lazy(async () => ({ default: (await import('./pages/EditSourcePage')).EditSourcePage }))
const SinksPage = lazy(async () => ({ default: (await import('./pages/SinksPage')).SinksPage }))
const AddSinkPage = lazy(async () => ({ default: (await import('./pages/AddSinkPage')).AddSinkPage }))
const EditSinkPage = lazy(async () => ({ default: (await import('./pages/EditSinkPage')).EditSinkPage }))
const UsersPage = lazy(async () => ({ default: (await import('./pages/UsersPage')).UsersPage }))
const ProfilePage = lazy(async () => ({ default: (await import('./pages/ProfilePage')).ProfilePage }))
const AddUserPage = lazy(async () => ({ default: (await import('./pages/AddUserPage')).AddUserPage }))
const EditUserPage = lazy(async () => ({ default: (await import('./pages/EditUserPage')).EditUserPage }))
const VHostsPage = lazy(async () => ({ default: (await import('./pages/VHostsPage')).VHostsPage }))
const AddVHostPage = lazy(async () => ({ default: (await import('./pages/AddVHostPage')).AddVHostPage }))
const EditVHostPage = lazy(async () => ({ default: (await import('./pages/EditVHostPage')).EditVHostPage }))
const WorkersPage = lazy(async () => ({ default: (await import('./pages/WorkersPage')).WorkersPage }))
const AddWorkerPage = lazy(async () => ({ default: (await import('./pages/AddWorkerPage')).AddWorkerPage }))
const EditWorkerPage = lazy(async () => ({ default: (await import('./pages/EditWorkerPage')).EditWorkerPage }))
const SetupPage = lazy(async () => ({ default: (await import('./pages/SetupPage')).SetupPage }))
const LoginPage = lazy(async () => ({ default: (await import('./pages/LoginPage')).LoginPage }))
const ForgotPasswordPage = lazy(async () => ({ default: (await import('./pages/ForgotPasswordPage')).ForgotPasswordPage }))
const ErrorPage = lazy(async () => ({ default: (await import('./pages/ErrorPage')).ErrorPage }))
const NotFoundPage = lazy(async () => ({ default: (await import('./pages/NotFoundPage')).NotFoundPage }))
const Layout = lazy(async () => ({ default: (await import('./components/Layout')).Layout }))
import { Center, Loader } from '@mantine/core'
import { apiFetch, getRoleFromToken } from './api'
import { getToken } from './auth/storage'
import {lazy, Suspense} from "react"
// Lazy-load remaining pages to comply with bundle-size & lazy-loading guidelines
const SettingsPage = lazy(async () => ({ default: (await import('./pages/SettingsPage')).SettingsPage }))
const LogsPage = lazy(async () => ({ default: (await import('./pages/LogsPage')).LogsPage }))
const SchemasPage = lazy(async () => ({ default: (await import('./pages/SchemasPage')).SchemasPage }))
const AuditLogsPage = lazy(async () => ({ default: (await import('./pages/AuditLogsPage')).AuditLogsPage }))
const LineagePage = lazy(async () => ({ default: (await import('./pages/LineagePage')).LineagePage }))
const GlobalHealthPage = lazy(async () => ({ default: (await import('./pages/GlobalHealthPage')).default }))
const CommunityMarketplace = lazy(async () => ({ default: (await import('./pages/Marketplace/CommunityMarketplace')).CommunityMarketplace }))
const WorkflowDetailPage = lazy(async () => ({ default: (await import('./pages/WorkflowDetailPage')).WorkflowDetailPage }))
const DashboardPage = lazy(async () => ({ default: (await import('./pages/DashboardPage')).DashboardPage }))
const WorkflowEditorPage = lazy(async () => ({ default: (await import('./pages/WorkflowEditorPage')).default }))
const WorkflowsPage = lazy(async () => ({ default: (await import('./pages/WorkflowsPage')).default }))
const CompliancePage = lazy(async () => ({ default: (await import('./pages/ComplianceDashboard')).ComplianceDashboard }))
const ApprovalsPage = lazy(async () => ({ default: (await import('./pages/ApprovalsPage')).ApprovalsPage }))
interface RouterContext {
  configStatus?: {
    configured: boolean
    user_setup: boolean
  }
}

// Simple cache for config status to avoid fetching on every navigation
const CONFIG_STATUS_CACHE_KEY = 'hermod_config_status_cache_v1'
const CONFIG_STATUS_TTL_MS = 30_000

async function getCachedConfigStatus() {
  try {
    const raw = sessionStorage.getItem(CONFIG_STATUS_CACHE_KEY)
    if (raw) {
      const cached = JSON.parse(raw) as { ts: number; data: { configured: boolean; user_setup: boolean } }
      if (cached && Date.now() - cached.ts < CONFIG_STATUS_TTL_MS) {
        return cached.data
      }
    }
  } catch {}

  const res = await apiFetch('/api/config/status')
  if (!res.ok) throw new Error('Failed to fetch config status')
  const data = await res.json()
  try {
    sessionStorage.setItem(CONFIG_STATUS_CACHE_KEY, JSON.stringify({ ts: Date.now(), data }))
  } catch {}
  return data
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader /></Center>}>
      <Layout>
        <Outlet />
      </Layout>
    </Suspense>
  ),
  errorComponent: ({ error, reset }) => {
    // For 401 Unauthorized, apiFetch already handles redirect
    if (error instanceof Error && error.message === 'Unauthorized') {
      return null;
    }
    
    return (
      <Suspense fallback={<Center h="100vh"><Loader /></Center>}>
        <Layout>
          <ErrorPage error={error} reset={reset} />
        </Layout>
      </Suspense>
    );
  },
  beforeLoad: async ({ location }: { location: any }) => {
    // Skip checks on public/setup pages to avoid loops
    if (location.pathname === '/setup' || location.pathname === '/login' || location.pathname === '/forgot-password') {
      return
    }

    try {
      // Defer network until we know user is authenticated
      const token = getToken()
      if (!token) {
        throw redirect({
          to: '/login',
          search: {
            redirect: location.pathname,
          },
        })
      }

      const data = await getCachedConfigStatus()
      if (!data.configured || !data.user_setup) {
        throw redirect({
          to: '/setup',
          search: {
            isConfigured: data.configured,
          }
        })
      }

      return { configStatus: data }
    } catch (error) {
      if (error instanceof Error && error.message === 'Failed to fetch config status') {
        // Allow error boundary to handle; nothing special here
      }
      throw error
    }
  },
})

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <DashboardPage />
    </Suspense>
  ),
})

const sourcesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/sources',
})

const sourcesIndexRoute = createRoute({
  getParentRoute: () => sourcesRoute,
  path: '/',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <SourcesPage />
    </Suspense>
  ),
})

const addSourceRoute = createRoute({
  getParentRoute: () => sourcesRoute,
  path: 'new',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <AddSourcePage />
    </Suspense>
  ),
})

const editSourceRoute = createRoute({
  getParentRoute: () => sourcesRoute,
  path: '$sourceId/edit',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <EditSourcePage />
    </Suspense>
  ),
})

const sinksRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/sinks',
})

const sinksIndexRoute = createRoute({
  getParentRoute: () => sinksRoute,
  path: '/',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <SinksPage />
    </Suspense>
  ),
})

const addSinkRoute = createRoute({
  getParentRoute: () => sinksRoute,
  path: 'new',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <AddSinkPage />
    </Suspense>
  ),
})

const editSinkRoute = createRoute({
  getParentRoute: () => sinksRoute,
  path: '$sinkId/edit',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <EditSinkPage />
    </Suspense>
  ),
})

const vhostsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/vhosts',
  beforeLoad: () => {
    if (getRoleFromToken() !== 'Administrator') {
      throw redirect({ to: '/' })
    }
  }
})

const vhostsIndexRoute = createRoute({
  getParentRoute: () => vhostsRoute,
  path: '/',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <VHostsPage />
    </Suspense>
  ),
})

const addVHostRoute = createRoute({
  getParentRoute: () => vhostsRoute,
  path: 'new',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <AddVHostPage />
    </Suspense>
  ),
})

const editVHostRoute = createRoute({
  getParentRoute: () => vhostsRoute,
  path: '$vhostId/edit',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <EditVHostPage />
    </Suspense>
  ),
})

const workersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/workers',
  beforeLoad: () => {
    if (getRoleFromToken() !== 'Administrator') {
      throw redirect({ to: '/' })
    }
  }
})

const workersIndexRoute = createRoute({
  getParentRoute: () => workersRoute,
  path: '/',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <WorkersPage />
    </Suspense>
  ),
})

const addWorkerRoute = createRoute({
  getParentRoute: () => workersRoute,
  path: 'new',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <AddWorkerPage />
    </Suspense>
  ),
})

const editWorkerRoute = createRoute({
  getParentRoute: () => workersRoute,
  path: '$workerId/edit',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <EditWorkerPage />
    </Suspense>
  ),
})


const workflowsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/workflows',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <WorkflowsPage />
    </Suspense>
  )
})

const workflowDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/workflows/$id',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <WorkflowDetailPage />
    </Suspense>
  ),
})

const workflowEditorRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/workflows/$id/edit',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <WorkflowEditorPage />
    </Suspense>
  ),
})

const addWorkflowRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/workflows/new',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <WorkflowEditorPage />
    </Suspense>
  ),
})

const usersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/users',
  beforeLoad: () => {
    if (getRoleFromToken() !== 'Administrator') {
      throw redirect({ to: '/' })
    }
  }
})

const usersIndexRoute = createRoute({
  getParentRoute: () => usersRoute,
  path: '/',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <UsersPage />
    </Suspense>
  ),
})

const addUserRoute = createRoute({
  getParentRoute: () => usersRoute,
  path: 'new',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <AddUserPage />
    </Suspense>
  ),
})

const editUserRoute = createRoute({
  getParentRoute: () => usersRoute,
  path: '$userId/edit',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <EditUserPage />
    </Suspense>
  ),
})

const profileRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/profile',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <ProfilePage />
    </Suspense>
  ),
})

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <SettingsPage />
    </Suspense>
  ),
  beforeLoad: () => {
    if (getRoleFromToken() !== 'Administrator') {
      throw redirect({ to: '/' })
    }
  }
})

const logsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/logs',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <LogsPage />
    </Suspense>
  ),
  validateSearch: (search: Record<string, unknown>): { workflow_id?: string } => {
    return {
      workflow_id: (search.workflow_id as string) || undefined,
    }
  },
})

const schemasRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/schemas',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader /></Center>}>
      <SchemasPage />
    </Suspense>
  ),
})

const auditLogsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/audit-logs',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <AuditLogsPage />
    </Suspense>
  ),
  beforeLoad: () => {
    if (getRoleFromToken() !== 'Administrator') {
      throw redirect({ to: '/' })
    }
  },
})

const lineageRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/lineage',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <LineagePage />
    </Suspense>
  ),
})

const healthRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/health',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <GlobalHealthPage />
    </Suspense>
  ),
})

const complianceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/compliance',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <CompliancePage />
    </Suspense>
  ),
})

const marketplaceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/marketplace',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <CommunityMarketplace />
    </Suspense>
  ),
})

const approvalsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/approvals',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <ApprovalsPage />
    </Suspense>
  ),
})

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <LoginPage />
    </Suspense>
  ),
  validateSearch: (search: Record<string, unknown>) => {
    return {
      redirect: (search.redirect as string) || '/',
    }
  },
  beforeLoad: () => {
    const token = localStorage.getItem('hermod_token')
    if (token) {
      throw redirect({
        to: '/',
      })
    }
  },
})

const forgotPasswordRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/forgot-password',
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <ForgotPasswordPage />
    </Suspense>
  ),
})

function SetupRouteComponent() {
  const { isConfigured } = setupRoute.useSearch()
  const navigate = useNavigate()
  
  return (
    <SetupPage
      isConfigured={isConfigured}
      onConfigured={() => {
        navigate({ to: '/' })
      }}
    />
  )
}

const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/setup',
  validateSearch: (search: Record<string, unknown>) => {
    return {
      isConfigured: (search.isConfigured as boolean) || false,
    }
  },
  beforeLoad: async () => {
    const data = await getCachedConfigStatus()
    if (data.configured && data.user_setup) {
      throw redirect({
        to: '/',
      })
    }
  },
  component: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <SetupRouteComponent />
    </Suspense>
  ),
})

const routeTree = rootRoute.addChildren([
  indexRoute,
  sourcesRoute.addChildren([
    sourcesIndexRoute,
    addSourceRoute,
    editSourceRoute,
  ]),
  sinksRoute.addChildren([
    sinksIndexRoute,
    addSinkRoute,
    editSinkRoute,
  ]),
  vhostsRoute.addChildren([
    vhostsIndexRoute,
    addVHostRoute,
    editVHostRoute,
  ]),
  workersRoute.addChildren([
    workersIndexRoute,
    addWorkerRoute,
    editWorkerRoute,
  ]),
  workflowsRoute,
  workflowDetailRoute,
  workflowEditorRoute,
  addWorkflowRoute,
  usersRoute.addChildren([
    usersIndexRoute,
    addUserRoute,
    editUserRoute,
  ]),
  profileRoute,
  settingsRoute,
  logsRoute,
  auditLogsRoute,
  schemasRoute,
  lineageRoute,
  healthRoute,
  complianceRoute,
  marketplaceRoute,
  approvalsRoute,
  loginRoute,
  forgotPasswordRoute,
  setupRoute,
])

export const router = createRouter({
  routeTree,
  defaultPendingComponent: () => (
    <Center h="100vh">
      <Loader size="xl" />
    </Center>
  ),
  defaultPendingMs: 0,
  defaultPendingMinMs: 500,
  defaultErrorComponent: ({ error, reset }) => {
    // For 401 Unauthorized, apiFetch already handles redirect
    if (error instanceof Error && error.message === 'Unauthorized') {
      return null;
    }

    return (
      <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
        <Layout>
          <ErrorPage error={error} reset={reset} />
        </Layout>
      </Suspense>
    );
  },
  defaultNotFoundComponent: () => (
    <Suspense fallback={<Center h="100vh"><Loader size="xl" /></Center>}>
      <Layout>
        <NotFoundPage />
      </Layout>
    </Suspense>
  ),
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

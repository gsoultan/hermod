import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  Outlet,
  redirect,
  useNavigate,
} from '@tanstack/react-router'
import { Layout } from './components/Layout'
import { SourcesPage } from './pages/SourcesPage'
import { AddSourcePage } from './pages/AddSourcePage'
import { EditSourcePage } from './pages/EditSourcePage'
import { SinksPage } from './pages/SinksPage'
import { AddSinkPage } from './pages/AddSinkPage'
import { EditSinkPage } from './pages/EditSinkPage'
const UsersPage = lazy(async () => ({ default: (await import('./pages/UsersPage')).UsersPage }))
import { AddUserPage } from './pages/AddUserPage'
import { EditUserPage } from './pages/EditUserPage'
import { VHostsPage } from './pages/VHostsPage'
import { AddVHostPage } from './pages/AddVHostPage'
import { EditVHostPage } from './pages/EditVHostPage'
import { WorkersPage } from './pages/WorkersPage'
import { AddWorkerPage } from './pages/AddWorkerPage'
import { EditWorkerPage } from './pages/EditWorkerPage'
import { lazy, Suspense } from 'react'
// Lazy routes to reduce initial bundle size
const WorkflowsPage = lazy(() => import('./pages/WorkflowsPage'))
// Lazy-load heavy editor page for better initial load performance
const WorkflowEditorPage = lazy(() => import('./pages/WorkflowEditorPage'))
const WorkflowDetailPage = lazy(async () => ({ default: (await import('./pages/WorkflowDetailPage')).WorkflowDetailPage }))
const SettingsPage = lazy(async () => ({ default: (await import('./pages/SettingsPage')).SettingsPage }))
import { NotificationSettingsPage } from './pages/NotificationSettingsPage'
const DashboardPage = lazy(async () => ({ default: (await import('./pages/DashboardPage')).DashboardPage }))
const LogsPage = lazy(async () => ({ default: (await import('./pages/LogsPage')).LogsPage }))
const AuditLogsPage = lazy(async () => ({ default: (await import('./pages/AuditLogsPage')).AuditLogsPage }))
import { SetupPage } from './pages/SetupPage'
import { LoginPage } from './pages/LoginPage'
import { ForgotPasswordPage } from './pages/ForgotPasswordPage'
import { ErrorPage } from './pages/ErrorPage'
import { NotFoundPage } from './pages/NotFoundPage'
import { Center, Loader } from '@mantine/core'
import { apiFetch, getRoleFromToken } from './api'

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
    <Layout>
      <Outlet />
    </Layout>
  ),
  errorComponent: ({ error, reset }) => {
    // For 401 Unauthorized, apiFetch already handles redirect
    if (error instanceof Error && error.message === 'Unauthorized') {
      return null;
    }
    
    return (
      <Layout>
        <ErrorPage error={error} reset={reset} />
      </Layout>
    );
  },
  beforeLoad: async ({ location }: { location: any }) => {
    // Skip config check for setup page to avoid infinite redirect
    if (location.pathname === '/setup' || location.pathname === '/login' || location.pathname === '/forgot-password') {
      return
    }

    try {
      const data = await getCachedConfigStatus()

      if (!data.configured || !data.user_setup) {
        throw redirect({
          to: '/setup',
          search: {
            isConfigured: data.configured,
          }
        })
      }

      const token = localStorage.getItem('hermod_token')
      if (!token) {
        throw redirect({
          to: '/login',
          search: {
            redirect: location.pathname,
          },
        })
      }
      
      return { configStatus: data }
    } catch (error) {
      if (error instanceof Error && error.message === 'Failed to fetch config status') {
        // Handle error
      }
      throw error
    }
  },
})

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: DashboardPage,
})

const sourcesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/sources',
})

const sourcesIndexRoute = createRoute({
  getParentRoute: () => sourcesRoute,
  path: '/',
  component: SourcesPage,
})

const addSourceRoute = createRoute({
  getParentRoute: () => sourcesRoute,
  path: 'new',
  component: AddSourcePage,
})

const editSourceRoute = createRoute({
  getParentRoute: () => sourcesRoute,
  path: '$sourceId/edit',
  component: EditSourcePage,
})

const sinksRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/sinks',
})

const sinksIndexRoute = createRoute({
  getParentRoute: () => sinksRoute,
  path: '/',
  component: SinksPage,
})

const addSinkRoute = createRoute({
  getParentRoute: () => sinksRoute,
  path: 'new',
  component: AddSinkPage,
})

const editSinkRoute = createRoute({
  getParentRoute: () => sinksRoute,
  path: '$sinkId/edit',
  component: EditSinkPage,
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
  component: VHostsPage,
})

const addVHostRoute = createRoute({
  getParentRoute: () => vhostsRoute,
  path: 'new',
  component: AddVHostPage,
})

const editVHostRoute = createRoute({
  getParentRoute: () => vhostsRoute,
  path: '$vhostId/edit',
  component: EditVHostPage,
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
  component: WorkersPage,
})

const addWorkerRoute = createRoute({
  getParentRoute: () => workersRoute,
  path: 'new',
  component: AddWorkerPage,
})

const editWorkerRoute = createRoute({
  getParentRoute: () => workersRoute,
  path: '$workerId/edit',
  component: EditWorkerPage,
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
  component: AddUserPage,
})

const editUserRoute = createRoute({
  getParentRoute: () => usersRoute,
  path: '$userId/edit',
  component: EditUserPage,
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

const notificationSettingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings/notifications',
  component: NotificationSettingsPage,
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

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: LoginPage,
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
  component: ForgotPasswordPage,
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
  component: SetupRouteComponent,
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
  settingsRoute,
  notificationSettingsRoute,
  logsRoute,
  auditLogsRoute,
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
      <Layout>
        <ErrorPage error={error} reset={reset} />
      </Layout>
    );
  },
  defaultNotFoundComponent: () => (
    <Layout>
      <NotFoundPage />
    </Layout>
  ),
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

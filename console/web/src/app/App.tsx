import { Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom'
import { useSession } from '../features/session/useSession'
import { FullScreenError, FullScreenLoading } from '../shared/components/States'
import { ConsoleShell } from '../layout/ConsoleShell'
import { LoginPage } from '../features/auth/LoginPage'
import { AppsPage } from '../features/apps/AppsPage'
import { PlatformOperatorsPage } from '../features/operators/PlatformOperatorsPage'
import { AppLayout } from '../features/apps/AppLayout'
import { OverviewTab } from '../features/apps/OverviewTab'
import { AppSettingsTab } from '../features/apps/AppSettingsTab'
import { ClientsTab } from '../features/clients/ClientsTab'
import { UsersTab } from '../features/users/UsersTab'
import { UserDetailPage } from '../features/users/UserDetailPage'
import { ConnectionsTab } from '../features/connections/ConnectionsTab'
import { AgentsTab } from '../features/agents/AgentsTab'
import { AuditTab } from '../features/audit/AuditTab'

function RequireAuth() {
  const { status, bootstrapError, retryBootstrap } = useSession()
  const location = useLocation()
  if (status === 'loading') return <FullScreenLoading />
  if (status === 'unavailable') {
    return <FullScreenError error={bootstrapError} onRetry={retryBootstrap} />
  }
  if (status === 'anonymous') {
    return <Navigate to="/login" state={{ from: location.pathname }} replace />
  }
  return <Outlet />
}

function NotFound() {
  return (
    <div className="empty">
      <p className="empty-title">页面不存在</p>
      <p className="empty-hint">
        <a className="table-link" href="#/apps">
          返回应用列表
        </a>
      </p>
    </div>
  )
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route element={<RequireAuth />}>
        <Route element={<ConsoleShell />}>
          <Route path="/" element={<Navigate to="/apps" replace />} />
          <Route path="/apps" element={<AppsPage />} />
          <Route path="/platform/operators" element={<PlatformOperatorsPage />} />
          <Route path="/apps/:appId" element={<AppLayout />}>
            <Route index element={<OverviewTab />} />
            <Route path="settings" element={<AppSettingsTab />} />
            <Route path="clients" element={<ClientsTab />} />
            <Route path="users" element={<UsersTab />} />
            <Route path="users/:userId" element={<UserDetailPage />} />
            <Route path="connections" element={<ConnectionsTab />} />
            <Route path="agents" element={<AgentsTab />} />
            <Route path="audit" element={<AuditTab />} />
          </Route>
          <Route path="*" element={<NotFound />} />
        </Route>
      </Route>
    </Routes>
  )
}

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createHashRouter, RouterProvider } from 'react-router-dom'
import { ProtectionProvider } from './app/protection'
import { ErrorBoundary } from './shared/components/ErrorBoundary'
import { SessionProvider } from './features/session/SessionContext'
import { initTheme } from './layout/theme'
import App from './app/App.tsx'
import './styles/index.css'

initTheme()

const router = createHashRouter([
  {
    path: '*',
    element: (
      <ErrorBoundary>
        <SessionProvider>
          <ProtectionProvider>
            <App />
          </ProtectionProvider>
        </SessionProvider>
      </ErrorBoundary>
    ),
  },
])

createRoot(document.getElementById('root')!, {
  onCaughtError: () => console.error('Console rendering failure'),
  onUncaughtError: () => console.error('Console rendering failure'),
}).render(
  <StrictMode>
    <ErrorBoundary>
      <RouterProvider router={router} />
    </ErrorBoundary>
  </StrictMode>,
)

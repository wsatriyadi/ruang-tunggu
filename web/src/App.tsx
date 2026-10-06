/**
 * Routing and the auth gate.
 *
 * The seven pages are lazy-loaded, so the first paint carries only the shell
 * and the route being opened. `/setup` and `/login` replace the shell
 * entirely; every other route needs an authenticated session.
 */

import { Suspense, lazy } from 'react';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import { Shell, Spinner } from './components';
import { useSession } from './state/useSession';
import LoginPage from './pages/Login';
import SetupPage from './pages/Setup';

const Dashboard = lazy(() => import('./pages/Dashboard'));
const VideoLibrary = lazy(() => import('./pages/VideoLibrary'));
const PlaylistPage = lazy(() => import('./pages/Playlist'));
const SchedulePage = lazy(() => import('./pages/Schedule'));
const LiveControl = lazy(() => import('./pages/LiveControl'));
const Logs = lazy(() => import('./pages/Logs'));
const SettingsPage = lazy(() => import('./pages/Settings'));

function PageFallback() {
  return (
    <div className="page">
      <Spinner size="lg" label="Memuat halaman" />
    </div>
  );
}

/** Panels, shown only to an authenticated session. */
function PanelRoutes() {
  return (
    <Shell>
      <Suspense fallback={<PageFallback />}>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/videos" element={<VideoLibrary />} />
          <Route path="/playlist" element={<PlaylistPage />} />
          <Route path="/schedule" element={<SchedulePage />} />
          <Route path="/live" element={<LiveControl />} />
          <Route path="/logs" element={<Logs />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </Suspense>
    </Shell>
  );
}

/**
 * Sends the browser to the screen the session state calls for.
 *
 * These are routes rather than an imperative navigate during render, so a
 * redirect is part of the history the back button understands.
 */
function Root() {
  const { state, loading } = useSession();

  if (loading && state === null) {
    return (
      <div className="page">
        <Spinner size="lg" label="Memeriksa sesi" />
      </div>
    );
  }

  if (state === 'setup') return <Navigate to="/setup" replace />;
  if (state === 'login') return <Navigate to="/login" replace />;

  return <PanelRoutes />;
}

export function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route
          path="/setup"
          element={
            <RequireState state="setup">
              <SetupPage />
            </RequireState>
          }
        />
        <Route
          path="/login"
          element={
            <RequireState state="login">
              <LoginPage />
            </RequireState>
          }
        />
        <Route path="*" element={<Root />} />
      </Routes>
    </BrowserRouter>
  );
}

interface RequireStateProps {
  state: 'setup' | 'login';
  children: React.ReactNode;
}

/** An auth screen reached out of turn redirects instead of rendering. */
function RequireState({ state, children }: RequireStateProps) {
  const session = useSession();
  if (session.state !== state) return <Navigate to="/" replace />;
  return children;
}
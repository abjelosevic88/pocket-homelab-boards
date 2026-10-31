import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import './styles/base.css'
import './styles/boards.css'
import './styles/app.css'
import './styles/settings.css'
import { AppProvider, useApp } from './lib/app-context'
import { Loading, ToastProvider } from './components/ui'
import Layout from './components/Layout'
import { ErrorBoundary } from './components/ErrorBoundary'
import Login from './pages/Login'
import Home from './pages/Home'
import BoardPage from './pages/Board'
import MyCards from './pages/MyCards'
import Search from './pages/Search'
import Trash from './pages/Trash'
import LoginFlow from './pages/LoginFlow'
import Settings from './pages/Settings'

function Gate({ children }: { children: React.ReactNode }) {
  const { status, user } = useApp()
  const loc = useLocation()
  if (!status) return <div className="boot"><img src="/favicon.svg" alt="" /><Loading /></div>
  if (status.needs_setup) return <Navigate to="/setup" replace />
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname + loc.search }} />
  return <>{children}</>
}

/** "/" opens Home or My cards (once per visit), as chosen in Settings → General. */
let startedOnce = false
function StartInner({ mine }: { mine: boolean }) {
  const [toMine] = React.useState(() => !startedOnce && mine)
  React.useEffect(() => { startedOnce = true }, [])
  return toMine ? <Navigate to="/my" replace /> : <Home />
}
function Start() {
  const { settings } = useApp()
  return settings ? <StartInner mine={settings.default_view === 'mine'} /> : <Loading />
}

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login mode="login" />} />
      <Route path="/setup" element={<Login mode="setup" />} />
      <Route path="/login/flow/:token" element={<Gate><LoginFlow /></Gate>} />
      <Route path="/settings/:tab?" element={<Gate><ErrorBoundary where="settings"><Settings /></ErrorBoundary></Gate>} />
      <Route element={<Gate><ErrorBoundary where="boards"><Layout /></ErrorBoundary></Gate>}>
        <Route path="/" element={<Start />} />
        <Route path="/b/:id" element={<BoardPage />} />
        <Route path="/b/:id/c/:cardId" element={<BoardPage />} />
        <Route path="/my" element={<MyCards />} />
        <Route path="/search" element={<Search />} />
        <Route path="/trash" element={<Trash />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <ErrorBoundary where="app"><ToastProvider><AppProvider><App /></AppProvider></ToastProvider></ErrorBoundary>
    </BrowserRouter>
  </React.StrictMode>,
)

// A Deck app (Nextcloud Login Flow v2) asks to sign in: the person allows it here.
import { useState } from 'react'
import { useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { Icon } from '../components/icons'
import { Loading, useAsync } from '../components/ui'

export default function LoginFlow() {
  const { token = '' } = useParams()
  const { user } = useApp()
  const { data, error, loading } = useAsync(() => api.get<{ client_name: string }>(`${V1}/loginflow/${encodeURIComponent(token)}`), [token])
  const [state, setState] = useState<'ask' | 'busy' | 'done' | 'denied'>('ask')
  const [err, setErr] = useState('')
  const allow = async () => { setState('busy'); try { await api.post(`${V1}/loginflow/${encodeURIComponent(token)}/approve`); setState('done') } catch (e) { setErr((e as Error).message); setState('ask') } }
  return (
    <div className="login-wrap">
      <div className="login-card">
        <div className="login-brand"><img src="/favicon.svg" alt="" width={44} height={44} /><h1>Connect an app</h1></div>
        {loading ? <Loading /> : error ? <div className="callout danger"><Icon name="alert" size={15} /><span>This sign-in link has expired or was already used. Start again in the app.</span></div>
          : state === 'done' ? <div className="stack"><div className="callout success"><Icon name="check-circle" size={15} /><span><b>Done.</b> Go back to {data?.client_name || 'the app'} — it is signed in as you.</span></div><p className="muted small">You can remove its access any time under Settings → API &amp; shortcuts.</p></div>
          : state === 'denied' ? <div className="callout"><Icon name="info" size={15} /><span>Not connected. You can close this page.</span></div>
          : <div className="stack">
              <p>Allow <b>{data?.client_name || 'this app'}</b> to see and change your boards as <b>{user?.name || user?.email}</b>?</p>
              <p className="muted small">It gets its own key (listed under Settings → API &amp; shortcuts) and works like a Nextcloud Deck client.</p>
              {err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
              <button type="button" className="btn primary lg block" onClick={allow} disabled={state === 'busy'}>{state === 'busy' ? 'Connecting…' : 'Allow'}</button>
              <button type="button" className="btn lg block" onClick={() => setState('denied')}>Cancel</button>
            </div>}
      </div>
    </div>
  )
}

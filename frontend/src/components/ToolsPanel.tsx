import { useEffect, useRef, useState } from 'react'
import { t } from '@/i18n'
import { useUiStore } from '@/store/ui-store'
import { Plus, Trash2 } from 'lucide-react'
import { api, ApiError } from '@/lib/api'
import type { MCPServerIn, MCPOAuthIn, ToolStatus } from '@/lib/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'

// 纯函数：从编辑状态组装 saveTools 的 server payload。
// 规则：HTTP 模式 → type/url；token_url+client_id 均空 → 不带 oauth；
// client_secret 留空 → 省略键（后端 has_secret 语义=保留已存）。
export function buildServerIn(srv: MCPServerIn): MCPServerIn {
  const out: MCPServerIn = { ...srv }
  if (srv.type === 'http') {
    out.url = srv.url?.trim() || undefined
  }
  const o = srv.oauth
  if (o && (o.token_url.trim() || o.client_id.trim())) {
    const built: MCPOAuthIn = {
      token_url: o.token_url.trim(),
      client_id: o.client_id.trim(),
      scopes: (o.scopes ?? [])
        .map((s) => s.trim())
        .filter(Boolean),
    }
    if (o.client_secret) built.client_secret = o.client_secret
    if (o.authorize_url?.trim()) built.authorize_url = o.authorize_url.trim()
    if (o.redirect_port && o.redirect_port > 0) built.redirect_port = o.redirect_port
    out.oauth = built
  } else {
    delete out.oauth
  }
  return out
}

export function ToolsPanel() {
  // 订阅 lang：切换语言时触发重渲染（t() 内部读取 store 最新值）
  useUiStore((s) => s.lang)
  const [status, setStatus] = useState<ToolStatus | null>(null)
  const [skillsEnabled, setSkillsEnabled] = useState(false)
  const [mcpEnabled, setMcpEnabled] = useState(false)
  const [servers, setServers] = useState<MCPServerIn[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)
  const cancelled = useRef(false)

  function applyStatus(st: ToolStatus) {
    if (cancelled.current) return
    setStatus(st)
    setSkillsEnabled(st.skills.enabled)
    setMcpEnabled(st.mcp.enabled)
    setServers(
      st.mcp.servers.map((s) => ({
        name: s.name,
        command: s.command,
        args: s.args,
        type: s.type,
        url: s.url,
        oauth: s.oauth
          ? {
              token_url: s.oauth.token_url,
              client_id: s.oauth.client_id,
              scopes: s.oauth.scopes,
              authorize_url: s.oauth.authorize_url,
              redirect_port: s.oauth.redirect_port,
            }
          : undefined,
      }))
    )
  }

  useEffect(() => {
    cancelled.current = false
    void api
      .getTools()
      .then(applyStatus)
      .catch((e) => {
        if (!cancelled.current) setError(e instanceof ApiError ? e.message : t('common.loadFailed'))
      })
    return () => {
      cancelled.current = true
    }
  }, [])

  async function save() {
    if (busy) return
    setBusy(true)
    setError('')
    setSaved(false)
    try {
      await api.saveTools({ skillsEnabled, mcpEnabled, mcpServers: servers.map(buildServerIn) })
      applyStatus(await api.getTools())
      setSaved(true)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t('mcp.saveFailed'))
    } finally {
      setBusy(false)
    }
  }

  function patchServer(idx: number, patch: Partial<MCPServerIn>) {
    setServers((prev) => prev.map((s, i) => (i === idx ? { ...s, ...patch } : s)))
  }

  const available = status?.skills.available ?? []
  const nextName = `server-${servers.length + 1}`

  return (
    <div className="flex flex-col gap-3">
      <div className="text-sm font-medium">{t('mcp.title')}</div>
      <Separator />

      {/* Skills */}
      <div className="flex flex-col gap-2">
        <label className="flex items-center gap-2 text-xs">
          <input
            type="checkbox"
            className="h-4 w-4"
            checked={skillsEnabled}
            onChange={(e) => setSkillsEnabled(e.target.checked)}
          />
          <span>{t('mcp.enableSkills')}</span>
        </label>
        {available.length > 0 ? (
          <div className="flex flex-col gap-1">
            {available.map((sk) => (
              <div key={sk.name} className="text-xs">
                <span className="font-medium">{sk.name}</span>{' '}
                <span className="text-muted-foreground">{sk.description}</span>
              </div>
            ))}
          </div>
        ) : (
          <div className="text-xs text-muted-foreground">{t('mcp.noSkills')}</div>
        )}
      </div>

      {/* MCP */}
      <div className="flex flex-col gap-2">
        <label className="flex items-center gap-2 text-xs">
          <input
            type="checkbox"
            className="h-4 w-4"
            checked={mcpEnabled}
            onChange={(e) => setMcpEnabled(e.target.checked)}
          />
          <span>{t('mcp.enableMcp')}</span>
        </label>
        {servers.map((srv, idx) => {
          const info = status?.mcp.servers.find((s) => s.name === srv.name)
          const isHttp = srv.type === 'http'
          const o = srv.oauth ?? {
            token_url: '',
            client_id: '',
            client_secret: '',
            scopes: [],
            authorize_url: '',
            redirect_port: 0,
          }
          const badge =
            !info || info.status === 'down' ? (
              <Badge variant="outline" className="text-[10px] text-muted-foreground">
                {t('mcp.notConnected')}
              </Badge>
            ) : info.status === 'up' ? (
              <Badge variant="outline" className="text-[10px] text-emerald-400">
                {t('mcp.connected')}
              </Badge>
            ) : (
              <Badge variant="outline" className="text-[10px] text-red-400">
                {t('mcp.error')}
              </Badge>
            )
          return (
            <div key={idx} className="flex flex-col gap-1">
              <div className="flex items-center gap-1">
                <Input
                  value={srv.name}
                  onChange={(e) => patchServer(idx, { name: e.target.value })}
                  placeholder="server"
                  className="h-8 flex-1 text-xs"
                />
                {!isHttp && (
                  <Input
                    value={srv.command}
                    onChange={(e) => patchServer(idx, { command: e.target.value })}
                    placeholder="command"
                    className="h-8 flex-1 text-xs"
                  />
                )}
                <label className="flex items-center gap-1 text-[10px] text-muted-foreground">
                  <input
                    type="checkbox"
                    className="h-3 w-3"
                    checked={isHttp}
                    onChange={(e) => patchServer(idx, { type: e.target.checked ? 'http' : 'stdio', command: e.target.checked ? '' : srv.command })}
                  />
                  {t('mcp.httpMode')}
                </label>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-8 w-8"
                  title={t('mcp.removeServerTitle')}
                  onClick={() => setServers((prev) => prev.filter((_, i) => i !== idx))}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
                <div className="ml-1">{badge}</div>
              </div>
              {isHttp && (
                <Input
                  value={srv.url ?? ''}
                  onChange={(e) => patchServer(idx, { url: e.target.value })}
                  placeholder={t('mcp.url')}
                  className="h-8 text-xs"
                />
              )}
              <label className="flex items-center gap-1 text-[10px] text-muted-foreground">
                <input
                  type="checkbox"
                  className="h-3 w-3"
                  checked={Boolean(srv.oauth)}
                  onChange={(e) =>
                    patchServer(idx, {
                      oauth: e.target.checked
                        ? { token_url: '', client_id: '', client_secret: '', scopes: [], authorize_url: '', redirect_port: 0 }
                        : undefined,
                    })
                  }
                />
                {t('mcp.oauth')}
              </label>
              {srv.oauth && (
                <div className="flex flex-col gap-1 pl-2">
                  <Input
                    value={o.token_url}
                    onChange={(e) => patchServer(idx, { oauth: { ...o, token_url: e.target.value } })}
                    placeholder={t('mcp.tokenUrl')}
                    className="h-7 text-xs"
                  />
                  <Input
                    value={o.client_id}
                    onChange={(e) => patchServer(idx, { oauth: { ...o, client_id: e.target.value } })}
                    placeholder={t('mcp.clientId')}
                    className="h-7 text-xs"
                  />
                  <Input
                    type="password"
                    value={o.client_secret ?? ''}
                    onChange={(e) => patchServer(idx, { oauth: { ...o, client_secret: e.target.value } })}
                    placeholder={t('mcp.clientSecret')}
                    className="h-7 text-xs"
                  />
                  <Input
                    value={(o.scopes ?? []).join(', ')}
                    onChange={(e) =>
                      patchServer(idx, {
                        oauth: { ...o, scopes: e.target.value.split(',').map((s) => s.trim()).filter(Boolean) },
                      })
                    }
                    placeholder={t('mcp.scopes')}
                    className="h-7 text-xs"
                  />
                  <div className="flex items-center gap-1">
                    <Input
                      value={o.authorize_url ?? ''}
                      onChange={(e) => patchServer(idx, { oauth: { ...o, authorize_url: e.target.value } })}
                      placeholder={t('mcp.authorizeUrl')}
                      className="h-7 flex-1 text-xs"
                    />
                    <Input
                      value={o.redirect_port && o.redirect_port > 0 ? String(o.redirect_port) : ''}
                      onChange={(e) =>
                        patchServer(idx, {
                          oauth: { ...o, redirect_port: Number(e.target.value) > 0 ? Number(e.target.value) : 0 },
                        })
                      }
                      placeholder={t('mcp.redirectPort')}
                      className="h-7 w-24 text-xs"
                    />
                  </div>
                </div>
              )}
              {info && info.status === 'error' && (
                <div className="text-xs text-red-400">
                  {info.name}: {t('mcp.connectError')}
                </div>
              )}
              {info && info.tools.length > 0 && (
                <div className="flex flex-col gap-0.5 pl-2">
                  {info.tools.map((ti) => (
                    <div key={ti.name} className="text-xs text-muted-foreground">
                      <span>{ti.name}</span>
                      {ti.description ? ` — ${ti.description}` : ''}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )
        })}
        <Button
          variant="outline"
          size="sm"
          className="h-8 text-xs"
          onClick={() => setServers((prev) => [...prev, { name: nextName, command: '' }])}
        >
          <Plus className="h-3.5 w-3.5" />
          {t('mcp.addServer')}
        </Button>
      </div>

      {error && <div className="text-xs text-red-400">{error}</div>}
      {saved && <div className="text-xs text-emerald-400">{t('mcp.saved')}</div>}
      <Button size="sm" className="h-8 text-xs" onClick={() => void save()} disabled={busy}>
        {busy ? t('mcp.saving') : t('mcp.save')}
      </Button>
    </div>
  )
}

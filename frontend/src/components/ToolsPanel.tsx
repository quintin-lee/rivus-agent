import { useEffect, useRef, useState } from 'react'
import { t } from '@/i18n'
import { useUiStore } from '@/store/ui-store'
import { Plus, Trash2 } from 'lucide-react'
import { api, ApiError } from '@/lib/api'
import type { MCPServerIn, ToolStatus } from '@/lib/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'

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
    setServers(st.mcp.servers.map((s) => ({ name: s.name, command: s.command, args: s.args })))
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
      await api.saveTools({ skillsEnabled, mcpEnabled, mcpServers: servers })
      applyStatus(await api.getTools())
      setSaved(true)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  const available = status?.skills.available ?? []
  const nextName = `server-${servers.length + 1}`

  return (
    <div className="flex flex-col gap-3">
      <div className="text-sm font-medium">工具设置</div>
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
          <span>启用 Skills</span>
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
          <div className="text-xs text-muted-foreground">无可用技能</div>
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
          <span>启用 MCP</span>
        </label>
        {servers.map((srv, idx) => {
          const info = status?.mcp.servers.find((s) => s.name === srv.name)
          const badge =
            !info || info.status === 'down' ? (
              <Badge variant="outline" className="text-[10px] text-muted-foreground">
                未连接
              </Badge>
            ) : info.status === 'up' ? (
              <Badge variant="outline" className="text-[10px] text-emerald-400">
                已连接
              </Badge>
            ) : (
              <Badge variant="outline" className="text-[10px] text-red-400">
                错误
              </Badge>
            )
          return (
            <div key={idx} className="flex flex-col gap-1">
              <div className="flex items-center gap-1">
                <Input
                  value={srv.name}
                  onChange={(e) =>
                    setServers((prev) =>
                      prev.map((s, i) => (i === idx ? { ...s, name: e.target.value } : s))
                    )
                  }
                  placeholder="server"
                  className="h-8 flex-1 text-xs"
                />
                <Input
                  value={srv.command}
                  onChange={(e) =>
                    setServers((prev) =>
                      prev.map((s, i) => (i === idx ? { ...s, command: e.target.value } : s))
                    )
                  }
                  placeholder="command"
                  className="h-8 flex-1 text-xs"
                />
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-8 w-8"
                  title="移除服务器"
                  onClick={() => setServers((prev) => prev.filter((_, i) => i !== idx))}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
                <div className="ml-1">{badge}</div>
              </div>
              {info && info.status === 'error' && (
                <div className="text-xs text-red-400">{info.name}: 连接错误</div>
              )}
              {info && info.tools.length > 0 && (
                <div className="flex flex-col gap-0.5 pl-2">
                  {info.tools.map((t) => (
                    <div key={t.name} className="text-xs text-muted-foreground">
                      <span>{t.name}</span>
                      {t.description ? ` — ${t.description}` : ''}
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
          添加服务器
        </Button>
      </div>

      {error && <div className="text-xs text-red-400">{error}</div>}
      {saved && <div className="text-xs text-emerald-400">已保存</div>}
      <Button size="sm" className="h-8 text-xs" onClick={() => void save()} disabled={busy}>
        {busy ? '保存中…' : '保存工具设置'}
      </Button>
    </div>
  )
}

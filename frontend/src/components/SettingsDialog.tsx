import { useEffect, useState } from 'react'
import { Settings2 } from 'lucide-react'
import { api, ApiError } from '@/lib/api'
import type { ModelSettings } from '@/lib/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'

export function SettingsDialog() {
  const [open, setOpen] = useState(false)
  const [settings, setSettings] = useState<ModelSettings | null>(null)
  const [baseUrl, setBaseUrl] = useState('')
  const [model, setModel] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    if (!open) return
    setError('')
    setSaved(false)
    setApiKey('')
    void api
      .getModelSettings()
      .then((s) => {
        setSettings(s)
        setBaseUrl(s.base_url)
        setModel(s.model)
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : '加载失败'))
  }, [open ])

  async function save() {
    if (busy) return
    setBusy(true)
    setError('')
    setSaved(false)
    try {
      await api.updateModelSettings({
        provider: 'openai_compat',
        base_url: baseUrl.trim() || undefined,
        model: model.trim() || undefined,
        api_key: apiKey.trim() || undefined,
      })
      setApiKey('')
      const s = await api.getModelSettings()
      setSettings(s)
      setBaseUrl(s.base_url)
      setModel(s.model)
      setSaved(true)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon" className="h-8 w-8" title="模型设置">
          <Settings2 className="h-4 w-4" />
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>模型设置</DialogTitle>
          <DialogDescription>
            全局默认模型，新提交的 Run 立即生效；在途 Run 不受影响。
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            供应商
            <Input value="openai_compat" disabled className="h-8 text-xs" />
          </label>
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            Base URL
            <Input
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              placeholder="https://api.openai.com/v1"
              className="h-8 text-xs"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            模型
            <Input
              value={model}
              onChange={(e) => setModel(e.target.value)}
              placeholder="gpt-4o-mini"
              className="h-8 text-xs"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            <span className="flex items-center gap-2">
              API Key
              {settings && (
                <Badge variant="outline" className="text-[10px]">
                  {settings.api_key_set ? '已设置' : '未设置'}
                </Badge>
              )}
            </span>
            <Input
              type="password"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder="留空表示不修改"
              className="h-8 text-xs"
              autoComplete="off"
            />
          </label>
          {error && <div className="text-xs text-red-400">{error}</div>}
          {saved && <div className="text-xs text-emerald-400">已保存，新提交的 Run 生效</div>}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={() => setOpen(false)}>
            关闭
          </Button>
          <Button onClick={() => void save()} disabled={busy}>
            {busy ? '保存中…' : '保存'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

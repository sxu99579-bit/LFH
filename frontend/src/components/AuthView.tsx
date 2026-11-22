import React, { useState } from 'react'
import { Server, ShieldCheck, Moon, Sun, ArrowRight } from 'lucide-react'
import { api } from '@/lib/api'
import type { ServerInfo, ThemeMode, User } from '@/types'
import { Badge, Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Input, useToast } from '@/components/ui'

export function AuthView({
  serverInfo,
  theme,
  onToggleTheme,
  onAuthenticated,
}: {
  serverInfo: ServerInfo | null
  theme: ThemeMode
  onToggleTheme: () => void
  onAuthenticated: (u: User) => void
}) {
  const { toast } = useToast()
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [inviteCode, setInviteCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const inviteRequired = serverInfo?.invite_required !== false
  const ver = serverInfo?.version || 'V0.11.22.3'

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setNotice('')
    if (!username.trim() || !password) {
      setError('请输入用户名和密码')
      return
    }
    setBusy(true)
    try {
      if (mode === 'login') {
        const res = await api.login(username.trim(), password)
        onAuthenticated(res.user)
        toast(`欢迎回来，${res.user.username}`, 'success')
      } else {
        const res = await api.register(username.trim(), password, inviteCode.trim())
        if (res.pending || !res.user) {
          setNotice(res.message || '注册申请已提交，请等待管理员审核通过后登录')
          setMode('login')
        } else {
          onAuthenticated(res.user)
          toast(`注册成功，欢迎 ${res.user.username}`, 'success')
        }
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="relative flex min-h-screen flex-col items-center justify-center bg-background px-4 py-8">
      <div className="absolute right-4 top-4 flex items-center gap-2">
        <Button variant="outline" size="sm" onClick={onToggleTheme}>
          {theme === 'dark' ? <Sun className="h-3.5 w-3.5" /> : <Moon className="h-3.5 w-3.5" />}
          <span>{theme === 'dark' ? '浅色' : '深色'}</span>
        </Button>
      </div>

      <div className="w-full max-w-md space-y-5">
        <div className="flex flex-col items-center text-center">
          <div className="mb-3 flex h-12 w-12 items-center justify-center rounded-xl border border-border bg-card shadow-sm overflow-hidden">
            <img src="/logo.png" alt="LFH" className="h-10 w-10 object-cover" />
          </div>
          <div className="flex items-center gap-2">
            <h1 className="text-xl font-bold tracking-tight">LAN File Hub</h1>
            <Badge variant="default" className="font-mono">{ver}</Badge>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            异构局域网分布式文件库 · 零拷贝共享池 · 分布式算力集群
          </p>
        </div>

        <Card className="shadow-lg">
          <CardHeader className="pb-3">
            <div className="grid grid-cols-2 rounded-lg bg-muted p-1">
              <button
                type="button"
                onClick={() => { setMode('login'); setError('') }}
                className={`rounded-md py-1.5 text-xs font-medium transition-colors cursor-pointer ${
                  mode === 'login' ? 'bg-card text-foreground shadow-xs' : 'text-muted-foreground hover:text-foreground'
                }`}
              >
                账号登录
              </button>
              <button
                type="button"
                onClick={() => { setMode('register'); setError('') }}
                className={`rounded-md py-1.5 text-xs font-medium transition-colors cursor-pointer ${
                  mode === 'register' ? 'bg-card text-foreground shadow-xs' : 'text-muted-foreground hover:text-foreground'
                }`}
              >
                注册新用户
              </button>
            </div>
            <CardTitle className="pt-3">{mode === 'login' ? '登录到你的私有云集群' : '创建成员账号'}</CardTitle>
            <CardDescription>
              {mode === 'login'
                ? '统一主节点单点认证，登录后自动访问全集群文件与插件算力。'
                : inviteRequired
                ? '管理员已开启邀请码校验，请输入有效的邀请码完成注册。'
                : '注册后需管理员在设置中心审核批准方可访问集群资源。'}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <form onSubmit={handleSubmit} className="space-y-3.5">
              {error && (
                <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                  {error}
                </div>
              )}
              {notice && (
                <div className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-500">
                  {notice}
                </div>
              )}

              <div className="space-y-1.5">
                <label className="text-xs font-medium text-foreground">用户名</label>
                <Input
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  placeholder="请输入用户名（首次默认 admin）"
                  autoComplete="username"
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-medium text-foreground">密码</label>
                <Input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="请输入登录密码"
                  autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
                />
              </div>

              {mode === 'register' && inviteRequired && (
                <div className="space-y-1.5">
                  <label className="text-xs font-medium text-foreground">邀请码</label>
                  <Input
                    value={inviteCode}
                    onChange={(e) => setInviteCode(e.target.value)}
                    placeholder="向管理员获取邀请码"
                  />
                </div>
              )}

              <Button type="submit" className="w-full" disabled={busy}>
                <span>{busy ? '正在提交...' : mode === 'login' ? '立即登录' : '注册账号'}</span>
                <ArrowRight className="h-4 w-4" />
              </Button>
            </form>

            {serverInfo?.pool && (
              <div className="mt-4 flex items-center justify-between border-t border-border pt-3 text-[11px] text-muted-foreground">
                <span className="inline-flex items-center gap-1.5">
                  <Server className="h-3.5 w-3.5 text-emerald-500" />
                  集群在线：{serverInfo.pool.online_nodes}/{serverInfo.pool.total_nodes} 节点
                </span>
                <span className="inline-flex items-center gap-1">
                  <ShieldCheck className="h-3.5 w-3.5 text-primary" />
                  局域网直连认证
                </span>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

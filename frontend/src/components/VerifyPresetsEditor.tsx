import { useEffect, useState } from 'react'
import { Plus, ShieldCheck, Terminal, Trash2 } from 'lucide-react'
import type { VerifyPreset } from '@/types'
import { Button, Input } from '@/components/ui'

interface VerifyPresetsEditorProps {
  value: VerifyPreset[]
  onChange: (next: VerifyPreset[]) => void
}

const PRESET_TEMPLATES: Array<{ name: string; preset: VerifyPreset }> = [
  {
    name: 'Go 单元测试',
    preset: { id: 'go-test', label: 'Go 单元测试', command: 'go', args: ['test', './...'], timeout_sec: 120 },
  },
  {
    name: 'Go 编译校验',
    preset: { id: 'go-vet', label: 'Go 编译校验', command: 'go', args: ['vet', './...'], timeout_sec: 60 },
  },
  {
    name: '前端 Build',
    preset: { id: 'npm-build', label: '前端构建', command: 'npm', args: ['run', 'build'], timeout_sec: 180 },
  },
  {
    name: 'Pytest',
    preset: { id: 'pytest', label: 'Python 测试', command: 'pytest', args: ['-q'], timeout_sec: 120 },
  },
]

function PresetArgsInput({
  args,
  onChange,
}: {
  args?: string[]
  onChange: (nextArgs: string[]) => void
}) {
  const joined = (args || []).join(' ')
  const [raw, setRaw] = useState(joined)

  useEffect(() => {
    const parsedCurrent = raw.trim().split(/\s+/).filter(Boolean)
    const incoming = (args || []).join(' ')
    if (parsedCurrent.join(' ') !== incoming) {
      setRaw(incoming)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [joined])

  return (
    <Input
      value={raw}
      onChange={(e) => {
        const nextRaw = e.target.value
        setRaw(nextRaw)
        onChange(nextRaw.trim().split(/\s+/).filter(Boolean))
      }}
      placeholder="参数（空格分隔），如 test ./..."
      className="font-mono text-xs"
    />
  )
}

function uniquePresetId(base: string, existing: VerifyPreset[]): string {
  const cleanBase = base.trim() || 'verify'
  const used = new Set(existing.map((p) => p.id.toLowerCase()))
  if (!used.has(cleanBase.toLowerCase())) return cleanBase
  let idx = 2
  while (used.has(`${cleanBase}-${idx}`.toLowerCase())) {
    idx++
  }
  return `${cleanBase}-${idx}`
}

export function VerifyPresetsEditor({ value, onChange }: VerifyPresetsEditorProps) {
  const addPreset = (template?: VerifyPreset) => {
    const baseId = template?.id || 'verify'
    const nextId = uniquePresetId(baseId, value)
    const next: VerifyPreset = template
      ? {
          ...template,
          id: nextId,
          args: template.args ? [...template.args] : [],
        }
      : {
          id: nextId,
          label: `校验预设 ${value.length + 1}`,
          command: 'go',
          args: ['version'],
          timeout_sec: 60,
        }
    onChange([...value, next])
  }

  const updateAt = (idx: number, patch: Partial<VerifyPreset>) => {
    const next = value.map((item, i) => (i === idx ? { ...item, ...patch } : item))
    onChange(next)
  }

  const removeAt = (idx: number) => {
    onChange(value.filter((_, i) => i !== idx))
  }

  return (
    <div className="space-y-2.5 rounded-lg border border-border p-3 bg-muted/20">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 font-medium">
          <Terminal className="h-3.5 w-3.5 text-primary" />
          <span>受控工作区构建/校验预设 ({value.length})</span>
        </div>
        <div className="flex flex-wrap items-center gap-1">
          {PRESET_TEMPLATES.map((tpl) => (
            <Button
              key={tpl.preset.id}
              type="button"
              size="xs"
              variant="outline"
              onClick={() => addPreset(tpl.preset)}
            >
              + {tpl.name}
            </Button>
          ))}
          <Button type="button" size="xs" variant="secondary" onClick={() => addPreset()}>
            <Plus className="h-3 w-3" />
            <span>自定义预设</span>
          </Button>
        </div>
      </div>

      <div className="flex items-start gap-1.5 text-[11px] text-muted-foreground">
        <ShieldCheck className="h-3.5 w-3.5 text-primary shrink-0 mt-0.5" />
        <span>
          白名单直调可执行文件与静态参数（无 Shell 解析），并发触发同预设自动单飞合流，仅回传末尾 128KB 并将完整日志写入目录 `.lfh-logs/&lt;id&gt;.log`。
        </span>
      </div>

      {value.length === 0 ? (
        <div className="rounded border border-dashed border-border p-3 text-center text-[11px] text-muted-foreground">
          暂未绑定校验预设。点击上方模板即可快速为当前挂载工作区添加 `go test`、`npm run build` 等受控验证任务。
        </div>
      ) : (
        <div className="space-y-2.5">
          {value.map((preset, idx) => (
            <div key={idx} className="rounded-md border border-border bg-card p-2.5 space-y-2">
              <div className="grid gap-2 sm:grid-cols-3">
                <div className="space-y-1">
                  <label className="text-[11px] text-muted-foreground">预设 ID *</label>
                  <Input
                    value={preset.id}
                    onChange={(e) => updateAt(idx, { id: e.target.value })}
                    placeholder="go-test"
                    className="font-mono text-xs"
                  />
                </div>
                <div className="space-y-1">
                  <label className="text-[11px] text-muted-foreground">展示名称</label>
                  <Input
                    value={preset.label}
                    onChange={(e) => updateAt(idx, { label: e.target.value })}
                    placeholder="Go 单元测试"
                  />
                </div>
                <div className="space-y-1 flex flex-col">
                  <label className="text-[11px] text-muted-foreground">超时（秒，1-300）</label>
                  <div className="flex items-center gap-1.5">
                    <Input
                      type="number"
                      min={1}
                      max={300}
                      value={preset.timeout_sec ?? 60}
                      onChange={(e) =>
                        updateAt(idx, {
                          timeout_sec: Math.max(1, Math.min(300, Number(e.target.value || 60))),
                        })
                      }
                      className="text-xs"
                    />
                    <Button
                      type="button"
                      size="icon"
                      variant="ghost"
                      className="h-8 w-8 text-destructive shrink-0"
                      onClick={() => removeAt(idx)}
                      title="移除该预设"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                </div>
              </div>

              <div className="grid gap-2 sm:grid-cols-2">
                <div className="space-y-1">
                  <label className="text-[11px] text-muted-foreground">可执行命令 *（禁止带路径符号或 Shell 管道）</label>
                  <Input
                    value={preset.command}
                    onChange={(e) => updateAt(idx, { command: e.target.value })}
                    placeholder="go / npm / pytest"
                    className="font-mono text-xs"
                  />
                </div>
                <div className="space-y-1">
                  <label className="text-[11px] text-muted-foreground">固定参数列表</label>
                  <PresetArgsInput
                    args={preset.args}
                    onChange={(nextArgs) => updateAt(idx, { args: nextArgs })}
                  />
                </div>
              </div>

              <div className="grid gap-2 sm:grid-cols-2">
                <div className="space-y-1">
                  <label className="text-[11px] text-muted-foreground">相对子目录（可选）</label>
                  <Input
                    value={preset.work_sub_dir || ''}
                    onChange={(e) => updateAt(idx, { work_sub_dir: e.target.value })}
                    placeholder="如 frontend 或 backend（留空则挂载根目录）"
                    className="font-mono text-xs"
                  />
                </div>
                <div className="space-y-1">
                  <label className="text-[11px] text-muted-foreground">相对日志路径（可选）</label>
                  <Input
                    value={preset.log_rel_path || ''}
                    onChange={(e) => updateAt(idx, { log_rel_path: e.target.value })}
                    placeholder={`默认 .lfh-logs/${preset.id || 'verify'}.log`}
                    className="font-mono text-xs"
                  />
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

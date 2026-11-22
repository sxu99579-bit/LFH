import React, { createContext, useCallback, useContext, useState } from 'react'
import { X, CheckCircle2, AlertCircle, Info } from 'lucide-react'
import { cn } from '@/lib/utils'

/* ============ Toast Context ============ */
interface ToastItem {
  id: number
  message: string
  variant: 'success' | 'error' | 'info'
}

const ToastCtx = createContext<{
  toast: (message: string, variant?: 'success' | 'error' | 'info') => void
}>({
  toast: () => {},
})

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([])
  const push = useCallback((message: string, variant: 'success' | 'error' | 'info' = 'info') => {
    const id = Date.now() + Math.random()
    setItems((prev) => [...prev.slice(-4), { id, message, variant }])
    setTimeout(() => {
      setItems((prev) => prev.filter((x) => x.id !== id))
    }, 3600)
  }, [])

  return (
    <ToastCtx.Provider value={{ toast: push }}>
      {children}
      <div className="fixed bottom-4 right-4 z-[90] flex flex-col gap-2 max-w-[92vw] sm:max-w-sm pointer-events-none">
        {items.map((t) => (
          <div
            key={t.id}
            className={cn(
              'pointer-events-auto flex items-start gap-2.5 rounded-lg border px-3.5 py-2.5 text-sm shadow-lg backdrop-blur-md transition-all',
              t.variant === 'error'
                ? 'border-destructive/40 bg-card text-destructive'
                : t.variant === 'success'
                ? 'border-emerald-500/40 bg-card text-emerald-500 dark:text-emerald-400'
                : 'border-border bg-card text-foreground'
            )}
          >
            {t.variant === 'error' ? (
              <AlertCircle className="h-4 w-4 shrink-0 mt-0.5" />
            ) : t.variant === 'success' ? (
              <CheckCircle2 className="h-4 w-4 shrink-0 mt-0.5" />
            ) : (
              <Info className="h-4 w-4 shrink-0 mt-0.5 text-primary" />
            )}
            <span className="leading-snug text-foreground">{t.message}</span>
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  )
}

export function useToast() {
  return useContext(ToastCtx)
}

/* ============ Button ============ */
export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'default' | 'secondary' | 'outline' | 'ghost' | 'destructive'
  size?: 'default' | 'sm' | 'xs' | 'icon'
}

export function Button({
  className,
  variant = 'default',
  size = 'default',
  type = 'button',
  ...props
}: ButtonProps) {
  return (
    <button
      type={type}
      className={cn(
        'inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md font-medium transition-colors cursor-pointer disabled:pointer-events-none disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
        variant === 'default' && 'bg-primary text-primary-foreground hover:bg-primary/90 shadow-xs',
        variant === 'secondary' && 'bg-secondary text-secondary-foreground hover:bg-secondary/80',
        variant === 'outline' && 'border border-border bg-background hover:bg-accent hover:text-accent-foreground',
        variant === 'ghost' && 'hover:bg-accent hover:text-accent-foreground',
        variant === 'destructive' && 'bg-destructive text-destructive-foreground hover:bg-destructive/90',
        size === 'default' && 'h-9 px-4 py-2 text-sm',
        size === 'sm' && 'h-8 rounded-md px-3 text-xs',
        size === 'xs' && 'h-7 rounded-md px-2.5 text-xs',
        size === 'icon' && 'h-8 w-8 p-0',
        className
      )}
      {...props}
    />
  )
}

/* ============ Badge ============ */
export interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement> {
  variant?: 'default' | 'secondary' | 'outline' | 'success' | 'warning' | 'destructive'
}

export function Badge({ className, variant = 'secondary', ...props }: BadgeProps) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] font-medium leading-tight',
        variant === 'default' && 'border-primary/30 bg-primary/15 text-primary',
        variant === 'secondary' && 'border-border bg-secondary text-secondary-foreground',
        variant === 'outline' && 'border-border bg-transparent text-muted-foreground',
        variant === 'success' && 'border-emerald-500/30 bg-emerald-500/15 text-emerald-600 dark:text-emerald-400',
        variant === 'warning' && 'border-amber-500/30 bg-amber-500/15 text-amber-600 dark:text-amber-400',
        variant === 'destructive' && 'border-destructive/30 bg-destructive/15 text-destructive',
        className
      )}
      {...props}
    />
  )
}

/* ============ Card ============ */
export function Card({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn('rounded-xl border border-border bg-card text-card-foreground shadow-xs', className)}
      {...props}
    />
  )
}

export function CardHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('flex flex-col space-y-1 p-4 sm:p-5', className)} {...props} />
}

export function CardTitle({ className, ...props }: React.HTMLAttributes<HTMLHeadingElement>) {
  return <h3 className={cn('text-sm font-semibold leading-none tracking-tight', className)} {...props} />
}

export function CardDescription({ className, ...props }: React.HTMLAttributes<HTMLParagraphElement>) {
  return <p className={cn('text-xs text-muted-foreground leading-relaxed', className)} {...props} />
}

export function CardContent({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('p-4 pt-0 sm:p-5 sm:pt-0', className)} {...props} />
}

/* ============ Inputs ============ */
export function Input({ className, ...props }: React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={cn(
        'flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm text-foreground shadow-xs transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50',
        className
      )}
      {...props}
    />
  )
}

export function Textarea({ className, ...props }: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      className={cn(
        'flex min-h-[80px] w-full rounded-md border border-input bg-background px-3 py-2 text-sm text-foreground shadow-xs transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50',
        className
      )}
      {...props}
    />
  )
}

export function Select({ className, ...props }: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        'flex h-9 rounded-md border border-input bg-background px-3 py-1 text-sm text-foreground shadow-xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50',
        className
      )}
      {...props}
    />
  )
}

export function Switch({
  checked,
  onCheckedChange,
  disabled,
}: {
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onCheckedChange(!checked)}
      className={cn(
        'relative inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50',
        checked ? 'bg-primary' : 'bg-input'
      )}
    >
      <span
        className={cn(
          'pointer-events-none block h-4 w-4 rounded-full bg-background shadow-sm transition-transform',
          checked ? 'translate-x-4' : 'translate-x-0'
        )}
      />
    </button>
  )
}

/* ============ Dialog ============ */
export function Dialog({
  open,
  onClose,
  title,
  description,
  children,
  size = 'lg',
  headerExtra,
}: {
  open: boolean
  onClose: () => void
  title: React.ReactNode
  description?: React.ReactNode
  children: React.ReactNode
  size?: 'sm' | 'md' | 'lg' | 'xl'
  headerExtra?: React.ReactNode
}) {
  if (!open) return null
  return (
    <div className="fixed inset-0 z-[70] flex items-center justify-center p-2 sm:p-4">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-xs" onClick={onClose} />
      <div
        className={cn(
          'relative z-10 flex max-h-[92vh] w-full flex-col rounded-xl border border-border bg-card text-card-foreground shadow-2xl',
          size === 'sm' && 'max-w-md',
          size === 'md' && 'max-w-xl',
          size === 'lg' && 'max-w-3xl',
          size === 'xl' && 'max-w-5xl'
        )}
      >
        <div className="flex items-start justify-between gap-3 border-b border-border px-4 py-3 sm:px-5">
          <div className="min-w-0">
            <h2 className="truncate text-base font-semibold">{title}</h2>
            {description && <div className="mt-0.5 text-xs text-muted-foreground truncate">{description}</div>}
          </div>
          <div className="flex items-center gap-2 shrink-0">
            {headerExtra}
            <Button variant="ghost" size="icon" onClick={onClose} aria-label="关闭">
              <X className="h-4 w-4" />
            </Button>
          </div>
        </div>
        <div className="flex-1 overflow-y-auto p-4 sm:p-5">{children}</div>
      </div>
    </div>
  )
}

/* ============ Sheet (Right drawer on Desktop, Bottom Sheet on Mobile) ============ */
export function Sheet({
  open,
  onClose,
  side = 'right',
  title,
  subtitle,
  children,
}: {
  open: boolean
  onClose: () => void
  side?: 'right' | 'bottom'
  title: React.ReactNode
  subtitle?: React.ReactNode
  children: React.ReactNode
}) {
  if (!open) return null
  return (
    <div className="fixed inset-0 z-[65]">
      <div className="fixed inset-0 bg-black/50 backdrop-blur-xs" onClick={onClose} />
      <div
        className={cn(
          'fixed z-10 flex flex-col border-border bg-card text-card-foreground shadow-2xl',
          side === 'right'
            ? 'inset-y-0 right-0 w-full max-w-md border-l'
            : 'inset-x-0 bottom-0 max-h-[86vh] rounded-t-2xl border-t pb-safe'
        )}
      >
        <div className="flex items-start justify-between gap-2 border-b border-border px-4 py-3.5">
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold">{title}</div>
            {subtitle && <div className="mt-0.5 truncate text-xs text-muted-foreground">{subtitle}</div>}
          </div>
          <Button variant="ghost" size="icon" onClick={onClose} aria-label="关闭">
            <X className="h-4 w-4" />
          </Button>
        </div>
        <div className="flex-1 overflow-y-auto p-4">{children}</div>
      </div>
    </div>
  )
}

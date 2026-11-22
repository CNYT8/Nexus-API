import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { telegramLogin, type TelegramAuthorization } from '../api'
import { useAuthRedirect } from '../hooks/use-auth-redirect'

let telegramCallbackSequence = 0

function TelegramWidget({ botName }: { botName: string }) {
  const { t } = useTranslation()
  const container = useRef<HTMLDivElement>(null)
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)
  const { handleLoginSuccess, redirectTo2FA } = useAuthRedirect()
  const handlers = useRef({ handleLoginSuccess, redirectTo2FA, t })
  useEffect(() => {
    handlers.current = { handleLoginSuccess, redirectTo2FA, t }
  }, [handleLoginSuccess, redirectTo2FA, t])

  useEffect(() => {
    const element = container.current
    if (!element) return
    const controller = new AbortController()
    let active = true
    let pending = false
    // This is a local callback name, not an authentication nonce.
    const callbackName = `nexusTelegramLogin_${Date.now()}_${++telegramCallbackSequence}`
    const callbacks = window as unknown as Record<string, unknown>
    callbacks[callbackName] = async (authorization: TelegramAuthorization) => {
      if (!active || pending || !authorization?.hash || !authorization.id) return
      pending = true
      setBusy(true)
      try {
        const response = await telegramLogin(authorization, controller.signal)
        if (!active) return
        if (!response.success) {
          toast.error(response.message || handlers.current.t('Login failed'))
        } else if (response.data?.require_2fa) {
          handlers.current.redirectTo2FA()
        } else {
          await handlers.current.handleLoginSuccess(response.data)
        }
      } catch {
        if (active) toast.error(handlers.current.t('Login failed'))
      } finally {
        pending = false
        if (active) setBusy(false)
      }
    }
    const script = document.createElement('script')
    script.src = 'https://telegram.org/js/telegram-widget.js?22'
    script.async = true
    script.setAttribute('data-telegram-login', botName.replace(/^@/, ''))
    script.setAttribute('data-size', 'large')
    script.setAttribute('data-onauth', `${callbackName}(user)`)
    script.setAttribute('data-request-access', 'write')
    script.onerror = () => { if (active) setFailed(true) }
    element.appendChild(script)
    return () => {
      active = false
      controller.abort()
      delete callbacks[callbackName]
      while (element.firstChild) element.removeChild(element.firstChild)
    }
  }, [botName])

  return (
    <div className='space-y-3 py-4 text-center' aria-busy={busy}>
      <div ref={container} className={busy ? 'pointer-events-none opacity-50' : ''} />
      {busy && <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>}
      {failed && <p className='text-destructive text-sm'>{t('Telegram 登录组件加载失败，请重试')}</p>}
    </div>
  )
}

export function TelegramLoginDialog({
  open,
  onOpenChange,
  botName,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  botName: string
}) {
  const { t } = useTranslation()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-md'>
        <DialogHeader><DialogTitle>{t('Continue with Telegram')}</DialogTitle></DialogHeader>
        {open && <TelegramWidget botName={botName} />}
      </DialogContent>
    </Dialog>
  )
}

import { useCallback, useEffect, useRef, useState } from 'react'
import { useBlocker } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import type { AccountSession } from './use-account-session'

type Confirmation = {
  desc: string
  title: string
  resolve: (accepted: boolean) => void
}

// All destructive actions and router transitions share one pending decision.
export function useEditGuard(dirty: boolean, session: AccountSession) {
  const { t } = useTranslation()
  const pendingRef = useRef<Confirmation | null>(null)
  const mountedRef = useRef(true)
  const [confirmation, setConfirmation] = useState<Confirmation | null>(null)

  const settle = useCallback((accepted: boolean) => {
    const pending = pendingRef.current
    if (!pending) return
    pendingRef.current = null
    if (mountedRef.current) setConfirmation(null)
    pending.resolve(accepted && mountedRef.current && session.isCurrent())
  }, [session])

  useEffect(() => {
    mountedRef.current = true
    const unsubscribe = session.onInvalidate(() => settle(false))
    return () => {
      mountedRef.current = false
      unsubscribe()
      settle(false)
    }
  }, [session, settle])

  const confirmDiscard = useCallback((clear = false): Promise<boolean> => {
    if (!mountedRef.current || !session.isCurrent() || pendingRef.current)
      return Promise.resolve(false)
    if (!dirty && !clear) return Promise.resolve(true)
    return new Promise((resolve) => {
      const pending = {
        title: t(clear ? '清空对话' : '放弃修改'),
        desc: [
          clear ? t('确定清空所有消息吗？此操作无法撤销。') : '',
          dirty ? t('放弃未保存的消息修改？') : '',
        ].filter(Boolean).join(' '),
        resolve,
      }
      pendingRef.current = pending
      setConfirmation(pending)
    })
  }, [dirty, session, t])

  const shouldBlockFn = useCallback(async () => {
    if (!session.isCurrent()) return false
    return !(await confirmDiscard())
  }, [session, confirmDiscard])
  useBlocker({ shouldBlockFn, enableBeforeUnload: false })

  useEffect(() => {
    if (!dirty) return
    const protectRefresh = (event: BeforeUnloadEvent) => {
      if (!session.isCurrent()) return
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', protectRefresh)
    return () => window.removeEventListener('beforeunload', protectRefresh)
  }, [dirty, session])

  return {
    confirmDiscard,
    dialogProps: {
      open: !!confirmation,
      title: confirmation?.title ?? '',
      desc: confirmation?.desc ?? '',
      onOpenChange: (open: boolean) => { if (!open) settle(false) },
      handleConfirm: () => settle(true),
    },
  }
}

import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { getLoginDevices, revokeLoginDevice, revokeOtherLoginDevices } from '../../api'
import { useSecurityResource } from '../../hooks/use-security-resource'
import { SECURITY_STATUS_LABELS } from '../../lib/security-management'
import type { LoginDevice } from '../../types'

export function LoginDevicesDialog({ onOpenChange }: { onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const manager = useSecurityResource(getLoginDevices)
  const [target, setTarget] = useState<LoginDevice | 'others' | null>(null)
  const date = (value: number) => new Date(value * 1000).toLocaleString()
  const revoke = async () => {
    if (!target) return
    if (target === 'others') {
      await manager.run(revokeOtherLoginDevices, () => setTarget(null))
    } else {
      await manager.run((signal, owner) => revokeLoginDevice(target.id, signal, owner), (result) => {
        setTarget(null)
        if (result?.current) {
          useAuthStore.getState().auth.reset()
          onOpenChange(false)
          void navigate({ to: '/sign-in' })
        }
      })
    }
  }

  return (
    <Dialog open onOpenChange={(next) => { if (!manager.busy) onOpenChange(next) }}>
      <DialogContent className='max-h-[90dvh] overflow-y-auto sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('登录设备')}</DialogTitle>
          <DialogDescription>{t('查看浏览器登录会话，可单独撤销或保留当前设备并撤销其他登录')}</DialogDescription>
        </DialogHeader>
        {manager.error && <p role='alert' className='text-destructive text-sm'>{manager.error}</p>}
        <div className='flex flex-wrap gap-2'>
          <Button variant='outline' disabled={manager.loading || manager.busy} onClick={() => void manager.refresh()}>{t('刷新')}</Button>
          <Button variant='destructive' disabled={manager.busy || !manager.data} onClick={() => setTarget('others')}>{t('撤销其他登录')}</Button>
        </div>
        {manager.loading && <p role='status'>{t('加载中...')}</p>}
        {manager.data && <p className='text-muted-foreground text-xs'>{t('最多显示最近 {{limit}} 条记录', { limit: manager.data.limit })}{manager.data.has_more && ` · ${t('还有更早记录未显示')}`}</p>}
        {target && <div role='alert' className='space-y-2 rounded-lg border p-3'>
          <p>{target === 'others' ? t('确认撤销其他所有有效登录？当前浏览器将保持登录。') : target.current ? t('确认撤销当前登录？此浏览器将立即退出。') : t('确认撤销此登录？该设备将需要重新登录。')}</p>
          <div className='flex gap-2'>
            <Button variant='outline' disabled={manager.busy} onClick={() => setTarget(null)}>{t('取消')}</Button>
            <Button variant='destructive' disabled={manager.busy} onClick={() => void revoke()}>{t('确认撤销')}</Button>
          </div>
        </div>}
        {manager.data?.items.map((device) => <div key={device.id} className='space-y-2 rounded-lg border p-3 text-sm'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <strong>{device.current ? t('当前设备') : `${t('登录设备')} #${device.id}`}</strong>
            <span>{t(SECURITY_STATUS_LABELS[device.status] || '未知状态')}</span>
          </div>
          <p className='break-all'>{device.user_agent || t('未知设备')}</p>
          <p className='break-all'>{t('登录方式')}：{device.login_method || '—'} · IP: {device.ip || '—'}</p>
          <p>{t('创建时间')}：{date(device.created_at)}</p>
          <p>{t('到期时间')}：{date(device.expires_at)}</p>
          <Button variant='destructive' size='sm' disabled={manager.busy || device.status !== 'active'} onClick={() => setTarget(device)}>{t('撤销')}</Button>
        </div>)}
      </DialogContent>
    </Dialog>
  )
}

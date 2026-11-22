/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { downloadLogExport } from '../lib/export'
import { logExportErrorMessage, useLogPolicy } from '../lib/log-policy'
import type { GetLogsParams } from '../types'

export function LogExportActions({
  getParams,
  isAdmin,
}: {
  getParams: () => GetLogsParams
  isAdmin: boolean
}) {
  const { t } = useTranslation()
  const { account, policy, loading, error, refresh } = useLogPolicy()
  const [exporting, setExporting] = useState(false)
  const request = useRef<AbortController | null>(null)
  useEffect(() => {
    setExporting(false)
    const cancel = () => { request.current?.abort(); request.current = null }
    const stop = account.onInvalidate(cancel)
    return () => { stop(); cancel() }
  }, [account, isAdmin])
  const allowed = account.isCurrent() && policy?.export_enabled === true && (isAdmin || policy.user_export_enabled)

  const handleExport = async () => {
    if (request.current || !allowed || loading) return
    const identity = () => {
      try {
        const user = JSON.parse(localStorage.getItem('user') || 'null')
        if (!account.isCurrent() || String(user?.id) !== account.userId || localStorage.getItem('uid') !== account.userId || (Number(user?.role) >= 10) !== isAdmin) return null
        return `${user.id}:${user.role}`
      } catch { return null }
    }
    const owner = identity()
    if (!owner) { toast.error(t('登录状态已失效，请重新登录后重试。')); return }
    const isCurrent = () => identity() === owner
    const controller = new AbortController()
    request.current = controller
    setExporting(true)
    try {
      const result = await downloadLogExport(
        getParams(),
        isAdmin,
        controller.signal,
        isCurrent
      )
      if (controller.signal.aborted || request.current !== controller || !isCurrent()) return
      if (result.limited) {
        toast.warning(
          t(
            '导出已达到 {{count}} 条上限，请缩小筛选范围后继续导出。',
            { count: result.count }
          )
        )
      } else if (result.complete === true) {
        toast.success(t('已导出 {{count}} 条日志。', { count: result.count }))
      } else {
        toast.error(t('日志导出不完整，请重试或缩小筛选范围。'))
      }
    } catch (error) {
      if (!controller.signal.aborted && request.current === controller && isCurrent()) {
        toast.error(logExportErrorMessage(error))
        refresh()
      }
    } finally {
      if (request.current === controller) {
        request.current = null
        setExporting(false)
      }
    }
  }

  return (
    <div className='flex flex-wrap items-center gap-2' aria-busy={exporting || loading}>
      <Button
        type='button'
        variant='outline'
        size='sm'
        disabled={exporting || loading || !allowed}
        onClick={() => void handleExport()}
      >
        {t('导出 NDJSON')}
      </Button>
      {exporting && (
        <span className='text-muted-foreground text-xs' role='status'>
          {t('正在导出日志…')}
        </span>
      )}
      {loading && <span className='text-muted-foreground text-xs' role='status'>{t('正在读取日志策略…')}</span>}
      {error && <span className='text-destructive text-xs' role='alert'>{t('读取日志策略失败，请重试。')}</span>}
      {policy && !allowed && <span className='text-muted-foreground text-xs' role='status'>{t('管理员已关闭普通用户日志导出。')}</span>}
      {policy && <span className='text-muted-foreground text-xs'>{t('导出跨度最多 {{days}} 天，每次最多 {{count}} 条；每个账户同时仅允许一个导出任务。', { days: policy.max_range_days, count: policy.export_max_rows })}</span>}
      {!loading && !exporting && <Button type='button' variant='ghost' size='sm' onClick={refresh}>{t('刷新日志策略')}</Button>}
    </div>
  )
}

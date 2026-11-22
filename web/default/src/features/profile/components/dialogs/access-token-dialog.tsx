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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { CopyButton } from '@/components/copy-button'
import { SecureVerificationDialog } from '@/features/auth/secure-verification/components/secure-verification-dialog'
import { useAccessToken } from '../../hooks'
import { personalTokenScopes, parsePersonalTokenScopes, SECURITY_STATUS_LABELS } from '../../lib/security-management'
import type { PersonalAccessToken } from '../../types'

// ============================================================================
// Access Token Dialog Component
// ============================================================================

interface AccessTokenDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  role: number
}

export function AccessTokenDialog({
  open,
  onOpenChange,
  role,
}: AccessTokenDialogProps) {
  const { t } = useTranslation()
  const manager = useAccessToken()
  const [name, setName] = useState('')
  const [days, setDays] = useState('30')
  const [scopes, setScopes] = useState<string[]>([])
  const [target, setTarget] = useState<PersonalAccessToken | null>(null)
  const [editing, setEditing] = useState<PersonalAccessToken | null>(null)
  const [actionError, setActionError] = useState('')
  const verification = manager.verification
  const date = (value: number) => new Date(value * 1000).toLocaleString()
  const allowedScopes = [...personalTokenScopes(role), 'token:reveal']
  const scopeOptions = [...new Set([...allowedScopes, ...scopes])]
  const pageMatches = manager.data?.page === manager.page && manager.data?.page_size === manager.pageSize
  const ready = pageMatches && !manager.loading
  const limitReached = !!manager.data && manager.data.active_count >= manager.data.limit
  const pageCount = Math.max(1, Math.ceil((manager.data?.total ?? 0) / manager.pageSize))
  // Blob uses UTF-8 without requiring TextEncoder on older iOS.
  const validDraft = !!name.trim() && new Blob([name.trim()]).size <= 100 &&
    scopes.length > 0 && scopes.length <= 32 && scopes.every((scope) => allowedScopes.includes(scope))
  const resetDraft = () => {
    setEditing(null)
    setName('')
    setScopes([])
    setActionError('')
  }
  const startEdit = (token: PersonalAccessToken) => {
    setTarget(null)
    setEditing(token)
    setName(token.name)
    setScopes([...parsePersonalTokenScopes(token.scopes)])
    setActionError('')
  }
  const save = async () => {
    if (manager.busy || !ready || !validDraft || (!editing && limitReached)) return
    setActionError('')
    // Capture a detached draft before verification; never mutate a list item.
    const draft = { name: name.trim(), scopes: [...scopes] }
    try {
      if (editing) {
        await manager.update(editing.id, draft, resetDraft)
      } else {
        await manager.create({ ...draft, expires_at: Math.floor(Date.now() / 1000) + Number(days) * 86400 }, resetDraft)
      }
    } catch {
      setActionError(t('请完成安全验证；未设置验证方式时，请重新登录后重试'))
    }
  }
  const revoke = async () => {
    if (!target) return
    setActionError('')
    try {
      await manager.revoke(target.id, () => setTarget(null))
    } catch {
      setActionError(t('请完成安全验证；未设置验证方式时，请重新登录后重试'))
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!manager.busy) onOpenChange(next) }}>
      <DialogContent className='max-h-[90dvh] overflow-y-auto sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('个人管理令牌')}</DialogTitle>
          <DialogDescription>{t('管理控制台 API 的个人令牌，权限仍受账号角色和模块授权限制')}</DialogDescription>
        </DialogHeader>

        <div className='space-y-4'>
          {(manager.error || actionError) && <p role='alert' className='text-destructive text-sm'>{actionError || manager.error}</p>}
          <Button variant='outline' disabled={manager.loading || manager.busy} onClick={() => void manager.refresh()}>{t('刷新')}</Button>
          {manager.data && <p className='text-muted-foreground text-xs'>{t('有效令牌 {{count}} / {{limit}}，共 {{total}} 条记录', { count: manager.data.active_count, limit: manager.data.limit, total: manager.data.total })}</p>}
          {limitReached && <p role='status' className='text-muted-foreground text-sm'>{t('已达到个人访问令牌数量上限，请先撤销不再使用的令牌')}</p>}
          {manager.loading && <p role='status'>{t('加载中...')}</p>}
          {ready && manager.data?.items.length === 0 && <p>{t('暂无个人管理令牌')}</p>}
          {(pageMatches ? manager.data?.items : [])?.map((token) => (
            <div key={token.id} className='space-y-2 rounded-lg border p-3 text-sm'>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <strong className='break-all'>{token.name}</strong>
                <span>{t(SECURITY_STATUS_LABELS[token.status] || '未知状态')}</span>
              </div>
              <p className='break-all font-mono'>{token.prefix}…</p>
              <p className='break-all'>{t('权限范围')}：{parsePersonalTokenScopes(token.scopes).join(', ')}</p>
              <p>{t('创建时间')}：{date(token.created_at)}</p>
              <p>{t('到期时间')}：{date(token.expires_at)}</p>
              <div className='flex flex-wrap gap-2'>
                <Button variant='outline' size='sm' disabled={manager.busy || !ready || !!editing || !!manager.secret || token.status !== 'active'} onClick={() => startEdit(token)}>{t('编辑')}</Button>
                <Button variant='destructive' size='sm' disabled={manager.busy || !ready || !!editing || token.status !== 'active'} onClick={() => setTarget(token)}>{t('撤销')}</Button>
              </div>
            </div>
          ))}
          <div className='flex flex-wrap items-center gap-2 text-sm'>
            <Label htmlFor='pat-page-size'>{t('每页条数')}</Label>
            <select id='pat-page-size' value={manager.pageSize} disabled={manager.busy || manager.loading || !!editing || !!target} onChange={(event) => manager.changePageSize(Number(event.target.value))} className='bg-background rounded-md border p-2'>
              {[10, 20, 50, 100].map((size) => <option key={size} value={size}>{size}</option>)}
            </select>
            <Button variant='outline' size='sm' disabled={manager.busy || manager.loading || !!editing || !!target || manager.page <= 1} onClick={() => manager.setPage(manager.page - 1)}>{t('上一页')}</Button>
            <span>{t('第 {{page}} 页，共 {{pages}} 页', { page: manager.page, pages: pageCount })}</span>
            <Button variant='outline' size='sm' disabled={manager.busy || manager.loading || !!editing || !!target || !ready || manager.page >= pageCount} onClick={() => manager.setPage(manager.page + 1)}>{t('下一页')}</Button>
          </div>
          {target && <div role='alert' className='space-y-2 rounded-lg border p-3'>
            <p>{t('确认撤销此令牌？使用它的程序将立即失去访问权限。')} <strong>{target.name}</strong></p>
            <div className='flex gap-2'>
              <Button variant='outline' disabled={manager.busy} onClick={() => setTarget(null)}>{t('取消')}</Button>
              <Button variant='destructive' disabled={manager.busy} onClick={() => void revoke()}>{t('确认撤销')}</Button>
            </div>
          </div>}
          {manager.secret ? <div className='space-y-3 rounded-lg border p-3'>
            <p role='status'>{t('令牌仅在本次创建后显示一次，请立即复制并妥善保存。关闭后无法再次查看。')}</p>
            <div className='flex gap-2'>
              <Input value={manager.secret} readOnly autoComplete='off' aria-label={t('个人管理令牌')} className='font-mono text-xs' />
              <CopyButton value={manager.secret} variant='outline' className='size-9' tooltip={t('复制')} aria-label={t('复制')} />
            </div>
            <Button variant='outline' onClick={manager.clearSecret}>{t('我已保存，隐藏令牌')}</Button>
          </div> : <form className='space-y-3 rounded-lg border p-3' onSubmit={(event) => { event.preventDefault(); void save() }}>
            <h3 className='text-sm font-medium'>{editing ? t('编辑个人管理令牌') : t('创建令牌')}</h3>
            {editing && <p className='text-muted-foreground text-xs'>{t('仅修改名称和权限范围，不会重置令牌、延长有效期或显示密钥')}</p>}
            <Label htmlFor='pat-name'>{t('令牌名称')}</Label>
            <Input id='pat-name' value={name} maxLength={100} required disabled={manager.busy} onChange={(event) => setName(event.target.value)} />
            <p className='text-muted-foreground text-xs'>{t('名称最多 100 个 UTF-8 字节')}</p>
            {!editing && <>
              <Label htmlFor='pat-expiry'>{t('有效期')}</Label>
              <select id='pat-expiry' value={days} disabled={manager.busy} onChange={(event) => setDays(event.target.value)} className='bg-background w-full rounded-md border p-2'>
                {[7, 30, 90, 365].map((value) => <option key={value} value={value}>{t('{{days}} 天', { days: value })}</option>)}
              </select>
            </>}
            <fieldset disabled={manager.busy} className='space-y-2'>
              <legend className='text-sm font-medium'>{t('权限范围')}</legend>
              <p className='text-muted-foreground text-xs'>{t('read 为读取，write 为修改；请仅选择所需权限')}</p>
              <p className='text-muted-foreground text-xs'>{t('token:reveal 用于查看 API 令牌密钥，read/write 不包含此权限；请谨慎授权')}</p>
              <div className='grid grid-cols-2 gap-2 text-xs sm:grid-cols-3'>
                {scopeOptions.map((scope) => <label key={scope} className='flex items-center gap-2 break-all'><input type='checkbox' checked={scopes.includes(scope)} disabled={!allowedScopes.includes(scope) && !scopes.includes(scope)} onChange={(event) => setScopes((previous) => event.target.checked ? [...previous, scope] : previous.filter((value) => value !== scope))} />{scope}</label>)}
              </div>
              {scopes.some((scope) => !allowedScopes.includes(scope)) && <p role='alert' className='text-destructive text-xs'>{t('存在当前角色不支持的权限，请取消选择后保存')}</p>}
            </fieldset>
            <div className='flex flex-wrap gap-2'>
              <Button type='submit' disabled={manager.busy || !ready || !!target || !validDraft || (!editing && limitReached)}>{editing ? t('保存修改') : t('创建令牌')}</Button>
              {editing && <Button type='button' variant='outline' disabled={manager.busy} onClick={resetDraft}>{t('取消')}</Button>}
            </div>
          </form>}
        </div>
        <SecureVerificationDialog open={verification.open} onOpenChange={(next) => { if (!next) verification.cancel() }} methods={verification.methods} state={verification.state} onVerify={async (method, code) => { try { await verification.executeVerification(method, code) } catch { /* verification dialog retains its retry state */ } }} onCancel={verification.cancel} onCodeChange={verification.setCode} onMethodChange={verification.switchMethod} />

        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            onClick={() => onOpenChange(false)}
            disabled={manager.busy}
          >
            {t('关闭')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

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
import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ShieldCheck, SlidersHorizontal } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useAuthStore } from '@/stores/auth-store'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { SectionPageLayout } from '@/components/layout'
import {
  SettingsControlGroup,
  SettingsSwitchContent,
  SettingsSwitchRow,
} from '../components/settings-form-layout'
import { SettingsSection } from '../components/settings-section'
import {
  getAdminPermissions,
  getAdminPermissionSelf,
  updateAdminPermissions,
} from './api'
import type { AdminPermissionModule, AdminPermissionUser } from './types'

const MODULE_LABELS: Record<string, { title: string; description: string }> = {
  channel: { title: '渠道管理', description: '配置上游服务商和路由。' },
  models: {
    title: '模型管理',
    description: '管理模型可见性、定价、分组、供应商和部署。',
  },
  user: { title: '用户管理', description: '管理用户账号和角色。' },
  redemption: {
    title: '兑换码管理',
    description: '创建和查看邀请码或额度兑换码。',
  },
  subscription: { title: '订阅管理', description: '管理订阅套餐和用户订阅。' },
  ticket: { title: '工单管理', description: '查看并回复用户支持工单。' },
}

function moduleEnabled(
  permissions: Record<string, boolean>,
  module: AdminPermissionModule
) {
  return permissions[module.key] ?? module.enabled
}

function actionAllowed(
  permissions: Record<string, boolean>,
  module: AdminPermissionModule,
  action: 'read' | 'write'
) {
  const enabled = moduleEnabled(permissions, module)
  return enabled && (permissions[`${module.key}:${action}`] ?? enabled)
}

function getAdminDisplayName(admin: AdminPermissionUser) {
  return admin.display_name || admin.username || `#${admin.id}`
}

function getEnabledPermissionCount(
  admin: AdminPermissionUser,
  modules: AdminPermissionModule[]
) {
  return modules.filter(
    (module) =>
      actionAllowed(admin.permissions, module, 'read') ||
      actionAllowed(admin.permissions, module, 'write')
  ).length
}

export function AdminPermissionsSettings() {
  const owner = useAuthStore((s) => s.auth.user?.id)
  return <AdminPermissionsContent key={owner ?? 'guest'} owner={owner} />
}

function AdminPermissionsContent({ owner }: { owner: number | undefined }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const adminPermissionsQueryKey = ['admin-permissions', owner] as const
  const lifetime = useRef<AbortController | null>(null)
  const locked = useRef(false)
  useEffect(() => {
    const controller = new AbortController()
    lifetime.current = controller
    return () => {
      controller.abort()
    }
  }, [])
  const isCurrent = () =>
    !!owner &&
    !lifetime.current?.signal.aborted &&
    useAuthStore.getState().auth.user?.id === owner &&
    localStorage.getItem('uid') === String(owner)
  const setUser = useAuthStore((s) => s.auth.setUser)
  const [selectedAdminId, setSelectedAdminId] = useState<number | null>(null)

  const { data, isLoading, isFetching, error } = useQuery({
    queryKey: adminPermissionsQueryKey,
    queryFn: ({ signal }) => getAdminPermissions(signal, owner!),
    enabled: !!owner,
  })

  const modules = useMemo(() => data?.data?.modules ?? [], [data?.data])
  const admins = useMemo(() => data?.data?.admins ?? [], [data?.data])
  const selectedAdmin = useMemo(
    () => admins.find((admin) => admin.id === selectedAdminId) ?? null,
    [admins, selectedAdminId]
  )

  const mutation = useMutation({
    mutationFn: ({
      admin,
      permissions,
    }: {
      admin: AdminPermissionUser
      permissions: Record<string, boolean>
    }) =>
      updateAdminPermissions(
        admin.id,
        permissions,
        lifetime.current!.signal,
        owner!
      ),
    onSuccess: async (response) => {
      if (!isCurrent()) return
      if (!response.success) {
        toast.error(response.message || t('保存失败'))
        return
      }
      await queryClient.invalidateQueries({
        queryKey: adminPermissionsQueryKey,
        exact: true,
      })
      if (!isCurrent()) return
      toast.success(t('保存成功'))

      if (response.data?.id === owner) {
        const selfResponse = await getAdminPermissionSelf(
          lifetime.current!.signal,
          owner!
        )
        if (
          isCurrent() &&
          selfResponse.success &&
          selfResponse.data &&
          selfResponse.data.id === owner
        ) {
          setUser(selfResponse.data)
        }
      }
    },
    onError: (error: Error) => {
      if (isCurrent()) toast.error(error.message || t('保存失败，请重试'))
    },
    onSettled: () => {
      locked.current = false
    },
  })

  const handleToggle = (
    admin: AdminPermissionUser,
    moduleKey: string,
    checked: boolean
  ) => {
    if (!isCurrent() || !lifetime.current || locked.current || isFetching)
      return
    const permissions = { ...admin.permissions, [moduleKey]: checked }
    if (getEnabledPermissionCount({ ...admin, permissions }, modules) === 0) {
      toast.error(t('至少需要保留一个管理员权限'))
      return
    }
    locked.current = true
    mutation.mutate({ admin, permissions })
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('管理员权限设置')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex w-full flex-col gap-4'>
          <SettingsSection title={t('管理员权限设置')}>
            {(error || data?.success === false) && (
              <p role='alert' className='text-destructive text-sm'>
                {error?.message || data?.message || t('加载失败，请重试')}
              </p>
            )}
            {isLoading ? (
              <div className='grid gap-4'>
                <Skeleton className='h-32 w-full' />
                <Skeleton className='h-32 w-full' />
              </div>
            ) : admins.length === 0 ? (
              <Empty className='border'>
                <EmptyMedia variant='icon'>
                  <ShieldCheck />
                </EmptyMedia>
                <EmptyHeader>
                  <EmptyTitle>{t('暂无存在管理员')}</EmptyTitle>
                  <EmptyDescription>
                    {t('只列出普通管理员，超级管理员不受这些开关限制')}
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <div className='grid gap-4'>
                {admins.map((admin, index) => (
                  <AdminPermissionRow
                    key={admin.id}
                    admin={admin}
                    index={index}
                    modules={modules}
                    onManage={() => setSelectedAdminId(admin.id)}
                  />
                ))}
              </div>
            )}
          </SettingsSection>
        </div>
      </SectionPageLayout.Content>

      <AdminPermissionDialog
        admin={selectedAdmin}
        modules={modules}
        open={selectedAdmin !== null}
        saving={mutation.isPending || isFetching}
        onOpenChange={(open) => {
          if (!open) {
            setSelectedAdminId(null)
          }
        }}
        onToggle={handleToggle}
      />
    </SectionPageLayout>
  )
}

function AdminPermissionRow({
  admin,
  index,
  modules,
  onManage,
}: {
  admin: AdminPermissionUser
  index: number
  modules: AdminPermissionModule[]
  onManage: () => void
}) {
  const { t } = useTranslation()
  const enabledCount = getEnabledPermissionCount(admin, modules)

  return (
    <SettingsControlGroup className='space-y-0 rounded-lg px-3 py-2'>
      <div className='flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between'>
        <div className='min-w-0'>
          <div className='flex min-w-0 items-center gap-2'>
            <span className='truncate text-sm font-medium'>
              {index + 1}. {getAdminDisplayName(admin)}
            </span>
            <Badge variant='secondary'>{t('管理员')}</Badge>
          </div>
          <p className='text-muted-foreground truncate text-xs'>
            {admin.email || admin.username}
          </p>
        </div>
        <div className='flex shrink-0 items-center gap-2 self-end sm:self-auto'>
          <Badge variant='outline'>
            {enabledCount}/{modules.length}
          </Badge>
          <Button variant='outline' size='sm' onClick={onManage}>
            <SlidersHorizontal data-icon='inline-start' />
            {t('管理权限')}
          </Button>
        </div>
      </div>
    </SettingsControlGroup>
  )
}

function AdminPermissionDialog({
  admin,
  modules,
  open,
  saving,
  onOpenChange,
  onToggle,
}: {
  admin: AdminPermissionUser | null
  modules: AdminPermissionModule[]
  open: boolean
  saving: boolean
  onOpenChange: (open: boolean) => void
  onToggle: (
    admin: AdminPermissionUser,
    moduleKey: string,
    checked: boolean
  ) => void
}) {
  const { t } = useTranslation()

  if (!admin) {
    return null
  }

  const enabledCount = getEnabledPermissionCount(admin, modules)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='h-[calc(100vh-2rem)] max-h-[calc(100vh-2rem)] sm:max-w-[calc(100vw-2rem)]'>
        <DialogHeader>
          <DialogTitle>{t('管理员权限设置')}</DialogTitle>
          <DialogDescription>
            {getAdminDisplayName(admin)} · {enabledCount}/{modules.length}
          </DialogDescription>
        </DialogHeader>

        <div className='min-h-0 space-y-3 overflow-y-auto pr-1'>
          <p className='text-muted-foreground text-xs'>
            {t(
              '模块关闭时读写均不可用；未单独配置的读写权限继承模块开关，写入不包含读取。'
            )}
          </p>
          <div className='grid gap-2 lg:grid-cols-2 xl:grid-cols-3'>
            {modules.map((module) => {
              const checked = moduleEnabled(admin.permissions, module)
              const labels = MODULE_LABELS[module.key]
              const wouldDisableLastModule =
                getEnabledPermissionCount(
                  {
                    ...admin,
                    permissions: { ...admin.permissions, [module.key]: false },
                  },
                  modules
                ) === 0
              return (
                <SettingsSwitchRow
                  key={`${admin.id}.${module.key}`}
                  className='bg-muted/20 flex-col items-stretch rounded-lg border px-3 py-2.5 last:border-b'
                >
                  <SettingsSwitchContent>
                    <div className='text-sm font-medium'>
                      {t(labels?.title ?? '未知权限模块')}
                    </div>
                    <p className='text-muted-foreground text-xs'>
                      {t(labels?.description ?? '请以服务器授权规则为准')}
                    </p>
                  </SettingsSwitchContent>
                  <div className='flex items-center justify-between gap-2'>
                    <span className='text-xs'>{t('模块开关')}</span>
                    <Switch
                      aria-label={t('模块开关')}
                      checked={checked}
                      onCheckedChange={(nextChecked) =>
                        onToggle(admin, module.key, nextChecked)
                      }
                      disabled={saving || (checked && wouldDisableLastModule)}
                    />
                  </div>
                  {(['read', 'write'] as const).map((action) => {
                    const key = `${module.key}:${action}`
                    const allowed = actionAllowed(
                      admin.permissions,
                      module,
                      action
                    )
                    const wouldDisableLastAction =
                      getEnabledPermissionCount(
                        {
                          ...admin,
                          permissions: { ...admin.permissions, [key]: false },
                        },
                        modules
                      ) === 0
                    return (
                      <div
                        key={key}
                        className='flex items-center justify-between gap-2'
                      >
                        <span className='text-xs'>
                          {action === 'read' ? t('读取权限') : t('写入权限')}
                        </span>
                        <Switch
                          aria-label={
                            action === 'read' ? t('读取权限') : t('写入权限')
                          }
                          checked={allowed}
                          disabled={
                            saving ||
                            !checked ||
                            (allowed && wouldDisableLastAction)
                          }
                          onCheckedChange={(nextChecked) =>
                            onToggle(admin, key, nextChecked)
                          }
                        />
                      </div>
                    )
                  })}
                </SettingsSwitchRow>
              )
            })}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

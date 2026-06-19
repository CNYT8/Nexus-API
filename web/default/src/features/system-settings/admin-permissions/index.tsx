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
import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ShieldCheck, SlidersHorizontal } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { getSelf } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { SectionPageLayout } from '@/components/layout'
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
import {
  SettingsControlGroup,
  SettingsSwitchContent,
  SettingsSwitchRow,
} from '../components/settings-form-layout'
import { SettingsSection } from '../components/settings-section'
import { getAdminPermissions, updateAdminPermissions } from './api'
import type { AdminPermissionModule, AdminPermissionUser } from './types'

const adminPermissionsQueryKey = ['admin-permissions'] as const

function getAdminDisplayName(admin: AdminPermissionUser) {
  return admin.display_name || admin.username || `#${admin.id}`
}

function getEnabledPermissionCount(
  admin: AdminPermissionUser,
  modules: AdminPermissionModule[]
) {
  return modules.filter((module) => admin.permissions[module.key] !== false)
    .length
}

export function AdminPermissionsSettings() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const currentUser = useAuthStore((s) => s.auth.user)
  const setUser = useAuthStore((s) => s.auth.setUser)
  const [selectedAdminId, setSelectedAdminId] = useState<number | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: adminPermissionsQueryKey,
    queryFn: getAdminPermissions,
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
    }) => updateAdminPermissions(admin.id, permissions),
    onSuccess: async (response) => {
      if (!response.success) {
        toast.error(response.message || t('Save failed'))
        return
      }
      await queryClient.invalidateQueries({ queryKey: adminPermissionsQueryKey })
      toast.success(t('Saved successfully'))

      if (response.data?.id === currentUser?.id) {
        const selfResponse = await getSelf()
        if (selfResponse.success && selfResponse.data) {
          setUser(selfResponse.data)
        }
      }
    },
    onError: (error: Error) => {
      toast.error(error.message || t('Save failed, please retry'))
    },
  })

  const handleToggle = (
    admin: AdminPermissionUser,
    moduleKey: string,
    checked: boolean
  ) => {
    const permissions = {
      ...admin.permissions,
      [moduleKey]: checked,
    }
    if (
      !checked &&
      getEnabledPermissionCount({ ...admin, permissions }, modules) === 0
    ) {
      toast.error(t('At least one admin permission must remain enabled'))
      return
    }
    mutation.mutate({ admin, permissions })
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('管理员权限设置')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex w-full flex-col gap-4'>
          <SettingsSection title={t('管理员权限设置')}>
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
                    {t('Only regular administrators are listed here.')}
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
        saving={mutation.isPending}
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
            <Badge variant='secondary'>{t('Admin')}</Badge>
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
            {t('Manage permissions')}
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

        <div className='min-h-0 overflow-y-auto pr-1'>
          <div className='grid gap-2 lg:grid-cols-2 xl:grid-cols-3'>
            {modules.map((module) => {
              const checked = admin.permissions[module.key] !== false
              return (
                <SettingsSwitchRow
                  key={`${admin.id}.${module.key}`}
                  className='bg-muted/20 rounded-lg border px-3 py-2.5 last:border-b'
                >
                  <SettingsSwitchContent>
                    <div className='text-sm font-medium'>
                      {t(module.title_key)}
                    </div>
                    <p className='text-muted-foreground text-xs'>
                      {t(module.description)}
                    </p>
                  </SettingsSwitchContent>
                  <Switch
                    checked={checked}
                    onCheckedChange={(nextChecked) =>
                      onToggle(admin, module.key, nextChecked)
                    }
                    disabled={saving || (checked && enabledCount <= 1)}
                  />
                </SettingsSwitchRow>
              )
            })}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

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
import { useMemo } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ShieldCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { getSelf } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
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
  SettingsControlChildren,
  SettingsControlGroup,
  SettingsSwitchContent,
  SettingsSwitchRow,
} from '../components/settings-form-layout'
import { SettingsSection } from '../components/settings-section'
import { getAdminPermissions, updateAdminPermissions } from './api'
import type { AdminPermissionUser } from './types'

const adminPermissionsQueryKey = ['admin-permissions'] as const

function getAdminDisplayName(admin: AdminPermissionUser) {
  return admin.display_name || admin.username || `#${admin.id}`
}

export function AdminPermissionsSettings() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const currentUser = useAuthStore((s) => s.auth.user)
  const setUser = useAuthStore((s) => s.auth.setUser)

  const { data, isLoading } = useQuery({
    queryKey: adminPermissionsQueryKey,
    queryFn: getAdminPermissions,
  })

  const modules = useMemo(() => data?.data?.modules ?? [], [data?.data])
  const admins = useMemo(() => data?.data?.admins ?? [], [data?.data])

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
                  <SettingsControlGroup key={admin.id}>
                    <div className='flex min-w-0 flex-wrap items-center justify-between gap-3 border-b pb-2.5'>
                      <div className='min-w-0'>
                        <div className='flex min-w-0 items-center gap-2'>
                          <span className='text-sm font-medium'>
                            {index + 1}. {getAdminDisplayName(admin)}
                          </span>
                          <Badge variant='secondary'>{t('Admin')}</Badge>
                        </div>
                        <p className='text-muted-foreground truncate text-xs'>
                          {admin.email || admin.username}
                        </p>
                      </div>
                    </div>

                    <SettingsControlChildren className='grid gap-3 md:grid-cols-2'>
                      {modules.map((module) => (
                        <SettingsSwitchRow
                          key={`${admin.id}.${module.key}`}
                          className='border-b-0 py-2'
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
                            checked={admin.permissions[module.key] !== false}
                            onCheckedChange={(checked) =>
                              handleToggle(admin, module.key, checked)
                            }
                            disabled={mutation.isPending}
                          />
                        </SettingsSwitchRow>
                      ))}
                    </SettingsControlChildren>
                  </SettingsControlGroup>
                ))}
              </div>
            )}
          </SettingsSection>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

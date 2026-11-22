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
import { useCallback, useEffect, useRef } from 'react'
import { createLanguageChangeQueue } from '../../../shared/language-change-queue'
import {
  INTERFACE_LANGUAGE_OPTIONS,
  normalizeInterfaceLanguage,
} from '@/i18n/languages'
import { Languages, Check } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { api } from '@/lib/api'
import { notifyServerError, requireServerSuccess } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

export function LanguageSwitcher() {
  const { i18n, t } = useTranslation()
  const user = useAuthStore((s) => s.auth.user)
  const currentLanguage = normalizeInterfaceLanguage(i18n.language)

  const mounted = useRef(true)
  const languageQueue = useRef<{
    userId: number | undefined
    change: ((language: string) => Promise<void>) | null
  } | null>(null)
  if (!languageQueue.current || languageQueue.current.userId !== user?.id) {
    languageQueue.current = { userId: user?.id, change: null }
  }
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  const handleChangeLanguage = useCallback(
    (code: string) => {
      const state = languageQueue.current
      if (!state) return
      state.change ??= createLanguageChangeQueue({
        getLanguage: () => normalizeInterfaceLanguage(i18n.language),
        changeLanguage: (language) => i18n.changeLanguage(language),
        isCurrent: () => mounted.current && languageQueue.current === state &&
          useAuthStore.getState().auth.user?.id === state.userId,
        saveLanguage: async (language) => {
          if (state.userId === undefined) return
          const response = await api.put('/api/user/self', { language }, {
            skipBusinessError: true,
            skipErrorHandler: true,
          })
          requireServerSuccess(response.data)
        },
        onConfirmed: (language) => {
          const { user: currentUser, setUser } = useAuthStore.getState().auth
          if (!currentUser) return
          let settings: Record<string, unknown> = {}
          try {
            const parsed: unknown = typeof currentUser.setting === 'string'
              ? JSON.parse(currentUser.setting)
              : currentUser.setting
            if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
              settings = parsed as Record<string, unknown>
            }
          } catch { /* Keep a malformed settings string from blocking language changes. */ }
          setUser({
            ...currentUser,
            setting: JSON.stringify({ ...settings, language }),
          })
        },
        onError: (error) => notifyServerError(error, t('语言切换失败，请重试')),
      })
      void state.change(code)
    },
    [i18n, t]
  )

  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger
        render={<Button variant='ghost' size='icon' className='h-9 w-9' />}
      >
        <Languages className='size-[1.2rem]' />
        <span className='sr-only'>{t('Change language')}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end'>
        {INTERFACE_LANGUAGE_OPTIONS.map((lang) => (
          <DropdownMenuItem
            key={lang.code}
            onClick={() => handleChangeLanguage(lang.code)}
          >
            {lang.label}
            <Check
              size={14}
              className={cn(
                'ms-auto',
                currentLanguage !== lang.code && 'hidden'
              )}
            />
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

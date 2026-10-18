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
import { DatabaseIcon, ExternalLinkIcon, RefreshCcwIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatTimestamp, formatTimestampToDate } from '@/lib/format'
import {
  fetchLatestRelease,
  NEW_API_BASE_RELEASE,
  NEXUS_API_LATEST_RELEASE_API,
  NEXUS_API_RELEASES_URL,
  type ReleaseInfo,
} from '@/lib/nexus-version'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Markdown } from '@/components/ui/markdown'
import { checkDatabaseIntegrity } from '../api'
import { SettingsSection } from '../components/settings-section'
import type { DatabaseIntegrityReport } from '../types'

type UpdateCheckerSectionProps = {
  currentVersion?: string | null
  startTime?: number | null
}

export function UpdateCheckerSection({
  currentVersion,
  startTime,
}: UpdateCheckerSectionProps) {
  const { t } = useTranslation()
  const [checking, setChecking] = useState(false)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [release, setRelease] = useState<ReleaseInfo | null>(null)
  const [checkingDb, setCheckingDb] = useState(false)
  const [dbDialogOpen, setDbDialogOpen] = useState(false)
  const [dbReport, setDbReport] = useState<DatabaseIntegrityReport | null>(null)
  const [latestNewApiRelease, setLatestNewApiRelease] =
    useState<ReleaseInfo | null>(() => NEW_API_BASE_RELEASE)

  const uptime = startTime ? formatTimestamp(startTime) : t('Unknown')
  const version = currentVersion || t('Unknown')

  const handleCheckUpdates = async () => {
    setChecking(true)
    try {
      const data = await fetchLatestRelease(NEXUS_API_LATEST_RELEASE_API)
      setLatestNewApiRelease(NEW_API_BASE_RELEASE)

      if (currentVersion && data.tag_name === currentVersion) {
        toast.success(
          t('You are running the latest version ({{version}}).', {
            version: data.tag_name,
          })
        )
        return
      }

      setRelease(data)
      setDialogOpen(true)
    } catch {
      toast.error(t('Failed to check for updates'))
    } finally {
      setChecking(false)
    }
  }

  const goToRelease = () => {
    window.open(
      release?.html_url ?? NEXUS_API_RELEASES_URL,
      '_blank',
      'noopener,noreferrer'
    )
  }

  const handleCheckDbIntegrity = async () => {
    setCheckingDb(true)
    try {
      const res = await checkDatabaseIntegrity()
      if (!res?.success || !res.data) {
        toast.error(res?.message || t('数据库完整性检查失败，请稍后重试'))
        return
      }
      if (res.data.healthy) {
        toast.success(
          `${t('数据库完整性正常')}（${res.data.main_dialect || 'SQL'}：${res.data.checked_tables || 0}/${res.data.total_tables || 0}）`
        )
        return
      }
      setDbReport(res.data)
      setDbDialogOpen(true)
    } catch {
      toast.error(t('数据库完整性检查失败，请稍后重试'))
    } finally {
      setCheckingDb(false)
    }
  }

  return (
    <>
      <SettingsSection title={t('System maintenance')}>
        <div className='space-y-6'>
          <div className='grid gap-4 md:grid-cols-2'>
            <div className='rounded-lg border p-4'>
              <div className='text-muted-foreground text-sm'>
                {t('Current version')}
              </div>
              <div className='text-lg font-semibold'>{version}</div>
              <div className='text-muted-foreground mt-1 text-xs'>
                {t('Based on new-api')}{' '}
                <a
                  href={latestNewApiRelease?.html_url}
                  target='_blank'
                  rel='noopener noreferrer'
                  className='hover:text-foreground underline-offset-2 hover:underline'
                >
                  {latestNewApiRelease?.tag_name ?? t('Unknown')}
                </a>
              </div>
            </div>
            <div className='rounded-lg border p-4'>
              <div className='text-muted-foreground text-sm'>
                {t('Uptime since')}
              </div>
              <div className='text-lg font-semibold'>{uptime}</div>
            </div>
          </div>

          <div className='flex flex-wrap gap-3'>
            <Button onClick={handleCheckUpdates} disabled={checking}>
              {checking ? (
                t('Checking updates...')
              ) : (
                <>
                  <RefreshCcwIcon className='me-2 h-4 w-4' />
                  {t('Check for updates')}
                </>
              )}
            </Button>
            <Button
              variant='outline'
              onClick={handleCheckDbIntegrity}
              disabled={checkingDb}
            >
              <DatabaseIcon className='me-2 h-4 w-4' />
              {t('检查数据库完整性')}
            </Button>
          </div>
        </div>
      </SettingsSection>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className='max-h-[80vh] overflow-y-auto'>
          <DialogHeader>
            <DialogTitle>
              {release?.tag_name
                ? t('New version available: {{version}}', {
                    version: release.tag_name,
                  })
                : t('Release details')}
            </DialogTitle>
            {release?.published_at && (
              <DialogDescription>
                {t('Published')}{' '}
                {formatTimestampToDate(
                  new Date(release.published_at).getTime(),
                  'milliseconds'
                )}
              </DialogDescription>
            )}
          </DialogHeader>

          <div className='space-y-4'>
            {release?.body ? (
              <Markdown>{release.body}</Markdown>
            ) : (
              <p className='text-muted-foreground text-sm'>
                {t('No release notes provided.')}
              </p>
            )}
          </div>

          <DialogFooter>
            <Button
              type='button'
              variant='secondary'
              onClick={() => setDialogOpen(false)}
            >
              {t('Close')}
            </Button>
            {release?.html_url && (
              <Button type='button' onClick={goToRelease}>
                <ExternalLinkIcon className='me-2 h-4 w-4' />
                {t('Open release')}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={dbDialogOpen} onOpenChange={setDbDialogOpen}>
        <DialogContent className='max-h-[80vh] max-w-2xl overflow-y-auto'>
          <DialogHeader>
            <DialogTitle>{t('数据库完整性检查异常日志')}</DialogTitle>
            <DialogDescription>
              {t(
                '检测到数据库连接或表结构存在以下异常（仅只读检测，未对数据库做任何修改）：'
              )}
            </DialogDescription>
          </DialogHeader>

          <div className='space-y-4'>
            {Array.isArray(dbReport?.issues) && dbReport.issues.length > 0 && (
              <pre className='bg-muted text-destructive max-h-56 overflow-y-auto rounded-lg p-3 font-mono text-xs break-words whitespace-pre-wrap'>
                {dbReport.issues.join('\n')}
              </pre>
            )}
            <div className='space-y-2'>
              <div className='text-sm font-medium'>{t('完整检测日志')}</div>
              <pre className='bg-muted max-h-64 overflow-y-auto rounded-lg p-3 font-mono text-xs break-words whitespace-pre-wrap'>
                {Array.isArray(dbReport?.logs) ? dbReport.logs.join('\n') : ''}
              </pre>
            </div>
          </div>

          <DialogFooter>
            <Button type='button' onClick={() => setDbDialogOpen(false)}>
              {t('Close')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

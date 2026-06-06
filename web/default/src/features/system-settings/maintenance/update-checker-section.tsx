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
import { useEffect, useState } from 'react'
import { ExternalLinkIcon, RefreshCcwIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatTimestamp, formatTimestampToDate } from '@/lib/format'
import {
  fetchLatestRelease,
  NEW_API_LATEST_RELEASE_API,
  NEW_API_RELEASES_URL,
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
import { SettingsSection } from '../components/settings-section'

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
  const [latestNewApiRelease, setLatestNewApiRelease] =
    useState<ReleaseInfo | null>(null)

  const uptime = startTime ? formatTimestamp(startTime) : t('Unknown')
  const version = currentVersion || t('Unknown')

  useEffect(() => {
    fetchLatestRelease(NEW_API_LATEST_RELEASE_API)
      .then(setLatestNewApiRelease)
      .catch(() => setLatestNewApiRelease(null))
  }, [])

  const handleCheckUpdates = async () => {
    setChecking(true)
    try {
      const data = await fetchLatestRelease(NEXUS_API_LATEST_RELEASE_API)
      fetchLatestRelease(NEW_API_LATEST_RELEASE_API)
        .then(setLatestNewApiRelease)
        .catch(() => setLatestNewApiRelease(null))

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

  return (
    <>
      <SettingsSection title={t('System maintenance')}>
        <div className='space-y-6'>
          <div className='grid gap-4 md:grid-cols-2'>
            <div className='rounded-lg border p-4'>
              <div className='text-muted-foreground text-sm'>
                {t('Current Nexus-API Version')}
              </div>
              <div className='text-lg font-semibold'>{version}</div>
            </div>
            <div className='rounded-lg border p-4'>
              <div className='text-muted-foreground text-sm'>
                {t('Latest Original new-api Version')}
              </div>
              <a
                href={latestNewApiRelease?.html_url ?? NEW_API_RELEASES_URL}
                target='_blank'
                rel='noopener noreferrer'
                className='text-lg font-semibold hover:underline'
              >
                {latestNewApiRelease?.tag_name ?? t('Unknown')}
              </a>
            </div>
            <div className='rounded-lg border p-4'>
              <div className='text-muted-foreground text-sm'>
                {t('Uptime since')}
              </div>
              <div className='text-lg font-semibold'>{uptime}</div>
            </div>
          </div>

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
    </>
  )
}

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
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { api } from '@/lib/api'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { taskArtifactContentUrl } from '../../../../../../shared/task-artifact-url'
import type { TaskLog } from '../../types'

type TaskResult = {
  text?: string
  artifacts?: {
    id: string
    kind: string
    url?: string
    content_url?: string
    mime_type?: string
  }[]
}
function safeUrl(value?: string) {
  if (!value || !/^https?:\/\//i.test(value)) return undefined
  try {
    const url = new URL(value)
    return !url.username && !url.password ? value : undefined
  } catch {
    return undefined
  }
}

export function TaskResultDialog({ log }: { log: TaskLog }) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const isOwner =
    typeof userId === 'number' && userId > 0 && userId === log.user_id
  const scope = `${userId}:${log.task_id}`
  const [open, setOpen] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const [state, setState] = useState<{
    scope: string
    result?: TaskResult
    error?: string
  } | null>(null)
  const current = state?.scope === scope ? state : null
  const resultUrl = isOwner ? safeUrl(log.result_url) : undefined

  useEffect(() => {
    if (!open || !isOwner || !log.task_id) return
    let cancelled = false
    void api
      .get(`/api/task/self/${encodeURIComponent(log.task_id)}/artifacts`, {
        skipBusinessError: true,
        skipErrorHandler: true,
      })
      .then(({ data }) => {
        if (
          data?.success !== true ||
          !data.data ||
          typeof data.data !== 'object'
        )
          throw data
        if (!cancelled) setState({ scope, result: data.data })
      })
      .catch((cause: unknown) => {
        if (!cancelled)
          setState({
            scope,
            error: getServerErrorMessage(
              cause,
              t('Failed to load task artifacts')
            ),
          })
      })
    return () => {
      cancelled = true
    }
  }, [open, isOwner, log.task_id, scope, attempt, t])

  const result = isOwner ? current?.result : undefined
  return (
    <>
      <button
        type='button'
        className='text-muted-foreground w-fit text-xs hover:underline'
        onClick={(event) => {
          event.stopPropagation()
          setState(null)
          setOpen(true)
        }}
      >
        {t('Task result details')}
      </button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='max-h-[85dvh] overflow-y-auto sm:max-w-2xl'>
          <DialogHeader>
            <DialogTitle>{t('Task result details')}</DialogTitle>
            <DialogDescription className='break-all'>
              {log.task_id}
            </DialogDescription>
          </DialogHeader>
          <dl className='grid grid-cols-2 gap-2 text-sm'>
            <dt>{t('Status')}</dt>
            <dd>{log.status || '-'}</dd>
            <dt>{t('Group')}</dt>
            <dd className='break-all'>{log.group || '-'}</dd>
            <dt>{t('Server quota')}</dt>
            <dd>{log.quota == null ? '-' : String(log.quota)}</dd>
          </dl>
          {log.usage_snapshot && (
            <section className='min-w-0 space-y-3 rounded-md border p-3'>
              <dl className='grid grid-cols-2 gap-2 text-sm'>
                <dt>{t('Name')}</dt>
                <dd className='break-all'>{log.usage_snapshot.name}</dd>
                <dt>{t('Version')}</dt>
                <dd className='break-all'>{log.usage_snapshot.version}</dd>
              </dl>
              <div className='space-y-2'>
                <h4 className='text-sm font-semibold'>
                  {t('Usage schema (JSON)')}
                </h4>
                <pre className='bg-muted/50 max-h-64 overflow-auto rounded-md p-3 text-xs break-words whitespace-pre-wrap'>
                  {JSON.stringify(log.usage_snapshot.schema, null, 2)}
                </pre>
              </div>
              <div className='space-y-2'>
                <h4 className='text-sm font-semibold'>
                  {t('Usage expression (USD)')}
                </h4>
                <pre className='bg-muted/50 max-h-40 overflow-auto rounded-md p-3 text-sm break-words whitespace-pre-wrap'>
                  {log.usage_snapshot.expression}
                </pre>
              </div>
            </section>
          )}
          {log.fail_reason && (
            <p className='text-destructive text-sm break-words whitespace-pre-wrap'>
              {log.fail_reason}
            </p>
          )}
          {resultUrl && (
            <a
              href={resultUrl}
              target='_blank'
              rel='noopener noreferrer'
              className='text-sm break-all underline'
            >
              {t('Open task result')}
            </a>
          )}
          {!isOwner && (
            <p className='text-muted-foreground text-sm'>
              {t('Artifacts are available only to the task owner')}
            </p>
          )}
          {isOwner && !current && (
            <p role='status' className='text-sm'>
              {t('Loading...')}
            </p>
          )}
          {isOwner && current?.error && (
            <div className='space-y-2'>
              <p role='alert' className='text-destructive text-sm break-words'>
                {current.error}
              </p>
              <Button
                variant='outline'
                onClick={() => {
                  setState(null)
                  setAttempt((value) => value + 1)
                }}
              >
                {t('Retry')}
              </Button>
            </div>
          )}
          {result && (
            <div className='space-y-4'>
              {result.text && (
                <section className='space-y-2'>
                  <h4 className='text-sm font-semibold'>{t('Result text')}</h4>
                  <pre className='bg-muted/50 max-h-80 overflow-auto rounded-md p-3 text-sm break-words whitespace-pre-wrap'>
                    {result.text}
                  </pre>
                </section>
              )}
              {Array.isArray(result.artifacts) &&
                result.artifacts.length > 0 && (
                  <section className='space-y-2'>
                    <h4 className='text-sm font-semibold'>
                      {t('Task artifacts')}
                    </h4>
                    <ul className='space-y-2'>
                      {result.artifacts.map((artifact, index) => {
                        const contentUrl = taskArtifactContentUrl(
                          artifact.content_url,
                          log.task_id,
                          artifact.id
                        )
                        const url =
                          artifact.content_url == null
                            ? safeUrl(artifact.url)
                            : contentUrl
                        return (
                          <li
                            key={`${artifact.id}:${index}`}
                            className='rounded-md border p-3 text-sm break-all'
                          >
                            <div>
                              {artifact.kind} · {artifact.id}
                            </div>
                            {artifact.mime_type && (
                              <div className='text-muted-foreground'>
                                {artifact.mime_type}
                              </div>
                            )}
                            {url && (
                              <a
                                href={url}
                                target={contentUrl ? undefined : '_blank'}
                                rel='noopener noreferrer'
                                className='underline'
                              >
                                {t('Open artifact')}
                              </a>
                            )}
                          </li>
                        )
                      })}
                    </ul>
                  </section>
                )}
              {!result.text && !result.artifacts?.length && (
                <p className='text-muted-foreground text-sm'>
                  {t('No task artifacts or text available')}
                </p>
              )}
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}

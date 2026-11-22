/*
Copyright (C) 2025 QuantumNous

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

import React, { useContext, useEffect, useState } from 'react';
import { Button, Modal, Typography } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { UserContext } from '../../../../context/User';
import { API } from '../../../../helpers';
import { getServerErrorMessage } from '../../../../helpers/server-error-message';
import { taskArtifactContentUrl } from '../../../../../../shared/task-artifact-url';

function safeUrl(value) {
  if (typeof value !== 'string' || !/^https?:\/\//i.test(value))
    return undefined;
  try {
    const url = new URL(value);
    return !url.username && !url.password ? value : undefined;
  } catch {
    return undefined;
  }
}

export default function TaskResultDetails({ record }) {
  const { t } = useTranslation();
  const [userState] = useContext(UserContext);
  const userId = userState.user?.id;
  const isOwner =
    typeof userId === 'number' && userId > 0 && userId === record.user_id;
  const scope = `${userId}:${record.task_id}`;
  const [open, setOpen] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState(null);
  const current = state?.scope === scope ? state : null;
  const resultUrl = isOwner ? safeUrl(record.result_url) : undefined;

  useEffect(() => {
    if (!open || !isOwner || !record.task_id) return;
    let cancelled = false;
    setState(null);
    void API.get(
      `/api/task/self/${encodeURIComponent(record.task_id)}/artifacts`,
      {
        skipErrorHandler: true,
      },
    )
      .then(({ data }) => {
        if (
          data?.success !== true ||
          !data.data ||
          typeof data.data !== 'object'
        )
          throw data;
        if (!cancelled) setState({ scope, result: data.data });
      })
      .catch((cause) => {
        if (!cancelled)
          setState({
            scope,
            error: getServerErrorMessage(
              cause,
              t('Failed to load task artifacts'),
            ),
          });
      });
    return () => {
      cancelled = true;
    };
  }, [open, isOwner, record.task_id, scope, attempt, t]);

  const result = isOwner ? current?.result : undefined;
  return (
    <>
      <Button
        size='small'
        theme='borderless'
        onClick={(event) => {
          event.stopPropagation();
          setOpen(true);
        }}
      >
        {t('Task result details')}
      </Button>
      <Modal
        title={t('Task result details')}
        visible={open}
        onCancel={() => setOpen(false)}
        footer={null}
        width='min(720px, calc(100vw - 32px))'
        bodyStyle={{
          maxHeight: '75vh',
          overflowY: 'auto',
          overflowWrap: 'anywhere',
        }}
      >
        <div className='flex flex-col gap-4'>
          <Typography.Text copyable>{record.task_id}</Typography.Text>
          <dl className='grid grid-cols-2 gap-2'>
            <dt>{t('Status')}</dt>
            <dd>{record.status || '-'}</dd>
            <dt>{t('Group')}</dt>
            <dd>{record.group || '-'}</dd>
            <dt>{t('Server quota')}</dt>
            <dd>{record.quota == null ? '-' : String(record.quota)}</dd>
          </dl>
          {record.usage_snapshot && (
            <section className='min-w-0 space-y-3'>
              <dl className='grid grid-cols-2 gap-2'>
                <dt>{t('Name')}</dt>
                <dd className='break-all'>{record.usage_snapshot.name}</dd>
                <dt>{t('Version')}</dt>
                <dd className='break-all'>{record.usage_snapshot.version}</dd>
              </dl>
              <div>
                <Typography.Text strong>
                  {t('Usage schema (JSON)')}
                </Typography.Text>
                <pre
                  style={{
                    maxHeight: 260,
                    overflow: 'auto',
                    whiteSpace: 'pre-wrap',
                    overflowWrap: 'anywhere',
                    padding: 12,
                    background: 'var(--semi-color-fill-0)',
                  }}
                >
                  {JSON.stringify(record.usage_snapshot.schema, null, 2)}
                </pre>
              </div>
              <div>
                <Typography.Text strong>
                  {t('Usage expression (USD)')}
                </Typography.Text>
                <pre
                  style={{
                    maxHeight: 160,
                    overflow: 'auto',
                    whiteSpace: 'pre-wrap',
                    overflowWrap: 'anywhere',
                    padding: 12,
                    background: 'var(--semi-color-fill-0)',
                  }}
                >
                  {record.usage_snapshot.expression}
                </pre>
              </div>
            </section>
          )}
          {record.fail_reason && (
            <p
              style={{
                color: 'var(--semi-color-danger)',
                whiteSpace: 'pre-wrap',
              }}
            >
              {record.fail_reason}
            </p>
          )}
          {resultUrl && (
            <a href={resultUrl} target='_blank' rel='noopener noreferrer'>
              {t('Open task result')}
            </a>
          )}
          {!isOwner && (
            <Typography.Paragraph type='tertiary'>
              {t('Artifacts are available only to the task owner')}
            </Typography.Paragraph>
          )}
          {isOwner && !current && <p role='status'>{t('Loading...')}</p>}
          {isOwner && current?.error && (
            <div>
              <p role='alert' style={{ color: 'var(--semi-color-danger)' }}>
                {current.error}
              </p>
              <Button onClick={() => setAttempt((value) => value + 1)}>
                {t('Retry')}
              </Button>
            </div>
          )}
          {result && (
            <>
              {result.text && (
                <section>
                  <Typography.Text strong>{t('Result text')}</Typography.Text>
                  <pre
                    style={{
                      maxHeight: 320,
                      overflow: 'auto',
                      whiteSpace: 'pre-wrap',
                      overflowWrap: 'anywhere',
                      padding: 12,
                      background: 'var(--semi-color-fill-0)',
                    }}
                  >
                    {result.text}
                  </pre>
                </section>
              )}
              {Array.isArray(result.artifacts) &&
                result.artifacts.length > 0 && (
                  <section>
                    <Typography.Text strong>
                      {t('Task artifacts')}
                    </Typography.Text>
                    <ul className='space-y-2'>
                      {result.artifacts.map((artifact, index) => {
                        const contentUrl = taskArtifactContentUrl(
                          artifact.content_url,
                          record.task_id,
                          artifact.id,
                        );
                        const url =
                          artifact.content_url == null
                            ? safeUrl(artifact.url)
                            : contentUrl;
                        return (
                          <li key={`${artifact.id}:${index}`} className='py-2'>
                            <div>
                              {artifact.kind} · {artifact.id}
                            </div>
                            {artifact.mime_type && (
                              <Typography.Text type='tertiary'>
                                {artifact.mime_type}
                              </Typography.Text>
                            )}
                            {url && (
                              <div>
                                <a
                                  href={url}
                                  target={contentUrl ? undefined : '_blank'}
                                  rel='noopener noreferrer'
                                >
                                  {t('Open artifact')}
                                </a>
                              </div>
                            )}
                          </li>
                        );
                      })}
                    </ul>
                  </section>
                )}
              {!result.text && !result.artifacts?.length && (
                <Typography.Paragraph type='tertiary'>
                  {t('No task artifacts or text available')}
                </Typography.Paragraph>
              )}
            </>
          )}
        </div>
      </Modal>
    </>
  );
}

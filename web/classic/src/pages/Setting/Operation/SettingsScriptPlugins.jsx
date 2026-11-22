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

import React, {
  useCallback,
  useContext,
  useEffect,
  useId,
  useState,
} from 'react';
import {
  Button,
  Card,
  Input,
  Modal,
  Popconfirm,
  Table,
  Tag,
  TextArea,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { UserContext } from '../../../context/User';
import { useAccountSession } from '../../../hooks/playground/accountSession';
import { API } from '../../../helpers';
import { getServerErrorMessage } from '../../../helpers/server-error-message';
import useScriptPluginRequests from './useScriptPluginRequests';
import ScriptPluginMarketplace from './ScriptPluginMarketplace';
import ScriptPluginCapabilities from './ScriptPluginCapabilities';
import ScriptPluginLifecycle from './ScriptPluginLifecycle';

const endpoint = '/api/task/script_plugin';
const revisionPageSize = 100;
const bindingPageSize = 20;
const maxSourceBytes = 1 << 20;
const sourceStyle = { fontFamily: 'monospace', tabSize: 2 };
const preStyle = {
  ...sourceStyle,
  maxHeight: 360,
  overflow: 'auto',
  whiteSpace: 'pre-wrap',
  overflowWrap: 'anywhere',
};
const positiveID = (value) => Number.isSafeInteger(value) && value > 0;
const byteLength = (value) => new TextEncoder().encode(value).length;
const validRevision = (value) =>
  value &&
  positiveID(value.id) &&
  typeof value.key === 'string' &&
  typeof value.name === 'string' &&
  typeof value.version === 'string' &&
  typeof value.enabled === 'boolean';
const validBinding = (value) =>
  value &&
  positiveID(value.channel_id) &&
  typeof value.model === 'string' &&
  value.model.length > 0 &&
  positiveID(value.plugin_id);
const revisionLabel = (revision) =>
  `#${revision.id} · ${revision.name} · ${revision.version}`;

// A successful HTTP status alone is not an acknowledgement of a write.
function unwrap(response) {
  if (response?.data?.success !== true) throw response?.data;
  return response.data.data;
}

export default function SettingsScriptPlugins() {
  const [userState] = useContext(UserContext);
  const session = useAccountSession();
  if (
    userState.user?.role !== 100 ||
    String(userState.user.id) !== session.userId
  )
    return null;
  // A new session discards source/binding drafts and every open confirmation.
  return <ScriptPluginPanel key={session.revision} session={session} />;
}

function ScriptPluginPanel({ session }) {
  const { t } = useTranslation();
  const fieldId = useId();
  const { pending, run, cancel } = useScriptPluginRequests(session);
  const [revisions, setRevisions] = useState([]);
  const [cursors, setCursors] = useState([0]);
  const [hasNext, setHasNext] = useState(false);
  const [listLoaded, setListLoaded] = useState(false);
  const [source, setSource] = useState('');
  const [detailId, setDetailId] = useState(null);
  const [detail, setDetail] = useState(null);
  const [bindingDraft, setBindingDraft] = useState({
    channel: '',
    model: '',
    revision: '',
  });
  const [channelFilter, setChannelFilter] = useState('');
  const [activeChannel, setActiveChannel] = useState(0);
  const [bindings, setBindings] = useState([]);
  const [bindingPage, setBindingPage] = useState(1);
  const [bindingsLoaded, setBindingsLoaded] = useState(false);
  const [unbindTarget, setUnbindTarget] = useState(null);
  const [errors, setErrors] = useState({});
  const [notice, setNotice] = useState('');
  const [marketBusy, setMarketBusy] = useState(false);
  const [revisionAction, setRevisionAction] = useState(null);

  const clearError = useCallback((slot) => {
    setErrors((current) => ({ ...current, [slot]: '' }));
  }, []);
  const report = useCallback(
    (slot, cause) => {
      setErrors((current) => ({
        ...current,
        [slot]: getServerErrorMessage(
          cause,
          t('Task plugin request failed; draft retained'),
        ),
      }));
    },
    [t],
  );
  const invalidResponse = useCallback(() => {
    throw new Error(t('数据格式错误'));
  }, [t]);

  const loadRevisions = useCallback(
    (nextCursors = [0]) => {
      clearError('list');
      const before = nextCursors[nextCursors.length - 1];
      return run(
        'list',
        async (config) => {
          const rows = unwrap(
            await API.get(endpoint, {
              ...config,
              params: { before_id: before },
            }),
          );
          if (
            !Array.isArray(rows) ||
            rows.length > revisionPageSize ||
            rows.some(
              (row, index) =>
                !validRevision(row) ||
                (before > 0 && row.id >= before) ||
                (index > 0 && row.id >= rows[index - 1].id),
            )
          ) {
            invalidResponse();
          }
          return rows;
        },
        (rows) => {
          setRevisions(rows);
          setCursors(nextCursors);
          setHasNext(rows.length === revisionPageSize);
          setListLoaded(true);
        },
        (cause) => report('list', cause),
      );
    },
    [clearError, invalidResponse, report, run],
  );

  const loadBindings = useCallback(
    (channel = 0) => {
      clearError('bindings');
      return run(
        'bindings',
        async (config) => {
          const rows = unwrap(
            await API.get(`${endpoint}/bindings`, {
              ...config,
              params: { channel_id: channel },
            }),
          );
          if (
            !Array.isArray(rows) ||
            rows.length > 1000 ||
            rows.some(
              (row) =>
                !validBinding(row) ||
                (channel > 0 && row.channel_id !== channel),
            )
          ) {
            invalidResponse();
          }
          return rows;
        },
        (rows) => {
          setBindings(rows);
          setActiveChannel(channel);
          setBindingPage(1);
          setBindingsLoaded(true);
        },
        (cause) => report('bindings', cause),
      );
    },
    [clearError, invalidResponse, report, run],
  );

  useEffect(() => {
    void loadRevisions();
    void loadBindings();
  }, [loadRevisions, loadBindings]);

  const loadDetail = (id) => {
    clearError('detail');
    setDetailId(id);
    setDetail(null);
    void run(
      'detail',
      async (config) => {
        const revision = unwrap(await API.get(`${endpoint}/${id}`, config));
        if (
          !validRevision(revision) ||
          revision.id !== id ||
          typeof revision.source !== 'string'
        ) {
          invalidResponse();
        }
        return revision;
      },
      setDetail,
      (cause) => report('detail', cause),
    );
  };

  const mutate = (action, apply) => {
    if (!session.isCurrent() || marketBusy || pending.mutation) return;
    clearError('mutation');
    setNotice('');
    // Reads begun before a write cannot later restore stale state.
    cancel('list');
    cancel('bindings');
    cancel('detail');
    return run(
      'mutation',
      action,
      (saved) => {
        cancel('list');
        cancel('bindings');
        cancel('detail');
        apply(saved);
      },
      (cause) => report('mutation', cause),
    );
  };

  const create = (draft) => {
    setNotice('');
    if (!draft.trim() || byteLength(draft) > maxSourceBytes || byteLength(JSON.stringify({ source: draft, enabled: false })) > 2 << 20) {
      report('mutation', new Error(t('请求参数无效')));
      return;
    }
    void mutate(
      async (config) => {
        const saved = unwrap(
          await API.post(endpoint, { source: draft, enabled: false }, config),
        );
        if (!validRevision(saved) || saved.enabled !== false) invalidResponse();
        return saved;
      },
      (saved) => {
        setRevisionAction(null);
        setDetailId(null); setDetail(null);
        setNotice(`${t('创建成功')} · #${saved.id} · ${t('已禁用')}`);
        void loadRevisions();
        void loadBindings(activeChannel);
      },
    );
  };

  const setEnabled = (revision) => {
    const enabled = !revision.enabled;
    void mutate(
      async (config) => {
        unwrap(
          await API.put(
            `${endpoint}/${revision.id}/enabled`,
            { enabled },
            config,
          ),
        );
      },
      () => {
        setRevisionAction(null);
        setRevisions((rows) =>
          rows.map((row) =>
            row.id === revision.id ? { ...row, enabled } : row,
          ),
        );
        setDetailId(null); setDetail(null);
        setNotice(t('保存成功'));
        void loadBindings(activeChannel);
      },
    );
  };

  const bindingPayload = (unbind) => {
    const channel = Number(bindingDraft.channel);
    const revision = Number(bindingDraft.revision);
    const model = bindingDraft.model;
    if (
      !/^\d+$/.test(bindingDraft.channel) ||
      !positiveID(channel) ||
      !model ||
      model !== model.trim() ||
      /[\r\n\0]/.test(model) ||
      byteLength(model) > 255 ||
      (!unbind &&
        (!/^\d+$/.test(bindingDraft.revision) || !positiveID(revision)))
    ) {
      throw new Error(t('请求参数无效'));
    }
    return { channel_id: channel, model, plugin_id: unbind ? 0 : revision };
  };

  const saveBinding = (payload) => {
    void mutate(
      async (config) => {
        const saved = unwrap(
          await API.put(`${endpoint}/bindings`, payload, config),
        );
        if (
          !saved ||
          saved.channel_id !== payload.channel_id ||
          saved.model !== payload.model ||
          saved.plugin_id !== payload.plugin_id
        ) {
          invalidResponse();
        }
        return saved;
      },
      (saved) => {
        setBindings((rows) => {
          const remaining = rows.filter(
            (row) =>
              row.channel_id !== saved.channel_id || row.model !== saved.model,
          );
          return saved.plugin_id > 0 &&
            (activeChannel === 0 || activeChannel === saved.channel_id)
            ? [saved, ...remaining].slice(0, 1000)
            : remaining;
        });
        setBindingPage(1);
        setUnbindTarget(null);
        setNotice(t('保存成功'));
        void loadBindings(activeChannel);
        void loadRevisions(cursors);
      },
    );
  };

  const submitBinding = (unbind) => {
    clearError('mutation');
    setNotice('');
    try {
      const payload = bindingPayload(unbind);
      if (unbind) setUnbindTarget(payload);
      else saveBinding(payload);
    } catch (cause) {
      report('mutation', cause);
    }
  };

  const updateBindingDraft = (key, value) => {
    setBindingDraft((current) => ({ ...current, [key]: value }));
    clearError('mutation');
    setNotice('');
  };
  const filterBindings = () => {
    const channel = channelFilter === '' ? 0 : Number(channelFilter);
    if (
      channelFilter !== '' &&
      (!/^\d+$/.test(channelFilter) || !positiveID(channel))
    ) {
      report('bindings', new Error(t('请求参数无效')));
      return;
    }
    void loadBindings(channel);
  };
  const busy = Boolean(pending.mutation) || marketBusy;
  const alert = (slot) =>
    errors[slot] ? (
      <p role='alert' style={{ color: 'var(--semi-color-danger)' }}>
        {errors[slot]}
      </p>
    ) : null;
  const revisionColumns = [
    { title: t('ID'), dataIndex: 'id', width: 80 },
    {
      title: t('Name'),
      dataIndex: 'name',
      render: (name, row) => (
        <div className='break-words'>
          <div>{name}</div>
          <Typography.Text type='tertiary'>{row.key}</Typography.Text>
        </div>
      ),
    },
    { title: t('Version'), dataIndex: 'version', width: 120 },
    {
      title: t('状态'),
      dataIndex: 'enabled',
      width: 100,
      render: (enabled) => (
        <Tag color={enabled ? 'green' : 'grey'}>
          {enabled ? t('已启用') : t('已禁用')}
        </Tag>
      ),
    },
    {
      title: t('操作'),
      width: 250,
      render: (_, row) => (
        <div className='flex flex-wrap gap-1'>
          <Button
            size='small'
            disabled={busy}
            onClick={() => loadDetail(row.id)}
          >
            {t('详情')}
          </Button>
          <Button
            size='small'
            disabled={busy || pending.list}
            onClick={() => { clearError('mutation'); setRevisionAction({ kind: 'enabled', revision: row }); }}
          >
            {row.enabled ? t('禁用') : t('启用')}
          </Button>
          <Button
            size='small'
            disabled={busy || !row.enabled || pending.list}
            onClick={() => updateBindingDraft('revision', String(row.id))}
          >
            {t('绑定')}
          </Button>
        </div>
      ),
    },
  ];
  const bindingColumns = [
    { title: t('渠道 ID'), dataIndex: 'channel_id', width: 110 },
    {
      title: t('模型'),
      dataIndex: 'model',
      render: (model) => <span className='break-all'>{model}</span>,
    },
    {
      title: `${t('Immutable revisions')} · ${t('ID')}`,
      dataIndex: 'plugin_id',
      width: 150,
    },
    {
      title: t('操作'),
      width: 200,
      render: (_, row) => (
        <div className='flex flex-wrap gap-1'>
          <Button
            size='small'
            disabled={busy}
            onClick={() => {
              setBindingDraft({
                channel: String(row.channel_id),
                model: row.model,
                revision: String(row.plugin_id),
              });
              clearError('mutation');
              setNotice('');
            }}
          >
            {t('编辑')}
          </Button>
          <Button
            size='small'
            type='danger'
            disabled={busy || pending.bindings}
            onClick={() => {
              clearError('mutation');
              setNotice('');
              setUnbindTarget({
                channel_id: row.channel_id,
                model: row.model,
                plugin_id: 0,
              });
            }}
          >
            {t('确认解绑')}
          </Button>
        </div>
      ),
    },
  ];

  return (
    <section className='mt-4 space-y-4' aria-labelledby={`${fieldId}-title`}>
      <Typography.Title heading={5} id={`${fieldId}-title`}>
        JavaScript · {t('Immutable revisions')}
      </Typography.Title>
      {notice && <p role='status'>{notice}</p>}
      {!unbindTarget && !revisionAction && alert('mutation')}
      <ScriptPluginMarketplace
        session={session}
        disabled={Boolean(pending.mutation) || revisionAction !== null || unbindTarget !== null}
        onBusyChange={setMarketBusy}
        onInstalled={(id) => { cancel('list'); cancel('detail'); void loadRevisions(); loadDetail(id); }}
      />
      <Card>
        <div className='mb-3 flex flex-wrap items-center justify-between gap-2'>
          <Typography.Text strong>{t('Immutable revisions')}</Typography.Text>
          <Button
            loading={pending.list}
            disabled={busy || pending.list}
            onClick={() => void loadRevisions()}
          >
            {t('刷新')}
          </Button>
        </div>
        {alert('list')}
        <Table
          rowKey='id'
          size='small'
          columns={revisionColumns}
          dataSource={revisions}
          pagination={false}
          loading={pending.list}
          empty={
            pending.list
              ? t('加载中...')
              : listLoaded
                ? t('No immutable revisions')
                : t('加载失败')
          }
          scroll={{ x: 720 }}
        />
        <div className='mt-3 flex items-center justify-end gap-2'>
          <Button
            disabled={busy || pending.list || cursors.length === 1}
            onClick={() => void loadRevisions(cursors.slice(0, -1))}
          >
            {t('上一页')}
          </Button>
          <span aria-live='polite'>{cursors.length}</span>
          <Button
            disabled={busy || pending.list || !hasNext}
            onClick={() =>
              void loadRevisions([
                ...cursors,
                revisions[revisions.length - 1].id,
              ])
            }
          >
            {t('下一页')}
          </Button>
        </div>
        {detailId !== null && (
          <div className='mt-4 space-y-3 border-t pt-4'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <Typography.Text strong>
                {t('详情')} · #{detailId}
              </Typography.Text>
              <div className='flex gap-2'>
                <Button
                  loading={pending.detail}
                  disabled={busy || pending.detail}
                  onClick={() => loadDetail(detailId)}
                >
                  {t('刷新')}
                </Button>
                <Button
                  disabled={busy}
                  onClick={() => {
                    cancel('detail');
                    setDetailId(null);
                    setDetail(null);
                    clearError('detail');
                  }}
                >
                  {t('取消')}
                </Button>
              </div>
            </div>
            {alert('detail')}
            {pending.detail && <p role='status'>{t('加载中...')}</p>}
            {detail && (
              <>
                <Typography.Text>{revisionLabel(detail)}</Typography.Text>
                <ScriptPluginCapabilities meta={detail.meta} />
                <ScriptPluginLifecycle
                  key={`${detail.id}:${detail.digest}`}
                  plugin={detail}
                  session={session}
                  busy={busy}
                  error={errors.mutation || ''}
                  onChange={(request, done) => {
                    void mutate(request, () => {
                      done(); setDetailId(null); setDetail(null); setNotice(t('插件操作成功'));
                      void loadRevisions(); void loadBindings(activeChannel);
                    });
                  }}
                />
                <pre style={preStyle}>{detail.source}</pre>
                <details>
                  <summary className='cursor-pointer'>{t('详情')}</summary>
                  <pre style={preStyle}>
                    {JSON.stringify({ ...detail, source: undefined }, null, 2)}
                  </pre>
                </details>
                <Popconfirm
                  title={t('Copy to new revision draft')}
                  okText={t('确认')}
                  cancelText={t('取消')}
                  onConfirm={() => {
                    if (!session.isCurrent() || busy) return;
                    setSource(detail.source);
                    clearError('mutation');
                    setNotice('');
                  }}
                >
                  <Button disabled={busy}>
                    {t('Copy to new revision draft')}
                  </Button>
                </Popconfirm>
              </>
            )}
          </div>
        )}
      </Card>
      <Card>
        <div className='mb-3 flex flex-wrap items-center justify-between gap-2'>
          <Typography.Text strong>
            {t('Create immutable revision')}
          </Typography.Text>
          <Tag>
            {t('默认')} · {t('已禁用')}
          </Tag>
        </div>
        <label htmlFor={`${fieldId}-source`}>JavaScript · {t('内容')}</label>
        <TextArea
          id={`${fieldId}-source`}
          rows={12}
          style={sourceStyle}
          spellCheck={false}
          value={source}
          disabled={busy}
          onChange={(value) => {
            setSource(value);
            clearError('mutation');
            setNotice('');
          }}
        />
        <div className='mt-3 flex flex-wrap items-center justify-between gap-2'>
          <Typography.Text type='tertiary'>
            {t('单条内容最大长度')} · 1 MiB
          </Typography.Text>
          <Button
            theme='solid'
            loading={busy}
            disabled={busy || !source.trim()}
            onClick={() => {
              clearError('mutation');
              if (!source.trim() || byteLength(source) > maxSourceBytes || byteLength(JSON.stringify({ source, enabled: false })) > 2 << 20) {
                report('mutation', new Error(t('请求参数无效'))); return;
              }
              setRevisionAction({ kind: 'create', source });
            }}
          >
            {t('Create immutable revision')}
          </Button>
        </div>
      </Card>
      <Card>
        <Typography.Title heading={6}>{t('管理绑定')}</Typography.Title>
        <div className='my-3 grid gap-3 sm:grid-cols-3'>
          <div>
            <label htmlFor={`${fieldId}-channel`}>{t('渠道 ID')}</label>
            <Input
              id={`${fieldId}-channel`}
              inputMode='numeric'
              value={bindingDraft.channel}
              disabled={busy}
              onChange={(value) => updateBindingDraft('channel', value)}
            />
          </div>
          <div>
            <label htmlFor={`${fieldId}-model`}>{t('模型')}</label>
            <Input
              id={`${fieldId}-model`}
              value={bindingDraft.model}
              disabled={busy}
              placeholder={t('请输入模型名称')}
              onChange={(value) => updateBindingDraft('model', value)}
            />
          </div>
          <div>
            <label htmlFor={`${fieldId}-revision`}>
              {t('Immutable revisions')} · {t('ID')}
            </label>
            <Input
              id={`${fieldId}-revision`}
              inputMode='numeric'
              value={bindingDraft.revision}
              disabled={busy}
              onChange={(value) => updateBindingDraft('revision', value)}
            />
          </div>
        </div>
        <div className='mb-5 flex flex-wrap gap-2'>
          <Button
            theme='solid'
            loading={busy}
            disabled={busy}
            onClick={() => submitBinding(false)}
          >
            {t('绑定')}
          </Button>
          <Button
            type='danger'
            disabled={busy}
            onClick={() => submitBinding(true)}
          >
            {t('确认解绑')}
          </Button>
        </div>
        <div className='mb-3 flex flex-wrap items-end gap-2'>
          <div>
            <label htmlFor={`${fieldId}-filter`}>
              {t('筛选')} · {t('渠道 ID')}
            </label>
            <Input
              id={`${fieldId}-filter`}
              inputMode='numeric'
              value={channelFilter}
              disabled={busy}
              onChange={setChannelFilter}
              onEnterPress={filterBindings}
            />
          </div>
          <Button
            loading={pending.bindings}
            disabled={busy || pending.bindings}
            onClick={filterBindings}
          >
            {t('筛选')}
          </Button>
          <Button
            disabled={busy || pending.bindings}
            onClick={() => {
              setChannelFilter('');
              void loadBindings();
            }}
          >
            {t('重置')}
          </Button>
          <Button
            disabled={busy || pending.bindings}
            onClick={() => void loadBindings(activeChannel)}
          >
            {t('刷新')}
          </Button>
        </div>
        {alert('bindings')}
        <Typography.Paragraph type='tertiary'>
          {t('最多显示最近 {{limit}} 条记录', { limit: 1000 })}
          {activeChannel > 0 && ` · ${t('渠道 ID')}: ${activeChannel}`}
        </Typography.Paragraph>
        <Table
          rowKey={(row) => JSON.stringify([row.channel_id, row.model])}
          size='small'
          columns={bindingColumns}
          dataSource={bindings.slice(
            (bindingPage - 1) * bindingPageSize,
            bindingPage * bindingPageSize,
          )}
          pagination={false}
          loading={pending.bindings}
          empty={
            pending.bindings
              ? t('加载中...')
              : bindingsLoaded
                ? t('暂无数据')
                : t('加载失败')
          }
          scroll={{ x: 650 }}
        />
        <div className='mt-3 flex items-center justify-end gap-2'>
          <Button
            disabled={busy || pending.bindings || bindingPage === 1}
            onClick={() => setBindingPage((page) => page - 1)}
          >
            {t('上一页')}
          </Button>
          <span aria-live='polite'>{bindingPage}</span>
          <Button
            disabled={
              busy ||
              pending.bindings ||
              bindingPage * bindingPageSize >= bindings.length
            }
            onClick={() => setBindingPage((page) => page + 1)}
          >
            {t('下一页')}
          </Button>
        </div>
      </Card>
      <Modal
        visible={revisionAction !== null}
        title={revisionAction?.kind === 'create' ? t('创建插件修订') : revisionAction?.revision.enabled ? t('确认禁用插件版本') : t('确认启用插件版本')}
        okText={t('确认')} cancelText={t('取消')} confirmLoading={busy}
        okButtonProps={{ type: revisionAction?.kind === 'enabled' && revisionAction.revision.enabled ? 'danger' : 'primary', disabled: busy }}
        cancelButtonProps={{ disabled: busy }} maskClosable={!busy} closable={!busy} closeOnEsc={!busy}
        onCancel={() => { if (!busy) setRevisionAction(null); }}
        onOk={() => {
          if (!revisionAction || busy || !session.isCurrent()) return;
          if (revisionAction.kind === 'create') create(revisionAction.source);
          else setEnabled(revisionAction.revision);
        }}
      >
        {revisionAction?.kind === 'create' ? <>
          <p>{t('新修订默认禁用；请审阅源码和能力后再启用。')}</p>
          <pre style={preStyle}>{revisionAction.source}</pre>
        </> : revisionAction && <>
          <p className='break-all'>{revisionLabel(revisionAction.revision)}</p>
          <p>{t('启停影响此修订的新任务使用，不会迁移已有绑定或历史任务。')}</p>
        </>}
        {alert('mutation')}
      </Modal>
      <Modal
        visible={unbindTarget !== null}
        title={t('确认解绑')}
        okText={t('确认解绑')}
        cancelText={t('取消')}
        confirmLoading={busy}
        okButtonProps={{ type: 'danger', disabled: busy }}
        cancelButtonProps={{ disabled: busy }}
        maskClosable={!busy}
        closable={!busy}
        closeOnEsc={!busy}
        onCancel={() => {
          if (!busy) setUnbindTarget(null);
        }}
        onOk={() => {
          if (!busy && unbindTarget) saveBinding(unbindTarget);
        }}
      >
        <p>
          {t('渠道 ID')}: {unbindTarget?.channel_id}
        </p>
        <p className='break-all'>
          {t('模型')}: {unbindTarget?.model}
        </p>
        {alert('mutation')}
      </Modal>
    </section>
  );
}

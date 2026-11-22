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

import React, { useCallback, useContext, useEffect, useId, useState } from 'react';
import { Button, Input, TextArea, Typography } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { UserContext } from '../../../context/User';
import { API } from '../../../helpers';
import { getServerErrorMessage } from '../../../helpers/server-error-message';
import SettingsScriptPlugins from './SettingsScriptPlugins';
import { useAccountSession } from '../../../hooks/playground/accountSession';
import useScriptPluginRequests from './useScriptPluginRequests';
const format = (value) => JSON.stringify(value, null, 2);
const blankDraft = { name: '', version: '', schema: '', expression: '' };
const preStyle = { maxHeight: 260, overflow: 'auto', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: 12 };

function unwrap(body) {
  if (body?.success !== true || body.data === undefined) throw body;
  return body.data;
}
function parseObject(value) {
  const parsed = JSON.parse(value);
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('Expected a JSON object');
  }
  return parsed;
}

const isRecord = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
const validDraft = (value) => isRecord(value) &&
  ['name', 'version', 'schema', 'expression'].every((key) => typeof value[key] === 'string');
const validPlugin = (value) => validDraft(value) && Number.isSafeInteger(value.id) && value.id > 0;
const validBindings = (value) => isRecord(value) && Object.entries(value).every(([model, id]) =>
  model.trim() !== '' && model === model.trim() && !/[\r\n\t\0]/.test(model) &&
  typeof id === 'number' && Number.isSafeInteger(id) && id >= 0);

function readPlugins(body, before = Infinity) {
  const value = unwrap(body) ?? [];
  if (!Array.isArray(value) || value.length > 100 || value.some((item, index) =>
    !validPlugin(item) || item.id >= (index ? value[index - 1].id : before))) throw null;
  return value;
}
function readBindings(body) {
  const value = unwrap(body);
  if (!isRecord(value) || !Object.hasOwn(value, 'bindings')) throw null;
  const mapping = value.bindings ?? {};
  if (!validBindings(mapping)) throw null;
  return mapping;
}
function readContract(body) {
  const value = unwrap(body);
  if (!isRecord(value) || value.kind !== 'declarative_usage_billing' || value.api_version !== 1 ||
    !validDraft(value.example_create) || !isRecord(value.example_response) ||
    !Number.isFinite(value.example_group_ratio) || value.example_group_ratio < 0 ||
    !isRecord(value.example_other_ratios) || Object.values(value.example_other_ratios).some((ratio) =>
      typeof ratio !== 'number' || !Number.isFinite(ratio) || ratio < 0) ||
    !isRecord(value.capabilities) || typeof value.capabilities.preview !== 'boolean' ||
    typeof value.capabilities.usage_billing !== 'boolean' || !isRecord(value.limits) ||
    !Array.isArray(value.expression_operators) || value.expression_operators.some((item) => typeof item !== 'string')) throw null;
  return value;
}

export default function SettingsTaskUsagePlugins() {
  const { t } = useTranslation();
  const [userState] = useContext(UserContext);
  const session = useAccountSession();
  if (userState.user?.role !== 100 || !session.userId || String(userState.user.id) !== session.userId) {
    return <p>{t('Root access is required to manage task usage plugins')}</p>;
  }
  return <TaskUsagePluginsContent key={session.revision} session={session} />;
}

function TaskUsagePluginsContent({ session }) {
  const { t } = useTranslation();
  const fieldId = useId();
  const [contract, setContract] = useState(null);
  const [plugins, setPlugins] = useState([]);
  const [more, setMore] = useState(false);
  const [draft, setDraft] = useState(blankDraft);
  const [response, setResponse] = useState('{}');
  const [groupRatio, setGroupRatio] = useState('1');
  const [otherRatios, setOtherRatios] = useState('{}');
  const [bindings, setBindings] = useState(null);
  const [preview, setPreview] = useState(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const { pending, run: request, cancel } = useScriptPluginRequests(session);
  const busy = Boolean(pending.load || pending.mutation);

  const load = useCallback(() => {
    if (!session.isCurrent()) return;
    setError('');
    setNotice('');
    return request('load', async (config) => {
      const [schemaResult, listResult, bindingsResult] = await Promise.all([
        API.get('/api/task/plugin/schema', config),
        API.get('/api/task/plugin', config),
        API.get('/api/task/plugin/bindings', config),
      ]);
      return { schema: readContract(schemaResult.data), list: readPlugins(listResult.data), saved: readBindings(bindingsResult.data) };
    }, ({ schema, list, saved }) => {
      setContract(schema);
      setPlugins(list);
      setMore(list.length === 100);
      setBindings((current) => current ?? format(saved));
    }, (cause) => setError(getServerErrorMessage(cause, t('Failed to load task plugins'))));
  }, [request, session, t]);

  useEffect(() => { void load(); }, [load]);

  const run = (action, apply) => {
    if (!session.isCurrent() || busy || !contract) return;
    cancel('load');
    setError('');
    setNotice('');
    setPreview(null);
    return request('mutation', action, apply,
      (cause) => setError(getServerErrorMessage(cause, t('Task plugin request failed; draft retained'))));
  };
  const updateDraft = (key, value) => {
    setDraft((current) => ({ ...current, [key]: value }));
    setPreview(null);
    setNotice('');
  };
  const validatedDraft = () => {
    if (!draft.name.trim() || !draft.version.trim() || !draft.expression.trim()) {
      throw new Error(t('Plugin name, version and expression are required'));
    }
    parseObject(draft.schema);
    return { ...draft, name: draft.name.trim(), version: draft.version.trim() };
  };
  const previewDraft = () => run(async (config) => {
    const ratios = parseObject(otherRatios);
    const ratio = Number(groupRatio);
    if (!groupRatio.trim() || !Number.isFinite(ratio) || ratio < 0 ||
        Object.values(ratios).some((value) => typeof value !== 'number' || !Number.isFinite(value) || value < 0)) {
      throw new Error(t('Multipliers must be finite non-negative numbers'));
    }
    const result = await API.post('/api/task/plugin/preview', {
      plugin: validatedDraft(), response: parseObject(response),
      group_ratio: ratio, other_ratios: ratios,
    }, config);
    const value = unwrap(result.data);
    if (!value || value.dry_run !== true || value.currency !== 'USD' ||
      !Number.isFinite(value.quota) || value.quota < 0 ||
      !Number.isFinite(value.quota_per_unit) || value.quota_per_unit <= 0) {
      throw new Error(t('Invalid preview response'));
    }
    return value;
  }, setPreview);
  const create = () => run(async (config) => {
    const result = await API.post('/api/task/plugin', validatedDraft(), config);
    const saved = unwrap(result.data);
    if (!validPlugin(saved)) throw null;
    return saved;
  }, (saved) => {
    setPlugins((current) => [saved, ...current.filter((item) => item.id !== saved.id)]);
    setPreview(null);
    setNotice(t('Created immutable revision #{{id}}. Bind a model to enable it.', { id: saved.id }));
  });
  const saveBindings = () => run(async (config) => {
    const mapping = parseObject(bindings ?? '{}');
    if (!validBindings(mapping)) {
      throw new Error(t('Bindings require exact model names and non-negative integer revision IDs'));
    }
    const result = await API.put('/api/task/plugin/bindings', { bindings: mapping }, config);
    return readBindings(result.data);
  }, (saved) => {
    setBindings(format(saved));
    setNotice(t('Model bindings saved'));
  });
  const loadMore = () => run(async (config) => {
    const before = plugins[plugins.length - 1]?.id;
    if (!before) throw null;
    const result = await API.get('/api/task/plugin', { ...config, params: { before_id: before } });
    return readPlugins(result.data, before);
  }, (list) => {
    setPlugins((current) => [...current, ...list.filter((item) => !current.some((old) => old.id === item.id))]);
    setMore(list.length === 100);
  });

  const disabled = busy || !contract;
  return (
    <section className='flex flex-col gap-4'>
      <Typography.Title heading={5}>{t('Task usage plugins')}</Typography.Title>
      <Typography.Paragraph type='tertiary'>
        {t('Immutable declarative usage expressions in USD. Invalid usage keeps the reserved quota.')}
      </Typography.Paragraph>
      <div><Button disabled={busy} onClick={() => void load()}>{t('Refresh')}</Button></div>
      {error && <p role='alert' style={{ color: 'var(--semi-color-danger)' }}>{error}</p>}
      {notice && <p role='status'>{notice}</p>}
      {busy && <p role='status'>{t('Loading...')}</p>}
      <details>
        <summary className='cursor-pointer'>{t('Usage schema and expression contract')}</summary>
        <pre style={preStyle}>{format({ usage_schema: contract?.usage_schema, expression_operators: contract?.expression_operators, limits: contract?.limits })}</pre>
      </details>
      <div className='flex flex-wrap items-center gap-2'>
        <Typography.Text strong>{t('Create immutable revision')}</Typography.Text>
        <Button disabled={disabled} onClick={() => {
          const example = contract.example_create;
          setDraft({ name: example.name, version: example.version, schema: example.schema, expression: example.expression });
          setResponse(format(contract.example_response));
          setGroupRatio(String(contract.example_group_ratio));
          setOtherRatios(format(contract.example_other_ratios));
          setPreview(null);
          setNotice('');
        }}>{t('Load example into draft')}</Button>
      </div>
      <div className='grid gap-4 sm:grid-cols-2'>
        <div><label htmlFor={`${fieldId}-name`}>{t('Name')}</label>
          <Input id={`${fieldId}-name`} disabled={disabled} value={draft.name} onChange={(value) => updateDraft('name', value)} /></div>
        <div><label htmlFor={`${fieldId}-version`}>{t('Version')}</label>
          <Input id={`${fieldId}-version`} disabled={disabled} value={draft.version} onChange={(value) => updateDraft('version', value)} /></div>
      </div>
      <div><label htmlFor={`${fieldId}-schema`}>{t('Usage schema (JSON)')}</label>
        <TextArea id={`${fieldId}-schema`} disabled={disabled} rows={6} value={draft.schema} onChange={(value) => updateDraft('schema', value)} /></div>
      <div><label htmlFor={`${fieldId}-expression`}>{t('Usage expression (USD)')}</label>
        <TextArea id={`${fieldId}-expression`} disabled={disabled} value={draft.expression} onChange={(value) => updateDraft('expression', value)} /></div>
      <div><Button theme='solid' disabled={disabled || !contract?.capabilities.usage_billing} onClick={() => void create()}>{t('Create immutable revision')}</Button></div>
      <Typography.Text strong>{t('Preview draft usage')}</Typography.Text>
      <Typography.Paragraph type='tertiary'>{t('Preview only: no task dispatch or balance changes. Quota is calculated by the server.')}</Typography.Paragraph>
      <div><label htmlFor={`${fieldId}-response`}>{t('Provider response (JSON)')}</label>
        <TextArea id={`${fieldId}-response`} disabled={disabled} rows={4} value={response} onChange={(value) => { setResponse(value); setPreview(null); }} /></div>
      <div><label htmlFor={`${fieldId}-group`}>{t('Group multiplier')}</label>
        <Input id={`${fieldId}-group`} disabled={disabled} value={groupRatio} onChange={(value) => { setGroupRatio(value); setPreview(null); }} /></div>
      <div><label htmlFor={`${fieldId}-ratios`}>{t('Other multipliers (JSON)')}</label>
        <TextArea id={`${fieldId}-ratios`} disabled={disabled} value={otherRatios} onChange={(value) => { setOtherRatios(value); setPreview(null); }} /></div>
      <div><Button disabled={disabled || !contract?.capabilities.preview} onClick={() => void previewDraft()}>{t('Preview draft usage')}</Button></div>
      {preview && <div role='status'>
        {t('Server quota')}: <code>{String(preview.quota)}</code><br />
        {t('Quota per USD')}: <code>{String(preview.quota_per_unit)}</code> ({preview.currency})
      </div>}
      <Typography.Text strong>{t('Immutable revisions')}</Typography.Text>
      <div className='max-h-72 space-y-2 overflow-auto'>
        {plugins.length === 0 && <Typography.Text type='tertiary'>{t('No immutable revisions')}</Typography.Text>}
        {plugins.map((plugin) => <details key={plugin.id}>
          <summary className='cursor-pointer break-words'>#{plugin.id} · {plugin.name} · {plugin.version}</summary>
          <pre style={preStyle}>{format(plugin)}</pre>
          <Button disabled={disabled} onClick={() => {
            setDraft({ name: plugin.name, version: '', schema: plugin.schema, expression: plugin.expression });
            setPreview(null);
            setNotice('');
          }}>{t('Copy to new revision draft')}</Button>
        </details>)}
      </div>
      {more && <div><Button disabled={disabled} onClick={() => void loadMore()}>{t('Load more')}</Button></div>}
      <div><label htmlFor={`${fieldId}-bindings`}>{t('Exact model bindings (JSON)')}</label>
        <Typography.Paragraph type='tertiary'>{t('Saving replaces the entire mapping. Use exact model names and revision IDs; remove a key or use 0 to unbind. Existing task snapshots stay unchanged.')}</Typography.Paragraph>
        <TextArea id={`${fieldId}-bindings`} disabled={disabled} rows={6} value={bindings ?? ''} placeholder={'{"model-name": 1}'} onChange={(value) => { setBindings(value); setNotice(''); }} /></div>
      <div><Button theme='solid' disabled={disabled || bindings === null} onClick={() => void saveBindings()}>{t('Save model bindings')}</Button></div>
      <SettingsScriptPlugins />
    </section>
  );
}

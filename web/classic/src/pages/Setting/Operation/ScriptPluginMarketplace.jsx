import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { Button, Card, Input, Modal } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { getServerErrorMessage } from '../../../helpers/server-error-message';
import useScriptPluginRequests from './useScriptPluginRequests';
import { listMarketSources, loadMarketIndex, installMarketRevision, saveMarketSources, validateSources } from './script-plugin-management-api';

const preStyle = { maxHeight: 260, overflow: 'auto', fontSize: 12, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' };
export default function ScriptPluginMarketplace({ session, disabled, onInstalled, onBusyChange }) {
  const { t } = useTranslation();
  const id = useId();
  const dirty = useRef(false);
  const [sources, setSources] = useState([]);
  const [draft, setDraft] = useState([]);
  const [loaded, setLoaded] = useState(false);
  const [selected, setSelected] = useState('');
  const [index, setIndex] = useState(null);
  const [page, setPage] = useState(0);
  const [pendingSave, setPendingSave] = useState(null);
  const [pendingInstall, setPendingInstall] = useState(null);
  const [validation, setValidation] = useState('');
  const [notice, setNotice] = useState('');
  const [errors, setErrors] = useState({});
  const { pending, run, cancel } = useScriptPluginRequests(session);
  useEffect(() => { onBusyChange(Boolean(pending.mutation)); return () => onBusyChange(false); }, [pending.mutation, onBusyChange]);
  const locked = disabled || Boolean(pending.mutation);
  const report = useCallback((slot, cause) => setErrors((current) => ({
    ...current, [slot]: getServerErrorMessage(cause, t('插件操作失败，请检查后重试')),
  })), [t]);
  const clear = useCallback((slot) => setErrors((current) => ({ ...current, [slot]: '' })), []);
  const loadSources = useCallback(() => {
    clear('sources');
    void run('sources', (config) => listMarketSources(config, t), (rows) => {
      setSources(rows);
      if (!dirty.current) setDraft(rows);
      setLoaded(true);
      setSelected((current) => rows.some((row) => row.id === current && row.enabled) ? current : rows.find((row) => row.enabled)?.id ?? '');
    }, (cause) => report('sources', cause));
  }, [run, t, clear, report]);
  useEffect(() => { loadSources(); }, [loadSources]);
  const clearCatalog = () => {
    cancel('catalog'); clear('catalog');
    setIndex(null); setPage(0); setPendingInstall(null);
  };
  const updateDraft = (value) => { dirty.current = true; setDraft(value); setValidation(''); setNotice(''); };
  const optionStyle = { width: '100%', minHeight: 32, border: '1px solid var(--semi-color-border)', borderRadius: 6, background: 'var(--semi-color-bg-2)', color: 'var(--semi-color-text-0)' };
  return (
    <Card>
      <section className='min-w-0 space-y-3' aria-labelledby={`${id}-title`}>
        <h5 id={`${id}-title`} className='font-semibold'>{t('插件市场')}</h5>
        <p>{t('从可信市场选择固定版本和摘要，安装后先审阅能力与源码，再决定是否启用。新安装的修订默认禁用。')}</p>
        <details>
          <summary className='cursor-pointer text-sm font-medium'>{t('管理市场源')}</summary>
          <div className='mt-3 space-y-3'>
            {draft.map((row, position) => <div key={position} className='grid items-end gap-2 sm:grid-cols-2'>
              <div><label htmlFor={`${id}-key-${position}`}>{t('市场源标识')}</label><Input id={`${id}-key-${position}`} value={row.id} disabled={locked || pending.sources} autoComplete='off' onChange={(value) => updateDraft(draft.map((item, i) => i === position ? { ...item, id: value } : item))} /></div>
              <div><label htmlFor={`${id}-name-${position}`}>{t('市场源名称')}</label><Input id={`${id}-name-${position}`} value={row.name} disabled={locked || pending.sources} onChange={(value) => updateDraft(draft.map((item, i) => i === position ? { ...item, name: value } : item))} /></div>
              <div><label htmlFor={`${id}-url-${position}`}>{t('市场索引地址')}</label><Input id={`${id}-url-${position}`} value={row.url} disabled={locked || pending.sources} autoComplete='off' onChange={(value) => updateDraft(draft.map((item, i) => i === position ? { ...item, url: value } : item))} /></div>
              <label className='flex items-center gap-2'><input type='checkbox' checked={row.enabled} disabled={locked || pending.sources} onChange={(event) => updateDraft(draft.map((item, i) => i === position ? { ...item, enabled: event.target.checked } : item))} />{t('启用市场源')}</label>
              <Button disabled={locked || pending.sources} aria-label={`${t('移除市场源')} ${row.name || position + 1}`} onClick={() => updateDraft(draft.filter((_, i) => i !== position))}>{t('移除市场源')}</Button>
            </div>)}
            <div className='flex flex-wrap gap-2'>
              <Button disabled={locked || pending.sources || draft.length >= 32} onClick={() => updateDraft([...draft, { id: '', name: '', url: '', enabled: true }])}>{t('添加市场源')}</Button>
              <Button disabled={locked || pending.sources || !loaded} onClick={() => {
                setValidation(''); clear('mutation');
                try { setPendingSave(validateSources(draft, t)); }
                catch (cause) { setValidation(cause instanceof Error ? cause.message : t('市场源配置无效')); }
              }}>{t('保存市场源')}</Button>
            </div>
          </div>
        </details>
        <div className='flex flex-wrap items-end gap-2'>
          <div className='min-w-0 flex-1'><label htmlFor={`${id}-source`}>{t('选择市场源')}</label><select id={`${id}-source`} style={optionStyle} value={selected} disabled={locked || pending.sources} onChange={(event) => { clearCatalog(); setSelected(event.target.value); setNotice(''); }}>
            <option value=''>{t('请选择市场源')}</option>{sources.filter((row) => row.enabled).map((row) => <option key={row.id} value={row.id}>{row.name}</option>)}
          </select></div>
          <Button disabled={locked || pending.sources} onClick={() => { clearCatalog(); loadSources(); }}>{t('刷新市场源')}</Button>
          <Button disabled={locked || !selected || pending.catalog} onClick={() => {
            clearCatalog(); setNotice('');
            void run('catalog', (config) => loadMarketIndex(selected, config, t), setIndex, (cause) => report('catalog', cause));
          }}>{t('浏览市场')}</Button>
        </div>
        {loaded && sources.length === 0 && <p>{t('尚未配置市场源')}</p>}
        {(pending.sources || pending.catalog) && <p role='status'>{t('正在读取插件市场')}</p>}
        {[errors.sources, errors.catalog, validation].filter(Boolean).map((message, position) => <p key={position} role='alert' style={{ color: 'var(--semi-color-danger)' }}>{message}</p>)}
        {notice && <p role='status'>{notice}</p>}
        {index && <div className='space-y-3'>
          <h6 className='font-medium'>{index.source.name}</h6>
          {index.plugins.length === 0 && <p>{t('此市场暂无插件')}</p>}
          {index.plugins.slice(page * 20, (page + 1) * 20).map((entry) => <div key={JSON.stringify([entry.key, entry.version])} className='space-y-2 rounded-md border p-3'>
            <p className='break-all'>{entry.name} · {entry.key} · {entry.version}</p>
            {entry.description && <p className='text-sm break-words'>{entry.description}</p>}
            <p className='text-xs break-all'>{entry.source_url}<br />SHA-256: {entry.sha256}</p>
            <Button disabled={locked} onClick={() => { clear('mutation'); setPendingInstall({ source: index.source, entry }); }}>{t('安装插件版本')}</Button>
          </div>)}
          {index.plugins.length > 20 && <div className='flex items-center gap-3'>
            <Button disabled={page === 0} onClick={() => setPage((value) => value - 1)}>{t('上一页')}</Button><span>{page + 1} / {Math.ceil(index.plugins.length / 20)}</span><Button disabled={(page + 1) * 20 >= index.plugins.length} onClick={() => setPage((value) => value + 1)}>{t('下一页')}</Button>
          </div>}
        </div>}
        <Modal visible={pendingSave !== null} title={t('保存市场源')} okText={t('确认')} cancelText={t('取消')} confirmLoading={pending.mutation}
          okButtonProps={{ disabled: locked }} cancelButtonProps={{ disabled: pending.mutation }} maskClosable={!pending.mutation} closable={!pending.mutation} closeOnEsc={!pending.mutation}
          onCancel={() => { if (!pending.mutation) setPendingSave(null); }}
          onOk={() => {
            if (!pendingSave || locked) return;
            cancel('sources'); clearCatalog(); clear('mutation');
            const rows = pendingSave;
            void run('mutation', (config) => saveMarketSources(rows, config), () => {
              dirty.current = false; setSources(rows); setDraft(rows); setSelected(rows.find((row) => row.enabled)?.id ?? ''); setPendingSave(null); setNotice(t('市场源已保存'));
            }, (cause) => report('mutation', cause));
          }}>
          <p>{t('保存将替换当前市场源配置；移除市场源不会删除已经安装的插件。')}</p>
          <pre style={preStyle}>{JSON.stringify(pendingSave, null, 2)}</pre>
          {errors.mutation && <p role='alert' style={{ color: 'var(--semi-color-danger)' }}>{errors.mutation}</p>}
        </Modal>
        <Modal visible={pendingInstall !== null} title={t('安装插件版本')} okText={t('确认')} cancelText={t('取消')} confirmLoading={pending.mutation}
          okButtonProps={{ disabled: locked }} cancelButtonProps={{ disabled: pending.mutation }} maskClosable={!pending.mutation} closable={!pending.mutation} closeOnEsc={!pending.mutation}
          onCancel={() => { if (!pending.mutation) setPendingInstall(null); }} onOk={() => {
            if (!pendingInstall || locked || !session.isCurrent()) return;
            const target = pendingInstall;
            clear('mutation');
            void run('mutation', (config) => installMarketRevision(target.source.id, target.entry, config, t), (revisionId) => {
              setPendingInstall(null); setNotice(t('插件已安装并禁用，请审阅能力和源码')); onInstalled(revisionId);
            }, (cause) => report('mutation', cause));
          }}>
          <p>{t('将按选定版本与 SHA-256 摘要安装禁用修订。安装后请审阅完整能力和源码，启用与绑定需要单独确认。')}</p>
          {pendingInstall && <div className='space-y-2 break-all' style={{ maxHeight: 260, overflow: 'auto' }}>
            <p>{pendingInstall.source.name} · {pendingInstall.source.id}</p>
            <p>{pendingInstall.entry.key} · {pendingInstall.entry.version}</p>
            <p>{pendingInstall.entry.source_url}</p>
            <p>SHA-256: {pendingInstall.entry.sha256}</p>
          </div>}
          {errors.mutation && <p role='alert' style={{ color: 'var(--semi-color-danger)' }}>{errors.mutation}</p>}
        </Modal>
      </section>
    </Card>
  );
}

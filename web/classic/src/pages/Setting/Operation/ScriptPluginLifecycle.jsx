import React, { useCallback, useEffect, useId, useState } from 'react';
import { Button, Input, Modal, TextArea } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { getServerErrorMessage } from '../../../helpers/server-error-message';
import useScriptPluginRequests from './useScriptPluginRequests';
import { changeScriptRevision, listActivations, parseDryRun, runScriptRevision, scriptHooks } from './script-plugin-management-api';

const preStyle = { maxHeight: 320, overflow: 'auto', fontSize: 12, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' };
export default function ScriptPluginLifecycle({ plugin, session, busy, error, onChange }) {
  const { t } = useTranslation();
  const id = useId();
  const [action, setAction] = useState(null);
  const [hook, setHook] = useState('buildSubmitRequest');
  const [args, setArgs] = useState('[{}]');
  const [fixtures, setFixtures] = useState('[]');
  const [activeId, setActiveId] = useState(null);
  const [activeError, setActiveError] = useState('');
  const [output, setOutput] = useState('');
  const [validation, setValidation] = useState('');
  const [runError, setRunError] = useState('');
  const { pending, run } = useScriptPluginRequests(session);
  const loadActive = useCallback(() => {
    setActiveId(null); setActiveError('');
    void run('active', (config) => listActivations(config, t), (rows) => {
      setActiveId(rows.find((row) => row.plugin_key === plugin.key)?.plugin_id ?? 0);
    }, (cause) => setActiveError(getServerErrorMessage(cause, t('插件激活状态读取失败'))));
  }, [plugin.key, run, t]);
  useEffect(() => { loadActive(); }, [loadActive]);
  const locked = busy || Boolean(pending.mutation);
  const resetOutput = () => { setOutput(''); setValidation(''); setRunError(''); };
  const title = action?.kind === 'delete' ? t('删除插件版本') : action?.kind === 'activate' ? t('激活插件版本') : t('试运行插件');
  return (
    <div className='space-y-3 border-t pt-3'>
      <div className='flex flex-wrap gap-2'>
        <Button disabled={locked || pending.active || activeId === null || activeId === plugin.id || !plugin.enabled} onClick={() => { if (activeId !== null) setAction({ kind: 'activate', expected: activeId }); }}>{t('激活插件版本')}</Button>
        <Button type='danger' disabled={locked || pending.active || activeId === null || activeId === plugin.id || plugin.enabled} onClick={() => setAction({ kind: 'delete' })}>{t('删除插件版本')}</Button>
        <Button disabled={locked || pending.active} onClick={loadActive}>{t('刷新激活状态')}</Button>
      </div>
      <p>{pending.active ? t('正在读取激活状态') : activeId === null ? t('激活状态未知') : activeId === 0 ? t('此插件尚无激活版本') : `${t('当前激活版本')} #${activeId}`}</p>
      {activeError && <p role='alert' style={{ color: 'var(--semi-color-danger)' }}>{activeError}</p>}
      <p>{t('激活前须先启用版本；删除前须先禁用，并解除渠道绑定和激活引用。历史任务仍可使用原修订。')}</p>
      <details>
        <summary className='cursor-pointer text-sm font-medium'>{t('试运行插件')}</summary>
        <div className='mt-3 space-y-3'>
          <p>{t('试运行仅使用模拟网络响应，不会访问外部服务、创建任务或计费。请使用脱敏参数，不要填写真实密钥。')}</p>
          <label htmlFor={`${id}-hook`}>{t('插件函数')}</label>
          <Input id={`${id}-hook`} list={`${id}-hooks`} value={hook} disabled={locked} autoComplete='off' onChange={(value) => { setHook(value); resetOutput(); }} />
          <datalist id={`${id}-hooks`}>{scriptHooks.map((value) => <option key={value} value={value} />)}</datalist>
          <label htmlFor={`${id}-args`}>{t('试运行参数（JSON 数组）')}</label>
          <TextArea id={`${id}-args`} value={args} rows={7} spellCheck={false} autoComplete='off' style={{ fontFamily: 'monospace', fontSize: 12 }} disabled={locked} onChange={(value) => { setArgs(value); resetOutput(); }} />
          <label htmlFor={`${id}-fixtures`}>{t('模拟网络响应（JSON 数组）')}</label>
          <TextArea id={`${id}-fixtures`} value={fixtures} rows={7} spellCheck={false} autoComplete='off' style={{ fontFamily: 'monospace', fontSize: 12 }} disabled={locked} onChange={(value) => { setFixtures(value); resetOutput(); }} />
          <p>{t('每项填写 url、method、status、headers 和 body；按调用顺序匹配，缺少或多余的模拟响应都会失败。')}</p>
          <Button disabled={locked} onClick={() => {
            resetOutput();
            try { setAction({ kind: 'dryrun', payload: parseDryRun(hook, args, fixtures, t) }); }
            catch (cause) { setValidation(cause instanceof Error ? cause.message : t('试运行参数无效')); }
          }}>{t('试运行插件')}</Button>
          {validation && <p role='alert' style={{ color: 'var(--semi-color-danger)' }}>{validation}</p>}
          {output && <div role='status'><p>{t('试运行结果')}</p><pre style={preStyle}>{output}</pre></div>}
        </div>
      </details>
      <Modal visible={action !== null} title={title} okText={t('确认')} cancelText={t('取消')}
        confirmLoading={locked} okButtonProps={{ type: action?.kind === 'delete' ? 'danger' : 'primary', disabled: locked }}
        cancelButtonProps={{ disabled: locked }} maskClosable={!locked} closable={!locked} closeOnEsc={!locked}
        onCancel={() => { if (!locked) setAction(null); }}
        onOk={() => {
          if (!action || locked || !session.isCurrent()) return;
          if (action.kind === 'dryrun') {
            setRunError('');
            void run('mutation', (config) => runScriptRevision(plugin.id, action.payload, config),
              (result) => { setOutput(JSON.stringify(result ?? null, null, 2)); setAction(null); },
              (cause) => setRunError(getServerErrorMessage(cause, t('插件操作失败，请检查后重试'))));
          } else {
            const kind = action.kind;
            const expected = action.kind === 'activate' ? action.expected : 0;
            onChange((config) => changeScriptRevision(plugin.id, kind, expected, config), () => setAction(null));
          }
        }}>
        <div className='space-y-2 break-all'>
          <p>#{plugin.id} · {plugin.key} · {plugin.version}</p><p>SHA-256: {plugin.digest}</p>
          <p>{action?.kind === 'delete'
            ? t('删除后不能再用于新绑定；版本须已禁用且没有绑定或激活引用。历史任务需要的源码仍会保留。')
            : action?.kind === 'activate'
              ? t('激活将切换此插件的当前版本。已有绑定和历史任务仍固定到原修订，如需切换绑定请另行修改。')
              : t('试运行仅使用模拟网络响应，不会访问外部服务、创建任务或计费。请使用脱敏参数，不要填写真实密钥。')}</p>
        </div>
        {action?.kind === 'dryrun' && <pre style={preStyle}>{JSON.stringify(action.payload, null, 2)}</pre>}
        {(action?.kind === 'dryrun' ? runError : error) && <p role='alert' style={{ color: 'var(--semi-color-danger)' }}>{action?.kind === 'dryrun' ? runError : error}</p>}
      </Modal>
    </div>
  );
}

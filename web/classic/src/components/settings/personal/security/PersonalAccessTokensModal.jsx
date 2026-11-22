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

import React, { useCallback, useRef, useState } from 'react';
import { isAxiosError } from 'axios';
import { Button, Input, Modal, Typography } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { useSecurityVerification } from './useSecurityVerification';
import SecureVerificationModal from '../../../common/modals/SecureVerificationModal';
import {
  createPersonalAccessToken,
  getPersonalAccessTokens,
  revokePersonalAccessToken,
  updatePersonalAccessToken,
} from './api';
import { useSecurityResource } from './useSecurityResource';
import {
  currentSecurityOwner,
  personalTokenScopes,
  parsePersonalTokenScopes,
  SECURITY_STATUS_LABELS,
} from './securityManagement';

const TOKEN_ERROR_KEYS = {
  ACCESS_TOKEN_INVALID: '个人访问令牌参数无效，请检查名称和权限',
  ACCESS_TOKEN_NOT_ACTIVE: '个人访问令牌不存在或已失效，请刷新列表',
  ACCESS_TOKEN_LIMIT: '已达到个人访问令牌数量上限，请先撤销不再使用的令牌',
  ACCESS_TOKEN_ERROR: '个人访问令牌操作失败，请稍后重试',
};

export default function PersonalAccessTokensModal({ owner, role, onClose }) {
  const { t } = useTranslation();
  const [tokenErrorKey, setTokenErrorKey] = useState('');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const load = useCallback(
    (signal, user) => getPersonalAccessTokens(signal, user, page, pageSize),
    [page, pageSize],
  );
  const manager = useSecurityResource(load, owner);
  const verification = useSecurityVerification(owner);
  const [name, setName] = useState('');
  const [days, setDays] = useState('30');
  const [scopes, setScopes] = useState([]);
  const [target, setTarget] = useState(null);
  const [editing, setEditing] = useState(null);
  const [secret, setSecret] = useState('');
  const [pending, setPending] = useState(false);
  const locked = useRef(false);
  const busy = manager.busy || pending || verification.isModalVisible;
  const date = (value) => new Date(value * 1000).toLocaleString();
  const allowedScopes = [...personalTokenScopes(role), 'token:reveal'];
  const scopeOptions = [...new Set([...allowedScopes, ...scopes])];
  const pageMatches = manager.data?.page === page && manager.data?.page_size === pageSize;
  const ready = pageMatches && !manager.loading;
  const limitReached = !!manager.data && manager.data.active_count >= manager.data.limit;
  const pageCount = Math.max(1, Math.ceil((manager.data?.total ?? 0) / pageSize));
  // Keep UTF-8 byte validation compatible with older iOS without TextEncoder.
  const validDraft = !!name.trim() && new Blob([name.trim()]).size <= 100 &&
    scopes.length > 0 && scopes.length <= 32 && scopes.every((scope) => allowedScopes.includes(scope));
  const resetDraft = () => {
    setEditing(null);
    setName('');
    setScopes([]);
  };
  const startEdit = (token) => {
    setTarget(null);
    setEditing(token);
    setName(token.name);
    setScopes([...parsePersonalTokenScopes(token.scopes)]);
  };
  const changePageSize = (size) => {
    if (![10, 20, 50, 100].includes(size)) return;
    setPage(1);
    setPageSize(size);
  };
  const tokenAction = async (request, signal, user) => {
    try {
      return await request();
    } catch (cause) {
      if (!signal.aborted && currentSecurityOwner() === user && isAxiosError(cause)) {
        const key = TOKEN_ERROR_KEYS[cause.response?.data?.code];
        if (key) setTokenErrorKey(key);
      }
      // Preserve VERIFICATION_REQUIRED for the shared verification flow.
      throw cause;
    }
  };
  const authorize = async (action) => {
    if (locked.current) return;
    locked.current = true;
    setTokenErrorKey('');
    setPending(true);
    try {
      return await verification.withVerification(action);
    } finally {
      locked.current = false;
      setPending(false);
    }
  };
  const save = () => {
    if (busy || !ready || !validDraft || (!editing && limitReached)) return;
    // Detached values survive verification without modifying list records.
    const draft = { name: name.trim(), scopes: [...scopes] };
    if (editing) {
      const id = editing.id;
      return authorize(() =>
        manager.run(
          (signal, user) => tokenAction(() => updatePersonalAccessToken(id, draft, signal, user), signal, user),
          resetDraft,
        ),
      );
    }
    const data = { ...draft, expires_at: Math.floor(Date.now() / 1000) + Number(days) * 86400 };
    return authorize(() =>
      manager.run(
        (signal, user) => tokenAction(() => createPersonalAccessToken(data, signal, user), signal, user),
        (result) => {
          if (result?.token) setSecret(result.token);
          resetDraft();
        },
      ),
    );
  };
  const revoke = () => {
    if (!target) return;
    const id = target.id;
    return authorize(() =>
      manager.run(
        (signal, user) => tokenAction(() => revokePersonalAccessToken(id, signal, user), signal, user),
        () => {
          setTarget(null);
        },
      ),
    );
  };

  return (
    <>
      <Modal
        visible
        title={t('个人管理令牌')}
        footer={null}
        style={{ width: 'min(640px, calc(100vw - 32px))' }}
        bodyStyle={{ maxHeight: '75vh', overflowY: 'auto' }}
        onCancel={() => {
          if (!busy) onClose();
        }}
      >
        <div className='space-y-4'>
          <p>
            {t('管理控制台 API 的个人令牌，权限仍受账号角色和模块授权限制')}
          </p>
          {(verification.error || tokenErrorKey || manager.error) && (
            <p role='alert' className='text-red-600'>
              {verification.error || (tokenErrorKey ? t(tokenErrorKey) : manager.error)}
            </p>
          )}
          <Button
            disabled={manager.loading || busy}
            onClick={() => { setTokenErrorKey(''); void manager.refresh(); }}
          >
            {t('刷新')}
          </Button>
          {manager.data && <p className='text-xs text-gray-500'>
            {t('有效令牌 {{count}} / {{limit}}，共 {{total}} 条记录', { count: manager.data.active_count, limit: manager.data.limit, total: manager.data.total })}
          </p>}
          {limitReached && <p role='status' className='text-sm text-gray-500'>{t('已达到个人访问令牌数量上限，请先撤销不再使用的令牌')}</p>}
          {manager.loading && <p role='status'>{t('加载中...')}</p>}
          {ready && manager.data?.items.length === 0 && <p>{t('暂无个人管理令牌')}</p>}
          {(pageMatches ? manager.data?.items : [])?.map((token) => (
            <div
              key={token.id}
              className='space-y-2 rounded-lg border p-3 text-sm'
            >
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <strong className='break-all'>{token.name}</strong>
                <span>
                  {t(SECURITY_STATUS_LABELS[token.status] || '未知状态')}
                </span>
              </div>
              <p className='break-all font-mono'>{token.prefix}…</p>
              <p className='break-all'>
                {t('权限范围')}：
                {parsePersonalTokenScopes(token.scopes).join(', ')}
              </p>
              <p>
                {t('创建时间')}：{date(token.created_at)}
              </p>
              <p>
                {t('到期时间')}：{date(token.expires_at)}
              </p>
              <div className='flex flex-wrap gap-2'>
                <Button disabled={busy || !ready || !!editing || !!secret || token.status !== 'active'} onClick={() => startEdit(token)}>{t('编辑')}</Button>
                <Button type='danger' disabled={busy || !ready || !!editing || token.status !== 'active'} onClick={() => setTarget(token)}>{t('撤销')}</Button>
              </div>
            </div>
          ))}
          <div className='flex flex-wrap items-center gap-2 text-sm'>
            <label htmlFor='classic-pat-page-size'>{t('每页条数')}</label>
            <select id='classic-pat-page-size' value={pageSize} disabled={busy || manager.loading || !!editing || !!target} onChange={(event) => changePageSize(Number(event.target.value))} className='rounded-md border bg-transparent p-2'>
              {[10, 20, 50, 100].map((size) => <option key={size} value={size}>{size}</option>)}
            </select>
            <Button disabled={busy || manager.loading || !!editing || !!target || page <= 1} onClick={() => setPage(page - 1)}>{t('上一页')}</Button>
            <span>{t('第 {{page}} 页，共 {{pages}} 页', { page, pages: pageCount })}</span>
            <Button disabled={busy || manager.loading || !!editing || !!target || !ready || page >= pageCount} onClick={() => setPage(page + 1)}>{t('下一页')}</Button>
          </div>
          {target && (
            <div role='alert' className='space-y-2 rounded-lg border p-3'>
              <p>
                {t('确认撤销此令牌？使用它的程序将立即失去访问权限。')}{' '}
                <strong>{target.name}</strong>
              </p>
              <div className='flex gap-2'>
                <Button disabled={busy} onClick={() => setTarget(null)}>
                  {t('取消')}
                </Button>
                <Button
                  type='danger'
                  disabled={busy}
                  onClick={() => void revoke()}
                >
                  {t('确认撤销')}
                </Button>
              </div>
            </div>
          )}
          {secret ? (
            <div className='space-y-3 rounded-lg border p-3'>
              <p role='status'>
                {t(
                  '令牌仅在本次创建后显示一次，请立即复制并妥善保存。关闭后无法再次查看。',
                )}
              </p>
              <Typography.Paragraph
                copyable={{ content: secret }}
                className='break-all font-mono text-xs'
              >
                {secret}
              </Typography.Paragraph>
              <Button onClick={() => setSecret('')}>
                {t('我已保存，隐藏令牌')}
              </Button>
            </div>
          ) : (
            <form
              className='space-y-3 rounded-lg border p-3'
              onSubmit={(event) => {
                event.preventDefault();
                void save();
              }}
            >
              <h3 className='text-sm font-medium'>{editing ? t('编辑个人管理令牌') : t('创建令牌')}</h3>
              {editing && <p className='text-xs text-gray-500'>{t('仅修改名称和权限范围，不会重置令牌、延长有效期或显示密钥')}</p>}
              <label htmlFor='classic-pat-name'>{t('令牌名称')}</label>
              <Input
                id='classic-pat-name'
                value={name}
                maxLength={100}
                disabled={busy}
                onChange={setName}
              />
              <p className='text-xs text-gray-500'>{t('名称最多 100 个 UTF-8 字节')}</p>
              {!editing && <>
              <label htmlFor='classic-pat-expiry'>{t('有效期')}</label>
              <select
                id='classic-pat-expiry'
                value={days}
                disabled={busy}
                onChange={(event) => setDays(event.target.value)}
                className='w-full rounded-md border bg-transparent p-2'
              >
                {[7, 30, 90, 365].map((value) => (
                  <option key={value} value={value}>
                    {t('{{days}} 天', { days: value })}
                  </option>
                ))}
              </select>
              </>}
              <fieldset disabled={busy} className='space-y-2'>
                <legend className='text-sm font-medium'>{t('权限范围')}</legend>
                <p className='text-xs text-gray-500'>
                  {t('read 为读取，write 为修改；请仅选择所需权限')}
                </p>
                <p className='text-xs text-gray-500'>{t('token:reveal 用于查看 API 令牌密钥，read/write 不包含此权限；请谨慎授权')}</p>
                <div className='grid grid-cols-2 gap-2 text-xs sm:grid-cols-3'>
                  {scopeOptions.map((scope) => (
                    <label key={scope} className='flex items-center gap-2 break-all'>
                      <input
                        type='checkbox'
                        checked={scopes.includes(scope)}
                        disabled={!allowedScopes.includes(scope) && !scopes.includes(scope)}
                        onChange={(event) =>
                          setScopes((previous) =>
                            event.target.checked
                              ? [...previous, scope]
                              : previous.filter((value) => value !== scope),
                          )
                        }
                      />
                      {scope}
                    </label>
                  ))}
                </div>
                {scopes.some((scope) => !allowedScopes.includes(scope)) && <p role='alert' className='text-xs text-red-600'>{t('存在当前角色不支持的权限，请取消选择后保存')}</p>}
              </fieldset>
              <div className='flex flex-wrap gap-2'>
                <Button htmlType='submit' theme='solid' disabled={busy || !ready || !!target || !validDraft || (!editing && limitReached)}>{editing ? t('保存修改') : t('创建令牌')}</Button>
                {editing && <Button htmlType='button' disabled={busy} onClick={resetDraft}>{t('取消')}</Button>}
              </div>
            </form>
          )}
        </div>
      </Modal>
      <SecureVerificationModal
        description={verification.error || undefined}
        visible={verification.isModalVisible}
        verificationMethods={verification.verificationMethods}
        verificationState={verification.verificationState}
        onVerify={verification.executeVerification}
        onCancel={verification.cancelVerification}
        onCodeChange={verification.setVerificationCode}
        onMethodSwitch={verification.switchVerificationMethod}
      />
    </>
  );
}

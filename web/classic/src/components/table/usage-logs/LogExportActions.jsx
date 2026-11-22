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
import React, { useEffect, useRef, useState } from 'react';
import { Button, Space } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { showError, showSuccess, showWarning } from '../../../helpers';
import { downloadLogExport } from '../../../hooks/usage-logs/logExport';
import { logExportErrorMessage, useLogPolicy } from '../../../hooks/usage-logs/logPolicy';

export default function LogExportActions({ getFormValues, isAdminUser }) {
  const { t } = useTranslation();
  const { account, policy, loading, error, refresh } = useLogPolicy();
  const [exporting, setExporting] = useState(false);
  const request = useRef(null);
  useEffect(() => {
    setExporting(false);
    const cancel = () => { request.current?.abort(); request.current = null; };
    const stop = account.onInvalidate(cancel);
    return () => { stop(); cancel(); };
  }, [account, isAdminUser]);
  const allowed = account.isCurrent() && policy?.export_enabled === true && (isAdminUser || policy.user_export_enabled);

  const handleExport = async () => {
    if (request.current || !allowed || loading) return;
    const identity = () => {
      try {
        const user = JSON.parse(localStorage.getItem('user') || 'null');
        if (!account.isCurrent() || String(user?.id) !== account.userId || (Number(user?.role) >= 10) !== isAdminUser) return null;
        return `${user.id}:${user.role}`;
      } catch { return null; }
    };
    const owner = identity();
    if (!owner) { showError(t('登录状态已失效，请重新登录后重试。')); return; }
    const isCurrent = () => identity() === owner;
    const controller = new AbortController();
    request.current = controller;
    setExporting(true);
    try {
      const result = await downloadLogExport(
        getFormValues(),
        isAdminUser,
        controller.signal,
        isCurrent,
      );
      if (controller.signal.aborted || request.current !== controller || !isCurrent()) return;
      if (result.limited) {
        showWarning(
          t('导出已达到 {{count}} 条上限，请缩小筛选范围后继续导出。', {
            count: result.count,
          }),
        );
      } else if (result.complete === true) {
        showSuccess(t('已导出 {{count}} 条日志。', { count: result.count }));
      } else {
        showError(t('日志导出不完整，请重试或缩小筛选范围。'));
      }
    } catch (error) {
      if (!controller.signal.aborted && request.current === controller && isCurrent()) {
        showError(logExportErrorMessage(error));
        refresh();
      }
    } finally {
      if (request.current === controller) {
        request.current = null;
        setExporting(false);
      }
    }
  };

  return (
    <Space wrap aria-busy={exporting || loading}>
      <Button size='small' loading={exporting} disabled={exporting || loading || !allowed} onClick={handleExport}>
        {t('导出 NDJSON')}
      </Button>
      {exporting && <span role='status'>{t('正在导出日志…')}</span>}
      {loading && <span role='status'>{t('正在读取日志策略…')}</span>}
      {error && <span style={{ color: 'var(--semi-color-danger)' }} role='alert'>{t('读取日志策略失败，请重试。')}</span>}
      {policy && !allowed && <span role='status'>{t('管理员已关闭普通用户日志导出。')}</span>}
      {policy && <span className='text-xs'>{t('导出跨度最多 {{days}} 天，每次最多 {{count}} 条；每个账户同时仅允许一个导出任务。', { days: policy.max_range_days, count: policy.export_max_rows })}</span>}
      {!loading && !exporting && <Button size='small' theme='borderless' onClick={refresh}>{t('刷新日志策略')}</Button>}
    </Space>
  );
}

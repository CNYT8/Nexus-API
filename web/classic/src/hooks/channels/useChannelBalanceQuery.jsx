import React, { useCallback, useEffect, useRef } from 'react';
import { Modal } from '@douyinfe/semi-ui';
import { API, showError, showInfo } from '../../helpers';

export function useChannelBalanceQuery({ t, updateChannelProperty, updateChannelBalance }) {
  const pending = useRef(new Set());
  const alive = useRef(true);
  const dialogs = useRef(new Set());
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      dialogs.current.forEach((dialog) => dialog.destroy());
      dialogs.current.clear();
    };
  }, []);
  return useCallback(async (record) => {
    if (record?.type === 57) return updateChannelBalance(record);
    if (!record || pending.current.has(record.id)) return;
    pending.current.add(record.id);
    try {
      const { data } = await API.get(`/api/channel/update_balance/${record.id}/`, { skipErrorHandler: true });
      if (!alive.current) return;
      if (data?.success && typeof data.balance === 'number' && Number.isFinite(data.balance)) {
        updateChannelProperty(record.id, (channel) => {
          channel.balance = data.balance;
          channel.balance_updated_time = Date.now() / 1000;
        });
        showInfo(t('通道 ${name} 余额更新成功！').replace('${name}', record.name));
      } else if (data?.success && Object.prototype.hasOwnProperty.call(data, 'raw_response')) {
        const dialog = Modal.info({
          title: t('余额查询原始响应'),
          style: { maxWidth: 'calc(100vw - 32px)', width: 640 },
          content: <div className='space-y-3'>
            <p>{t('接口未返回可用余额，以下为原始响应；余额和更新时间未修改')}</p>
            <pre className='max-h-96 overflow-auto whitespace-pre-wrap break-all text-xs'>{JSON.stringify(data.raw_response, null, 2)}</pre>
          </div>,
          okText: t('关闭'),
          onOk: () => dialogs.current.delete(dialog),
          onCancel: () => dialogs.current.delete(dialog),
        });
        dialogs.current.add(dialog);
      } else {
        showError(data?.message || t('余额查询未返回有效余额或原始响应'));
      }
    } catch {
      if (alive.current) showError(t('余额查询失败，请稍后重试'));
    } finally {
      pending.current.delete(record.id);
    }
  }, [t, updateChannelProperty, updateChannelBalance]);
}

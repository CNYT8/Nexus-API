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

import React, { useContext, useState } from 'react';
import { Checkbox, Modal, Space } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { UserContext } from '../../../../context/User';

export default function DeleteModelsModal({ models, loading, onCancel, onConfirm }) {
  const { t } = useTranslation();
  const [userState] = useContext(UserContext);
  const [removeChannels, setRemoveChannels] = useState(false);
  const [removePricing, setRemovePricing] = useState(false);
  const exact = models.every((model) => (model.name_rule ?? 0) === 0);
  const root = userState.user?.role === 100;
  const admin = userState.user?.permissions?.sidebar_modules?.admin;
  const canRemoveChannels = exact && (root || (admin?.enabled === true && admin?.channel === true));
  const canRemovePricing = exact && root;

  return (
    <Modal
      title={t('批量删除模型')}
      visible
      confirmLoading={loading}
      cancelButtonProps={{ disabled: loading }}
      maskClosable={!loading}
      closable={!loading}
      onCancel={loading ? undefined : onCancel}
      onOk={() => onConfirm({
        remove_from_channels: canRemoveChannels && removeChannels,
        remove_pricing: canRemovePricing && removePricing,
      })}
      type='warning'
    >
      <Space vertical align='start' spacing='loose'>
        <div>{t('确定要删除所选的 {{count}} 个模型吗？', { count: models.length })}</div>
        <Checkbox checked={canRemoveChannels && removeChannels} disabled={!canRemoveChannels || loading} onChange={(event) => setRemoveChannels(event.target.checked)}>
          {t('同时删除渠道中的精确匹配模型')}
        </Checkbox>
        <Checkbox checked={canRemovePricing && removePricing} disabled={!canRemovePricing || loading} onChange={(event) => setRemovePricing(event.target.checked)}>
          {t('同时删除精确匹配的定价配置')}
        </Checkbox>
        <div className='text-xs text-gray-500'>{t('联动删除仅支持精确匹配模型，并需要相应权限')}</div>
      </Space>
    </Modal>
  );
}

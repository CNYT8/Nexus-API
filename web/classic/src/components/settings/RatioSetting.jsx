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
import { Banner, Button, Card, Spin, Tabs } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';

import ModelPricingCombined from '../../pages/Setting/Ratio/ModelPricingCombined';
import GroupRatioSettings from '../../pages/Setting/Ratio/GroupRatioSettings';
import ModelRatioNotSetEditor from '../../pages/Setting/Ratio/ModelRationNotSetEditor';
import UpstreamRatioSync from '../../pages/Setting/Ratio/UpstreamRatioSync';
import ToolPriceSettings from '../../pages/Setting/Ratio/ToolPriceSettings';

import { API, showError, toBoolean } from '../../helpers';

const RatioSetting = () => {
  const { t } = useTranslation();

  let [inputs, setInputs] = useState({
    ModelPrice: '',
    ModelRatio: '',
    CacheRatio: '',
    CreateCacheRatio: '',
    CompletionRatio: '',
    GroupRatio: '',
    GroupGroupRatio: '',
    ImageRatio: '',
    AudioRatio: '',
    AudioCompletionRatio: '',
    AutoGroups: '',
    DefaultUseAutoGroup: false,
    ExposeRatioEnabled: false,
    CompletionRatioOverrideEnabled: true,
    UserUsableGroups: '',
    'group_ratio_setting.group_special_usable_group': '',
    'group_ratio_setting.group_descriptions': '',
  });

  const [loading, setLoading] = useState(false);
  const [readFailed, setReadFailed] = useState(false);
  const readSequence = useRef(0);

  const getOptions = async (sequence) => {
    const res = await API.get('/api/option/', { disableDuplicate: true });
    if (sequence !== readSequence.current) return false;
    const { success, message, data } = res.data;
    if (success && Array.isArray(data)) {
      let newInputs = {};
      data.forEach((item) => {
        let value = item.value;
        if (typeof value === 'string' && (value.startsWith('{') || value.startsWith('['))) {
          try {
            value = JSON.stringify(JSON.parse(value), null, 2);
          } catch (e) {
            // 如果后端返回的不是合法 JSON，直接展示
          }
        }
        if (
          [
            'DefaultUseAutoGroup',
            'ExposeRatioEnabled',
            'CompletionRatioOverrideEnabled',
          ].includes(item.key)
        ) {
          newInputs[item.key] = toBoolean(value);
        } else {
          newInputs[item.key] = value;
        }
      });
      setInputs(newInputs);
      setReadFailed(false);
      return newInputs;
    } else {
      showError(message || t('刷新失败'));
      setReadFailed(true);
      return false;
    }
  };

  const onRefresh = async () => {
    const sequence = ++readSequence.current;
    try {
      setLoading(true);
      return await getOptions(sequence);
    } catch (error) {
      if (sequence === readSequence.current) {
        setReadFailed(true);
        showError(t('刷新失败'));
      }
      return false;
    } finally {
      if (sequence === readSequence.current) setLoading(false);
    }
  };

  useEffect(() => {
    onRefresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <Spin spinning={loading} size='large'>
      <Card style={{ marginTop: '10px' }}>
        {readFailed && (
          <Banner
            type='danger'
            closeIcon={null}
            description={t('刷新失败')}
            footer={<Button loading={loading} onClick={onRefresh}>{t('重试')}</Button>}
          />
        )}
        <Tabs type='card' defaultActiveKey='pricing'>
          <Tabs.TabPane tab={t('模型定价设置')} itemKey='pricing'>
            <ModelPricingCombined options={inputs} refresh={onRefresh} />
          </Tabs.TabPane>
          <Tabs.TabPane tab={t('分组相关设置')} itemKey='group'>
            <GroupRatioSettings options={inputs} refresh={onRefresh} />
          </Tabs.TabPane>
          <Tabs.TabPane tab={t('未设置价格模型')} itemKey='unset_models'>
            <ModelRatioNotSetEditor options={inputs} refresh={onRefresh} />
          </Tabs.TabPane>
          <Tabs.TabPane tab={t('上游价格同步')} itemKey='upstream_sync'>
            <UpstreamRatioSync options={inputs} refresh={onRefresh} />
          </Tabs.TabPane>
          <Tabs.TabPane tab={t('工具调用定价')} itemKey='tool_price'>
            <ToolPriceSettings options={inputs} refresh={onRefresh} />
          </Tabs.TabPane>
        </Tabs>
      </Card>
    </Spin>
  );
};

export default RatioSetting;

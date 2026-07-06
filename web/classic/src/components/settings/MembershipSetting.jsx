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

import React, { useContext, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Card,
  Col,
  Divider,
  Form,
  Input,
  InputNumber,
  Row,
  Select,
  Space,
  Spin,
  Switch,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { Crown, Plus, Trash2 } from 'lucide-react';
import {
  API,
  getCurrencyConfig,
  showError,
  showSuccess,
  toBoolean,
} from '../../helpers';
import { StatusContext } from '../../context/Status';

const { Text } = Typography;
const amountPrecision = 6;
const amountStep = 0.000001;

const createTierId = () => `tier_${Date.now().toString(36)}_${Math.random()
  .toString(36)
  .slice(2, 6)}`;

const createDefaultTier = (index) => ({
  id: createTierId(),
  name: `VIP ${index + 1}`,
  threshold_amount: 0,
  auto_upgrade_enabled: true,
  enabled: true,
  sort_order: index + 1,
  discount_all_groups: false,
  all_group_discount: 1,
  group_discounts: [],
});

const normalizeDiscount = (value) => {
  const numeric = Number(value);
  if (!Number.isFinite(numeric) || numeric <= 0 || numeric > 1) return 1;
  return Number(numeric.toFixed(4));
};

const getQuotaPerUnit = () => {
  const quotaPerUnit = Number(localStorage.getItem('quota_per_unit') || 1);
  return Number.isFinite(quotaPerUnit) && quotaPerUnit > 0 ? quotaPerUnit : 1;
};

const amountToDisplayValue = (amount) => {
  const numeric = Number(amount || 0);
  if (!Number.isFinite(numeric) || numeric === 0) return 0;
  const { type, rate } = getCurrencyConfig();
  if (type === 'TOKENS') return numeric * getQuotaPerUnit();
  if (type === 'USD') return numeric;
  return numeric * (rate || 1);
};

const displayValueToAmount = (value) => {
  const numeric = Number(value || 0);
  if (!Number.isFinite(numeric) || numeric === 0) return 0;
  const { type, rate } = getCurrencyConfig();
  if (type === 'TOKENS') return numeric / getQuotaPerUnit();
  if (type === 'USD') return numeric;
  return numeric / (rate || 1);
};

const normalizeTier = (tier, index) => ({
  id: String(tier.id || createTierId()).trim(),
  name: String(tier.name || '').trim(),
  threshold_amount: Math.max(0, Number(tier.threshold_amount || 0)),
  auto_upgrade_enabled: tier.auto_upgrade_enabled !== false,
  enabled: tier.enabled !== false,
  sort_order: index + 1,
  discount_all_groups: tier.discount_all_groups === true,
  all_group_discount: normalizeDiscount(tier.all_group_discount),
  group_discounts: (tier.group_discounts || [])
    .filter((item) => item?.group)
    .map((item) => ({
      group: String(item.group).trim(),
      discount: normalizeDiscount(item.discount),
    })),
});

const MembershipSetting = () => {
  const { t } = useTranslation();
  const [statusState, statusDispatch] = useContext(StatusContext);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [tiers, setTiers] = useState([]);
  const [groupOptions, setGroupOptions] = useState([]);

  const { symbol: currencySymbol, type: currencyType } = getCurrencyConfig();

  const sortedTiers = useMemo(
    () => [...tiers].sort((a, b) => (a.sort_order || 0) - (b.sort_order || 0)),
    [tiers],
  );

  const loadGroups = async () => {
    try {
      const res = await API.get('/api/group/');
      if (res.data.success) {
        setGroupOptions(
          (res.data.data || []).map((group) => ({
            label: group,
            value: group,
          })),
        );
      }
    } catch (error) {
      showError(t('分组加载失败'));
    }
  };

  const loadOptions = async () => {
    setLoading(true);
    try {
      const res = await API.get('/api/option/');
      const { success, message, data } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      let nextEnabled = false;
      let nextTiers = [];
      data.forEach((item) => {
        if (item.key === 'membership_setting.enabled') {
          nextEnabled = toBoolean(item.value);
        }
        if (item.key === 'membership_setting.tiers') {
          try {
            nextTiers = JSON.parse(item.value || '[]');
          } catch (error) {
            nextTiers = [];
          }
        }
      });
      setEnabled(nextEnabled);
      setTiers(nextTiers.map(normalizeTier));
    } catch (error) {
      showError(t('刷新失败'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadOptions();
    loadGroups();
  }, []);

  const updateTier = (tierId, key, value) => {
    setTiers((current) =>
      current.map((tier) =>
        tier.id === tierId ? { ...tier, [key]: value } : tier,
      ),
    );
  };

  const updateTierDiscount = (tierId, discountIndex, key, value) => {
    setTiers((current) =>
      current.map((tier) => {
        if (tier.id !== tierId) return tier;
        const discounts = [...(tier.group_discounts || [])];
        discounts[discountIndex] = {
          ...discounts[discountIndex],
          [key]: value,
        };
        return { ...tier, group_discounts: discounts };
      }),
    );
  };

  const addTier = () => {
    setTiers((current) => {
      const tier = createDefaultTier(current.length);
      tier.name = `${t('会员等级')} ${current.length + 1}`;
      return [...current, tier];
    });
  };

  const removeTier = (tierId) => {
    setTiers((current) => current.filter((tier) => tier.id !== tierId));
  };

  const addGroupDiscount = (tierId) => {
    setTiers((current) =>
      current.map((tier) => {
        if (tier.id !== tierId) return tier;
        return {
          ...tier,
          group_discounts: [
            ...(tier.group_discounts || []),
            { group: '', discount: 1 },
          ],
        };
      }),
    );
  };

  const removeGroupDiscount = (tierId, discountIndex) => {
    setTiers((current) =>
      current.map((tier) => {
        if (tier.id !== tierId) return tier;
        return {
          ...tier,
          group_discounts: (tier.group_discounts || []).filter(
            (_, itemIndex) => itemIndex !== discountIndex,
          ),
        };
      }),
    );
  };

  const saveMembership = async () => {
    const normalizedTiers = tiers
      .map(normalizeTier)
      .filter((tier) => tier.id && tier.name);
    setSaving(true);
    try {
      let res = await API.put('/api/option/', {
        key: 'membership_setting.tiers',
        value: JSON.stringify(normalizedTiers),
      });
      if (!res.data.success) {
        showError(res.data.message);
        return;
      }
      res = await API.put('/api/option/', {
        key: 'membership_setting.enabled',
        value: String(enabled),
      });
      if (!res.data.success) {
        showError(res.data.message);
        return;
      }
      setTiers(normalizedTiers);
      statusDispatch({
        type: 'set',
        payload: {
          ...(statusState?.status || {}),
          membership_enabled: enabled,
        },
      });
      showSuccess(t('保存成功'));
    } catch (error) {
      showError(t('保存失败，请重试'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Spin spinning={loading} size='large'>
      <Card style={{ marginTop: 10 }}>
        <Form.Section
          text={t('会员设置')}
          extraText={t('按累计充值金额解锁会员等级，并为指定分组应用折扣')}
        >
          <div
            className='mb-4 flex items-center justify-between p-4'
            style={{
              backgroundColor: 'var(--semi-color-bg-2)',
              border: '1px solid var(--semi-color-border)',
              borderRadius: 6,
            }}
          >
            <div className='flex items-center gap-3'>
              <Crown size={20} />
              <div>
                <Text strong>{t('启用会员功能')}</Text>
                <div>
                  <Text type='secondary' size='small'>
                    {t('关闭后不会展示会员中心，也不会应用会员折扣')}
                  </Text>
                </div>
              </div>
            </div>
            <Switch checked={enabled} onChange={setEnabled} />
          </div>

          <div className='mb-3 flex items-center justify-between'>
            <Text strong>{t('会员等级')}</Text>
            <Button icon={<Plus size={14} />} onClick={addTier}>
              {t('添加等级')}
            </Button>
          </div>

          <div className='space-y-3'>
            {sortedTiers.map((tier) => (
              <Card
                key={tier.id}
                bodyStyle={{ padding: 16 }}
                style={{
                  backgroundColor: 'var(--semi-color-bg-2)',
                  borderColor: 'var(--semi-color-border)',
                  borderRadius: 6,
                }}
              >
                <div className='mb-3 flex flex-wrap items-center justify-between gap-2'>
                  <Space wrap>
                    <Tag color={tier.enabled ? 'blue' : 'grey'} shape='circle'>
                      {tier.enabled ? t('启用') : t('停用')}
                    </Tag>
                    <Text strong>{tier.name || t('未命名等级')}</Text>
                  </Space>
                  <Button
                    type='danger'
                    theme='borderless'
                    icon={<Trash2 size={14} />}
                    onClick={() => removeTier(tier.id)}
                  />
                </div>

                <Row gutter={[12, 12]}>
                  <Col xs={24} md={8}>
                    <div className='mb-1'>
                      <Text size='small'>{t('会员名称')}</Text>
                    </div>
                    <Input
                      value={tier.name}
                      placeholder={t('请输入会员名称')}
                      onChange={(value) => updateTier(tier.id, 'name', value)}
                    />
                  </Col>
                  <Col xs={24} md={8}>
                    <div className='mb-1'>
                      <Text size='small'>{t('累充门槛')}</Text>
                    </div>
                    <InputNumber
                      value={amountToDisplayValue(tier.threshold_amount)}
                      prefix={
                        currencyType === 'TOKENS' ? undefined : currencySymbol
                      }
                      precision={amountPrecision}
                      min={0}
                      step={currencyType === 'TOKENS' ? 1 : amountStep}
                      style={{ width: '100%' }}
                      onChange={(value) =>
                        updateTier(
                          tier.id,
                          'threshold_amount',
                          displayValueToAmount(value),
                        )
                      }
                    />
                  </Col>
                  <Col xs={24} md={8}>
                    <div className='mb-1'>
                      <Text size='small'>{t('等级状态')}</Text>
                    </div>
                    <Space>
                      <Switch
                        checked={tier.enabled !== false}
                        onChange={(checked) =>
                          updateTier(tier.id, 'enabled', checked)
                        }
                      />
                      <Text type='secondary' size='small'>
                        {t('允许展示和使用')}
                      </Text>
                    </Space>
                  </Col>
                  <Col xs={24} md={8}>
                    <div className='mb-1'>
                      <Text size='small'>{t('自动升级')}</Text>
                    </div>
                    <Space>
                      <Switch
                        checked={tier.auto_upgrade_enabled !== false}
                        onChange={(checked) =>
                          updateTier(tier.id, 'auto_upgrade_enabled', checked)
                        }
                      />
                      <Text type='secondary' size='small'>
                        {t('累充达到后自动解锁')}
                      </Text>
                    </Space>
                  </Col>
                  <Col xs={24} md={8}>
                    <div className='mb-1'>
                      <Text size='small'>{t('全部分组折扣')}</Text>
                    </div>
                    <Space align='center'>
                      <Switch
                        checked={tier.discount_all_groups === true}
                        onChange={(checked) =>
                          updateTier(tier.id, 'discount_all_groups', checked)
                        }
                      />
                      <InputNumber
                        value={tier.all_group_discount}
                        min={0.01}
                        max={1}
                        precision={4}
                        step={0.01}
                        style={{ width: 110 }}
                        onChange={(value) =>
                          updateTier(tier.id, 'all_group_discount', value || 1)
                        }
                      />
                    </Space>
                  </Col>
                </Row>

                <Divider margin='12px' />
                <div className='mb-2 flex items-center justify-between'>
                  <Text size='small' type='secondary'>
                    {t('指定分组权益')}
                  </Text>
                  <Button
                    size='small'
                    type='tertiary'
                    onClick={() => addGroupDiscount(tier.id)}
                  >
                    {t('添加分组折扣')}
                  </Button>
                </div>
                <div className='space-y-2'>
                  {(tier.group_discounts || []).map((item, discountIndex) => (
                    <Row key={`${tier.id}-${discountIndex}`} gutter={8}>
                      <Col span={14}>
                        <Select
                          value={item.group}
                          placeholder={t('选择分组')}
                          optionList={groupOptions}
                          style={{ width: '100%' }}
                          onChange={(value) =>
                            updateTierDiscount(
                              tier.id,
                              discountIndex,
                              'group',
                              value,
                            )
                          }
                        />
                      </Col>
                      <Col span={8}>
                        <InputNumber
                          value={item.discount}
                          min={0.01}
                          max={1}
                          precision={4}
                          step={0.01}
                          style={{ width: '100%' }}
                          onChange={(value) =>
                            updateTierDiscount(
                              tier.id,
                              discountIndex,
                              'discount',
                              value || 1,
                            )
                          }
                        />
                      </Col>
                      <Col span={2}>
                        <Button
                          type='danger'
                          theme='borderless'
                          icon={<Trash2 size={14} />}
                          onClick={() =>
                            removeGroupDiscount(tier.id, discountIndex)
                          }
                        />
                      </Col>
                    </Row>
                  ))}
                </div>
              </Card>
            ))}
          </div>

          <div className='mt-4 flex justify-end'>
            <Button type='primary' loading={saving} onClick={saveMembership}>
              {t('保存设置')}
            </Button>
          </div>
        </Form.Section>
      </Card>
    </Spin>
  );
};

export default MembershipSetting;

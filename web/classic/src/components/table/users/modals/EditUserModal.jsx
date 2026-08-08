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

import React, { useEffect, useMemo, useState, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import {
  API,
  showError,
  showSuccess,
  renderQuota,
  getCurrencyConfig,
  copy,
} from '../../../../helpers';
import {
  quotaToDisplayAmount,
  displayAmountToQuota,
} from '../../../../helpers/quota';
import { useIsMobile } from '../../../../hooks/common/useIsMobile';
import {
  Button,
  Modal,
  SideSheet,
  Space,
  Spin,
  Typography,
  Card,
  Tag,
  Form,
  Avatar,
  Row,
  Col,
  InputNumber,
  RadioGroup,
  Radio,
} from '@douyinfe/semi-ui';
import {
  IconUser,
  IconSave,
  IconClose,
  IconLink,
  IconUserGroup,
  IconEdit,
  IconGlobe,
  IconLayers,
} from '@douyinfe/semi-icons';
import UserBindingManagementModal from './UserBindingManagementModal';

const { Text, Title } = Typography;

const EditUserModal = (props) => {
  const { t } = useTranslation();
  const userId = props.editingUser.id;
  const [loading, setLoading] = useState(true);
  const [adjustModalOpen, setAdjustModalOpen] = useState(false);
  const [adjustQuotaLocal, setAdjustQuotaLocal] = useState('');
  const [adjustAmountLocal, setAdjustAmountLocal] = useState('');
  const [adjustMode, setAdjustMode] = useState('add');
  const [adjustLoading, setAdjustLoading] = useState(false);
  const isMobile = useIsMobile();
  const [groupOptions, setGroupOptions] = useState([]);
  const [bindingModalVisible, setBindingModalVisible] = useState(false);
  const formApiRef = useRef(null);
  const [showAdjustQuotaRaw, setShowAdjustQuotaRaw] = useState(false);
  const [showQuotaInput, setShowQuotaInput] = useState(false);
  const [inputs, setInputs] = useState(null);
  const [groupRatioDetails, setGroupRatioDetails] = useState([]);
  const [groupRatioInputs, setGroupRatioInputs] = useState({});
  const [groupRatioSaving, setGroupRatioSaving] = useState('');

  const isEdit = Boolean(userId);
  const membershipTierSelectOptions = useMemo(
    () => [
      { label: t('无'), value: '' },
      ...(props.membershipTierOptions || []),
    ],
    [props.membershipTierOptions, t],
  );

  const getInitValues = () => ({
    username: '',
    display_name: '',
    password: '',
    github_id: '',
    oidc_id: '',
    discord_id: '',
    wechat_id: '',
    telegram_id: '',
    linux_do_id: '',
    email: '',
    quota: 0,
    quota_amount: 0,
    group: 'default',
    membership_tier_id: '',
    remark: '',
  });

  const fetchGroups = async () => {
    try {
      let res = await API.get(`/api/group/`);
      setGroupOptions(res.data.data.map((g) => ({ label: g, value: g })));
    } catch (e) {
      showError(e.message);
    }
  };

  const handleCancel = () => props.handleClose();

  const formatRatio = (value) => {
    const ratio = Number(value);
    if (!Number.isFinite(ratio)) return '-';
    return ratio.toFixed(12).replace(/\.?0+$/, '');
  };

  const applyGroupRatioDetails = (details = []) => {
    setGroupRatioDetails(details);
    setGroupRatioInputs(
      Object.fromEntries(
        details.map((detail) => [
          detail.group,
          detail.has_custom_ratio ? detail.custom_ratio : '',
        ]),
      ),
    );
  };

  const loadUser = async () => {
    setLoading(true);
    applyGroupRatioDetails([]);
    const url = userId ? `/api/user/${userId}` : `/api/user/self`;
    const groupRatioRequest = userId
      ? API.get(`/api/user/${userId}/group-ratios`).catch((error) => {
          // Group ratios are supplementary; a failed request must not block
          // the core user editor from loading.
          showError(error.message);
          return null;
        })
      : Promise.resolve(null);

    try {
      const [res, groupRatioRes] = await Promise.all([
        API.get(url),
        groupRatioRequest,
      ]);
      const { success, message, data } = res.data;
      if (success) {
        data.password = '';
        data.quota_amount = Number(
          quotaToDisplayAmount(data.quota || 0).toFixed(6),
        );
        setInputs({ ...getInitValues(), ...data });
      } else {
        showError(message);
      }
      if (groupRatioRes) {
        if (groupRatioRes.data.success) {
          applyGroupRatioDetails(groupRatioRes.data.data || []);
        } else {
          showError(groupRatioRes.data.message);
        }
      }
    } catch (error) {
      showError(error.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (inputs && formApiRef.current) {
      formApiRef.current.setValues(inputs);
    }
  }, [inputs]);

  useEffect(() => {
    loadUser();
    if (userId) fetchGroups();
    setBindingModalVisible(false);
  }, [props.editingUser.id, props.membershipEnabled]);

  const openBindingModal = () => {
    setBindingModalVisible(true);
  };

  const closeBindingModal = () => {
    setBindingModalVisible(false);
  };

  /* ----------------------- submit ----------------------- */
  const submit = async (values) => {
    setLoading(true);
    let payload = { ...values };
    delete payload.quota;
    delete payload.quota_amount;
    if (!props.membershipEnabled) {
      delete payload.membership_tier_id;
    } else {
      payload.membership_tier_id = payload.membership_tier_id || '';
    }
    if (userId) {
      payload.id = parseInt(userId);
    }
    const url = userId ? `/api/user/` : `/api/user/self`;
    const res = await API.put(url, payload);
    const { success, message } = res.data;
    if (success) {
      showSuccess(t('用户信息更新成功！'));
      props.refresh();
      props.handleClose();
    } else {
      showError(message);
    }
    setLoading(false);
  };

  /* --------------------- atomic quota adjust -------------------- */
  const adjustQuota = async () => {
    const quotaVal = parseInt(adjustQuotaLocal) || 0;
    if (quotaVal <= 0 && adjustMode !== 'override') return;
    if (
      adjustMode === 'override' &&
      (adjustQuotaLocal === '' || adjustQuotaLocal == null)
    )
      return;
    setAdjustLoading(true);
    try {
      const res = await API.post('/api/user/manage', {
        id: parseInt(userId),
        action: 'add_quota',
        mode: adjustMode,
        value: adjustMode === 'override' ? quotaVal : Math.abs(quotaVal),
      });
      const { success, message } = res.data;
      if (success) {
        showSuccess(t('调整额度成功'));
        setAdjustModalOpen(false);
        setAdjustQuotaLocal('');
        setAdjustAmountLocal('');
        const userRes = await API.get(`/api/user/${userId}`);
        if (userRes.data.success) {
          const data = userRes.data.data;
          data.password = '';
          data.quota_amount = Number(
            quotaToDisplayAmount(data.quota || 0).toFixed(6),
          );
          setInputs({ ...getInitValues(), ...data });
        }
        props.refresh();
      } else {
        showError(message);
      }
    } catch (e) {
      showError(e.message);
    }
    setAdjustLoading(false);
  };

  const getPreviewText = () => {
    const current = formApiRef.current?.getValue('quota') || 0;
    const val = parseInt(adjustQuotaLocal) || 0;
    let result;
    switch (adjustMode) {
      case 'add':
        result = current + Math.abs(val);
        return `${t('当前额度')}：${renderQuota(current)}，+${renderQuota(Math.abs(val))} = ${renderQuota(result)}`;
      case 'subtract':
        result = current - Math.abs(val);
        return `${t('当前额度')}：${renderQuota(current)}，-${renderQuota(Math.abs(val))} = ${renderQuota(result)}`;
      case 'override':
        return `${t('当前额度')}：${renderQuota(current)} → ${renderQuota(val)}`;
      default:
        return '';
    }
  };

  const renderIpValue = (ip) =>
    ip ? (
      <Tag
        shape='circle'
        className='font-mono cursor-pointer'
        onClick={async () => {
          if (await copy(ip)) showSuccess(t('已复制：') + ip);
        }}
      >
        {ip}
      </Tag>
    ) : (
      <Tag color='white' shape='circle'>
        -
      </Tag>
    );

  const saveGroupRatio = async (group) => {
    const ratio = Number(groupRatioInputs[group]);
    if (
      groupRatioInputs[group] === '' ||
      groupRatioInputs[group] == null ||
      !Number.isFinite(ratio) ||
      ratio < 0 ||
      ratio > 1000
    ) {
      showError(t('请输入 0 到 1000 之间的有效倍率'));
      return;
    }
    setGroupRatioSaving(`${group}:save`);
    try {
      const res = await API.put(`/api/user/${userId}/group-ratios`, {
        group,
        ratio,
      });
      if (res.data.success) {
        applyGroupRatioDetails(res.data.data || []);
        showSuccess(t('分组倍率设置成功'));
      } else {
        showError(res.data.message);
      }
    } catch (error) {
      showError(error.message);
    } finally {
      setGroupRatioSaving('');
    }
  };

  const resetGroupRatio = async (group) => {
    setGroupRatioSaving(`${group}:reset`);
    try {
      const res = await API.delete(`/api/user/${userId}/group-ratios`, {
        params: { group },
      });
      if (res.data.success) {
        applyGroupRatioDetails(res.data.data || []);
        showSuccess(t('已恢复继承分组倍率'));
      } else {
        showError(res.data.message);
      }
    } catch (error) {
      showError(error.message);
    } finally {
      setGroupRatioSaving('');
    }
  };

  /* --------------------------- UI --------------------------- */
  return (
    <>
      <SideSheet
        placement='right'
        title={
          <Space>
            <Tag color='blue' shape='circle'>
              {t(isEdit ? '编辑' : '新建')}
            </Tag>
            <Title heading={4} className='m-0'>
              {isEdit ? t('编辑用户') : t('创建用户')}
            </Title>
          </Space>
        }
        bodyStyle={{ padding: 0 }}
        visible={props.visible}
        width={isMobile ? '100%' : 600}
        footer={
          <div className='flex justify-end bg-white'>
            <Space>
              <Button
                theme='solid'
                onClick={() => formApiRef.current?.submitForm()}
                icon={<IconSave />}
                loading={loading}
              >
                {t('提交')}
              </Button>
              <Button
                theme='light'
                type='primary'
                onClick={handleCancel}
                icon={<IconClose />}
              >
                {t('取消')}
              </Button>
            </Space>
          </div>
        }
        closeIcon={null}
        onCancel={handleCancel}
      >
        <Spin spinning={loading}>
          <Form
            initValues={getInitValues()}
            getFormApi={(api) => (formApiRef.current = api)}
            onSubmit={submit}
          >
            {({ values }) => (
              <div className='p-2 space-y-3'>
                {/* 基本信息 */}
                <Card className='!rounded-2xl shadow-sm border-0'>
                  <div className='flex items-center mb-2'>
                    <Avatar
                      size='small'
                      color='blue'
                      className='mr-2 shadow-md'
                    >
                      <IconUser size={16} />
                    </Avatar>
                    <div>
                      <Text className='text-lg font-medium'>
                        {t('基本信息')}
                      </Text>
                      <div className='text-xs text-gray-600'>
                        {t('用户的基本账户信息')}
                      </div>
                    </div>
                  </div>

                  <Row gutter={12}>
                    <Col span={24}>
                      <Form.Input
                        field='username'
                        label={t('用户名')}
                        placeholder={t('请输入新的用户名')}
                        rules={[{ required: true, message: t('请输入用户名') }]}
                        showClear
                      />
                    </Col>

                    <Col span={24}>
                      <Form.Input
                        field='password'
                        label={t('密码')}
                        placeholder={t('请输入新的密码，最短 8 位')}
                        mode='password'
                        showClear
                      />
                    </Col>

                    <Col span={24}>
                      <Form.Input
                        field='display_name'
                        label={t('显示名称')}
                        placeholder={t('请输入新的显示名称')}
                        showClear
                      />
                    </Col>

                    <Col span={24}>
                      <Form.Input
                        field='remark'
                        label={t('备注')}
                        placeholder={t('请输入备注（仅管理员可见）')}
                        showClear
                      />
                    </Col>
                  </Row>
                </Card>

                {/* 权限设置 */}
                {userId && (
                  <Card className='!rounded-2xl shadow-sm border-0'>
                    <div className='flex items-center mb-2'>
                      <Avatar
                        size='small'
                        color='green'
                        className='mr-2 shadow-md'
                      >
                        <IconUserGroup size={16} />
                      </Avatar>
                      <div>
                        <Text className='text-lg font-medium'>
                          {t('权限设置')}
                        </Text>
                        <div className='text-xs text-gray-600'>
                          {t('用户分组和额度管理')}
                        </div>
                      </div>
                    </div>

                    <Row gutter={12}>
                      <Col
                        span={props.membershipEnabled && !isMobile ? 12 : 24}
                      >
                        <Form.Select
                          field='group'
                          label={t('分组')}
                          placeholder={t('请选择分组')}
                          optionList={groupOptions}
                          allowAdditions
                          search
                          rules={[{ required: true, message: t('请选择分组') }]}
                        />
                      </Col>

                      {props.membershipEnabled && (
                        <Col span={isMobile ? 24 : 12}>
                          <Form.Select
                            field='membership_tier_id'
                            label={t('会员阶级')}
                            placeholder={t('请选择会员阶级')}
                            optionList={membershipTierSelectOptions}
                            showClear
                            search
                          />
                        </Col>
                      )}

                      <Col span={10}>
                        <Form.InputNumber
                          field='quota_amount'
                          label={t('金额')}
                          prefix={getCurrencyConfig().symbol}
                          precision={6}
                          step={0.000001}
                          style={{ width: '100%' }}
                          readonly
                        />
                      </Col>

                      <Col span={14}>
                        <Form.Slot label={t('调整额度')}>
                          <Button
                            icon={<IconEdit />}
                            onClick={() => setAdjustModalOpen(true)}
                          >
                            {t('调整额度')}
                          </Button>
                        </Form.Slot>
                      </Col>

                      <Col span={24}>
                        <div
                          className='text-xs cursor-pointer'
                          style={{ color: 'var(--semi-color-text-2)' }}
                          onClick={() => setShowQuotaInput((v) => !v)}
                        >
                          {showQuotaInput
                            ? `▾ ${t('收起原生额度输入')}`
                            : `▸ ${t('使用原生额度输入')}`}
                        </div>
                        <div
                          style={{ display: showQuotaInput ? 'block' : 'none' }}
                          className='mt-2'
                        >
                          <Form.InputNumber
                            field='quota'
                            label={t('额度')}
                            placeholder={t('请输入额度')}
                            style={{ width: '100%' }}
                            readonly
                          />
                        </div>
                      </Col>
                    </Row>
                  </Card>
                )}

                {/* 分组倍率设置 */}
                {userId && (
                  <Card className='!rounded-2xl shadow-sm border-0'>
                    <div className='flex items-center mb-2'>
                      <Avatar
                        size='small'
                        color='orange'
                        className='mr-2 shadow-md'
                      >
                        <IconLayers size={16} />
                      </Avatar>
                      <div>
                        <Text className='text-lg font-medium'>
                          {t('分组倍率设置')}
                        </Text>
                        <div className='text-xs text-gray-600'>
                          {t('当前实际倍率已包含分组和会员计算结果')}
                        </div>
                      </div>
                    </div>

                    <div
                      className='max-h-80 overflow-y-auto'
                      style={{
                        borderTop: '1px solid var(--semi-color-border)',
                      }}
                    >
                      {groupRatioDetails.length === 0 ? (
                        <div className='py-6 text-center'>
                          <Text type='tertiary'>{t('暂无可设置的分组')}</Text>
                        </div>
                      ) : (
                        groupRatioDetails.map((detail) => (
                          <div
                            key={detail.group}
                            className='flex flex-wrap items-center justify-between gap-3 py-3'
                            style={{
                              borderBottom:
                                '1px solid var(--semi-color-border)',
                            }}
                          >
                            <div className='min-w-0 flex-1'>
                              <Space wrap spacing={6}>
                                <Text strong>{detail.group}</Text>
                                <Tag
                                  color={
                                    detail.has_custom_ratio
                                      ? 'blue'
                                      : detail.membership_discount?.applied
                                        ? 'amber'
                                        : 'grey'
                                  }
                                  shape='circle'
                                >
                                  {detail.has_custom_ratio
                                    ? t('个人倍率')
                                    : detail.membership_discount?.applied
                                      ? t('会员')
                                      : t('继承')}
                                </Tag>
                              </Space>
                              <div className='mt-1 text-xs'>
                                <Text type='secondary'>
                                  {t('当前实际倍率')}：
                                </Text>
                                <Text strong>
                                  {formatRatio(detail.effective_ratio)}x
                                </Text>
                                {detail.membership_discount?.applied && (
                                  <Text type='tertiary' className='ml-2'>
                                    {detail.has_custom_ratio
                                      ? t(
                                          '会员计算值 {{ratio}}x 已被个人设置覆盖',
                                          {
                                            ratio: formatRatio(
                                              detail.membership_ratio,
                                            ),
                                          },
                                        )
                                      : t('{{tier}} 会员计算值', {
                                          tier: detail.membership_discount
                                            .tier_name,
                                        })}
                                  </Text>
                                )}
                              </div>
                            </div>

                            <Space spacing={6} wrap>
                              <InputNumber
                                value={groupRatioInputs[detail.group]}
                                placeholder={t('输入最终倍率')}
                                min={0}
                                max={1000}
                                precision={12}
                                step={0.1}
                                suffix='x'
                                style={{ width: 142 }}
                                onChange={(value) =>
                                  setGroupRatioInputs((current) => ({
                                    ...current,
                                    [detail.group]: value,
                                  }))
                                }
                              />
                              <Button
                                type='primary'
                                theme='solid'
                                loading={
                                  groupRatioSaving === `${detail.group}:save`
                                }
                                disabled={groupRatioSaving !== ''}
                                onClick={() => saveGroupRatio(detail.group)}
                              >
                                {t('保存')}
                              </Button>
                              {detail.has_custom_ratio && (
                                <Button
                                  type='tertiary'
                                  loading={
                                    groupRatioSaving === `${detail.group}:reset`
                                  }
                                  disabled={groupRatioSaving !== ''}
                                  onClick={() => resetGroupRatio(detail.group)}
                                >
                                  {t('恢复继承')}
                                </Button>
                              )}
                            </Space>
                          </div>
                        ))
                      )}
                    </div>
                  </Card>
                )}

                {/* IP 信息 */}
                {userId && (
                  <Card className='!rounded-2xl shadow-sm border-0'>
                    <div className='flex items-center mb-2'>
                      <Avatar
                        size='small'
                        color='blue'
                        className='mr-2 shadow-md'
                      >
                        <IconGlobe size={16} />
                      </Avatar>
                      <div>
                        <Text className='text-lg font-medium'>
                          {t('IP 信息')}
                        </Text>
                        <div className='text-xs text-gray-600'>
                          {t('用户注册、登录与调用来源 IP')}
                        </div>
                      </div>
                    </div>

                    <Row gutter={12}>
                      <Col span={isMobile ? 24 : 8}>
                        <Form.Slot label={t('注册 IP')}>
                          {renderIpValue(inputs?.register_ip)}
                        </Form.Slot>
                      </Col>
                      <Col span={isMobile ? 24 : 8}>
                        <Form.Slot label={t('最近一次登录 IP')}>
                          {renderIpValue(inputs?.last_login_ip)}
                        </Form.Slot>
                      </Col>
                      <Col span={isMobile ? 24 : 8}>
                        <Form.Slot label={t('最近一次调用 API 的 IP')}>
                          {renderIpValue(inputs?.last_api_ip)}
                        </Form.Slot>
                      </Col>
                    </Row>
                  </Card>
                )}

                {/* 绑定信息入口 */}
                {userId && (
                  <Card className='!rounded-2xl shadow-sm border-0'>
                    <div className='flex items-center justify-between gap-3'>
                      <div className='flex items-center min-w-0'>
                        <Avatar
                          size='small'
                          color='purple'
                          className='mr-2 shadow-md'
                        >
                          <IconLink size={16} />
                        </Avatar>
                        <div className='min-w-0'>
                          <Text className='text-lg font-medium'>
                            {t('绑定信息')}
                          </Text>
                          <div className='text-xs text-gray-600'>
                            {t('管理用户已绑定的第三方账户，支持筛选与解绑')}
                          </div>
                        </div>
                      </div>
                      <Button
                        type='primary'
                        theme='outline'
                        onClick={openBindingModal}
                      >
                        {t('管理绑定')}
                      </Button>
                    </div>
                  </Card>
                )}
              </div>
            )}
          </Form>
        </Spin>
      </SideSheet>

      <UserBindingManagementModal
        visible={bindingModalVisible}
        onCancel={closeBindingModal}
        userId={userId}
        isMobile={isMobile}
        formApiRef={formApiRef}
      />

      {/* 调整额度模态框 */}
      <Modal
        centered
        visible={adjustModalOpen}
        onOk={adjustQuota}
        onCancel={() => {
          setAdjustModalOpen(false);
          setAdjustQuotaLocal('');
          setAdjustAmountLocal('');
          setAdjustMode('add');
        }}
        confirmLoading={adjustLoading}
        closable={null}
        title={
          <div className='flex items-center'>
            <IconEdit className='mr-2' />
            {t('调整额度')}
          </div>
        }
      >
        <div className='mb-4'>
          <Text type='secondary' className='block mb-2'>
            {getPreviewText()}
          </Text>
        </div>
        <div className='mb-3'>
          <div className='mb-1'>
            <Text size='small'>{t('操作')}</Text>
          </div>
          <RadioGroup
            type='button'
            value={adjustMode}
            onChange={(e) => {
              setAdjustMode(e.target.value);
              setAdjustQuotaLocal('');
              setAdjustAmountLocal('');
            }}
            style={{ width: '100%' }}
          >
            <Radio value='add'>{t('添加')}</Radio>
            <Radio value='subtract'>{t('减少')}</Radio>
            <Radio value='override'>{t('覆盖')}</Radio>
          </RadioGroup>
        </div>
        <div className='mb-3'>
          <div className='mb-1'>
            <Text size='small'>{t('金额')}</Text>
          </div>
          <InputNumber
            prefix={getCurrencyConfig().symbol}
            placeholder={t('输入金额')}
            value={adjustAmountLocal}
            precision={6}
            min={adjustMode === 'override' ? undefined : 0}
            step={0.000001}
            onChange={(val) => {
              const amount = val === '' || val == null ? '' : val;
              setAdjustAmountLocal(amount);
              setAdjustQuotaLocal(
                amount === ''
                  ? ''
                  : adjustMode === 'override'
                    ? displayAmountToQuota(amount)
                    : displayAmountToQuota(Math.abs(amount)),
              );
            }}
            style={{ width: '100%' }}
            showClear
          />
        </div>
        <div
          className='text-xs cursor-pointer mt-2'
          style={{ color: 'var(--semi-color-text-2)' }}
          onClick={() => setShowAdjustQuotaRaw((v) => !v)}
        >
          {showAdjustQuotaRaw
            ? `▾ ${t('收起原生额度输入')}`
            : `▸ ${t('使用原生额度输入')}`}
        </div>
        <div
          style={{ display: showAdjustQuotaRaw ? 'block' : 'none' }}
          className='mt-2'
        >
          <div className='mb-1'>
            <Text size='small'>{t('额度')}</Text>
          </div>
          <InputNumber
            placeholder={t('输入额度')}
            value={adjustQuotaLocal}
            min={adjustMode === 'override' ? undefined : 0}
            onChange={(val) => {
              const quota = val === '' || val == null ? '' : val;
              setAdjustQuotaLocal(quota);
              setAdjustAmountLocal(
                quota === ''
                  ? ''
                  : adjustMode === 'override'
                    ? Number(quotaToDisplayAmount(quota).toFixed(6))
                    : Number(quotaToDisplayAmount(Math.abs(quota)).toFixed(6)),
              );
            }}
            style={{ width: '100%' }}
            showClear
            step={500000}
          />
        </div>
      </Modal>
    </>
  );
};

export default EditUserModal;

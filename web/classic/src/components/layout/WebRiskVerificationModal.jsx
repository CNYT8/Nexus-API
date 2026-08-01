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

import React, { useCallback, useEffect, useState } from 'react';
import { Modal, Spin, Typography } from '@douyinfe/semi-ui';
import Turnstile from 'react-turnstile';
import { useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { API, getUserIdFromLocalStorage, showError } from '../../helpers';
import {
  WEB_RISK_REQUIRED_EVENT,
  dispatchWebRiskVerificationRequired,
} from '../../helpers/webRisk';

const { Text } = Typography;
const STATUS_CHECK_INTERVAL = 60 * 1000;
let lastStatusCheckAt = 0;
let lastStatusUserId = '';

const WebRiskVerificationModal = () => {
  const { t } = useTranslation();
  const location = useLocation();
  const [visible, setVisible] = useState(false);
  const [siteKey, setSiteKey] = useState('');
  const [verifying, setVerifying] = useState(false);
  const [widgetKey, setWidgetKey] = useState(0);

  const requireVerification = useCallback((nextSiteKey) => {
    if (nextSiteKey) setSiteKey(nextSiteKey);
    setVisible(true);
  }, []);

  useEffect(() => {
    const handleRequired = (event) => {
      requireVerification(event?.detail?.siteKey || '');
    };
    window.addEventListener(WEB_RISK_REQUIRED_EVENT, handleRequired);
    return () => {
      window.removeEventListener(WEB_RISK_REQUIRED_EVENT, handleRequired);
    };
  }, [requireVerification]);

  useEffect(() => {
    const userId = String(getUserIdFromLocalStorage() || '');
    if (!userId) {
      lastStatusUserId = '';
      lastStatusCheckAt = 0;
      return;
    }
    const now = Date.now();
    if (
      userId === lastStatusUserId &&
      now - lastStatusCheckAt < STATUS_CHECK_INTERVAL
    ) {
      return;
    }
    lastStatusUserId = userId;
    lastStatusCheckAt = now;

    API.get('/api/user/web-risk/status', { skipErrorHandler: true })
      .then((response) => {
        const data = response?.data?.data;
        if (response?.data?.success && data?.required) {
          dispatchWebRiskVerificationRequired(data.turnstile_site_key || '');
        }
      })
      .catch(() => {});
  }, [location.pathname]);

  const verify = async (token) => {
    if (!token || verifying) return;
    setVerifying(true);
    try {
      const response = await API.post(
        '/api/user/web-risk/verify',
        { turnstile: token },
        { skipErrorHandler: true },
      );
      if (response?.data?.success) {
        window.location.reload();
        return;
      }
      showError(response?.data?.message || t('验证失败，请重试'));
    } catch (error) {
      showError(error?.response?.data?.message || t('验证失败，请重试'));
    } finally {
      setVerifying(false);
      setWidgetKey((value) => value + 1);
    }
  };

  return (
    <Modal
      title={t('请验证您的真实性')}
      visible={visible}
      footer={null}
      centered
      closable={false}
      maskClosable={false}
      closeOnEsc={false}
    >
      <div className='flex flex-col items-center gap-4 py-2'>
        <Text type='tertiary' className='text-center'>
          {t('检测到账号短时间内使用多个网络地址，请完成验证后继续')}
        </Text>
        {siteKey && (
          <Turnstile
            key={widgetKey}
            sitekey={siteKey}
            onVerify={verify}
            onExpire={() => setWidgetKey((value) => value + 1)}
            onError={() => setWidgetKey((value) => value + 1)}
          />
        )}
        {verifying && (
          <div className='flex items-center gap-2'>
            <Spin size='small' />
            <Text type='tertiary'>{t('正在验证')}</Text>
          </div>
        )}
      </div>
    </Modal>
  );
};

export default WebRiskVerificationModal;

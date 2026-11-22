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

import React, { useContext, useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  API,
  showError,
  showSuccess,
  updateAPI,
  setUserData,
} from '../../helpers';
import { UserContext } from '../../context/User';
import Loading from '../common/ui/Loading';
import { consumeOAuthReturn } from './oauth-return-target';
import TwoFAVerification from './TwoFAVerification';

const OAuth2Callback = (props) => {
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();
  const [, userDispatch] = useContext(UserContext);
  const navigate = useNavigate();
  
  // 防止 React 18 Strict Mode 下重复执行
  const hasExecuted = useRef(false);
  const [showVerification, setShowVerification] = useState(false);

  const returnTo = useRef({ target: null, bind: false });
  const finishFailure = () => {
    const { target, bind } = returnTo.current;
    navigate(bind ? '/console/personal' : '/login', {
      replace: true,
      state: target ? { from: target } : undefined,
    });
  };

  const finishLogin = (data) => {
    if (data?.id == null) {
      showError(t('授权失败'));
      finishFailure();
      return;
    }
    userDispatch({ type: 'login', payload: data });
    localStorage.setItem('user', JSON.stringify(data));
    setUserData(data);
    updateAPI();
    showSuccess(t('登录成功！'));
    navigate(returnTo.current.target || '/console/token', { replace: true });
  };

  const sendCode = async (code, state) => {
    try {
      const { data: resData } = await API.get(
        `/api/oauth/${encodeURIComponent(props.type)}?${new URLSearchParams({ code, state })}`,
        { skipErrorHandler: true },
      );

      const { success, message, data } = resData;

      if (!success) {
        // 业务错误不重试，直接显示错误
        showError(message || t('授权失败'));
        finishFailure();
        return;
      }

      if (data?.action === 'bind' || message === 'bind') {
        showSuccess(t('绑定成功！'));
        navigate('/console/personal', { replace: true });
      } else if (returnTo.current.bind) {
        showError(t('授权失败'));
        finishFailure();
      } else if (data?.require_2fa === true) {
        setShowVerification(true);
      } else {
        finishLogin(data);
      }
    } catch (error) {
      // Authorization codes are single-use: never replay after an uncertain response.
      showError(error.message || t('授权失败'));
      finishFailure();
    }
  };

  useEffect(() => {
    // 防止 React 18 Strict Mode 下重复执行
    if (hasExecuted.current) {
      return;
    }
    hasExecuted.current = true;

    const code = searchParams.get('code');
    const state = searchParams.get('state');

    returnTo.current = consumeOAuthReturn(state);
    if (!code || !state || searchParams.has('error') || (returnTo.current.bind && !returnTo.current.matched)) {
      showError(t('授权失败'));
      finishFailure();
      return;
    }

    sendCode(code, state);
  }, []);

  return showVerification
    ? <TwoFAVerification onSuccess={finishLogin} onBack={finishFailure} />
    : <Loading />;
};

export default OAuth2Callback;

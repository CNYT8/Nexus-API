import React, { useContext, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Button, Card, Typography } from '@douyinfe/semi-ui';
import { UserContext } from '../../../../context/User';
import LoginDevicesModal from './LoginDevicesModal';
import PersonalAccessTokensModal from './PersonalAccessTokensModal';

function SecurityManagementForUser({ user, onLogout }) {
  const { t } = useTranslation();
  const [dialog, setDialog] = useState(null);
  return (
    <>
      <Card className='!rounded-xl w-full'>
        <div className='space-y-4'>
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <div>
              <Typography.Title heading={6}>{t('登录设备')}</Typography.Title>
              <p className='text-sm'>{t('查看并撤销浏览器登录会话')}</p>
            </div>
            <Button onClick={() => setDialog('devices')}>{t('登录设备')}</Button>
          </div>
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <div>
              <Typography.Title heading={6}>{t('个人管理令牌')}</Typography.Title>
              <p className='text-sm'>{t('创建、查看权限和撤销个人管理令牌')}</p>
            </div>
            <Button onClick={() => setDialog('tokens')}>{t('个人管理令牌')}</Button>
          </div>
        </div>
      </Card>
      {dialog === 'devices' && <LoginDevicesModal owner={user.id} onClose={() => setDialog(null)} onLogout={onLogout} />}
      {dialog === 'tokens' && <PersonalAccessTokensModal owner={user.id} role={user.role} onClose={() => setDialog(null)} />}
    </>
  );
}

export default function SecurityManagement() {
  const [userState, userDispatch] = useContext(UserContext);
  const navigate = useNavigate();
  const user = userState.user;
  if (!user?.id) return null;
  const logout = () => {
    localStorage.removeItem('user');
    userDispatch({ type: 'logout' });
    navigate('/login');
  };
  return <SecurityManagementForUser key={user.id} user={user} onLogout={logout} />;
}

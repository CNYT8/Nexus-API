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

import React, { useEffect, useState } from 'react';
import { Button, Popover, SideSheet, Typography } from '@douyinfe/semi-ui';
import { SlidersHorizontal, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import ParameterControl from './ParameterControl';

const PlaygroundParameterPanel = ({
  inputs,
  parameterEnabled,
  onInputChange,
  onParameterToggle,
  isMobile = false,
  disabled = false,
}) => {
  const { t } = useTranslation();
  const [visible, setVisible] = useState(false);
  const enabledCount = Object.values(parameterEnabled).filter(Boolean).length;

  useEffect(() => {
    setVisible(false);
  }, [disabled, isMobile]);

  const handleVisibleChange = (nextVisible) => {
    setVisible(!disabled && nextVisible);
  };
  const closePanel = () => setVisible(false);
  const content = (
    <div
      role='region'
      aria-label={t('Parameter settings')}
      style={{
        width: isMobile ? '100vw' : 360,
        maxWidth: '100vw',
        padding: 16,
        boxSizing: 'border-box',
      }}
    >
      <div className='flex items-center justify-between gap-2 mb-2'>
        <Typography.Text strong>{t('Parameter settings')}</Typography.Text>
        <Button
          theme='borderless'
          type='tertiary'
          size='small'
          icon={<X size={16} />}
          aria-label={t('关闭')}
          onClick={closePanel}
          className='!rounded-lg'
        />
      </div>
      <Typography.Paragraph type='tertiary' className='text-xs mb-4'>
        {t('Only enabled parameters are sent with the request.')}
      </Typography.Paragraph>
      <div
        className='overflow-y-auto pr-2 model-settings-scroll'
        style={{ maxHeight: 'min(60vh, 560px)' }}
      >
        <ParameterControl
          inputs={inputs}
          parameterEnabled={parameterEnabled}
          onInputChange={onInputChange}
          onParameterToggle={onParameterToggle}
          disabled={disabled}
        />
      </div>
    </div>
  );
  const trigger = (
    <Button
      theme='outline'
      type='tertiary'
      size='small'
      icon={<SlidersHorizontal size={14} />}
      aria-label={t('Parameters')}
      title={t('Parameters')}
      aria-expanded={visible && !disabled}
      disabled={disabled}
      onClick={isMobile ? () => handleVisibleChange(true) : undefined}
      className='!rounded-lg'
    >
      <span className='text-xs tabular-nums'>{enabledCount}</span>
    </Button>
  );

  if (isMobile) {
    return (
      <>
        {trigger}
        <SideSheet
          placement='bottom'
          visible={visible && !disabled}
          onCancel={closePanel}
          closable={false}
          height='auto'
          width='100vw'
          style={{ maxWidth: '100vw' }}
          headerStyle={{ display: 'none' }}
          bodyStyle={{ padding: 0 }}
        >
          {content}
        </SideSheet>
      </>
    );
  }

  return (
    <Popover
      trigger='click'
      position='topLeft'
      visible={visible && !disabled}
      onVisibleChange={handleVisibleChange}
      content={content}
      showArrow
    >
      {trigger}
    </Popover>
  );
};

export default PlaygroundParameterPanel;

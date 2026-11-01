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

import React, { useState, useRef, useEffect, useCallback } from 'react';
import {
  Button,
  Dropdown,
  Select,
  TextArea,
  Toast,
  Tooltip,
  Typography,
} from '@douyinfe/semi-ui';
import {
  ArrowDown,
  Camera,
  Check,
  ChevronsUpDown,
  Cpu,
  FileText,
  Globe,
  Image as ImageIcon,
  Layers,
  Paperclip,
  ScreenShare,
  Send,
  Square,
  Trash2,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';

// 与新版一致：触发按钮只显示分组名，描述与倍率仅在展开项中展示。
const playgroundGroupOptions = (groups) =>
  groups.map((group) => ({
    ...group,
    label: group.value || '用户分组',
    description: group.fullLabel || group.description || group.desc || '',
  }));

const renderGroupOption = ({
  description,
  ratio,
  label,
  className,
  style,
  onClick,
  onMouseEnter,
  selected,
}) => (
  <div
    className={className}
    style={{ ...style, padding: '8px 12px' }}
    onClick={onClick}
    onMouseEnter={onMouseEnter}
  >
    <div className='flex items-center justify-between gap-4'>
      <div className='min-w-0'>
        <div className='truncate text-xs font-medium'>{label}</div>
        {(description || ratio) && (
          <div
            className='truncate text-[11px] leading-5'
            style={{ color: 'var(--semi-color-text-2)' }}
          >
            {[description, ratio ? `倍率 ${ratio}` : '']
              .filter(Boolean)
              .join(' · ')}
          </div>
        )}
      </div>
      {selected && (
        <Check
          size={14}
          className='shrink-0'
          style={{ color: 'var(--semi-color-primary)' }}
        />
      )}
    </div>
  </div>
);

const ChatArea = ({
  message = [],
  inputs = {},
  models = [],
  groups = [],
  styleState = {},
  isGenerating = false,
  onInputChange,
  onMessageSend,
  onStopGenerator,
  onClearMessages,
  renderCustomChatContent,
  renderChatBoxAction,
}) => {
  const { t } = useTranslation();
  const [inputText, setInputText] = useState('');
  const [showScrollBottom, setShowScrollBottom] = useState(false);
  const scrollContainerRef = useRef(null);
  const shouldAutoScrollRef = useRef(true);
  const scrollFrameRef = useRef(null);
  const programmaticScrollRef = useRef(false);
  const lastScrollTopRef = useRef(0);

  const handleWheel = useCallback(() => {
    programmaticScrollRef.current = false;
  }, []);

  const handleTouchStart = useCallback(() => {
    programmaticScrollRef.current = false;
  }, []);

  const updateScrollAffordance = useCallback(() => {
    const el = scrollContainerRef.current;
    if (!el) return;
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    const nearBottom = distanceFromBottom <= 80;
    setShowScrollBottom(!nearBottom && el.scrollHeight > el.clientHeight + 120);
  }, []);

  const checkScrollPosition = useCallback(() => {
    const el = scrollContainerRef.current;
    if (!el) return;
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    const nearBottom = distanceFromBottom <= 80;
    if (el.scrollTop < lastScrollTopRef.current && !nearBottom) {
      // 即使用户在系统平滑滚动途中上滑，也立即交还滚动控制权并暂停跟随。
      programmaticScrollRef.current = false;
      shouldAutoScrollRef.current = false;
    } else if (nearBottom) {
      shouldAutoScrollRef.current = true;
    }
    lastScrollTopRef.current = el.scrollTop;
    updateScrollAffordance();
  }, [updateScrollAffordance]);

  const scrollToBottom = useCallback(
    (smooth = true) => {
      const el = scrollContainerRef.current;
      if (!el) return;
      shouldAutoScrollRef.current = true;
      setShowScrollBottom(false);
      programmaticScrollRef.current = true;
      el.scrollTo({
        top: el.scrollHeight,
        behavior: smooth ? 'smooth' : 'auto',
      });
      // 用户在此期间主动滚动，以用户操作为准。
      window.setTimeout(
        () => {
          programmaticScrollRef.current = false;
          if (scrollContainerRef.current) {
            lastScrollTopRef.current = scrollContainerRef.current.scrollTop;
          }
        },
        smooth ? 400 : 0,
      );
    },
    [updateScrollAffordance],
  );

  // 流式消息可能一帧更新多次，滚动只按显示帧合并执行，避免连续跳动。
  useEffect(() => {
    if (scrollFrameRef.current !== null) {
      cancelAnimationFrame(scrollFrameRef.current);
    }
    scrollFrameRef.current = requestAnimationFrame(() => {
      scrollFrameRef.current = null;
      if (shouldAutoScrollRef.current) {
        const el = scrollContainerRef.current;
        if (el) {
          programmaticScrollRef.current = true;
          el.scrollTop = el.scrollHeight;
          lastScrollTopRef.current = el.scrollTop;
          programmaticScrollRef.current = false;
        }
      }
      updateScrollAffordance();
    });
    return () => {
      if (scrollFrameRef.current !== null) {
        cancelAnimationFrame(scrollFrameRef.current);
        scrollFrameRef.current = null;
      }
    };
  }, [message, updateScrollAffordance]);

  const handleSubmit = useCallback(() => {
    const trimmed = inputText.trim();
    if (!trimmed || isGenerating) return;
    shouldAutoScrollRef.current = true;
    onMessageSend(trimmed);
    setInputText('');
  }, [inputText, isGenerating, onMessageSend]);

  const handleKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent?.isComposing) {
      e.preventDefault();
      handleSubmit();
    }
  };

  const handleDevFeatureToast = (featureName) => {
    Toast.info({
      content: `${t('功能开发中')}: ${featureName}`,
      duration: 2,
    });
  };

  return (
    <div
      className='relative flex flex-col w-full h-full overflow-hidden'
      style={{
        backgroundColor: 'var(--semi-color-bg-0)',
        color: 'var(--semi-color-text-0)',
      }}
    >
      {/* 顶部滚动对话区域（与新版 PlaygroundChat 一致的居中单栏 max-w-4xl 流式布局） */}
      <div
        ref={scrollContainerRef}
        onScroll={checkScrollPosition}
        onWheel={handleWheel}
        onTouchStart={handleTouchStart}
        className='flex-1 overflow-y-auto overscroll-contain'
      >
        <div className='mx-auto w-full max-w-4xl px-4 py-6 space-y-6'>
          {message.map((msg, index) => {
            const isUser = msg.role === 'user';
            const isLastAssistant =
              index === message.length - 1 && msg.role === 'assistant';

            return (
              <div
                key={msg.id || index}
                className={`group flex w-full flex-col ${
                  isUser ? 'items-end' : 'items-start'
                }`}
              >
                <div
                  className={
                    isUser
                      ? 'w-fit max-w-[85%] sm:max-w-[62ch] md:max-w-[68ch] lg:max-w-[72ch] rounded-3xl px-4 py-2.5'
                      : 'w-full max-w-none py-1'
                  }
                  style={
                    isUser
                      ? {
                          backgroundColor: 'var(--semi-color-fill-0)',
                          color: 'var(--semi-color-text-0)',
                          border: '1px solid var(--semi-color-border)',
                        }
                      : {
                          color: 'var(--semi-color-text-0)',
                        }
                  }
                >
                  {renderCustomChatContent({
                    message: msg,
                    className: '',
                  })}
                </div>

                {/* 消息操作按钮行（悬停显示，最后一条助手消息常驻显示） */}
                <div
                  className={`mt-1.5 transition-opacity ${
                    isLastAssistant || styleState.isMobile
                      ? 'opacity-100'
                      : 'opacity-0 group-hover:opacity-100'
                  }`}
                >
                  {renderChatBoxAction({ message: msg })}
                </div>
              </div>
            );
          })}
        </div>
      </div>

      {/* 回到底部悬浮按钮 */}
      {showScrollBottom && (
        <div className='absolute bottom-40 left-1/2 -translate-x-1/2 z-10'>
          <div className='nexus-reveal'>
            <Button
              theme='solid'
              type='tertiary'
              size='small'
              icon={<ArrowDown size={14} />}
              onClick={() => scrollToBottom(true)}
              className='!rounded-full shadow-md'
              style={{
                backgroundColor: 'var(--semi-color-bg-2)',
                color: 'var(--semi-color-text-0)',
                border: '1px solid var(--semi-color-border)',
              }}
            />
          </div>
        </div>
      )}

      {/* 底部居中输入区（对齐新版 PlaygroundInput 的卡片容器 + 工具栏 + 建议词） */}
      <div className='mx-auto w-full max-w-4xl px-3 sm:px-4 pb-3 sm:pb-4 pt-1 shrink-0'>
        <div
          className='rounded-2xl transition-shadow'
          style={{
            backgroundColor: 'var(--semi-color-bg-1)',
            border: '1px solid var(--semi-color-border)',
            boxShadow: '0 8px 30px rgba(0, 0, 0, 0.06)',
          }}
        >
          <div className='px-3 pt-2.5 pb-1'>
            <TextArea
              value={inputText}
              onChange={(val) => setInputText(val)}
              onKeyDown={handleKeyDown}
              disabled={isGenerating}
              autosize={{ minRows: 2, maxRows: 7 }}
              placeholder={t('Ask anything')}
              className='!border-0 !bg-transparent focus:!bg-transparent !p-1'
              style={{
                backgroundColor: 'transparent',
                border: 'none',
                boxShadow: 'none',
                fontSize: '15px',
              }}
            />
          </div>

          {/* 底部工具条：辅助操作与主操作自然留白，不再挤在两侧 */}
          <div className='playground-input-toolbar flex flex-wrap items-center justify-between gap-x-3 gap-y-2 px-3 py-2'>
            <div className='flex items-center gap-2'>
              <Dropdown
                trigger='click'
                position='topLeft'
                render={
                  <Dropdown.Menu>
                    <Dropdown.Item
                      icon={<FileText size={15} />}
                      onClick={() => handleDevFeatureToast(t('上传文件'))}
                    >
                      {t('上传文件')}
                    </Dropdown.Item>
                    <Dropdown.Item
                      icon={<ImageIcon size={15} />}
                      onClick={() => handleDevFeatureToast(t('上传图片'))}
                    >
                      {t('上传图片')}
                    </Dropdown.Item>
                    <Dropdown.Item
                      icon={<ScreenShare size={15} />}
                      onClick={() => handleDevFeatureToast(t('屏幕截图'))}
                    >
                      {t('屏幕截图')}
                    </Dropdown.Item>
                    <Dropdown.Item
                      icon={<Camera size={15} />}
                      onClick={() => handleDevFeatureToast(t('拍照'))}
                    >
                      {t('拍照')}
                    </Dropdown.Item>
                  </Dropdown.Menu>
                }
              >
                <Button
                  theme='outline'
                  type='tertiary'
                  size='small'
                  disabled={isGenerating}
                  icon={<Paperclip size={14} />}
                  className='!rounded-lg'
                >
                  <span className='hidden sm:inline'>{t('附件')}</span>
                </Button>
              </Dropdown>

              <Button
                theme='outline'
                type='tertiary'
                size='small'
                disabled={isGenerating}
                icon={<Globe size={14} />}
                onClick={() => handleDevFeatureToast(t('联网搜索'))}
                className='!rounded-lg'
              >
                <span className='hidden sm:inline'>{t('联网')}</span>
              </Button>

              {message.length > 0 && (
                <Tooltip content={t('清空对话')} position='top'>
                  <Button
                    theme='borderless'
                    type='tertiary'
                    size='small'
                    disabled={isGenerating}
                    icon={<Trash2 size={14} />}
                    onClick={onClearMessages}
                    className='!rounded-lg'
                  />
                </Tooltip>
              )}
            </div>

            <div className='playground-input-primary flex flex-wrap items-center gap-2'>
              <div className='playground-selector-group flex items-center gap-2'>
                {/* 分组名只放在按钮上，描述与倍率留在展开项 */}
                <Select
                  filter
                  size='small'
                  placeholder={t('Group')}
                  value={inputs.group}
                  optionList={playgroundGroupOptions(groups)}
                  disabled={isGenerating || groups.length === 0}
                  onChange={(val) => onInputChange('group', val)}
                  renderSelectedItem={(option) => option.value || t('Group')}
                  prefix={<Layers size={14} />}
                  renderOptionItem={renderGroupOption}
                  arrowIcon={<ChevronsUpDown size={14} />}
                  dropdownMatchSelectWidth={false}
                  dropdownStyle={{ width: 240, maxWidth: '80vw' }}
                  className='playground-selector !rounded-lg'
                />

                {/* 模型按钮按内容宽度伸缩，保留搜索 */}
                <Select
                  filter
                  size='small'
                  placeholder={t('Model')}
                  value={inputs.model}
                  optionList={models}
                  disabled={isGenerating || models.length === 0}
                  onChange={(val) => onInputChange('model', val)}
                  prefix={<Cpu size={14} />}
                  arrowIcon={<ChevronsUpDown size={14} />}
                  className='playground-selector playground-model-selector !rounded-lg'
                  dropdownMatchSelectWidth={false}
                  dropdownStyle={{ width: 300, maxWidth: '85vw' }}
                />
              </div>

              {/* 发送 / 停止按钮 */}
              {isGenerating ? (
                <Button
                  theme='solid'
                  type='danger'
                  size='small'
                  icon={<Square size={13} className='fill-current' />}
                  onClick={onStopGenerator}
                  className='!rounded-lg'
                >
                  <span className='hidden sm:inline'>{t('停止')}</span>
                </Button>
              ) : (
                <Button
                  theme='solid'
                  type='primary'
                  size='small'
                  disabled={!inputText.trim()}
                  icon={<Send size={13} />}
                  onClick={handleSubmit}
                  className='!rounded-lg'
                >
                  <span className='hidden sm:inline'>{t('发送')}</span>
                </Button>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

export default ChatArea;

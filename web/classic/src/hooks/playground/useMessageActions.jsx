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

import { useCallback } from 'react';
import { Toast } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { getTextContent } from '../../helpers';
import { ERROR_MESSAGES } from '../../constants/playground.constants';

export const useMessageActions = (
  message,
  setMessage,
  onMessageSend,
  canSend,
  getMessages,
  requestAction,
) => {
  const { t } = useTranslation();

  // 复制消息
  const handleMessageCopy = useCallback(
    (targetMessage) => {
      const textToCopy = getTextContent(targetMessage);

      if (!textToCopy) {
        Toast.warning({
          content: t(ERROR_MESSAGES.NO_TEXT_CONTENT),
          duration: 2,
        });
        return;
      }

      const copyToClipboard = async (text) => {
        if (navigator.clipboard?.writeText) {
          try {
            await navigator.clipboard.writeText(text);
            Toast.success({
              content: t('消息已复制到剪贴板'),
              duration: 2,
            });
          } catch (err) {
            console.error('Clipboard API 复制失败:', err);
            fallbackCopy(text);
          }
        } else {
          fallbackCopy(text);
        }
      };

      const fallbackCopy = (text) => {
        try {
          const textArea = document.createElement('textarea');
          textArea.value = text;
          textArea.style.cssText = `
          position: fixed;
          top: -9999px;
          left: -9999px;
          opacity: 0;
          pointer-events: none;
          z-index: -1;
        `;
          textArea.setAttribute('readonly', '');

          document.body.appendChild(textArea);
          textArea.select();
          textArea.setSelectionRange(0, text.length);

          const successful = document.execCommand('copy');
          document.body.removeChild(textArea);

          if (successful) {
            Toast.success({
              content: t('消息已复制到剪贴板'),
              duration: 2,
            });
          } else {
            throw new Error('execCommand copy failed');
          }
        } catch (err) {
          console.error('回退复制方案也失败:', err);

          let errorMessage = t(ERROR_MESSAGES.COPY_FAILED);
          if (
            window.location.protocol === 'http:' &&
            window.location.hostname !== 'localhost'
          ) {
            errorMessage = t(ERROR_MESSAGES.COPY_HTTPS_REQUIRED);
          } else if (!navigator.clipboard && !document.execCommand) {
            errorMessage = t(ERROR_MESSAGES.BROWSER_NOT_SUPPORTED);
          }

          Toast.error({
            content: errorMessage,
            duration: 4,
          });
        }
      };

      copyToClipboard(textToCopy);
    },
    [t],
  );

  // 重新生成消息
  const handleMessageReset = useCallback(
    (targetMessage) => {
      if (!canSend()) return;
      return requestAction(() => {
        if (!canSend()) return false;
        const previous = getMessages();
        let index = previous.findIndex((msg) => msg.id === targetMessage.id);
        if (index === -1) return false;
        while (index >= 0 && previous[index].role !== 'user') index--;
        if (index < 0) return false;
        // Preserve the original user's images and attachment text.
        return onMessageSend(
          getTextContent(previous[index]),
          undefined,
          previous.slice(0, index + 1),
        );
      });
    },
    [canSend, getMessages, onMessageSend, requestAction],
  );

  // 删除消息
  const handleMessageDelete = useCallback(
    (targetMessage) => {
      if (!canSend()) return;
      return requestAction(() => {
        if (!canSend()) return false;
        const previous = getMessages();
        const index = previous.findIndex((msg) => msg.id === targetMessage.id);
        if (index === -1) return false;
        const withReply = previous[index].role === 'user' &&
          previous[index + 1]?.role === 'assistant';
        setMessage(previous.filter((_, i) => i !== index && (!withReply || i !== index + 1)));
        Toast.success({
          content: t(withReply ? '已删除消息及其回复' : '消息已删除'),
          duration: 2,
        });
        return true;
      }, {
        title: t('确认删除'),
        content: t('确定要删除这条消息吗？'),
      });
    },
    [setMessage, t, canSend, getMessages, requestAction],
  );

  // 切换角色
  const handleRoleToggle = useCallback(
    (targetMessage) => {
      if (!canSend()) return;
      if (
        !(targetMessage.role === 'assistant' || targetMessage.role === 'system')
      ) {
        return;
      }

      return requestAction(() => {
        if (!canSend()) return false;
        const previous = getMessages();
        const target = previous.find((msg) => msg.id === targetMessage.id);
        if (!target || !['assistant', 'system'].includes(target.role)) return false;
        const newRole = target.role === 'assistant' ? 'system' : 'assistant';
        setMessage(previous.map((msg) => msg.id === target.id ? { ...msg, role: newRole } : msg));
        Toast.success({
          content: t(newRole === 'system' ? '已切换为System角色' : '已切换为Assistant角色'),
          duration: 2,
        });
        return true;
      });
    },
    [setMessage, t, canSend, getMessages, requestAction],
  );

  return {
    handleMessageCopy,
    handleMessageReset,
    handleMessageDelete,
    handleRoleToggle,
  };
};

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

import { useCallback, useState, useRef } from 'react';
import { Toast } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { getTextContent } from '../../helpers';
import { MESSAGE_ROLES } from '../../constants/playground.constants';
import { useEditGuard } from './useEditGuard';

export const useMessageEdit = (
  setMessage,
  submitEdited,
  canSend,
  getMessages,
  session,
) => {
  const { t } = useTranslation();
  const [editingMessageId, setEditingMessageId] = useState(null);
  const [editValue, setEditValue] = useState('');
  const editingMessageRef = useRef(null);

  const dirty = !!editingMessageId && editValue !== getTextContent(editingMessageRef.current);
  const { confirmDiscard } = useEditGuard(dirty, session);
  const resetEdit = useCallback(() => {
    setEditingMessageId(null);
    editingMessageRef.current = null;
    setEditValue('');
  }, []);
  const requestAction = useCallback(async (run, options) => {
    if (!(await confirmDiscard(options)) || !session.isCurrent()) return false;
    if (run() !== true) return false;
    resetEdit();
    return true;
  }, [confirmDiscard, resetEdit, session]);

  const handleMessageEdit = useCallback(
    (targetMessage) => {
      if (!canSend() || editingMessageId === targetMessage.id) return;
      void confirmDiscard().then((accepted) => {
        if (!accepted || !canSend()) return;
        const current = getMessages().find((msg) => msg.id === targetMessage.id);
        if (!current) return;
        setEditingMessageId(current.id);
        editingMessageRef.current = current;
        setEditValue(getTextContent(current));
      });
    },
    [canSend, editingMessageId, confirmDiscard, getMessages],
  );

  const handleEditSave = useCallback((submit = false) => {
    if (!canSend() || !editingMessageId || !editValue.trim()) return;
    const prevMessages = getMessages();
    let messageIndex = prevMessages.findIndex(
      (msg) => msg === editingMessageRef.current,
    );

    if (messageIndex === -1) {
      messageIndex = prevMessages.findIndex(
        (msg) => msg.id === editingMessageId,
      );
    }

    const targetMessage = prevMessages[messageIndex];
    if (!targetMessage) return;
    let newContent;

    if (Array.isArray(targetMessage.content)) {
      newContent = targetMessage.content.map((item) =>
        item.type === 'text' ? { ...item, text: editValue.trim() } : item,
      );
    } else {
      newContent = editValue.trim();
    }

    const updatedMessages = prevMessages.map((msg) =>
      msg.id === editingMessageId ? { ...msg, content: newContent } : msg,
    );

    if (submit && targetMessage.role === MESSAGE_ROLES.USER) {
      if (!submitEdited(updatedMessages.slice(0, messageIndex + 1))) return;
    } else {
      setMessage(updatedMessages);
    }

    resetEdit();
    Toast.success({ content: t('消息已更新'), duration: 2 });
  }, [
    editingMessageId,
    editValue,
    t,
    submitEdited,
    setMessage,
    canSend,
    getMessages,
    resetEdit,
  ]);

  const handleEditCancel = useCallback(() => requestAction(() => true), [requestAction]);

  return {
    editingMessageId,
    editValue,
    setEditValue,
    handleMessageEdit,
    handleEditSave,
    handleEditCancel,
    resetEdit,
    requestAction,
  };
};

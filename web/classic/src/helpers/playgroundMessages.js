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

import {
  createTextTrimmer,
  hasTextAttachment,
  isStoredImageUrl,
} from '../../../shared/playground-history-storage';

export const messageHistoryAdapter = {
  hasAttachments: (message) => typeof message.content === 'string'
    ? hasTextAttachment(message.content)
    : message.content.some((part) => part.type === 'image_url' ||
        (part.type === 'text' && hasTextAttachment(part.text))),
  trimText: (message, limit) => {
    const trim = createTextTrimmer(limit);
    return {
      ...message,
      content: typeof message.content === 'string' ? trim(message.content) :
        message.content.map((part) => part.type === 'text' ? { ...part, text: trim(part.text) } : part),
      reasoningContent: typeof message.reasoningContent === 'string' ? trim(message.reasoningContent) : message.reasoningContent,
    };
  },
  textLength: (message) => (typeof message.content === 'string' ? message.content.length :
    message.content.reduce((size, part) => size + (part.type === 'text' ? part.text.length : 0), 0)) +
    (message.reasoningContent?.length || 0),
};

const record = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);

export function restoreMessages(value) {
  const rows = Array.isArray(value) ? value : record(value) && Array.isArray(value.messages) ? value.messages : [];
  const ids = new Set();
  return rows.flatMap((row, index) => {
    if (!record(row) || !['user', 'assistant', 'system'].includes(row.role)) return [];
    let content = row.content;
    if (Array.isArray(content)) {
      content = content.flatMap((part) => {
        if (!record(part)) return [];
        if (part.type === 'text' && typeof part.text === 'string') return [{ type: 'text', text: part.text }];
        if (part.type === 'image_url' && record(part.image_url) && typeof part.image_url.url === 'string' && (isStoredImageUrl(part.image_url.url) || part.image_url.url === '[inline-image-omitted]')) return [{ type: 'image_url', image_url: { url: part.image_url.url } }];
        return [];
      });
    } else if (typeof content !== 'string') return [];
    let id = typeof row.id === 'string' && row.id ? row.id : `restored-${index}`;
    while (ids.has(id)) id += '-duplicate';
    ids.add(id);
    return [{
      id,
      role: row.role,
      content,
      status: row.status === 'error' ? 'error' : ['loading', 'streaming', 'incomplete'].includes(row.status) ? 'incomplete' : 'complete',
      reasoningContent: typeof row.reasoningContent === 'string' ? row.reasoningContent : null,
      errorCode: typeof row.errorCode === 'string' ? row.errorCode : null,
      isThinkingComplete: true,
      isReasoningExpanded: false,
      hasAutoCollapsed: true,
    }];
  });
}

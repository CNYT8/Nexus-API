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

/**
 * 操练场附件处理（纯前端，绝不上传到服务器）
 *
 * 设计要点：
 * 1. 文本文件（.md/.txt 等）在浏览器内用 FileReader 读成纯文本，直接拼进消息正文，
 *    因此上游模型能真正读到文件内容，且服务端不存储任何文件。
 * 2. 图片在浏览器内读成 data URL（data:image/png;base64,...），走项目既有的
 *    OpenAI 多模态 image_url 通道，后端 relay 原生支持 data URL。
 * 3. 安全边界：只接受白名单扩展名 + 白名单 MIME；文件内容不会被当作代码执行
 *    （这里只做文本读取或 base64 编码），任何文件都不会离开浏览器，也不会被服务端落盘。
 */

/** 允许上传的文本文件扩展名 */
export const TEXT_FILE_EXTENSIONS = [
  '.txt',
  '.md',
  '.markdown',
  '.csv',
  '.json',
  '.log',
];

/** 文本文件大小上限（字节）。超出则拒绝，避免把超长内容塞进上下文。 */
export const MAX_TEXT_FILE_BYTES = 2 * 1024 * 1024; // 2MB

/** 图片文件大小上限（字节）。base64 后约膨胀 4/3，需要给请求体留足余量。 */
export const MAX_IMAGE_FILE_BYTES = 5 * 1024 * 1024; // 5MB

/** 允许的图片 MIME 前缀 */
const ALLOWED_IMAGE_MIME_PREFIX = 'image/';

/** 图片 MIME 白名单：只接受浏览器可安全渲染的通用位图格式 */
const ALLOWED_IMAGE_MIMES = new Set([
  'image/png',
  'image/jpeg',
  'image/jpg',
  'image/webp',
  'image/gif',
  'image/bmp',
]);

/** 文本文件 MIME 白名单（部分系统对 .md 返回空 MIME，因此额外按扩展名兜底判断） */
const ALLOWED_TEXT_MIMES = new Set([
  'text/plain',
  'text/markdown',
  'text/x-markdown',
  'text/csv',
  'application/json',
  'text/json',
  'application/octet-stream', // 部分系统对 .md/.log 返回该值，由扩展名二次确认
  '',
]);

export const TEXT_FILE_ACCEPT = TEXT_FILE_EXTENSIONS.join(',');
export const IMAGE_FILE_ACCEPT =
  'image/png,image/jpeg,image/webp,image/gif,image/bmp';

/** 取小写扩展名（含点） */
function getExtension(fileName) {
  const name = typeof fileName === 'string' ? fileName : '';
  const dotIndex = name.lastIndexOf('.');
  if (dotIndex <= 0) return '';
  return name.slice(dotIndex).toLowerCase();
}

/** 人类可读的文件大小 */
export function formatFileSize(bytes) {
  const size = Number(bytes);
  if (!Number.isFinite(size) || size <= 0) return '0 B';
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(2)} MB`;
}

/**
 * 判断文件是否为允许的文本文件。
 * 扩展名必须命中白名单；MIME 若明确不是文本类则拒绝。
 */
export function isAllowedTextFile(file) {
  if (!file) return false;
  const ext = getExtension(file.name);
  if (!TEXT_FILE_EXTENSIONS.includes(ext)) return false;

  const mime = String(file.type || '').toLowerCase();
  if (!ALLOWED_TEXT_MIMES.has(mime)) {
    // 浏览器给出的 MIME 不在白名单，但扩展名合法时仍允许（避免误杀 .md/.log）
    return false;
  }
  return true;
}

/** 判断文件是否为允许的图片 */
export function isAllowedImageFile(file) {
  if (!file) return false;
  const mime = String(file.type || '').toLowerCase();
  if (!mime.startsWith(ALLOWED_IMAGE_MIME_PREFIX)) return false;
  return ALLOWED_IMAGE_MIMES.has(mime);
}

/** 读取文本文件为字符串 */
function readFileAsText(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ''));
    reader.onerror = () => reject(reader.error || new Error('read failed'));
    reader.readAsText(file, 'utf-8');
  });
}

/** 读取图片文件为 data URL */
function readFileAsDataURL(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ''));
    reader.onerror = () => reject(reader.error || new Error('read failed'));
    reader.readAsDataURL(file);
  });
}

/**
 * 解析文本文件附件。
 * @returns {Promise<{ok:true,name:string,size:number,text:string}|{ok:false,reason:string,name:string}>}
 */
export async function parseTextAttachment(file) {
  const name = file?.name || '';
  if (!isAllowedTextFile(file)) {
    return { ok: false, name, reason: 'unsupported_type' };
  }
  if (file.size > MAX_TEXT_FILE_BYTES) {
    return { ok: false, name, reason: 'too_large' };
  }

  try {
    const text = await readFileAsText(file);
    return { ok: true, name, size: file.size, text };
  } catch {
    return { ok: false, name, reason: 'read_failed' };
  }
}

/**
 * 解析图片文件附件。
 * @returns {Promise<{ok:true,name:string,size:number,dataUrl:string}|{ok:false,reason:string,name:string}>}
 */
export async function parseImageAttachment(file) {
  const name = file?.name || '';
  if (!isAllowedImageFile(file)) {
    return { ok: false, name, reason: 'unsupported_type' };
  }
  if (file.size > MAX_IMAGE_FILE_BYTES) {
    return { ok: false, name, reason: 'too_large' };
  }

  try {
    const dataUrl = await readFileAsDataURL(file);
    if (!dataUrl.startsWith('data:image/')) {
      return { ok: false, name, reason: 'unsupported_type' };
    }
    return { ok: true, name, size: file.size, dataUrl };
  } catch {
    return { ok: false, name, reason: 'read_failed' };
  }
}

/**
 * 把多个文本附件合并成一段可直接放进消息正文的文本。
 * 使用清晰的定界符，模型能明确区分「用户的问题」与「附件内容」。
 */
export function buildAttachmentTextBlock(attachments) {
  const list = Array.isArray(attachments) ? attachments : [];
  const valid = list.filter(
    (item) => item && item.ok !== false && typeof item.text === 'string',
  );
  if (valid.length === 0) return '';

  return valid
    .map(
      (item) =>
        `--- 附件: ${item.name} (${formatFileSize(item.size)}) ---\n${item.text}\n--- 附件结束: ${item.name} ---`,
    )
    .join('\n\n');
}

/**
 * 从消息 content 中剥离内联 base64 图片，避免把大体积 data URL 写进 localStorage。
 * 历史记录里只保留占位标记，重新加载时不会撑爆浏览器存储配额。
 */
export function stripInlineImageDataForStorage(messages) {
  if (!Array.isArray(messages)) return messages;

  return messages.map((message) => {
    const content = message?.content;
    if (!Array.isArray(content)) return message;

    const nextContent = content.map((part) => {
      if (
        part &&
        part.type === 'image_url' &&
        part.image_url &&
        typeof part.image_url.url === 'string' &&
        part.image_url.url.startsWith('data:')
      ) {
        return {
          ...part,
          image_url: { url: '[inline-image-omitted]' },
        };
      }
      return part;
    });

    return { ...message, content: nextContent };
  });
}

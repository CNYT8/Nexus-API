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
  STORAGE_KEYS,
  DEFAULT_CONFIG,
} from '../../constants/playground.constants';
import { stripInlineImageDataForStorage } from '../../helpers/playgroundAttachments';

const LEGACY_SHARED_KEYS = [
  ...Object.values(STORAGE_KEYS),
  'playground_parameter_enabled',
];

const getStorageKey = (baseKey) => {
  try {
    const rawUser = localStorage.getItem('user');
    if (!rawUser) return null;
    const userId = JSON.parse(rawUser)?.id;
    if (!userId) return null;
    for (const legacyKey of LEGACY_SHARED_KEYS) {
      localStorage.removeItem(legacyKey);
    }
    return `${baseKey}:classic:u${userId}`;
  } catch {
    return null;
  }
};
/**
 * 保存配置到 localStorage
 * @param {Object} config - 要保存的配置对象
 */
export const saveConfig = (config) => {
  try {
    const sanitizedInputs = config?.inputs
      ? {
          ...config.inputs,
          imageUrls: Array.isArray(config.inputs.imageUrls)
            ? config.inputs.imageUrls.filter(
                (url) =>
                  typeof url === 'string' &&
                  url.trim() !== '' &&
                  !url.startsWith('data:'),
              )
            : [],
        }
      : config?.inputs;
    const configToSave = {
      ...config,
      ...(sanitizedInputs ? { inputs: sanitizedInputs } : {}),
      timestamp: new Date().toISOString(),
    };
    const key = getStorageKey(STORAGE_KEYS.CONFIG);
    if (!key) return;
    localStorage.setItem(key, JSON.stringify(configToSave));
  } catch (error) {
    console.error('保存配置失败:', error);
  }
};

/**
 * 保存消息到 localStorage
 * @param {Array} messages - 要保存的消息数组
 */
export const saveMessages = (messages) => {
  try {
    // 内联 base64 图片体积很大（单张可达数 MB），直接写入 localStorage 会撑爆配额
    // 导致整个历史记录保存失败；存入前先剔除图片数据，仅保留占位标记。
    const messagesToSave = {
      messages: stripInlineImageDataForStorage(messages),
      timestamp: new Date().toISOString(),
    };
    const key = getStorageKey(STORAGE_KEYS.MESSAGES);
    if (!key) return;
    localStorage.setItem(key, JSON.stringify(messagesToSave));
  } catch (error) {
    console.error('保存消息失败:', error);
  }
};

/**
 * 从 localStorage 加载配置
 * @returns {Object} 配置对象，如果不存在则返回默认配置
 */
export const loadConfig = () => {
  try {
    const key = getStorageKey(STORAGE_KEYS.CONFIG);
    const savedConfig = key ? localStorage.getItem(key) : null;
    if (savedConfig) {
      const parsedConfig = JSON.parse(savedConfig);
      const parsedMaxTokens = parseInt(parsedConfig?.inputs?.max_tokens, 10);

      const mergedConfig = {
        inputs: {
          ...DEFAULT_CONFIG.inputs,
          ...parsedConfig.inputs,
          max_tokens: Number.isNaN(parsedMaxTokens)
            ? parsedConfig?.inputs?.max_tokens
            : parsedMaxTokens,
        },
        parameterEnabled: {
          ...DEFAULT_CONFIG.parameterEnabled,
          ...parsedConfig.parameterEnabled,
        },
        showDebugPanel:
          parsedConfig.showDebugPanel || DEFAULT_CONFIG.showDebugPanel,
        customRequestMode:
          parsedConfig.customRequestMode || DEFAULT_CONFIG.customRequestMode,
        customRequestBody:
          parsedConfig.customRequestBody || DEFAULT_CONFIG.customRequestBody,
      };

      return mergedConfig;
    }
  } catch (error) {
    console.error('加载配置失败:', error);
  }

  return DEFAULT_CONFIG;
};

/**
 * 从 localStorage 加载消息
 * @returns {Array} 消息数组，如果不存在则返回 null
 */
export const loadMessages = () => {
  try {
    const key = getStorageKey(STORAGE_KEYS.MESSAGES);
    const savedMessages = key ? localStorage.getItem(key) : null;
    if (savedMessages) {
      const parsedMessages = JSON.parse(savedMessages);
      return parsedMessages.messages || null;
    }
  } catch (error) {
    console.error('加载消息失败:', error);
  }

  return null;
};

/**
 * 清除保存的配置
 */
export const clearConfig = () => {
  try {
    const configKey = getStorageKey(STORAGE_KEYS.CONFIG);
    const messagesKey = getStorageKey(STORAGE_KEYS.MESSAGES);
    if (configKey) localStorage.removeItem(configKey);
    if (messagesKey) localStorage.removeItem(messagesKey); // 同时清除消息
  } catch (error) {
    console.error('清除配置失败:', error);
  }
};

/**
 * 清除保存的消息
 */
export const clearMessages = () => {
  try {
    const key = getStorageKey(STORAGE_KEYS.MESSAGES);
    if (key) localStorage.removeItem(key);
  } catch (error) {
    console.error('清除消息失败:', error);
  }
};

/**
 * 检查是否有保存的配置
 * @returns {boolean} 是否存在保存的配置
 */
export const hasStoredConfig = () => {
  try {
    const key = getStorageKey(STORAGE_KEYS.CONFIG);
    return key ? localStorage.getItem(key) !== null : false;
  } catch (error) {
    console.error('检查配置失败:', error);
    return false;
  }
};

/**
 * 获取配置的最后保存时间
 * @returns {string|null} 最后保存时间的 ISO 字符串
 */
export const getConfigTimestamp = () => {
  try {
    const key = getStorageKey(STORAGE_KEYS.CONFIG);
    const savedConfig = key ? localStorage.getItem(key) : null;
    if (savedConfig) {
      const parsedConfig = JSON.parse(savedConfig);
      return parsedConfig.timestamp || null;
    }
  } catch (error) {
    console.error('获取配置时间戳失败:', error);
  }
  return null;
};

/**
 * 导出配置为 JSON 文件（包含消息）
 * @param {Object} config - 要导出的配置
 * @param {Array} messages - 要导出的消息
 */
export const exportConfig = (config, messages = null) => {
  try {
    const configToExport = {
      ...config,
      messages: messages || loadMessages(), // 包含消息数据
      exportTime: new Date().toISOString(),
      version: '1.0',
    };

    const dataStr = JSON.stringify(configToExport, null, 2);
    const dataBlob = new Blob([dataStr], { type: 'application/json' });

    const link = document.createElement('a');
    link.href = URL.createObjectURL(dataBlob);
    link.download = `playground-config-${new Date().toISOString().split('T')[0]}.json`;
    link.click();

    URL.revokeObjectURL(link.href);
  } catch (error) {
    console.error('导出配置失败:', error);
  }
};

/**
 * 从文件导入配置（包含消息）
 * @param {File} file - 包含配置的 JSON 文件
 * @returns {Promise<Object>} 导入的配置对象
 */
export const importConfig = (file) => {
  return new Promise((resolve, reject) => {
    try {
      const reader = new FileReader();
      reader.onload = (e) => {
        try {
          const importedConfig = JSON.parse(e.target.result);

          if (importedConfig.inputs && importedConfig.parameterEnabled) {
            // 如果导入的配置包含消息，也一起导入
            if (
              importedConfig.messages &&
              Array.isArray(importedConfig.messages)
            ) {
              saveMessages(importedConfig.messages);
            }

            resolve(importedConfig);
          } else {
            reject(new Error('配置文件格式无效'));
          }
        } catch (parseError) {
          reject(new Error('解析配置文件失败: ' + parseError.message));
        }
      };
      reader.onerror = () => reject(new Error('读取文件失败'));
      reader.readAsText(file);
    } catch (error) {
      reject(new Error('导入配置失败: ' + error.message));
    }
  });
};

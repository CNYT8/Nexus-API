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

export const GLOBAL_SIDEBAR_DEFAULT_CONFIG = {
  chat: {
    enabled: true,
    playground: true,
    chat: true,
  },
  console: {
    enabled: true,
    detail: true,
    token: true,
    log: true,
    model_monitor: true,
    midjourney: true,
    task: true,
  },
  personal: {
    enabled: true,
    topup: true,
    personal: true,
  },
};

export const GLOBAL_SIDEBAR_SECTION_CONFIGS = [
  {
    key: 'chat',
    title: '聊天区域',
    description: '操练场和聊天功能',
    modules: [
      {
        key: 'playground',
        title: '操练场',
        description: 'AI模型测试环境',
      },
      {
        key: 'chat',
        title: '聊天',
        description: '聊天会话管理',
      },
    ],
  },
  {
    key: 'console',
    title: '控制台区域',
    description: '数据管理和日志查看',
    modules: [
      {
        key: 'detail',
        title: '数据看板',
        description: '系统数据统计',
      },
      {
        key: 'token',
        title: '令牌管理',
        description: 'API令牌管理',
      },
      {
        key: 'log',
        title: '使用日志',
        description: 'API使用记录',
      },
      {
        key: 'model_monitor',
        title: '模型监控',
        description: '全局模型体验评分',
      },
      {
        key: 'midjourney',
        title: '绘图日志',
        description: '绘图任务记录',
      },
      {
        key: 'task',
        title: '任务日志',
        description: '系统任务记录',
      },
    ],
  },
  {
    key: 'personal',
    title: '个人中心区域',
    description: '用户个人功能',
    modules: [
      {
        key: 'topup',
        title: '钱包管理',
        description: '余额充值管理',
      },
      {
        key: 'personal',
        title: '个人设置',
        description: '个人信息设置',
      },
    ],
  },
];

const deepClone = (value) => JSON.parse(JSON.stringify(value));

export const cloneGlobalSidebarAdminConfig = () =>
  deepClone(GLOBAL_SIDEBAR_DEFAULT_CONFIG);

export const mergeGlobalSidebarAdminConfig = (savedConfig) => {
  const merged = cloneGlobalSidebarAdminConfig();
  if (!savedConfig || typeof savedConfig !== 'object') return merged;

  for (const [sectionKey, sectionConfig] of Object.entries(savedConfig)) {
    if (!sectionConfig || typeof sectionConfig !== 'object') continue;
    if (sectionKey === 'admin') continue;

    if (!merged[sectionKey]) {
      merged[sectionKey] = { ...sectionConfig };
      continue;
    }

    merged[sectionKey] = { ...merged[sectionKey], ...sectionConfig };
  }

  return merged;
};

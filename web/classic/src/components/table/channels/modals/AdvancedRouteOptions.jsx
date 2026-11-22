import React from 'react';
import { Switch, TextArea } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';

// Keep the JSON editor authoritative, including unknown extension fields.
export default function AdvancedRouteOptions({ value, onChange }) {
  const { t } = useTranslation();
  let config;
  try {
    config = JSON.parse(value || '{}');
  } catch {
    return null;
  }
  if (!Array.isArray(config?.advanced_routes)) return null;

  const updateRoute = (index, patch) => {
    const routes = [...config.advanced_routes];
    routes[index] = { ...routes[index], ...patch };
    onChange(JSON.stringify({ ...config, advanced_routes: routes }, null, 2));
  };

  return (
    <div className='space-y-3'>
      {config.advanced_routes.map((route, index) => {
        if (
          !route ||
          typeof route !== 'object' ||
          typeof route.incoming_path !== 'string' ||
          route.incoming_path.trim() === '/v1/models'
        ) return null;
        const converter = typeof route.converter === 'string' ? route.converter.trim() : '';
        const native = converter === '' || converter === 'none';
        const models = Array.isArray(route.models)
          ? route.models.filter((model) => typeof model === 'string')
          : [];
        return (
          <div key={index} className='rounded border p-3 space-y-2'>
            <div className='text-sm font-medium'>{index + 1}. {route.incoming_path}</div>
            <label className='block text-sm'>
              {t('Client model rules')}
              <TextArea
                value={models.join('\n')}
                onChange={(text) => updateRoute(index, { models: text.split('\n') })}
                rows={2}
                aria-label={t('Client model rules')}
              />
            </label>
            <p className='text-xs text-gray-500'>
              {t('One rule per line: exact model name or re:expression. Leave empty for the final fallback.')}
            </p>
            <label className='flex items-center gap-2 text-sm'>
              <Switch
                checked={native && route.pass_through_body_enabled === true}
                disabled={!native}
                onChange={(checked) => updateRoute(index, { pass_through_body_enabled: checked })}
                aria-label={t('Route body passthrough')}
              />
              {t('Route body passthrough')}
            </label>
            <p className='text-xs text-gray-500'>
              {t('Only native forwarding supports route body passthrough.')}
            </p>
          </div>
        );
      })}
    </div>
  );
}

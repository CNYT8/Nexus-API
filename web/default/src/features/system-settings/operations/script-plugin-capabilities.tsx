import { useTranslation } from 'react-i18next'
import { metaRecord } from './script-plugin-management-api'

export function ScriptPluginCapabilities({ meta }: { meta: unknown }) {
  const { t } = useTranslation()
  const value = metaRecord(meta)
  const fields: [string, unknown][] = value ? [
    [t('插件 API 版本'), value.apiVersion],
    [t('支持模型'), value.models],
    [t('渠道类型'), value.channelTypes],
    [t('支持协议'), value.protocols],
    [t('原生路由'), value.routes],
    [t('任务查询模式'), value.fetchMode],
    [t('提交响应类型'), value.submitResponseTypes],
    [t('网络访问域名'), value.allowedHosts],
    [t('认证方式'), value.auth],
    [t('所需能力'), value.requiredCapabilities],
    [t('计费用量字段'), value.usageSchema],
    [t('按模型计费用量'), value.usageProfiles],
  ] : []
  return (
    <div className='min-w-0 space-y-3 rounded-md border p-3 text-sm'>
      <h6 className='font-semibold'>{t('插件能力与权限')}</h6>
      <p className='text-muted-foreground'>
        {t('支持任务提交与查询、流式提交响应、文件产物、协议展示及按模型提取计费用量；具体能力取决于插件声明和服务端校验。')}
      </p>
      <p className='text-muted-foreground'>
        {t('插件可处理请求内容并按网络权限访问外部服务；仅安装可信来源，绑定前核对模型、渠道、认证和计费字段。')}
      </p>
      <p className='text-muted-foreground'>
        {t('绑定固定到修订 ID 和摘要；启停或激活不会自动迁移已有绑定，历史任务继续使用原修订。')}
      </p>
      {!value ? <p role='status'>{t('暂无可读取的能力声明')}</p> : (
        <dl className='grid min-w-0 gap-3 sm:grid-cols-2'>
          {fields.map(([label, content]) => (
            <div key={label} className='min-w-0'>
              <dt className='font-medium'>{label}</dt>
              <dd className='mt-1'>
                {content === undefined || content === null ? t('未声明') : (
                  <pre className='max-h-40 overflow-auto text-xs break-all whitespace-pre-wrap'>
                    {typeof content === 'string' ? content : JSON.stringify(content, null, 2)}
                  </pre>
                )}
              </dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  )
}

import { API } from '../../../helpers';

export const scriptHooks = [
  'resolveRequest', 'buildSubmitRequest', 'parseSubmitResponse',
  'parseSubmitEvent', 'parseSubmitEventDelta', 'extractUsage',
  'extractUsageOnSubmit', 'extractUsageOnComplete', 'buildQueryRequest',
  'parseTaskResult', 'buildBatchQueryRequest', 'parseBatchResult',
  'listArtifacts', 'buildContentRequest', 'renderers.openai_video',
];
const endpoint = '/api/task/script_plugin';
const byteLength = (text) => new TextEncoder().encode(text).byteLength;
function unwrap(body) {
  if (body?.success !== true) throw body;
  return body.data;
}
export function metaRecord(value) {
  try {
    const parsed = typeof value === 'string' ? JSON.parse(value) : value;
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : null;
  } catch { return null; }
}
function secureMarketUrl(value) {
  const url = new URL(value);
  if (byteLength(value) > 4096 || url.protocol !== 'https:' || url.username || url.password || url.hash)
    throw new Error('Invalid marketplace URL');
  return url.href;
}
export function validateSources(value, t) {
  if (!Array.isArray(value) || value.length > 32)
    throw new Error(t('市场源最多配置 32 项'));
  const seen = new Set();
  const rows = value.map((item) => {
    const source = metaRecord(item);
    if (!source || typeof source.id !== 'string' || !/^[a-zA-Z0-9_-]{1,64}$/.test(source.id) ||
        typeof source.name !== 'string' || !source.name.trim() || byteLength(source.name.trim()) > 128 ||
        typeof source.url !== 'string' || !source.url.trim() || typeof source.enabled !== 'boolean')
      throw new Error(t('请填写有效的市场源标识、名称和地址；标识限 64 位字母、数字、下划线或连字符，名称限 128 字节'));
    let url;
    try { url = secureMarketUrl(source.url.trim()); }
    catch { throw new Error(t('市场地址必须是无凭据的 HTTPS 地址，且不超过 4096 字节')); }
    if (seen.has(source.id)) throw new Error(t('市场源标识不能重复'));
    seen.add(source.id);
    return { id: source.id, name: source.name.trim(), url, enabled: source.enabled };
  });
  if (byteLength(JSON.stringify({ sources: rows })) > 128 << 10)
    throw new Error(t('市场源配置不能超过 128 KiB'));
  return rows;
}
export async function listMarketSources(config, t) {
  const response = await API.get(`${endpoint}/marketplace/sources`, config);
  return validateSources(unwrap(response.data), t);
}
export async function saveMarketSources(sources, config) {
  const response = await API.put(`${endpoint}/marketplace/sources`, { sources }, config);
  unwrap(response.data);
}
export async function listActivations(config, t) {
  const response = await API.get(`${endpoint}/activations`, config);
  const rows = unwrap(response.data);
  const keys = new Set();
  if (!Array.isArray(rows) || rows.some((row) => {
    const entry = metaRecord(row);
    if (!entry || typeof entry.plugin_key !== 'string' || keys.has(entry.plugin_key) ||
        !Number.isSafeInteger(entry.plugin_id) || entry.plugin_id <= 0) return true;
    keys.add(entry.plugin_key);
    return false;
  })) throw new Error(t('插件激活状态格式无效'));
  return rows;
}
export async function changeScriptRevision(id, action, expected, config) {
  const response = action === 'activate'
    ? await API.post(`${endpoint}/${id}/activate`, { expected_plugin_id: expected }, config)
    : await API.delete(`${endpoint}/${id}`, config);
  unwrap(response.data);
}
export function parseDryRun(hook, input, fixtures, t) {
  if (!/^[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*){0,8}$/.test(hook))
    throw new Error(t('请选择或输入有效的插件函数'));
  let args;
  let responses;
  try { args = JSON.parse(input); }
  catch { throw new Error(t('试运行参数必须是最多 8 项的 JSON 数组')); }
  if (!Array.isArray(args) || args.length > 8) throw new Error(t('试运行参数必须是最多 8 项的 JSON 数组'));
  try { responses = JSON.parse(fixtures); }
  catch { throw new Error(t('模拟响应必须是最多 16 项的 JSON 数组')); }
  if (!Array.isArray(responses) || responses.length > 16)
    throw new Error(t('模拟响应必须是最多 16 项的 JSON 数组'));
  const fetch_responses = responses.map((item) => {
    const row = metaRecord(item);
    const headers = row && metaRecord(row.headers);
    if (!row || typeof row.url !== 'string' || !row.url || typeof row.method !== 'string' || !/^[A-Za-z]+$/.test(row.method) ||
        !Number.isInteger(row.status) || row.status < 100 || row.status > 599 ||
        typeof row.body !== 'string' || byteLength(row.body) > 1 << 20 || !headers || Object.values(headers).some((value) => typeof value !== 'string'))
      throw new Error(t('模拟响应需包含有效的 url、method、status、headers 和 body，正文不能超过 1 MiB'));
    return { url: row.url, method: row.method.toUpperCase(), status: row.status, headers, body: row.body };
  });
  const [name, ...members] = hook.split('.');
  const payload = { hook: name, members, args, fetch_responses };
  if (byteLength(JSON.stringify({ ...payload, plugin_id: Number.MAX_SAFE_INTEGER })) > 3 << 20)
    throw new Error(t('试运行请求不能超过 3 MiB'));
  return payload;
}
export async function runScriptRevision(id, payload, config) {
  const response = await API.post(`${endpoint}/dryrun`, { ...payload, plugin_id: id }, config);
  return unwrap(response.data);
}
export async function loadMarketIndex(sourceId, config, t) {
  const response = await API.get(`${endpoint}/marketplace/catalog`, { ...config, params: { source_id: sourceId } });
  const data = metaRecord(unwrap(response.data));
  const source = data && validateSources([data.source], t)[0];
  if (!data || !source || source.id !== sourceId || !source.enabled || !Array.isArray(data.plugins) || data.plugins.length > 512)
    throw new Error(t('市场索引格式无效'));
  const seen = new Set();
  const plugins = data.plugins.map((value) => {
    const entry = metaRecord(value);
    if (!entry || typeof entry.key !== 'string' || !entry.key || byteLength(entry.key) > 30 ||
        typeof entry.version !== 'string' || !entry.version || byteLength(entry.version) > 64 ||
        typeof entry.name !== 'string' || typeof entry.description !== 'string' ||
        typeof entry.sha256 !== 'string' || !/^[a-f\d]{64}$/.test(entry.sha256) || typeof entry.source_url !== 'string')
      throw new Error(t('市场索引格式无效'));
    const key = JSON.stringify([entry.key, entry.version]);
    let url;
    try { url = secureMarketUrl(entry.source_url); } catch { throw new Error(t('市场索引格式无效')); }
    if (seen.has(key) || new URL(url).host.toLowerCase() !== new URL(source.url).host.toLowerCase())
      throw new Error(t('市场索引格式无效'));
    seen.add(key);
    return entry;
  });
  return { source, plugins };
}
export async function installMarketRevision(sourceId, entry, config, t) {
  const response = await API.post(`${endpoint}/marketplace/install`, {
    source_id: sourceId, key: entry.key, version: entry.version, sha256: entry.sha256, enabled: false,
  }, config);
  const revision = metaRecord(unwrap(response.data));
  if (!revision || !Number.isSafeInteger(revision.id) || revision.id <= 0 || revision.key !== entry.key ||
      revision.version !== entry.version || revision.digest !== entry.sha256 || revision.enabled !== false)
    throw new Error(t('插件安装响应无效，请刷新修订列表确认结果'));
  return revision.id;
}

// Local, bounded interpreter. Keep byte-identical to default/pricing/lib/request-simulation.js.
const VARIABLES = ['p', 'c', 'len', 'cr', 'cc', 'cc1h', 'cc12h', 'img', 'img_o', 'ai', 'ao'];
const ARITY = { tier: 2, call: 1, header: 1, param: 1, has: 2, hour: 1, minute: 1, weekday: 1, month: 1, day: 1, cn_holiday: 1, cn_offday: 1, cn_workday: 1, min: 2, max: 2, abs: 1, ceil: 1, floor: 1 };
const PRECEDENCE = { '||': 10, '&&': 15, '==': 20, '!=': 20, '<': 20, '<=': 20, '>': 20, '>=': 20, '+': 30, '-': 30, '*': 60, '/': 60, '%': 60, '**': 100 };
const own = (value, key) => Object.prototype.hasOwnProperty.call(value, key);
const fail = (code) => { throw new Error(code); };
const number = (value) => typeof value === 'number' && Number.isFinite(value) ? value : fail('expression');
const boolean = (value) => typeof value === 'boolean' ? value : fail('expression');
const string = (value) => typeof value === 'string' ? value : fail('expression');

function compile(source) {
  if (!source.trim() || source.length > 8192) fail('expression');
  // Match pkg/billingexpr.ParseExprVersion; unsupported versions still fail.
  if (source.startsWith('v1:')) source = source.slice(3);
  const tokens = [];
  const pattern = /\s+|(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[A-Za-z_][A-Za-z_0-9]*|\*\*|&&|\|\||==|!=|<=|>=|[+\-*/%<>()!?:,]/gy;
  let offset = 0;
  while (offset < source.length) {
    pattern.lastIndex = offset;
    const match = pattern.exec(source);
    if (!match || tokens.length >= 2048) fail('expression');
    offset = pattern.lastIndex;
    if (!/^\s+$/.test(match[0])) tokens.push(match[0]);
  }
  let index = 0;
  let depth = 0;
  const take = (value) => tokens[index] === value ? (index++, true) : false;
  const expect = (value) => { if (!take(value)) fail('expression'); };
  function parse(min = 0) {
    if (++depth > 128) fail('expression');
    const token = tokens[index++];
    let left;
    if (token === '(') {
      left = parse();
      expect(')');
    } else if (['!', 'not', '-', '+'].includes(token)) {
      left = ['unary', token, parse(token === '!' || token === 'not' ? 50 : 90)];
    } else if (/^(?:\d|\.\d)/.test(token || '')) {
      left = ['literal', number(Number(token))];
    } else if (token?.startsWith('"') || token?.startsWith("'")) {
      try {
        if (token[0] === "'" && /[\\\n\r]/.test(token)) fail('expression');
        const text = token[0] === '"' ? JSON.parse(token) : token.slice(1, -1);
        left = ['literal', text];
      } catch { fail('expression'); }
    } else if (['true', 'false', 'nil', 'null'].includes(token)) {
      left = ['literal', token === 'true' ? true : token === 'false' ? false : null];
    } else if (own(ARITY, token) && take('(')) {
      const args = [];
      if (!take(')')) {
        do { args.push(parse()); } while (take(','));
        expect(')');
      }
      if (args.length !== ARITY[token]) fail('expression');
      left = ['call', token, args];
    } else if (VARIABLES.includes(token)) {
      left = ['variable', token];
    } else { fail('expression'); }
    while (index < tokens.length) {
      const op = ({ and: '&&', or: '||' })[tokens[index]] || tokens[index];
      const precedence = PRECEDENCE[op];
      if (!precedence || precedence < min) break;
      index++;
      left = ['binary', op, left, parse(precedence + (op === '**' ? 0 : 1))];
    }
    if (min === 0 && take('?')) {
      const yes = parse();
      expect(':');
      left = ['conditional', left, yes, parse()];
    }
    depth--;
    return left;
  }
  const result = parse();
  if (index !== tokens.length) fail('expression');
  return result;
}

function readPath(body, path) {
  path = string(path).trim();
  if (!path) return null;
  if (/[*?\[\]()|@!]/.test(path)) fail('path');
  const parts = [];
  let part = '';
  for (let i = 0; i < path.length; i++) {
    if (path[i] === '\\') {
      if (++i === path.length) fail('path');
      part += path[i];
    } else if (path[i] === '.') { parts.push(part); part = ''; }
    else part += path[i];
  }
  parts.push(part);
  let value = body;
  for (let i = 0; i < parts.length; i++) {
    const key = parts[i];
    if (key === '#') {
      if (i !== parts.length - 1) fail('path');
      return Array.isArray(value) ? value.length : null;
    }
    if (Array.isArray(value)) {
      if (!/^\d+$/.test(key)) return null;
      const index = Number(key);
      value = Number.isSafeInteger(index) && index < value.length ? value[index] : null;
    } else {
      if (!value || typeof value !== 'object' || !own(value, key)) return null;
      value = value[key];
    }
  }
  return value ?? null;
}

// Mirrors pkg/billingexpr/cn_holiday.go, including its fallback outside 2024–2030.
const HOLIDAYS = {
  2023: ['1230-1231'],
  2024: ['0101', '0210-0217', '0404-0406', '0501-0505', '0608-0610', '0915-0917', '1001-1007'],
  2025: ['0101', '0128-0131', '0201-0204', '0404-0406', '0501-0505', '0531', '0601-0602', '1001-1008'],
  2026: ['0101-0103', '0215-0223', '0404-0406', '0501-0505', '0619-0621', '0925-0927', '1001-1007'],
  2027: ['0101-0103', '0205-0212', '0403-0405', '0501-0505', '0609-0611', '0915-0917', '1001-1007'],
  2028: ['0101-0103', '0125-0131', '0201', '0402-0404', '0501-0505', '0527-0529', '1001-1008'],
  2029: ['0101', '0212-0219', '0404-0406', '0501-0505', '0616-0618', '0922-0924', '1001-1007'],
  2030: ['0101', '0202-0209', '0405-0407', '0501-0505', '0605-0607', '0912-0914', '1001-1007'],
};
function timeParts(date, zone) {
  zone = string(zone).trim() || 'UTC';
  if (zone === 'Local') fail('local_zone');
  let formatter;
  try { formatter = new Intl.DateTimeFormat('en-GB', { timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }); }
  catch { formatter = new Intl.DateTimeFormat('en-GB', { timeZone: 'UTC', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }); }
  const parts = Object.fromEntries(formatter.formatToParts(date).map((part) => [part.type, Number(part.value)]));
  const weekday = new Date(Date.UTC(parts.year, parts.month - 1, parts.day)).getUTCDay();
  const md = String(parts.month).padStart(2, '0') + String(parts.day).padStart(2, '0');
  let holiday = (HOLIDAYS[parts.year] || []).some((range) => {
    const [start, end = start] = range.split('-');
    return md >= start && md <= end;
  });
  if (parts.year < 2024 || parts.year > 2030) {
    holiday ||= md === '0101' || ['0404', '0405'].includes(md) || (md >= '0501' && md <= '0505') || (md >= '1001' && md <= '1007');
  }
  const offday = holiday || weekday === 0 || weekday === 6;
  return { ...parts, weekday, cn_holiday: +holiday, cn_offday: +offday, cn_workday: +!offday };
}

export function parseSimulationTime(text, now = new Date()) {
  if (!text.trim()) return now;
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{1,3}))?)?(Z|[+-]\d{2}:\d{2})$/.exec(text);
  if (!match) fail('time');
  const [, year, month, day, hour, minute, second = '0', , zone] = match;
  const days = new Date(Date.UTC(Number(year), Number(month), 0)).getUTCDate();
  const date = new Date(text);
  if (+year < 100 || +month < 1 || +month > 12 || +day < 1 || +day > days || +hour > 23 || +minute > 59 || +second > 59 || (zone !== 'Z' && (+zone.slice(1, 3) > 23 || +zone.slice(4) > 59)) || !Number.isFinite(date.getTime())) fail('time');
  return date;
}

function objectInput(text, code) {
  if (text.length > 65536) fail(code);
  let value;
  try { value = JSON.parse(text.trim() || '{}'); } catch { fail(code); }
  if (!value || typeof value !== 'object' || Array.isArray(value)) fail(code);
  return value;
}

function evaluate(ast, vars, body, headers, date) {
  const trace = { tier: '' };
  function run(node) {
    const [kind, value, a, b] = node;
    if (kind === 'literal') return value;
    if (kind === 'variable') return vars[value];
    if (kind === 'conditional') return run(boolean(run(value)) ? a : b);
    if (kind === 'unary') {
      const arg = run(a);
      return value === '!' || value === 'not' ? !boolean(arg) : value === '-' ? -number(arg) : number(arg);
    }
    if (kind === 'binary') {
      const left = run(a);
      if (value === '&&') return boolean(left) && boolean(run(b));
      if (value === '||') return boolean(left) || boolean(run(b));
      const right = run(b);
      if (value === '==' || value === '!=') {
        if (left !== null && right !== null && (typeof left === 'object' || typeof right === 'object')) fail('path');
        return value === '==' ? left === right : left !== right;
      }
      if (['<', '<=', '>', '>='].includes(value)) {
        if (!((typeof left === 'number' && typeof right === 'number') || (typeof left === 'string' && typeof right === 'string'))) fail('expression');
        return value === '<' ? left < right : value === '<=' ? left <= right : value === '>' ? left > right : left >= right;
      }
      const x = number(left), y = number(right);
      return number(value === '+' ? x + y : value === '-' ? x - y : value === '*' ? x * y : value === '/' ? x / y : value === '%' ? x % y : x ** y);
    }
    const args = a.map(run);
    if (value === 'tier') { trace.tier = string(args[0]); return number(args[1]); }
    if (value === 'call') return number(args[0]) * 1e6;
    if (value === 'header') return headers[string(args[0]).trim().toLowerCase()] || '';
    if (value === 'param') return readPath(body, args[0]);
    if (value === 'has') {
      const needle = string(args[1]);
      if (args[0] === null || !needle) return false;
      if (typeof args[0] === 'object') fail('path');
      return String(args[0]).includes(needle);
    }
    if (['min', 'max', 'abs', 'ceil', 'floor'].includes(value)) return Math[value](...args.map(number));
    return timeParts(date, args[0])[value];
  }
  return { value: number(run(ast)), tier: trace.tier };
}

export function simulateRequest({ expression, rules = '', bodyText = '{}', headersText = '{}', timeText = '', variablesText = '{}' }) {
  try {
    if (!expression.trim()) fail('empty');
    const body = objectInput(bodyText, 'body');
    const rawHeaders = objectInput(headersText, 'headers');
    const headers = Object.create(null);
    for (const [key, value] of Object.entries(rawHeaders)) {
      if (typeof value !== 'string') fail('headers');
      const name = key.trim().toLowerCase();
      if (name && value.trim()) headers[name] = value.trim();
    }
    const input = objectInput(variablesText, 'variables');
    if (Object.keys(input).some((key) => !VARIABLES.includes(key))) fail('variables');
    const vars = Object.fromEntries(VARIABLES.map((key) => [key, own(input, key) ? input[key] : 0]));
    if (Object.values(vars).some((value) => typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0)) fail('variables');
    if (!own(input, 'len')) vars.len = vars.p + vars.cr + vars.cc + vars.cc1h + vars.cc12h + vars.img + vars.ai;
    if (!Number.isSafeInteger(vars.len)) fail('variables');
    const date = parseSimulationTime(timeText);
    const base = evaluate(compile(expression), vars, body, headers, date);
    const factor = rules.trim() ? evaluate(compile(rules), vars, body, headers, date).value : 1;
    const cost = base.value * factor / 1e6;
    if (base.value < 0 || factor < 0 || !Number.isFinite(cost)) fail('result');
    return { ok: true, baseCost: base.value / 1e6, multiplier: factor, cost, tier: base.tier, time: date.toISOString() };
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : 'expression' };
  }
}

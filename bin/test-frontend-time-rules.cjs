#!/usr/bin/env node
/*
 * Run from any directory: node bin/test-frontend-time-rules.cjs
 * No build/install/browser required. Uses the installed Default TypeScript to
 * transpile real sources in memory, Node assertions, and an isolated hook runner.
 * Optional --i18n-details prints all pre-existing translation audit findings.
 */
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const { execFileSync } = require('node:child_process');
const root = path.resolve(__dirname, '..');
const ts = createRequire(path.join(root, 'web/default/package.json'))('typescript');
const files = {
  defaultRules: 'web/default/src/features/pricing/lib/billing-expr.ts',
  classicRules: 'web/classic/src/pages/Setting/Ratio/components/requestRuleExpr.js',
  defaultEditor: 'web/default/src/features/system-settings/models/tiered-pricing-editor.tsx',
  classicEditor: 'web/classic/src/pages/Setting/Ratio/components/TieredPricingEditor.jsx',
};
const read = (file) => fs.readFileSync(path.join(root, file), 'utf8');
const source = (file, text = read(file)) => ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true,
  /\.[jt]sx$/.test(file) ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
const plain = (value) => JSON.parse(JSON.stringify(value));
function compile(text, file, imports = {}, globals = {}) {
  const result = ts.transpileModule(text, { fileName: file, reportDiagnostics: true,
    compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.React } });
  assert.equal(result.diagnostics?.length || 0, 0, `${file}: transpilation diagnostics`);
  const module = { exports: {} };
  new Function('module', 'exports', 'require', ...Object.keys(globals), result.outputText)(
    module, module.exports, (id) => {
      assert.ok(Object.hasOwn(imports, id), `Unexpected import: ${id}`);
      return imports[id];
    }, ...Object.values(globals));
  return module.exports;
}
const rules = {
  default: compile(read(files.defaultRules), files.defaultRules),
  classic: compile(read(files.classicRules), files.classicRules),
};
const defaultTier = compile(read('web/default/src/features/pricing/lib/tier-expr.ts'), 'tier-expr.ts',
  { './billing-expr': rules.default });
const classicAST = source(files.classicEditor);
const classicPure = classicAST.statements.filter((node) =>
  ts.isFunctionDeclaration(node) && ['buildConditionStr', 'getTierCacheMode', 'normalizeVisualTier',
    'createDefaultVisualConfig', 'normalizeVisualConfig', 'buildTierBodyExpr', 'generateExprFromVisualConfig',
    'tryParseVisualConfig', 'evalExprLocally'].includes(node.name?.text)).map((node) => node.getText(classicAST)).join('\n');
const classicVars = compile(read('web/classic/src/constants/billing.constants.js'), 'billing.constants.js');
const classicTier = compile(`${classicPure}\nexports.api = { createDefaultVisualConfig, tryParseVisualConfig, generateExprFromVisualConfig, evalExprLocally };`,
  'classic-pure.ts', {}, { CACHE_MODE_TIMED: 'timed', CACHE_MODE_GENERIC: 'generic',
    CACHE_VAR_MAP: classicVars.BILLING_CACHE_VAR_MAP,
    EXTRA_ESTIMATOR_FIELDS: classicVars.BILLING_EXTRA_VARS.map((v) => ({ var: v.key, stateKey: `${v.key}Tokens` })) }).api;

let semanticCases = 0;
let evaluations = 0;
const domains = { hour: [0, 23], minute: [0, 59], weekday: [0, 6], month: [1, 12], day: [1, 31] };
const factor = (condition) => `(${condition} ? 0.5 : 1)`;
const time = (fn, tz = 'UTC') => `${fn}("${tz}")`;
function evaluator(expr, fn = 'hour') {
  // Only repository fixtures/generated numeric literals are evaluated here.
  const names = [...Object.keys(domains), 'header', 'param', 'has', 'nil', 'tier', 'call', 'p', 'c', 'len', 'cr', 'cc', 'cc1h'];
  const execute = new Function(...names, `return (${expr})`);
  return (value, flag) => {
    evaluations++;
    return execute(...Object.keys(domains).map((name) => () => name === fn ? value : domains[name][0]),
      () => flag ? 'yes' : '', () => flag, (s, v) => s.includes(v), null, (_n, v) => v, (x) => x * 1e6, 100, 20, 100, 0, 0, 0);
  };
}
function checkEquivalent(expr, fn, mustParse = false) {
  semanticCases++;
  const parsed = Object.values(rules).map((api) => api.tryParseRequestRuleExpr(expr));
  assert.deepEqual(plain(parsed[0]), plain(parsed[1]), `frontend parity: ${expr}`);
  if (mustParse) assert.ok(parsed[0], `must support: ${expr}`);
  if (!parsed[0]) return;
  const original = evaluator(expr, fn);
  const rebuilt = Object.values(rules).map((api, i) => api.buildRequestRuleExpr(parsed[i]));
  assert.equal(rebuilt[0], rebuilt[1]);
  const next = evaluator(rebuilt[0], fn);
  for (let v = domains[fn][0]; v <= domains[fn][1]; v++) {
    for (const flag of [false, true]) assert.equal(next(v, flag), original(v, flag), `${expr} -> ${rebuilt[0]} @ ${v}/${flag}`);
  }
}

// Exhaust every valid bound pair/operator, including equal, reversed AND and
// legacy daytime OR, both standalone and among other conditions.
for (const [fn, [min, max]] of Object.entries(domains)) {
  const f = time(fn);
  for (let start = min; start <= max; start++) for (let end = min; end <= max; end++) {
    for (const op of ['&&', '||']) {
      const condition = `${f} >= ${start} ${op} ${f} < ${end}`;
      const supported = op === (start > end ? '||' : '&&');
      for (const text of [condition, `(${condition})`, `header("x") == "yes" && (${condition})`,
        `${condition} && header("x") == "yes"`, `header("x") == "yes" && ${condition}`]) {
        checkEquivalent(factor(text), fn, supported && (!text.includes('header') || text.includes(`(${condition})`) || op === '&&'));
      }
      for (const api of Object.values(rules)) {
        const generated = api.buildRequestRuleExpr([{ conditions: [{ source: 'time', timeFunc: fn, timezone: 'UTC',
          mode: 'range', rangeStart: String(start), rangeEnd: String(end) }], multiplier: '0.5' }]);
        assert.equal(generated, factor(`${f} >= ${start} ${start > end ? '||' : '&&'} ${f} < ${end}`));
      }
    }
  }
}
for (const [fn, [min, max]] of Object.entries(domains)) {
  for (const value of [String(min - 1), String(max + 1), '-1', '1.5', 'NaN', 'Infinity', '1e999', '1e', '']) {
    for (const api of Object.values(rules)) {
      assert.equal(api.tryParseRequestRuleExpr(factor(`${time(fn)} >= ${value}`)), null);
      assert.equal(api.tryParseRequestRuleExpr(factor(`${time(fn)} >= ${value} || ${time(fn)} < ${min}`)), null);
      const cond = { source: 'time', timeFunc: fn, timezone: 'UTC', mode: 'range', rangeStart: value, rangeEnd: String(min) };
      assert.equal(api.buildRequestRuleExpr([{ conditions: [cond], multiplier: '2' }]), '');
    }
  }
  for (const op of ['==', '>=', '<']) for (const value of [min, max]) checkEquivalent(factor(`${time(fn)} ${op} ${value}`), fn, true);
}
const extraConditions = [
  'hour("UTC") >= 9e0 && hour("UTC") < 1.2e1',
  'hour("UTC") >= 21 && hour("UTC") < 6 && param("stream") == true',
  'hour("UTC") >= 9 && hour("Asia/Shanghai") < 12',
  'hour("UTC") >= 9 && minute("UTC") < 12',
  'hour("UTC") >= 9 && header("x") == "yes" && hour("UTC") < 12',
  'hour("UTC") >= 9 || minute("UTC") < 12',
  'hour("UTC") >= 9 && hour("UTC") < 12 && (minute("UTC") >= 50 || minute("UTC") < 10)',
  '(hour("UTC") >= 21 || hour("UTC") < 6) && (minute("UTC") >= 50 || minute("UTC") < 10)',
  'hour("UTC") >= 9 && header("x") == "a && b"',
  'hour("UTC") >= 9 && header("x") == "a || b"',
  'hour("UTC") >= 9 && header("x") == "(x)"',
];
for (const condition of extraConditions) checkEquivalent(factor(condition), 'hour');
// Distinct timezone arguments must not be normalized/merged.
for (const api of Object.values(rules)) {
  const expr = factor('hour("UTC") >= 9 && hour("Asia/Shanghai") < 12');
  assert.equal(api.buildRequestRuleExpr(api.tryParseRequestRuleExpr(expr)), expr);
  for (const condition of [String.raw`hour("\u0055TC") >= 9`,
    String.raw`hour("\u0055TC") >= 9 && hour("\u0055TC") < 12`,
    'hour("UTC") >= 9 && ', ' && hour("UTC") < 12']) {
    assert.equal(api.tryParseRequestRuleExpr(factor(condition)), null);
  }
}
for (const api of Object.values(rules)) {
  assert.deepEqual(api.tryParseRequestRuleExpr(''), []);
  assert.deepEqual(api.tryParseRequestRuleExpr(null), []);
  assert.equal(api.buildRequestRuleExpr([]), '');
  assert.equal(api.buildRequestRuleExpr(null), '');
  assert.equal(api.normalizeCondition(null).value, '');
  const reverse = api.tryParseRequestRuleExpr(factor('hour("UTC") >= 21 && hour("UTC") < 6 && header("x") == "yes"'));
  assert.deepEqual(reverse[0].conditions.map((c) => c.mode), ['gte', 'lt', 'eq']);
  assert.equal(api.tryParseRequestRuleExpr(factor('hour("UTC") >= 9 || hour("UTC") < 12')), null);
  assert.equal(api.tryParseRequestRuleExpr(factor('hour("UTC") >= 9 || hour("UTC") < 9')), null);
  assert.equal(api.tryParseRequestRuleExpr(factor('hour("UTC") >= 21 || hour("UTC") < 6 && header("x") == "yes"')), null);
}

// call/per_call_cost and legacy token tiers must survive split/parse/rebuild.
const bases = ['tier("base", call(5))', 'tier("base", call(3) + p * 2 + c * 6)',
  'len <= 128000 ? tier("short", call(3)) : tier("long", call(5))',
  'tier("base", p * 2 + c * 4)', 'tier("base", p * 3 + c * 15 + cr * 0.3 + cc * 3.75 + cc1h * 6)'];
const dayRule = factor('hour("UTC") >= 9 && hour("UTC") < 12');
const legacyRule = factor('hour("UTC") >= 9 || hour("UTC") < 12');
const nightRule = factor('hour("UTC") >= 21 || hour("UTC") < 6');
for (const base of bases) {
  for (const tier of [defaultTier, classicTier]) {
    const config = tier.tryParseVisualConfig(base);
    assert.ok(config, base);
    assert.equal(tier.generateExprFromVisualConfig(config), base);
    assert.equal(config.tiers[0].per_call_cost, base.includes('call(5)') && !base.includes('call(3)') ? 5 : base.includes('call(3)') ? 3 : 0);
    const result = tier.evalExprLocally(base, 100, 20, {});
    assert.equal(result.error, null);
    assert.equal(result.cost, evaluator(base)(0, false));
  }
  for (const api of Object.values(rules)) {
    for (const rule of [dayRule, nightRule, `${dayRule} * ${nightRule}`]) {
      const combined = api.combineBillingExpr(base, rule);
      assert.deepEqual(api.splitBillingExprAndRequestRules(combined), { billingExpr: base, requestRuleExpr: rule });
      const rebuilt = api.buildRequestRuleExpr(api.tryParseRequestRuleExpr(rule));
      assert.equal(api.combineBillingExpr(base, rebuilt), combined);
    }
    const raw = api.combineBillingExpr(base, legacyRule);
    assert.deepEqual(api.splitBillingExprAndRequestRules(raw), { billingExpr: raw, requestRuleExpr: '' });
  }
}
assert.equal(rules.default.parseTiersFromExpr(bases[0])[0].per_call_cost, 5);
assert.equal(rules.default.parseTiersFromExpr(bases[1])[0].per_call_cost, 3);

// Execute actual editor initialization/effects/handlers (not copied logic).
// JSX is excluded: this is a state/data-path regression, not a browser test.
function editorBody(file) {
  const ast = source(file);
  let fn;
  for (const node of ast.statements) {
    if (ts.isFunctionDeclaration(node) && node.name?.text === 'TieredPricingEditor') fn = node;
    if (ts.isVariableStatement(node)) for (const decl of node.declarationList.declarations) {
      if (decl.name.getText(ast) === 'TieredPricingEditor') fn = decl.initializer.arguments[0];
    }
  }
  assert.ok(fn, file);
  const statements = fn.body.statements.filter((node) => !ts.isReturnStatement(node)).map((node) => node.getText(ast));
  const names = file === files.defaultEditor
    ? 'handleModeChange, handleRuleGroupsChange, handleVisualChange, handleRawChange, applyPreset'
    : 'handleModeSwitch, handleRequestRuleGroupsChange, handleVisualChange, handleRawChange, applyPreset';
  return `exports.render = function(${fn.parameters.map((p) => p.getText(ast)).join(',')}) {\n${statements.join('\n')}\nreturn { editorMode, rawExpr, visualConfig, requestRuleGroups, ${names} };\n}`;
}
function mountEditor(kind, base, rule) {
  let cursor = 0, dirty = false, view;
  const slots = [], pending = [], calls = [], warnings = [];
  const hooks = {
    useState(initial) {
      const index = cursor++;
      if (!(index in slots)) slots[index] = typeof initial === 'function' ? initial() : initial;
      return [slots[index], (value) => {
        const next = typeof value === 'function' ? value(slots[index]) : value;
        if (!Object.is(next, slots[index])) { slots[index] = next; dirty = true; }
      }];
    },
    useRef: (value) => hooks.useState(() => ({ current: value }))[0],
    useMemo(fn, deps) {
      const index = cursor++;
      if (!slots[index] || deps.some((value, i) => !Object.is(value, slots[index].deps[i]))) {
        slots[index] = { deps, value: fn() };
      }
      return slots[index].value;
    },
    useCallback: (fn, deps) => hooks.useMemo(() => fn, deps),
    useEffect(fn, deps) {
      const index = cursor++;
      if (!slots[index] || deps.some((value, i) => !Object.is(value, slots[index][i]))) {
        slots[index] = deps;
        pending.push(fn);
      }
    },
  };
  const props = { modelName: 'test', model: { name: 'test', billingExpr: base }, billingExpr: base, requestRuleExpr: rule,
    t: (s) => s,
    onExprChange(value) { calls.push(['billing', value]); props.model.billingExpr = value; props.billingExpr = value; dirty = true; },
    onRequestRuleExprChange(value) { calls.push(['rules', value]); props.requestRuleExpr = value; dirty = true; },
  };
  props.onBillingExprChange = props.onExprChange;
  const render = compile(editorBody(files[`${kind}Editor`]), 'editor.tsx', {}, {
    ...rules[kind], ...(kind === 'default' ? defaultTier : classicTier), ...hooks,
    useTranslation: () => ({ t: props.t }), toast: { warning: (s) => warnings.push(s) },
    showWarning: (s) => warnings.push(s), localStorage: { getItem: () => null },
  }).render;
  function flush() {
    for (let i = 0; i < 15; i++) {
      dirty = false; cursor = 0; view = render(props);
      while (pending.length) pending.shift()();
      if (!dirty) return;
    }
    assert.fail(`${kind}: editor render loop`);
  }
  const switchMode = (mode) => {
    if (kind === 'default') view.handleModeChange(mode);
    else view.handleModeSwitch({ target: { value: mode } });
    flush();
  };
  flush();
  return { get view() { return view; }, props, calls, warnings, flush, switchMode };
}
for (const kind of ['default', 'classic']) {
  for (const rule of [dayRule, legacyRule, nightRule, factor('hour("UTC") >= 24')]) {
    const base = 'tier("base", call(5))';
    const editor = mountEditor(kind, base, rule);
    assert.deepEqual(editor.calls, [], `${kind}: mounting must not publish prices`);
    editor.switchMode('raw');
    assert.equal(editor.view.rawExpr, rules[kind].combineBillingExpr(base, rule));
    assert.deepEqual(editor.calls, [], `${kind}: opening raw must not publish prices`);
    editor.switchMode('visual');
    if ([dayRule, nightRule].includes(rule)) {
      assert.equal(editor.view.editorMode, 'visual');
      assert.equal(editor.view.visualConfig.tiers[0].per_call_cost, 5);
    } else {
      assert.equal(editor.view.editorMode, 'raw');
      assert.equal(editor.warnings.length, 1);
      assert.deepEqual(editor.calls, []);
      assert.equal(editor.props.requestRuleExpr, rule);
    }
  }
  for (const raw of [rules[kind].combineBillingExpr(bases[0], legacyRule), 'call(7) + max(p, c)']) {
    const editor = mountEditor(kind, raw, '');
    assert.equal(editor.view.editorMode, 'raw');
    assert.deepEqual(editor.calls, []);
    editor.switchMode('visual');
    assert.equal(editor.view.editorMode, 'raw');
    assert.equal(editor.props.billingExpr, raw);
    assert.deepEqual(editor.calls, []);
  }
  const editor = mountEditor(kind, bases[0], dayRule);
  const change = kind === 'default' ? 'handleRuleGroupsChange' : 'handleRequestRuleGroupsChange';
  editor.view[change](rules[kind].tryParseRequestRuleExpr(nightRule)); editor.flush();
  assert.equal(editor.props.requestRuleExpr, nightRule);
  editor.view.handleVisualChange((kind === 'default' ? defaultTier : classicTier).tryParseVisualConfig(bases[1])); editor.flush();
  assert.equal(editor.props.billingExpr, bases[1]);
  editor.view.applyPreset({ expr: bases[2], requestRules: rules[kind].tryParseRequestRuleExpr(dayRule) }); editor.flush();
  assert.equal(editor.props.billingExpr, bases[2]);
  assert.equal(editor.props.requestRuleExpr, dayRule);
  editor.switchMode('raw');
  editor.view.handleRawChange(rules[kind].combineBillingExpr(bases[0], legacyRule)); editor.flush();
  assert.equal(editor.props.billingExpr, rules[kind].combineBillingExpr(bases[0], legacyRule));
  assert.equal(editor.props.requestRuleExpr, '');
}
console.log(`PASS: ${semanticCases} semantic/parity cases, ${evaluations} evaluations; raw preservation, editor lifecycle, call/per_call_cost, legacy tiers`);

// AP G3: strict changed-key/namespace/duplicate audit; full-page coverage and
// Chinese-English residue are also compared with HEAD, not silently ignored.
function jsonWithUniqueKeys(text, file) {
  const ast = ts.parseJsonText(file, text);
  assert.equal(ast.parseDiagnostics.length, 0, file);
  function walk(node) {
    if (ts.isObjectLiteralExpression(node)) {
      const seen = new Set();
      for (const p of node.properties) {
        const key = p.name.text;
        assert.ok(!seen.has(key), `${file}: duplicate ${key}`);
        seen.add(key);
      }
    }
    ts.forEachChild(node, walk);
  }
  walk(ast);
  const data = JSON.parse(text);
  assert.deepEqual(Object.keys(data), ['translation'], `${file}: only translation namespace allowed`);
  return data.translation;
}
function translationKeys(file) {
  const ast = source(file);
  const keys = new Set();
  function walk(node) {
    if (ts.isCallExpression(node) && node.expression.getText(ast) === 't' && node.arguments.length &&
      ts.isStringLiteralLike(node.arguments[0])) keys.add(node.arguments[0].text);
    // Only properties actually passed to t(): Default preset labels are raw
    // text, Classic preset labels are translated; tier labels are data.
    if (ts.isPropertyAssignment(node) && ts.isStringLiteralLike(node.initializer)) {
      let owner = node.parent;
      while (owner && !ts.isVariableDeclaration(owner)) owner = owner.parent;
      const container = owner?.name.getText(ast);
      const property = node.name.getText(ast);
      if ((container === 'PRESET_GROUPS' && (property === 'group' ||
          (file === files.classicEditor && property === 'label'))) ||
          ['TIME_FUNC_LABELS', 'TIME_FUNC_HINTS'].includes(container)) keys.add(node.initializer.text);
    }
    ts.forEachChild(node, walk);
  }
  walk(ast);
  return keys;
}
const details = [];
let localeCount = 0, changedKeyCount = 0, missingCount = 0, residueCount = 0;
for (const kind of ['default', 'classic']) {
  const keys = translationKeys(files[`${kind}Editor`]);
  const api = rules[kind];
  for (const source of ['time', 'param', 'header']) {
    for (const option of api.getRequestRuleMatchOptions(source, (s) => s)) keys.add(option.labelKey || option.label);
  }
  const localeDir = `web/${kind}/src/i18n/locales`;
  for (const name of fs.readdirSync(path.join(root, localeDir)).filter((n) => n.endsWith('.json'))) {
    const file = `${localeDir}/${name}`;
    const current = jsonWithUniqueKeys(read(file), file);
    const baseline = JSON.parse(execFileSync('git', ['show', `HEAD:${file}`], { cwd: root, encoding: 'utf8' })).translation;
    localeCount++;
    const changed = Object.keys(current).filter((key) => current[key] !== baseline[key]);
    for (const key of changed) {
      changedKeyCount++;
      const value = current[key];
      assert.ok(typeof value === 'string' && value.trim(), `${file}: empty ${key}`);
      if (name !== 'en.json' && /[A-Za-z]{3}/.test(key)) assert.notEqual(value, key, `${file}: untranslated ${key}`);
      if (name.startsWith('zh')) assert.ok(/[\u3400-\u9fff]/.test(value), `${file}: missing Chinese ${key}`);
      if (!name.startsWith('zh') && name !== 'ja.json') assert.ok(!/[\u3400-\u9fff]/.test(value), `${file}: Chinese residue ${key}`);
    }
    const missing = [...keys].filter((key) => !(key in current));
    assert.deepEqual(missing.filter((key) => key in baseline), [], `${file}: newly missing page keys`);
    missingCount += missing.length;
    if (missing.length) details.push({ file, preExistingMissingPageKeys: missing });
    if (name.startsWith('zh')) {
      const isResidue = (v) => typeof v === 'string' && v.length > 4 && /[A-Za-z]{3}/.test(v) && !/[\u3040-\u30ff\u3400-\u9fff\uac00-\ud7af]/.test(v);
      const residue = Object.keys(current).filter((key) => isResidue(current[key]));
      assert.deepEqual(residue.filter((key) => current[key] !== baseline[key]), [], `${file}: new English residue`);
      residueCount += residue.length;
      if (residue.length) details.push({ file, preExistingEnglishCandidates: residue.map((key) => ({ key, value: current[key] })) });
    }
    const newKeys = kind === 'default' ? ['Time range', 'Start ≤ end: within the day; start > end: across midnight']
      : ['时间范围', '开始 ≤ 结束为当日区间，开始 > 结束为跨零点区间'];
    for (const key of newKeys) assert.ok(current[key], `${file}: required time-rule key ${key}`);
  }
}
assert.equal(localeCount, 14);
console.log(`PASS: ${localeCount} locale JSONs, unique translation/keys, ${changedKeyCount} changed translations, no new missing keys or Chinese/English residue`);
console.log(`BASELINE AUDIT (not a full i18n pass): ${missingCount} pre-existing missing page keys, ${residueCount} Chinese-locale English candidates; --i18n-details lists all`);
if (process.argv.includes('--i18n-details')) console.log(JSON.stringify(details, null, 2));

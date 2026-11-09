/* ccl 控制台 —— 额度渲染口径的单测
 *
 * 为什么要单独测：
 *
 *   "网页与状态栏口径一致"是**看不见**的契约 —— 网页显示 "6%"、状态栏显示
 *   "6% 6/100 %"，界面上看不出任何问题，两个数却在打架。而这正是 spec
 *   （web-config-console）的硬要求：
 *
 *     The rendered values MUST use the same thresholds, the same bar rendering
 *     and the same value formats as the terminal status line, so that the
 *     console and the status line never disagree about the same provider.
 *
 *   网页之前**没有按 kind 分派**，window 维度也显示"已用/总量" → 与状态栏
 *   不一致（minimax 就是 window）。界面上看着"冗余"，根子是口径漂了。
 *
 * 期望值抄自终端状态栏（~/.claude/statusline.js 的 renderDimension /
 * rateLimitStr / balanceStr / quotaKindStr）—— 状态栏是真相来源，网页向它
 * 对齐，不是反过来。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SRC = path.join(HERE, '..', 'assets', 'providers.js');

/* loadQuotaFormat 在 vm 沙箱里加载 providers.js，取它导出的纯逻辑。
 *
 * providers.js 顶层只定义函数 + 挂 window.CCL，**不碰 document**（DOM 只在
 * 渲染时用），所以沙箱里给个空 window 就够。
 */
function loadQuotaFormat() {
  const src = fs.readFileSync(SRC, 'utf8');
  const sandbox = { window: {} };
  vm.createContext(sandbox);
  vm.runInContext(src, sandbox, { filename: SRC });
  return sandbox.window.CCL.quotaFormat;
}

const qf = loadQuotaFormat();

/* minimax 的真实维度形状（kind = window）。 */
const windowDim = {
  id: 'interval',
  label: '5h',
  kind: 'window',
  used_percentage: 7,
  used: 7,
  limit: 100,
  unit: '%',
  duration_seconds: 18000,
  resets_at: 1794225538,
};

/* 有分母的额度（kind = quota）。 */
const quotaDim = {
  id: 'quota',
  label: '令牌',
  kind: 'quota',
  used_percentage: 3,
  used: 3,
  limit: 1000,
  unit: '%',
};

/* 余额（kind = balance，无分母）。 */
const balanceDim = {
  id: 'balance',
  label: 'CNY',
  kind: 'balance',
  remaining: 5.17,
  unit: 'CNY',
};

/* --------------------------------------------------------------------- */
/* 按 kind 分派                                                            */
/* --------------------------------------------------------------------- */

test('dimKind：window / balance / 其余都是 quota', () => {
  assert.equal(qf.dimKind(windowDim), 'window');
  assert.equal(qf.dimKind(balanceDim), 'balance');
  assert.equal(qf.dimKind(quotaDim), 'quota');
  // 未知 kind 走 quotaKindStr 的兜底（状态栏 renderDimension 的最后一行）
  assert.equal(qf.dimKind({ id: 'x', label: 'x' }), 'quota');
});

/* --------------------------------------------------------------------- */
/* 数值文本：逐条对齐状态栏                                                 */
/* --------------------------------------------------------------------- */

test('window 维度：只有百分比，不显示已用/总量（rateLimitStr 口径）', () => {
  assert.equal(qf.dimText(windowDim), '7%');
});

test('window 维度绝不含 "已用/总量"（回归：之前不分 kind，一律显示 7/100）', () => {
  assert.ok(!qf.dimText(windowDim).includes('/'), 'window 维度不该出现 "/"');
});

test('quota 维度：百分比 + 已用/总量（quotaKindStr 口径）', () => {
  assert.equal(qf.dimText(quotaDim), '3% 3/1000 %');
});

test('balance 维度：只有数字，无百分比、无条形（balanceStr 口径）', () => {
  assert.equal(qf.dimText(balanceDim), '5.17');
});

test('label 就是 unit 时不重复显示单位（unitSuffix 口径）', () => {
  assert.equal(qf.dimText({ ...balanceDim, label: '余额', unit: 'CNY' }), '5.17 CNY');
});

test('没有百分比：只显示剩余量（quotaKindStr 的无百分比分支）', () => {
  assert.equal(
    qf.dimText({ id: 'x', label: '额度', kind: 'quota', remaining: 12.5, unit: 'USD' }),
    '12.5 USD'
  );
});

test('balance 且没有 remaining：显示 "--"', () => {
  assert.equal(qf.dimText({ id: 'b', label: 'CNY', kind: 'balance', unit: 'CNY' }), '--');
});

/* --------------------------------------------------------------------- */
/* 百分比口径：window 与 quota 不一样                                       */
/* --------------------------------------------------------------------- */

test('window 的百分比一律四舍五入（rateLimitStr 口径）', () => {
  assert.equal(qf.dimPctText(0.025, 'window'), '0');
  assert.equal(qf.dimPctText(0.6, 'window'), '1');
  assert.equal(qf.dimPctText(7, 'window'), '7');
});

test('quota 的百分比小于 1% 时保留两位小数（quotaKindStr 口径）', () => {
  assert.equal(qf.dimPctText(0.025, 'quota'), '0.03');
  assert.equal(qf.dimPctText(7, 'quota'), '7');
});

test('window 维度的小百分比不吃两位小数那套（回归：之前网页不分 kind）', () => {
  assert.equal(qf.dimText({ ...windowDim, used_percentage: 0.025 }), '0%');
});

/* --------------------------------------------------------------------- */
/* 条形                                                                    */
/* --------------------------------------------------------------------- */

test('barRatio 夹在 0..1', () => {
  assert.equal(qf.barRatio(0), 0);
  assert.equal(qf.barRatio(50), 0.5);
  assert.equal(qf.barRatio(100), 1);
  assert.equal(qf.barRatio(150), 1);
  assert.equal(qf.barRatio(-10), 0);
  assert.equal(qf.barRatio(undefined), 0);
  assert.equal(qf.barRatio('x'), 0);
});

test('pctColor 阈值 90/75/50 与状态栏一致', () => {
  assert.equal(qf.pctColor(0), 'ok-bar');
  assert.equal(qf.pctColor(49.9), 'ok-bar');
  assert.equal(qf.pctColor(50), 'warn-bar');
  assert.equal(qf.pctColor(74.9), 'warn-bar');
  assert.equal(qf.pctColor(75), 'bad-bar');
  assert.equal(qf.pctColor(89.9), 'bad-bar');
  assert.equal(qf.pctColor(90), 'bad-bar');
  assert.equal(qf.pctColor(100), 'bad-bar');
});

/* --------------------------------------------------------------------- */
/* 绝对量格式                                                              */
/* --------------------------------------------------------------------- */

test('fmtAmount 去掉无意义的尾随零（与状态栏一致）', () => {
  assert.equal(qf.fmtAmount(18), '18');
  assert.equal(qf.fmtAmount(5.17), '5.17');
  assert.equal(qf.fmtAmount(0.1), '0.1');
  assert.equal(qf.fmtAmount(100), '100');
  assert.equal(qf.fmtAmount(0), '0');
  assert.equal(qf.fmtAmount(undefined), '--');
});

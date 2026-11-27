/* ccl 控制台 —— jsonc.js（JSONC 注释剥离器）的单元测试
 *
 * 为什么要有这个文件：
 *
 *   provider 配置支持注释，而后端（internal/jsonc）已经容忍它。**前端不跟上
 *   就是"能读不能改"**——editor.js 用标准 JSON.parse 校验，带注释的配置被判为
 *   非法，**保存按钮被禁用**（实测：页面显示"JSON 解析失败"，保存 disabled）。
 *
 *   而"前端能正确剥离注释"这件事，Go 侧的任何测试都抓不住：Go 那边只能断言
 *   "文件被 embed 了"，碰不到 JS 逻辑。
 *
 * 为什么用 node 而不是用 Go 重写一份：
 *
 *   **两份实现必然漂移**——要测的正是浏览器里跑的那份代码。jsonc.js 是纯 JS
 *   （无 DOM 依赖），用 vm 沙箱加载即可。
 *
 * 期望值与 Go 版（internal/jsonc/jsonc_test.go）一一对应——两边是同一套语义。
 *
 * 跑法：
 *
 *   node --test internal/web/jstest/jsonc.test.mjs
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SRC = path.join(HERE, '..', 'assets', 'jsonc.js');

function loadJsonc() {
  const src = fs.readFileSync(SRC, 'utf8');
  const sandbox = { window: {} };
  vm.createContext(sandbox);
  vm.runInContext(src, sandbox, { filename: SRC });
  return sandbox.window.CCL.jsonc;
}

const jsonc = loadJsonc();

/* 位置不变：剥注释不改任何字节位置 → 报的行号仍指向原文件。 */
test('剥注释不改变任何字节位置', () => {
  const inStr = '{\n  "env": {\n    // 一条注释\n    "A": "1"\n  }\n}';
  const out = jsonc.stripComments(inStr);
  assert.equal(out.length, inStr.length);
  assert.equal((out.match(/\n/g) || []).length, (inStr.match(/\n/g) || []).length);
  assert.ok(!out.includes('一条注释'));
  assert.ok(out.includes('"A": "1"'));
});

/* 无注释的 JSON 逐字节不变 —— "零迁移"的前提。 */
test('无注释的 JSON 被恒等变换', () => {
  const inStr = '{"env":{"ANTHROPIC_BASE_URL":"https://ark.cn-beijing.volces.com/api/plan","N":1}}';
  assert.equal(jsonc.stripComments(inStr), inStr);
});

/* **字符串里的 // 不是注释** —— provider 配置里全是 URL。 */
test('URL 里的双斜杠不是注释', () => {
  const got = jsonc.parse('{"base":"https://ark.cn-beijing.volces.com/api/plan"}');
  assert.equal(got.base, 'https://ark.cn-beijing.volces.com/api/plan');
});

test('字符串里的块注释标记不是注释', () => {
  const got = jsonc.parse('{"note":"use /* this */ literally"}');
  assert.equal(got.note, 'use /* this */ literally');
});

test('转义引号后的双斜杠不是注释', () => {
  const got = jsonc.parse('{"note":"say \\"hi\\" // now"}');
  assert.equal(got.note, 'say "hi" // now');
});

test('字符串里的值逐字节保留', () => {
  const got = jsonc.parse('{"a":"https://x.example/p","b":"use /* this */ literally","c":"say \\"hi\\" // now"}');
  assert.equal(got.a, 'https://x.example/p');
  assert.equal(got.b, 'use /* this */ literally');
  assert.equal(got.c, 'say "hi" // now');
});

/* 注释出现在任何位置都能处理。 */
test('注释出现在任何位置', () => {
  const got = jsonc.parse(`{
  // 键之前
  "env": { /* 冒号后面 */ "A": "1" // 值后面
  },
  // 成员之间
  "b": [1, /* 数组里 */ 2],
  "c": 3 // 最后一行
}`);
  assert.equal(got.b[1], 2);
  assert.equal(got.c, 3);
});

/* 报错带行号，且指向**原文件**的行号。 */
test('报错行号指向原文件', () => {
  const inStr =
    '{\n' +
    '  // 注释 1\n' +
    '  // 注释 2\n' +
    '  /* 块注释 */\n' +
    '  // 注释 3\n' +
    '  // 注释 4\n' +
    '  // 注释 5\n' +
    '  // 注释 6\n' +
    '  // 注释 7\n' +
    '  "a": @\n' +
    '  // 注释 8\n' +
    '}';
  assert.throws(
    () => jsonc.parse(inStr),
    (e) => {
      assert.ok(e.message.includes('第 10 行'), '错误信息应含行号，实际: ' + e.message);
      return true;
    }
  );
});

/* 尾随逗号仍然报错（JSONC 规范本身不允许）。 */
test('尾随逗号被拒', () => {
  assert.throws(() => jsonc.parse('{"env": {"A": "1"},}'));
});

/* 块注释没闭合 → 报错。 */
test('没闭合的块注释被拒', () => {
  assert.throws(() => jsonc.parse('{"a": 1} /* 没闭合'), /闭合/);
});

/* 行注释在文件末尾（没有换行）也能处理。 */
test('文件末尾的行注释', () => {
  assert.equal(jsonc.parse('{"a": 1} // 末尾注释').a, 1);
});

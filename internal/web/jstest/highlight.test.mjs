/* ccl 控制台 —— highlight.js 的单元测试
 *
 * 为什么要有这个文件：
 *
 *   整个 provider 编辑器的 XSS 防线**只建立在 escapeHTML 一个函数上**——
 *   凭证是明文 token，可能含 < & "，高亮层用 innerHTML 渲染（editor.js:47）。
 *   转义逻辑一旦被改坏（比如有人图省事改成塞原文），Go 侧的任何测试都抓不住：
 *   Go 那边只能断言"文件被 embed 了"，碰不到 JS 逻辑。
 *
 * 为什么用 node 而不是用 Go 重写一份 tokenizer：
 *
 *   **两份实现必然漂移**——要测的正是浏览器里跑的那份代码。highlight.js 是
 *   纯 JS（无 DOM 依赖），用 vm 沙箱加载即可，不需要 jsdom / 真浏览器。
 *
 * 跑法：
 *
 *   node --test internal/web/jstest/highlight.test.mjs
 *
 * 也由 internal/web/jstest_test.go 包一层，`go test ./...` 会自动跑
 * （机器上没有 node 时跳过，不让 Go 测试挂）。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const HL_PATH = path.join(HERE, '..', 'assets', 'highlight.js');

/* loadHighlight 在 vm 沙箱里加载 highlight.js，返回它导出的 API。
 *
 * highlight.js 是个 IIFE，挂到 window.CCL.highlight 上——沙箱里给个空 window
 * 就行，不需要伪造 DOM。
 */
function loadHighlight() {
  const src = fs.readFileSync(HL_PATH, 'utf8');
  const sandbox = { window: {} };
  vm.createContext(sandbox);
  vm.runInContext(src, sandbox, { filename: HL_PATH });
  return sandbox.window.CCL.highlight;
}

const hl = loadHighlight();

/* tokens 把 tokenize 的结果压成 "class:text" 的数组，断言时更好读。
 *
 * 必须用 Array.from 而不是 .map()：tokenize 跑在 vm 沙箱里，返回的数组的
 * 原型是**另一个 realm** 的 Array.prototype——deepStrictEqual 会报
 * "same structure but not reference-equal"。Array.from 在当前 realm 建新数组，
 * 原型才对得上。
 */
function tokens(src) {
  return Array.from(hl.tokenize(src), (t) => t.cls + ':' + t.text);
}

/* ------------------------------------------------------------------------- */
/* escapeHTML                                                                 */
/* ------------------------------------------------------------------------- */

test('escapeHTML 转义 & < > "', () => {
  assert.equal(hl.escapeHTML('&'), '&amp;');
  assert.equal(hl.escapeHTML('<'), '&lt;');
  assert.equal(hl.escapeHTML('>'), '&gt;');
  assert.equal(hl.escapeHTML('"'), '&quot;');
});

test('escapeHTML 不动单引号（输出只用双引号属性，不拼进属性值里）', () => {
  assert.equal(hl.escapeHTML("'"), "'");
});

test('escapeHTML 处理混合串', () => {
  assert.equal(
    hl.escapeHTML(`sk-a&b<c>d"e'f`),
    'sk-a&amp;b&lt;c&gt;d&quot;e\'f'
  );
});

/* ------------------------------------------------------------------------- */
/* highlightJSON —— XSS 防线                                                  */
/* ------------------------------------------------------------------------- */

test('highlightJSON 把裸 < 转义掉（凭证里带 < 也不能变成标签）', () => {
  const out = hl.highlightJSON('{"token":"<script>alert(1)</script>"}');
  assert.ok(!out.includes('<script'), '输出里不该出现裸 <script');
  assert.ok(!out.includes('</script>'), '输出里不该出现裸 </script>');
  assert.ok(out.includes('&lt;script&gt;'), '应该转义成 &lt;script&gt;');
});

test('highlightJSON 把裸 & 与 " 转义掉', () => {
  const out = hl.highlightJSON('{"a":"x & y","b":"say \\"hi\\""}');
  assert.ok(out.includes('&amp;'), '& 应该被转义');
  assert.ok(out.includes('&quot;'), '" 应该被转义');
  // 除了我们自己加的 span 标签，不该有任何裸的 < 或 >
  const withoutTags = out.replace(/<\/?span[^>]*>/g, '');
  assert.ok(!withoutTags.includes('<'), '除标签外不该有裸 <');
  assert.ok(!withoutTags.includes('>'), '除标签外不该有裸 >');
});

test('highlightJSON 的输出里只有自己造的 span 标签', () => {
  const out = hl.highlightJSON('{"a":"<b>"}');
  const tags = out.match(/<[^>]*>/g) || [];
  for (const tag of tags) {
    assert.ok(
      tag === '<span class="tok-key">' ||
        tag === '<span class="tok-str">' ||
        tag === '<span class="tok-num">' ||
        tag === '<span class="tok-lit">' ||
        tag === '<span class="tok-punct">' ||
        tag === '</span>',
      '出现了预期外的标签: ' + tag
    );
  }
});

/* ------------------------------------------------------------------------- */
/* tokenizer —— key / string 的区分                                            */
/* ------------------------------------------------------------------------- */

test('key 与 string 值被区分', () => {
  assert.deepEqual(tokens('{"a":"b"}'), [
    'tok-punct:{',
    'tok-key:"a"',
    'tok-punct::',
    'tok-str:"b"',
    'tok-punct:}',
  ]);
});

test('值里的冒号不会把它误判成 key', () => {
  assert.deepEqual(tokens('{"a":"x:y"}'), [
    'tok-punct:{',
    'tok-key:"a"',
    'tok-punct::',
    'tok-str:"x:y"',
    'tok-punct:}',
  ]);
});

test('key 与冒号之间有空白也算 key', () => {
  assert.deepEqual(tokens('{ "a" : "b" }'), [
    'tok-punct:{',
    ': ',
    'tok-key:"a"',
    ': ',
    'tok-punct::',
    ': ',
    'tok-str:"b"',
    ': ',
    'tok-punct:}',
  ]);
});

/* ------------------------------------------------------------------------- */
/* tokenizer —— 字符串里的花括号不误切（任务 8.1 明确要求的边界）              */
/* ------------------------------------------------------------------------- */

test('字符串里的花括号不被切成标点', () => {
  // 朴素的正则切法会在这里把 "}{" 拆成 '}' + '{' 两个标点 token，
  // 颜色就乱了（而且未闭合字符串会一路吃到文件末尾）。
  assert.deepEqual(tokens('{"a":"}{"}'), [
    'tok-punct:{',
    'tok-key:"a"',
    'tok-punct::',
    'tok-str:"}{"',
    'tok-punct:}',
  ]);
});

test('字符串里的方括号与逗号也不误切', () => {
  assert.deepEqual(tokens('{"a":"],["}'), [
    'tok-punct:{',
    'tok-key:"a"',
    'tok-punct::',
    'tok-str:"],["',
    'tok-punct:}',
  ]);
});

test('字符串里的转义引号不结束字符串', () => {
  assert.deepEqual(tokens('{"a":"say \\"hi\\""}'), [
    'tok-punct:{',
    'tok-key:"a"',
    'tok-punct::',
    'tok-str:"say \\"hi\\""',
    'tok-punct:}',
  ]);
});

test('字符串里的反斜杠不结束字符串', () => {
  assert.deepEqual(tokens('{"a":"C:\\\\path\\\\to"}'), [
    'tok-punct:{',
    'tok-key:"a"',
    'tok-punct::',
    'tok-str:"C:\\\\path\\\\to"',
    'tok-punct:}',
  ]);
});

test('未闭合的字符串不会崩、也不会吞掉后面的 token', () => {
  const t = tokens('{"a":"unterminated');
  assert.deepEqual(t, [
    'tok-punct:{',
    'tok-key:"a"',
    'tok-punct::',
    'tok-str:"unterminated',
  ]);
});

/* ------------------------------------------------------------------------- */
/* tokenizer —— 其它 token 种类                                               */
/* ------------------------------------------------------------------------- */

test('数字 / 布尔 / null / 标点各自成类', () => {
  assert.deepEqual(tokens('{"n":-1.5e3,"b":true,"c":false,"z":null}'), [
    'tok-punct:{',
    'tok-key:"n"',
    'tok-punct::',
    'tok-num:-1.5e3',
    'tok-punct:,',
    'tok-key:"b"',
    'tok-punct::',
    'tok-lit:true',
    'tok-punct:,',
    'tok-key:"c"',
    'tok-punct::',
    'tok-lit:false',
    'tok-punct:,',
    'tok-key:"z"',
    'tok-punct::',
    'tok-lit:null',
    'tok-punct:}',
  ]);
});

test('空对象与空数组', () => {
  assert.deepEqual(tokens('{}'), ['tok-punct:{', 'tok-punct:}']);
  assert.deepEqual(tokens('[]'), ['tok-punct:[', 'tok-punct:]']);
});

test('非法 JSON 也不崩（高亮是给人看的，合法性由 JSON.parse 判）', () => {
  assert.doesNotThrow(() => hl.highlightJSON('{not json'));
  assert.doesNotThrow(() => hl.highlightJSON(''));
  assert.doesNotThrow(() => hl.highlightJSON(' '));
});

/* ------------------------------------------------------------------------- */
/* 真实 provider 配置片段                                                      */
/* ------------------------------------------------------------------------- */

test('真实形状的 provider 配置高亮后不含裸尖括号', () => {
  const src = `{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic",
    "ANTHROPIC_AUTH_TOKEN": "sk-a&b<c>d"
  },
  "note": "测试 <用>",
  "order": 5
}`;
  const out = hl.highlightJSON(src);
  const withoutTags = out.replace(/<\/?span[^>]*>/g, '');
  assert.ok(!withoutTags.includes('<'), '除标签外不该有裸 <');
  assert.ok(!withoutTags.includes('>'), '除标签外不该有裸 >');
  assert.ok(out.includes('tok-key'), '应该有 key 的样式');
  assert.ok(out.includes('tok-num'), 'order 应该被当成数字');
});

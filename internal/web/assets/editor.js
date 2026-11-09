/* ccl 控制台 —— 原文编辑器
 *
 * 透明 textarea 叠在预渲染的高亮层上：
 *   - textarea 负责输入、光标、选择、滚动
 *   - 底下那层（<pre>）负责颜色
 *
 * 两层共享同一套字体度量（同一份 CSS 规则），滚动同步。
 *
 * **为什么不用 contenteditable**：它会把用户的引号自动变成中文引号、
 * 回车变成 <div>——改 JSON 会被编辑器改坏。textarea 原样保留输入。
 */
(function () {
  'use strict';

  /* createEditor 在 host 里建一个编辑器。
   *
   * opts:
   *   value    初始内容
   *   mode     'json'（高亮 + 解析校验）| 'text'（不高亮）
   *   onChange 内容变化时的回调（值, 编辑器）
   *
   * 返回：
   *   getValue() / setValue(v) / setStatus(text, kind) / focus() / element
   */
  function createEditor(host, opts) {
    opts = opts || {};
    var mode = opts.mode === 'text' ? 'text' : 'json';

    var wrap = document.createElement('div');
    wrap.className = 'editor';

    var pre = document.createElement('pre');
    pre.className = 'hl';

    var ta = document.createElement('textarea');
    ta.spellcheck = false;
    ta.autocapitalize = 'off';
    ta.setAttribute('autocomplete', 'off');
    ta.value = opts.value || '';

    wrap.appendChild(pre);
    wrap.appendChild(ta);
    host.appendChild(wrap);

    function render() {
      if (mode === 'json') {
        pre.innerHTML = window.CCL.highlight.highlightJSON(ta.value);
      } else {
        pre.textContent = ta.value;
      }
      /* <pre> 末尾补一个空格：否则最后一行空行在部分浏览器里不占高，
       * 滚动到底会差半行。 */
      pre.appendChild(document.createTextNode(' '));
    }

    /* 校验：JSON 模式下解析失败就不给保存（调用方据此禁用保存按钮）。 */
    function validate() {
      if (mode !== 'json') { return null; }
      var v = ta.value.trim();
      if (v === '') { return null; }
      try {
        JSON.parse(v);
        return null;
      } catch (e) {
        return e.message;
      }
    }

    ta.addEventListener('input', function () {
      render();
      if (opts.onChange) { opts.onChange(ta.value, api); }
    });

    ta.addEventListener('scroll', function () {
      pre.scrollTop = ta.scrollTop;
      pre.scrollLeft = ta.scrollLeft;
    });

    render();

    var api = {
      element: wrap,
      textarea: ta,
      getValue: function () { return ta.value; },
      setValue: function (v) {
        ta.value = v == null ? '' : v;
        render();
      },
      validate: validate,
      focus: function () { ta.focus(); },
      setStatus: function () { /* 状态条由调用方渲染 */ }
    };
    return api;
  }

  window.CCL = window.CCL || {};
  window.CCL.createEditor = createEditor;
})();

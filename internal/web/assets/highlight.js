/* ccl 控制台 —— JSON 语法高亮
 *
 * 自己写，不引第三方库（决策 1：零依赖、零构建、不动发布链路）。
 *
 * JSON 的 token 种类很少（key / string / number / true-false-null / 标点），
 * 一个扫描器就够；比 vendor 一个高亮库省 15~50KB 二进制体积，也不用跟着
 * 第三方更新。
 *
 * **必须先转义再上色**：provider JSON 里有明文凭证，还可能有 < & " ——
 * 直接把原文塞进 innerHTML 就是 XSS。转义在这里做，调用方拿到的 HTML
 * 只含我们自己的 span 标签。
 */
(function () {
  'use strict';

  var ESCAPES = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' };

  /* escapeHTML 转义 HTML 敏感字符。 */
  function escapeHTML(s) {
    return String(s).replace(/[&<>"]/g, function (c) { return ESCAPES[c]; });
  }

  /* tokenize 把 JSON 文本切成 token。
   *
   * 宽容：遇到非法字符也照切（高亮是给人看的，**合法性由 JSON.parse 判**），
   * 不因为一个错字就整片不高亮。
   */
  function tokenize(src) {
    var out = [];
    var i = 0;
    var n = src.length;

    while (i < n) {
      var c = src.charAt(i);

      /* 字符串（可能带转义） */
      if (c === '"') {
        var j = i + 1;
        while (j < n) {
          if (src.charAt(j) === '\\') { j += 2; continue; }
          if (src.charAt(j) === '"') { j++; break; }
          j++;
        }
        /* 后面（跳过空白）是 ':' 才是 key */
        var k = j;
        while (k < n && /\s/.test(src.charAt(k))) { k++; }
        var isKey = src.charAt(k) === ':';
        out.push({ cls: isKey ? 'tok-key' : 'tok-str', text: src.slice(i, j) });
        i = j;
        continue;
      }

      /* 数字 */
      if (c === '-' || (c >= '0' && c <= '9')) {
        var j2 = i;
        while (j2 < n && /[-+0-9.eE]/.test(src.charAt(j2))) { j2++; }
        out.push({ cls: 'tok-num', text: src.slice(i, j2) });
        i = j2;
        continue;
      }

      /* true / false / null */
      if (c === 't' || c === 'f' || c === 'n') {
        var word = c === 't' ? 'true' : c === 'f' ? 'false' : 'null';
        if (src.substr(i, word.length) === word) {
          out.push({ cls: 'tok-lit', text: word });
          i += word.length;
          continue;
        }
      }

      /* 标点 */
      if ('{}[]:,'.indexOf(c) >= 0) {
        out.push({ cls: 'tok-punct', text: c });
        i++;
        continue;
      }

      /* 其它（空白、非法字符）：原样输出，不着色 */
      out.push({ cls: '', text: c });
      i++;
    }
    return out;
  }

  /* highlightJSON 返回高亮后的 HTML（内容已转义）。 */
  function highlightJSON(src) {
    return tokenize(src).map(function (t) {
      var esc = escapeHTML(t.text);
      return t.cls ? '<span class="' + t.cls + '">' + esc + '</span>' : esc;
    }).join('');
  }

  window.CCL = window.CCL || {};
  window.CCL.highlight = {
    escapeHTML: escapeHTML,
    tokenize: tokenize,
    highlightJSON: highlightJSON
  };
})();

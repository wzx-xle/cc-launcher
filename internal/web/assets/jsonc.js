/* ccl 控制台 —— JSONC（带注释的 JSON）剥离器
 *
 * 为什么需要：
 *
 *   provider 的配置支持注释（// 与 /* *\/），而网页的 JSON 编辑器用**标准
 *   JSON.parse** 校验 → 带注释的配置被判为"JSON 解析失败"，**保存按钮被禁用**。
 *   后端已经容忍注释（internal/jsonc），前端不跟上就是"能读不能改"。
 *
 * **与 Go 版（internal/jsonc）逻辑一致**：
 *
 *   - 注释换成**等长空白**、**换行保留** → 位置不变，报错仍指向原文件
 *   - **字符串里的 // 不是注释**（provider 配置里全是 URL）
 *   - 不支持尾随逗号（JSONC 规范本身不允许）
 *
 * 为什么自己写而不引库：整个控制台是**零依赖、零构建**的（见 highlight.js 的
 * 说明），引一个 JSON 库会破坏这条约定。
 */
(function () {
  'use strict';

  /* stripComments 把 JSONC 里的注释换成等长空白，返回可直接 JSON.parse 的文本。
   *
   * 非法输入（块注释没闭合）抛 Error——与 Go 版一样，让调用方能提示用户。
   */
  function stripComments(src) {
    var out = src.split('');
    var n = src.length;
    var i = 0;

    var NORMAL = 0, STRING = 1, LINE = 2, BLOCK = 3;
    var state = NORMAL;

    while (i < n) {
      var c = out[i];

      if (state === NORMAL) {
        if (c === '"') {
          state = STRING;
          i++;
        } else if (c === '/' && i + 1 < n && out[i + 1] === '/') {
          /* 行注释：两个斜杠都换成空格 */
          out[i] = ' ';
          out[i + 1] = ' ';
          i += 2;
          state = LINE;
        } else if (c === '/' && i + 1 < n && out[i + 1] === '*') {
          /* 块注释 */
          out[i] = ' ';
          out[i + 1] = ' ';
          i += 2;
          state = BLOCK;
        } else {
          i++;
        }
        continue;
      }

      if (state === STRING) {
        /* **字符串里的 // 不是注释** —— provider 配置里全是 URL */
        if (c === '\\') {
          i += 2; /* 转义：跳过下一个字符 */
          continue;
        }
        if (c === '"') {
          state = NORMAL;
        }
        i++;
        continue;
      }

      if (state === LINE) {
        if (c === '\n') {
          state = NORMAL; /* 换行保留 */
          i++;
          continue;
        }
        out[i] = ' ';
        i++;
        continue;
      }

      /* BLOCK */
      if (c === '\n') {
        i++; /* **块注释里的换行必须保留**（后面那几行的行号要对得上） */
        continue;
      }
      if (c === '*' && i + 1 < n && out[i + 1] === '/') {
        out[i] = ' ';
        out[i + 1] = ' ';
        i += 2;
        state = NORMAL;
        continue;
      }
      out[i] = ' ';
      i++;
    }

    if (state === BLOCK) {
      throw new Error('块注释没有闭合（缺少 */）');
    }
    return out.join('');
  }

  /* parse 解析带注释的 JSON：先剥注释，再 JSON.parse。
   *
   * 错误信息带行号——与 Go 版一样（JSON.parse 自带的 position 换算成行列）。
   */
  function parse(src) {
    var stripped = stripComments(src);
    try {
      return JSON.parse(stripped);
    } catch (e) {
      throw annotatePosition(e, stripped);
    }
  }

  /* annotatePosition 把 JSON.parse 的错误信息补上行号列号。
   *
   * **位置指向剥注释后的文本**——而剥注释不改任何字节位置，所以它同时也是
   * 原文件的位置。
   *
   * V8 的错误格式有**两种**，都要处理：
   *
   *   A) "Expected property name or '}' in JSON at position 4 (line 2 column 3)"
   *      —— 带 position，直接换算
   *   B) "Unexpected token '@', \"...\" is not valid JSON"
   *      —— **没有 position**（只有出错字符与一段上下文）。这时用出错字符
   *      在**字符串字面量之外**的第一次出现来定位——JSON 里非法字符只能
   *      出现在字符串内部，跳过那些，剩下的第一个就是出错位置。
   */
  function annotatePosition(err, text) {
    var msg = err && err.message ? err.message : String(err);

    /* A) 带 position */
    var m = /at position (\d+)/.exec(msg);
    if (m) {
      var pos = parseInt(m[1], 10);
      return wrap(msg, text, pos);
    }

    /* B) 只有出错字符 */
    var m2 = /Unexpected token '?(.)'?,\s/.exec(msg);
    if (m2) {
      var p = findOutsideString(text, m2[1]);
      if (p >= 0) { return wrap(msg, text, p); }
    }

    return err;
  }

  /* findOutsideString 返回字符 ch 在 text 里**第一次出现在字符串字面量之外**的位置。
   *
   * 找不到返回 -1。
   */
  function findOutsideString(text, ch) {
    var n = text.length;
    var i = 0;
    while (i < n) {
      var c = text.charAt(i);
      if (c === '"') {
        i++;
        while (i < n) {
          if (text.charAt(i) === '\\') { i += 2; continue; }
          if (text.charAt(i) === '"') { i++; break; }
          i++;
        }
        continue;
      }
      if (c === ch) { return i; }
      i++;
    }
    return -1;
  }

  /* wrap 按字节偏移算出行列，附在错误信息里。 */
  function wrap(msg, text, pos) {
    if (pos > text.length) { pos = text.length; }
    var line = 1, col = 1;
    for (var i = 0; i < pos; i++) {
      if (text.charAt(i) === '\n') { line++; col = 1; } else { col++; }
    }
    var clean = msg.replace(/ \(line \d+ column \d+\)/, '');
    var wrapped = new Error('第 ' + line + ' 行第 ' + col + ' 列: ' + clean);
    wrapped.name = 'SyntaxError';
    return wrapped;
  }

  window.CCL = window.CCL || {};
  window.CCL.jsonc = {
    stripComments: stripComments,
    parse: parse
  };
})();

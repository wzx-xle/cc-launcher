/* ccl 控制台 —— 外壳
 *
 * 三个视图（用量 / 提供商 / Claude）共用这一个外壳：
 *   - 顶栏：主视图切换 + 主题切换
 *   - 左栏：对象列表（**随视图变**——用量页留空，主区全宽）
 *   - 右栏：详情 / 编辑区
 *
 * 零依赖、无构建：每个视图模块自己往 window.CCL.views 上注册，外壳只管切换。
 *
 * 路由用 hash（#/usage）：刷新页面不依赖服务端回退。
 *
 * 视图模块的接口：
 *   window.CCL.views.<id> = {
 *     sidebar: true|false,   // 要不要左栏
 *     activate: function () {},   // 被切到时
 *     deactivate: function () {}  // 被切走时
 *   }
 */
(function () {
  'use strict';

  var VIEWS = [
    { id: 'usage', label: '用量' },
    { id: 'providers', label: '提供商' },
    { id: 'claude', label: 'Claude' }
  ];

  var current = null;

  function $(id) { return document.getElementById(id); }

  /* 切视图：藏旧屏、显新屏、通知两边。 */
  function showView(name) {
    if (!VIEWS.some(function (v) { return v.id === name; })) { name = 'usage'; }
    if (current === name) { return; }

    var prev = current;
    current = name;

    if (prev) {
      var old = window.CCL && window.CCL.views && window.CCL.views[prev];
      if (old && old.deactivate) { old.deactivate(); }
    }

    VIEWS.forEach(function (v) {
      var panel = $('view-' + v.id);
      if (panel) { panel.hidden = v.id !== name; }
      var btn = document.querySelector('.nav-btn[data-view="' + v.id + '"]');
      if (btn) { btn.setAttribute('aria-pressed', v.id === name ? 'true' : 'false'); }
    });

    var view = window.CCL && window.CCL.views && window.CCL.views[name];
    var wantsSidebar = !!(view && view.sidebar);
    var sidebar = $('sidebar');
    if (sidebar) {
      sidebar.hidden = !wantsSidebar;
      if (!wantsSidebar) { sidebar.textContent = ''; }
    }

    if (view && view.activate) { view.activate(); }
  }

  /* 从 hash 读当前视图。 */
  function viewFromHash() {
    var h = (window.location.hash || '').replace(/^#\/?/, '');
    return h;
  }

  /* ------------------------------------------------------------------ */
  /* API                                                                  */
  /* ------------------------------------------------------------------ */

  /* apiRequest 发一个 JSON 请求。
   *
   * 失败时抛一个带 status / data 的 Error——调用方据此区分
   * "409 冲突（别人改过）" / "400 配置不对" / "429 限流中"。
   */
  function apiRequest(method, path, body, etag) {
    var opts = {
      method: method,
      headers: { 'Content-Type': 'application/json' },
      /* session cookie 是 HttpOnly + SameSite=Lax，同源请求自动带上 */
      credentials: 'same-origin'
    };
    if (etag) { opts.headers['If-Match'] = etag; }
    if (body !== null && body !== undefined) { opts.body = JSON.stringify(body); }

    return fetch(path, opts).then(function (res) {
      return res.text().then(function (text) {
        var data = null;
        try { data = text ? JSON.parse(text) : null; } catch (e) { data = null; }
        if (!res.ok) {
          var err = new Error((data && data.error) || ('HTTP ' + res.status));
          err.status = res.status;
          err.data = data;
          throw err;
        }
        return data;
      });
    });
  }

  window.CCL = window.CCL || {};
  window.CCL.api = {
    get: function (path) { return apiRequest('GET', path); },
    send: function (method, path, body, etag) { return apiRequest(method, path, body, etag); }
  };

  function bind() {
    Array.prototype.forEach.call(document.querySelectorAll('.nav-btn'), function (btn) {
      btn.addEventListener('click', function () {
        var id = btn.dataset.view;
        if (window.location.hash !== '#' + id) {
          window.location.hash = '#' + id;
        } else {
          showView(id);
        }
      });
    });

    /* 主题切换放在外壳：主题是全局的，不该由某一个视图管。 */
    var toggle = $('theme-toggle');
    if (toggle) {
      toggle.addEventListener('click', function () {
        var root = document.documentElement;
        var cur = root.getAttribute('data-theme');
        if (!cur) {
          cur = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
        }
        root.setAttribute('data-theme', cur === 'dark' ? 'light' : 'dark');
      });
    }

    window.addEventListener('hashchange', function () { showView(viewFromHash()); });
  }

  function start() {
    bind();
    showView(viewFromHash() || 'usage');
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})();

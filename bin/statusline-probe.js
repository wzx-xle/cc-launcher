#!/usr/bin/env node
// bin/statusline-probe.js — 复测状态栏的额度通道
//
// 用途：quota-daemon-endpoint §7.6「Windows 复测」——状态栏改成走 daemon 的
// 回环端点后，额度**稳定显示**（不再随系统繁忙回落），且 `ccl quota --json`
// 的耗时分布留档。
//
// 用法:
//   node bin/statusline-probe.js
//   node bin/statusline-probe.js --runs 30 --load 8
//   node bin/statusline-probe.js --provider minimax-m3
//   node bin/statusline-probe.js --url http://127.0.0.1:4318/ccl/quota
//   node bin/statusline-probe.js --exe "C:\Users\me\scoop\apps\cc-launcher\current\cc-launcher.exe"
//   node bin/statusline-probe.js --json
//
// 它测三件事：
//
//  1. **两条通道的耗时分布**：HTTP 回环端点 vs `ccl quota --json` 命令。
//     命令那条走的是**状态栏的真实路径**（Windows 上是 cmd.exe(scoop shim)
//     → cmd.exe(app\ccl.cmd) → cc-launcher.exe）
//  2. **状态栏在"空闲"与"繁忙"两种负载下**，额度段到底是 ccl 的额度，还是
//     回落到 Claude Code 自己的 rate_limits
//  3. 距离 **250ms** 预算的余量（状态栏给每次调用设的 timeout）
//
// **判据为什么可靠**：喂给 statusline.js 的 stdin JSON 里，rate_limits 塞的
// 是**哨兵值**（5h=77.7% / 7d=88.8%）；另跑一次**不带 CCL_PROVIDER** 的作对
// 照（没有 provider 名 → 状态栏必然回落 rate_limits）。于是：
//
//   带 CCL_PROVIDER 那次的额度段 ≠ 哨兵 → 走的是 ccl 的额度通道 ✓
//   带 CCL_PROVIDER 那次的额度段 = 哨兵 → 回落了（失败）✗
//
// 不靠"ccl 的额度恰好是某个数"这种脆弱假设。

'use strict';

const { execSync, spawn, spawnSync } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');
const http = require('http');

// ---------------------------------------------------------------------------
// 参数
// ---------------------------------------------------------------------------

function parseArgs(argv) {
  const opts = {
    runs: 20,
    load: 6,
    provider: process.env.CCL_PROVIDER || '',
    url: process.env.CCL_QUOTA_URL || 'http://127.0.0.1:4318/ccl/quota',
    statusline: path.join(os.homedir(), '.claude', 'statusline.js'),
    cwd: process.cwd(),
    exe: '',
    json: false,
  };
  for (let i = 2; i < argv.length; i++) {
    const a = argv[i];
    const next = () => {
      if (i + 1 >= argv.length) {
        console.error(`选项 ${a} 缺参数`);
        process.exit(2);
      }
      return argv[++i];
    };
    switch (a) {
      case '--runs': opts.runs = Number(next()); break;
      case '--load': opts.load = Number(next()); break;
      case '--provider': opts.provider = next(); break;
      case '--url': opts.url = next(); break;
      case '--statusline': opts.statusline = next(); break;
      case '--cwd': opts.cwd = path.resolve(next()); break;
      case '--exe': opts.exe = next(); break;
      case '--json': opts.json = true; break;
      case '-h': case '--help':
        console.log(fs.readFileSync(__filename, 'utf8').split('\n').slice(2, 24).join('\n'));
        process.exit(0);
      default:
        console.error(`未知选项: ${a}`);
        process.exit(2);
    }
  }
  return opts;
}

const opts = parseArgs(process.argv);

// 状态栏给每次调用设的 timeout（Claude Code 的状态栏有 300ms 去抖窗口）。
const BUDGET_MS = 250;
// 哨兵值：Claude Code 自己的 rate_limits（构造的 stdin JSON 里塞这个）。
//
// **必须取真实百分比不可能取到的值**（> 100）：ccl 的额度百分比就在 0~100
// 之间，取 77 这类"正常值"有 1/100 的概率撞上 → 误判成"回落了"。取 4321
// 就永远撞不上。
const SENTINEL_5H = 4321;
const SENTINEL_7D = 8765;

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

function stripAnsi(s) {
  return s.replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, '').replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, '');
}

function pctOf(line, label) {
  // 形如 "5h: ███░░░░░░░ 31% -> 11/13 01:08"
  const m = line.match(new RegExp(label + ':[^\\n]*?([\\d]+(?:\\.\\d+)?)%'));
  return m ? Number(m[1]) : null;
}

function percentile(sorted, p) {
  if (!sorted.length) return null;
  const idx = Math.min(sorted.length - 1, Math.max(0, Math.ceil((p / 100) * sorted.length) - 1));
  return sorted[idx];
}

function fmtMs(v) {
  if (v == null) return '-';
  if (v < 10) return v.toFixed(2) + 'ms';
  return v.toFixed(1) + 'ms';
}

function summarize(times, failures) {
  const sorted = [...times].sort((a, b) => a - b);
  return {
    n: times.length,
    p50: percentile(sorted, 50),
    p95: percentile(sorted, 95),
    max: sorted.length ? sorted[sorted.length - 1] : null,
    min: sorted.length ? sorted[0] : null,
    failures: failures || 0,
  };
}

// ---------------------------------------------------------------------------
// 通道 1：回环 HTTP 端点（零进程）
// ---------------------------------------------------------------------------

function fetchQuota(base, provider, timeoutMs) {
  return new Promise((resolve, reject) => {
    const req = http.get(
      `${base}?provider=${encodeURIComponent(provider)}`,
      { timeout: timeoutMs },
      (res) => {
        let body = '';
        res.setEncoding('utf8');
        res.on('data', (c) => { body += c; });
        res.on('end', () => {
          if (res.statusCode !== 200) { reject(new Error(`status ${res.statusCode}`)); return; }
          try { resolve(JSON.parse(body)); } catch (e) { reject(e); }
        });
      },
    );
    const timer = setTimeout(() => req.destroy(new Error('timeout')), timeoutMs);
    req.on('error', (e) => { clearTimeout(timer); reject(e); });
    req.on('close', () => clearTimeout(timer));
  });
}

// ---------------------------------------------------------------------------
// 通道 2：命令 `ccl quota --json`（状态栏的真实路径）
// ---------------------------------------------------------------------------

function runCommand(cmd, args, timeoutMs) {
  const t0 = process.hrtime.bigint();
  try {
    const out = execSync(cmd, {
      args,
      stdio: ['ignore', 'pipe', 'pipe'],
      encoding: 'utf8',
      timeout: timeoutMs,
    });
    const ms = Number(process.hrtime.bigint() - t0) / 1e6;
    return { ok: true, ms };
  } catch (e) {
    const ms = Number(process.hrtime.bigint() - t0) / 1e6;
    // 超时（ETIMEDOUT / SIGTERM）与非零退出码都算失败——对状态栏而言效果一样：
    // catch → 回落 rate_limits。
    const timedOut = e.killed === true || e.signal === 'SIGTERM' || e.code === 'ETIMEDOUT';
    return { ok: false, ms, timedOut };
  }
}

// ---------------------------------------------------------------------------
// 状态栏实测
// ---------------------------------------------------------------------------

// 造一份 stdin JSON：rate_limits 塞**哨兵值**，好和 ccl 的额度区分开。
function buildStdin(cwd) {
  return JSON.stringify({
    model: { display_name: 'probe' },
    cwd,
    workspace: { current_dir: cwd },
    context_window: { used_percentage: 0 },
    rate_limits: {
      five_hour: { used_percentage: SENTINEL_5H, resets_at: Math.floor(Date.now() / 1000) + 3600 },
      seven_day: { used_percentage: SENTINEL_7D, resets_at: Math.floor(Date.now() / 1000) + 86400 },
    },
  });
}

// 跑一次状态栏脚本，返回 {ms, pct5h, pct7d, timedOut}
//
// **用异步 spawn 而不是 spawnSync**：spawnSync 的同步包装（等进程退出 + 同步
// 读管道）在 Windows 上要 **100ms 以上**，那是**测量工具自己的开销**，会被
// 算进"状态栏整体"里 → 数字虚高。异步 spawn 更接近 Claude Code 实际调
// statusLine 的方式（异步起进程 + 收 stdout）。
function runStatusline(input, env) {
  return new Promise((resolve) => {
    const t0 = process.hrtime.bigint();
    const child = spawn(process.execPath, [opts.statusline], { env });
    let out = '';
    let timedOut = false;
    child.stdout.setEncoding('utf8');
    child.stdout.on('data', (c) => { out += c; });
    child.stderr.on('data', () => { /* 忽略 */ });
    child.stdin.end(input);
    const guard = setTimeout(() => { timedOut = true; child.kill('SIGKILL'); }, 10000);
    child.on('close', () => {
      clearTimeout(guard);
      const ms = Number(process.hrtime.bigint() - t0) / 1e6;
      const plain = stripAnsi(out);
      const quotaLine = plain.split('\n').find((l) => l.includes('5h:')) || '';
      resolve({
        ms,
        pct5h: pctOf(quotaLine, '5h'),
        pct7d: pctOf(quotaLine, '7d'),
        timedOut,
      });
    });
  });
}

// ---------------------------------------------------------------------------
// 繁忙负载：起几个烧满 CPU 的子进程（模拟 Defender 扫描 / hook 并发）
// ---------------------------------------------------------------------------

function startLoad(n) {
  const procs = [];
  const script = 'const t=Date.now();while(Date.now()-t<120000){}';
  for (let i = 0; i < n; i++) {
    try {
      procs.push(spawn(process.execPath, ['-e', script], { stdio: 'ignore', detached: false }));
    } catch { /* 起不来就少几个 */ }
  }
  return {
    stop() {
      for (const p of procs) {
        try { p.kill('SIGKILL'); } catch { /* 已经死了 */ }
      }
    },
  };
}

// ---------------------------------------------------------------------------
// 主流程
// ---------------------------------------------------------------------------

async function main() {
  const report = { env: {}, channels: {}, statusline: {}, conclusion: [] };

  // ---- 环境 ----
  let cclPath = '';
  try {
    cclPath = execSync(process.platform === 'win32' ? 'where ccl' : 'which ccl', {
      stdio: ['ignore', 'pipe', 'pipe'], encoding: 'utf8',
    }).split('\n')[0].trim();
  } catch { /* 找不到 */ }

  report.env = {
    node: process.version,
    platform: `${process.platform} ${os.release()}`,
    ccl: cclPath || '(找不到)',
    statusline: opts.statusline,
    statuslineExists: fs.existsSync(opts.statusline),
    cwd: opts.cwd,
    provider: opts.provider,
    url: opts.url,
    runs: opts.runs,
    load: opts.load,
  };

  if (!report.env.statuslineExists) {
    console.error(`状态栏脚本不存在: ${opts.statusline}（用 --statusline 指定）`);
    process.exit(2);
  }
  if (!opts.provider) {
    console.error('没有 provider 名。用 --provider 指定，或在 ccl 起的会话里跑（它会注入 CCL_PROVIDER）。');
    process.exit(2);
  }

  // ---- 端点可达性 ----
  let endpoint = { reachable: false, status: null, error: '' };
  try {
    const q = await fetchQuota(opts.url, opts.provider, 2000);
    endpoint = { reachable: true, status: 200, error: '', dimensions: (q.dimensions || []).length };
  } catch (e) {
    endpoint = { reachable: false, status: null, error: String(e.message || e) };
  }
  report.env.endpoint = endpoint;

  // ---- 通道耗时（空闲）----
  const httpTimes = [], httpFails = [];
  for (let i = 0; i < opts.runs; i++) {
    const t0 = process.hrtime.bigint();
    try {
      await fetchQuota(opts.url, opts.provider, BUDGET_MS);
      httpTimes.push(Number(process.hrtime.bigint() - t0) / 1e6);
    } catch (e) {
      httpTimes.push(Number(process.hrtime.bigint() - t0) / 1e6);
      httpFails.push(1);
    }
  }
  report.channels.http = summarize(httpTimes, httpFails.length);

  const cmdTimes = [], cmdFails = [], cmdTimeouts = [];
  for (let i = 0; i < opts.runs; i++) {
    const r = runCommand('ccl', ['quota', '--json'], BUDGET_MS);
    cmdTimes.push(r.ms);
    if (!r.ok) { cmdFails.push(1); if (r.timedOut) cmdTimeouts.push(1); }
  }
  report.channels.cmd = summarize(cmdTimes, cmdFails.length);
  report.channels.cmd.timeouts = cmdTimeouts.length;

  // Windows：scoop 装的 ccl 是 .cmd shim → 真实 exe 在 apps 目录里。自动找一下，
  // 好让"直调 exe"那一行自动出现（用来证明耗时全在 cmd.exe 两层）。
  //
  // 两条线索（scoop 可能装在 D:\Scoop 这种非默认位置）：
  //   1. `where ccl` 的结果 → <scoop>/shims/ccl → 上溯两级得到 <scoop>
  //   2. SCOOP 环境变量（scoop 自己会设）
  if (!opts.exe && process.platform === 'win32') {
    const scoopRoots = [];
    if (cclPath) {
      const shimDir = path.dirname(cclPath);
      const root = path.dirname(shimDir);
      if (path.basename(shimDir).toLowerCase() === 'shims') scoopRoots.push(root);
    }
    if (process.env.SCOOP) scoopRoots.push(process.env.SCOOP);
    scoopRoots.push(path.join(os.homedir(), 'scoop'));
    for (const root of scoopRoots) {
      for (const rel of [
        ['apps', 'cc-launcher', 'current', 'cc-launcher.exe'],
        ['apps', 'cc-launcher', 'current', 'cc-launcher', 'cc-launcher.exe'],
      ]) {
        const c = path.join(root, ...rel);
        if (fs.existsSync(c)) { opts.exe = c; break; }
      }
      if (opts.exe) break;
    }
  }

  // 可选：直调真实 exe（绕过 cmd.exe 两层）—— 用来证明"耗时全在进程启动"
  if (opts.exe && !fs.existsSync(opts.exe)) {
    console.error(`--exe 指定的文件不存在: ${opts.exe}`);
    process.exit(2);
  }
  if (opts.exe) {
    const exeTimes = [], exeFails = [];
    for (let i = 0; i < opts.runs; i++) {
      const r = runCommand(opts.exe, ['quota', '--json'], BUDGET_MS);
      exeTimes.push(r.ms);
      if (!r.ok) exeFails.push(1);
    }
    report.channels.exe = summarize(exeTimes, exeFails.length);
  }

  // ---- 状态栏实测：空闲 + 繁忙 ----
  const stdin = buildStdin(opts.cwd);
  const envWithCcl = { ...process.env, CCL_PROVIDER: opts.provider, CCL_QUOTA_URL: opts.url };
  const envWithout = { ...process.env };
  delete envWithout.CCL_PROVIDER;
  delete envWithout.CCL_QUOTA_URL;

  // 对照组：不给 CCL_PROVIDER → 状态栏必然回落 rate_limits（= 哨兵值）
  const control = await runStatusline(stdin, envWithout);
  report.statusline.control = control;
  if (control.pct5h !== SENTINEL_5H) {
    console.error(`对照组异常：不带 CCL_PROVIDER 时额度段不是哨兵值（${control.pct5h}）——` +
      'statusline.js 的回落逻辑可能变了，先人工核对再采信下面的数字。');
    process.exit(2);
  }

  const measure = async (env) => {
    const times = [], viaCcl = [], fellBack = [], timeouts = [];
    for (let i = 0; i < opts.runs; i++) {
      const r = await runStatusline(stdin, env);
      times.push(r.ms);
      if (r.timedOut) timeouts.push(1);
      if (r.pct5h !== null && r.pct5h !== SENTINEL_5H) viaCcl.push(1);
      else fellBack.push(1);
    }
    return {
      times: summarize(times, 0),
      viaCcl: viaCcl.length,
      fellBack: fellBack.length,
      timeouts: timeouts.length,
    };
  };

  report.statusline.idle = await measure(envWithCcl);

  let load = null;
  if (opts.load > 0) {
    load = startLoad(opts.load);
    await new Promise((r) => setTimeout(r, 800)); // 让负载先把 CPU 吃掉
    report.statusline.busy = await measure(envWithCcl);
    load.stop();
  }

  // ---- 耗时构成：状态栏那 286ms 花在哪 ----
  //
  // 状态栏脚本 = node 启动 + 若干子进程（常见的是 `git branch --show-current`）
  // + 额度通道。额度通道已经是毫秒级了，剩下的要单独量出来才知道该优化谁。
  const breakdown = {};
  {
    // node 启动基线：跑一个空脚本
    const t0 = process.hrtime.bigint();
    for (let i = 0; i < Math.min(opts.runs, 10); i++) {
      spawnSync(process.execPath, ['-e', '0'], { stdio: 'ignore' });
    }
    breakdown.nodeStartup = Number(process.hrtime.bigint() - t0) / 1e6 / Math.min(opts.runs, 10);
  }
  if (fs.existsSync(path.join(opts.cwd, '.git'))) {
    const t0 = process.hrtime.bigint();
    let ok = 0;
    for (let i = 0; i < Math.min(opts.runs, 10); i++) {
      const r = spawnSync('git', ['branch', '--show-current'], {
        cwd: opts.cwd, stdio: ['ignore', 'pipe', 'ignore'], encoding: 'utf8',
      });
      if (r.status === 0) ok++;
    }
    breakdown.gitBranch = Number(process.hrtime.bigint() - t0) / 1e6 / Math.min(opts.runs, 10);
    breakdown.gitBranchOk = ok;

    // 零进程方案：直接读 .git/HEAD（状态栏脚本现在用的就是这个）
    const t1 = process.hrtime.bigint();
    for (let i = 0; i < Math.min(opts.runs, 10); i++) {
      try {
        const dotgit = path.join(opts.cwd, '.git');
        let gitDir = dotgit;
        const st = fs.statSync(dotgit);
        if (st.isFile()) {
          const m = fs.readFileSync(dotgit, 'utf8').match(/gitdir:\s*(.+)/);
          if (!m) continue;
          gitDir = m[1].trim();
        }
        fs.readFileSync(path.join(gitDir, 'HEAD'), 'utf8');
      } catch { /* 读不到就当没有 */ }
    }
    breakdown.gitHeadRead = Number(process.hrtime.bigint() - t1) / 1e6 / Math.min(opts.runs, 10);
  }
  report.breakdown = breakdown;

  // ---- 结论 ----
  const c = report.conclusion;
  const httpP95 = report.channels.http.p95;
  const cmdP95 = report.channels.cmd.p95;
  if (httpP95 != null) {
    c.push(`HTTP 通道 p95 = ${fmtMs(httpP95)}，距 ${BUDGET_MS}ms 预算余量 ${fmtMs(BUDGET_MS - httpP95)}` +
      `（${(BUDGET_MS / Math.max(httpP95, 0.01)).toFixed(0)} 倍）`);
  }
  if (cmdP95 != null) {
    c.push(`命令通道 p95 = ${fmtMs(cmdP95)}，超时 ${report.channels.cmd.timeouts}/${opts.runs} 次` +
      (cmdP95 > BUDGET_MS ? ' —— **已越过预算**：繁忙时必然超时 → 状态栏静默回落' : ' —— 在预算内'));
  }
  for (const scene of ['idle', 'busy']) {
    const s = report.statusline[scene];
    if (!s) continue;
    const ok = s.viaCcl === s.times.n;
    c.push(`${scene === 'idle' ? '空闲' : '繁忙（' + opts.load + ' 个 CPU hog）'}：` +
      `状态栏 ${s.times.n} 次里 ${s.viaCcl} 次读到 ccl 的额度、${s.fellBack} 次回落 rate_limits` +
      (ok ? ' ✓ 稳定' : ' ✗ **有回落**'));
    // 整体耗时 vs 预算 —— 额度通道再快，整个脚本超了预算照样会被 abort
    if (s.times.p50 != null && s.times.p50 > BUDGET_MS) {
      c.push(`  ⚠ 状态栏整体 p50 = ${fmtMs(s.times.p50)}，**超过 ${BUDGET_MS}ms 预算** —— ` +
        '额度数据是对的，但 Claude Code 可能直接 abort 这次调用（状态栏不刷新）。' +
        '看下面的"耗时构成"：瓶颈多半不在额度通道，而在 node 启动或某个子进程');
    }
  }
  if (report.breakdown) {
    const b = report.breakdown;
    const parts = [`node 启动 ${fmtMs(b.nodeStartup)}`];
    if (b.gitBranch != null) parts.push(`git branch 子进程 ${fmtMs(b.gitBranch)}`);
    if (report.channels.http) parts.push(`额度通道 ${fmtMs(report.channels.http.p50)}`);
    c.push(`耗时构成：${parts.join(' + ')}（node 启动 + 子进程 + 额度通道）`);
    if (b.gitBranch != null) {
      c.push(`  git branch 子进程 ${fmtMs(b.gitBranch)} vs 零进程读 .git/HEAD ${fmtMs(b.gitHeadRead)}` +
        `（省 ${fmtMs(b.gitBranch - b.gitHeadRead)}）`);
      if (b.gitBranch > BUDGET_MS / 4) {
        c.push('  → git 那一次子进程就吃掉大半预算：状态栏脚本应该改成零进程读 .git/HEAD');
      }
    }
  }

  if (opts.json) {
    console.log(JSON.stringify(report, null, 2));
  } else {
    printHuman(report);
  }

  // 有回落 → 退出码非 0（方便 CI / 脚本判定）
  const bad = Object.values(report.statusline)
    .filter((s) => s && s.times)
    .some((s) => s.fellBack > 0);
  process.exit(bad ? 1 : 0);
}

function printHuman(r) {
  const L = [];
  const e = r.env;
  L.push('=== 环境');
  L.push(`  node         ${e.node}`);
  L.push(`  平台         ${e.platform}`);
  L.push(`  ccl          ${e.ccl}`);
  L.push(`  状态栏脚本   ${e.statusline}`);
  L.push(`  项目目录     ${e.cwd}`);
  L.push(`  provider     ${e.provider}`);
  L.push(`  额度端点     ${e.url}  → ${e.endpoint.reachable ? '可达（200，' + e.endpoint.dimensions + ' 个维度）' : '不可达：' + e.endpoint.error}`);
  L.push('');
  L.push(`=== 通道耗时（每次调用都新起进程 = 状态栏的真实路径；n=${e.runs}）`);
  L.push('  通道                          p50      p95      max      超时/失败');
  const row = (name, s, timeouts) => {
    if (!s) return;
    const t = timeouts != null ? timeouts : '-';
    L.push(`  ${name.padEnd(28)} ${fmtMs(s.p50).padEnd(8)} ${fmtMs(s.p95).padEnd(8)} ` +
      `${fmtMs(s.max).padEnd(8)} ${`${t}/${s.failures}`}`);
  };
  row('HTTP（回环端点，零进程）', r.channels.http);
  row('命令（ccl quota --json）', r.channels.cmd, r.channels.cmd.timeouts);
  row('直调 exe（绕过 cmd.exe）', r.channels.exe);
  L.push('');
  L.push('=== 状态栏实测（喂带哨兵 rate_limits 的 stdin）');
  L.push(`  对照组（不给 CCL_PROVIDER）→ 回落哨兵 5h=${r.statusline.control.pct5h}% ✓`);
  const srow = (name, s) => {
    if (!s) return;
    L.push(`  ${name.padEnd(28)} 读到 ccl 额度 ${String(s.viaCcl).padStart(2)}/${s.times.n}` +
      `   回落 ${String(s.fellBack).padStart(2)}   脚本超时 ${s.timeouts}` +
      `   p50 ${fmtMs(s.times.p50)}`);
  };
  srow('空闲', r.statusline.idle);
  srow(`繁忙（${e.load} 个 CPU hog）`, r.statusline.busy);
  L.push('');
  L.push('=== 耗时构成（状态栏整体那几百毫秒花在哪）');
  if (r.breakdown) {
    L.push(`  node 启动（空脚本）      ${fmtMs(r.breakdown.nodeStartup)}`);
    if (r.breakdown.gitBranch != null) {
      L.push(`  git branch 子进程        ${fmtMs(r.breakdown.gitBranch)}  （${r.breakdown.gitBranchOk} 次成功）`);
      L.push(`  零进程读 .git/HEAD       ${fmtMs(r.breakdown.gitHeadRead)}`);
    }
    L.push(`  额度通道（HTTP）         ${fmtMs(r.channels.http ? r.channels.http.p50 : null)}`);
  }
  L.push('');
  L.push('=== 结论');
  for (const c of r.conclusion) L.push(`  - ${c}`);
  console.log(L.join('\n'));
}

main().catch((e) => {
  console.error('probe 失败:', e && e.stack ? e.stack : e);
  process.exit(2);
});

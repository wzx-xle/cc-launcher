#!/usr/bin/env node
// bin/patch-statusline-git.js — 把状态栏脚本的"取分支"从子进程改成零进程
//
// 用途：Windows 上起一个进程要付 Defender 对未签名 exe 的实时深扫——实测
// （2026-11-13，Windows 11 24H2）：
//
//   node 启动（空脚本）          88~123ms
//   git branch 子进程          ~62ms     ← 这个可以省掉
//   cc-launcher.exe 启动      ~259ms
//   额度通道（HTTP，零进程）    5~6ms
//
// 状态栏的总预算是 250ms（Claude Code 的去抖窗口）。`git branch --show-current`
// 那一次子进程纯属浪费——分支名就在 `.git/HEAD` 里，直接读文件是 0.26ms。
//
// 用法:
//   node bin/patch-statusline-git.js                      # 改 ~/.claude/statusline.js
//   node bin/patch-statusline-git.js "C:\path\statusline.js"
//
// **幂等**：已经是零进程版就什么都不做。
// **安全**：先备份（.bak-<时间戳>），改完做语法自检，失败自动回滚。

'use strict';

const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const target = process.argv[2] ||
  path.join(os.homedir(), '.claude', 'statusline.js');

if (!fs.existsSync(target)) {
  console.error(`找不到状态栏脚本: ${target}`);
  process.exit(2);
}

let src = fs.readFileSync(target, 'utf8');

if (src.includes('readGitBranch')) {
  console.log(`✓ 已经是零进程版（用到 readGitBranch），无需改动: ${target}`);
  process.exit(0);
}

// 老块的结构（用户自己的脚本可能空白细节不同 → 用宽松匹配）：
//   const gitCwd = ...;
//   let gitBranch = '';
//   if (gitCwd) {
//     try { ... execSync('git branch --show-current', ...) ... } catch {}
//   }
const re = /const gitCwd = [^;]*;\nlet gitBranch = '';\nif \(gitCwd\) \{[\s\S]*?\n\}\n/;

if (!re.test(src)) {
  console.error('没找到预期的 git 分支代码块——你的脚本结构与已知形态不同，' +
    '请手工替换（见 docs/provider-quota.md 的说明）。没有改动任何文件。');
  process.exit(2);
}

const replacement = `const gitCwd = data.workspace?.current_dir ?? data.cwd;
const branchName = readGitBranch(gitCwd);
const gitBranch = branchName ? \`\${DIM}git(\${branchName})\${RESET}\` : '';

// readGitBranch 零进程读当前分支（直接读 .git/HEAD）。
//
// **为什么不再 execSync('git branch --show-current')**：Windows 上起一个进程
// 要付 **Defender 对未签名 exe 的实时深扫**——实测（2026-11-13，Windows 11）：
//
//   node 启动（空脚本）          88~123ms
//   git branch 子进程          ~62ms     ← 纯浪费
//   cc-launcher.exe 启动      ~259ms
//   额度通道（HTTP，零进程）    5~6ms
//
// 状态栏的总预算是 250ms（Claude Code 的去抖窗口）。零进程读 .git/HEAD 是
// 纯文件读（0.26ms）。
//
// **行为保持一致**：detached HEAD 时不显示分支（与 \`git branch --show-current\`
// 一致，它在 detached 时输出空）。
function readGitBranch(cwd) {
  if (!cwd) return '';
  try {
    const dotgit = path.join(cwd, '.git');
    let gitDir = dotgit;
    let st = fs.statSync(dotgit);
    if (st.isFile()) {
      // worktree / 子模块：.git 是文件，内容是 "gitdir: <路径>"
      const m = fs.readFileSync(dotgit, 'utf8').match(/gitdir:\\s*(.+)/);
      if (!m) return '';
      gitDir = m[1].trim();
      st = fs.statSync(gitDir);
      if (!st.isDirectory()) return '';
    } else if (!st.isDirectory()) {
      return '';
    }
    const head = fs.readFileSync(path.join(gitDir, 'HEAD'), 'utf8').trim();
    const m = head.match(/^ref:\\s*refs\\/heads\\/(.+)$/);
    // detached HEAD（HEAD 是裸 sha）→ 与 git branch --show-current 一致：不显示
    return m ? m[1] : '';
  } catch {
    return '';
  }
}
`;

const patched = src.replace(re, replacement);

const backup = `${target}.bak-${new Date().toISOString().replace(/[:.]/g, '-')}`;
fs.writeFileSync(backup, src, 'utf8');
fs.writeFileSync(target, patched, 'utf8');

// 语法自检：跑 node --check，失败就回滚
const check = spawnSync(process.execPath, ['--check', target], { encoding: 'utf8' });
if (check.status !== 0) {
  fs.writeFileSync(target, src, 'utf8');
  console.error('✗ 改完语法检查失败，已回滚（原文件未变）。node --check 的输出：\n' + check.stderr);
  process.exit(1);
}

console.log(`✓ 已改成零进程读 .git/HEAD: ${target}`);
console.log(`  备份: ${backup}`);
console.log('  验证: node bin/statusline-probe.js --runs 30 --load 8 --cwd <项目目录>');

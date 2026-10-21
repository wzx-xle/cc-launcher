package release

// WindowsShim 是 Windows 版 zip 里 ccl.cmd 的完整内容。
//
// ccl.cmd 是 scoop manifest 的 `bin` 入口（用户敲 `ccl` 时由 cmd.exe 解析它），
// 它把命令转发给同目录的 cc-launcher.exe：
//
//	@echo off
//	"%~dp0cc-launcher.exe" %*
//
// **行尾必须是 CRLF**：.cmd 是 cmd.exe 读的批处理文件，Windows 惯例（也是
// cmd.exe 自己写出来的）是 CRLF。LF-only 的 .cmd 在最简单的场景通常也能跑，
// 但 cmd.exe 的行解析在 label / goto / 块结构（`(` `)`）上有已知的 LF 坑。
// v0.14.0 及之前的 zip 里是 LF（bash heredoc 写的），本 change 改成显式 CRLF。
//
// **单一来源**：release workflow（.github/workflows/release.yml 的 package 步骤）
// 与本机 build.sh（pack_windows_zip_for_scoop）都从这里取字节 ——
// 走 `cmd/release-notes windows-shim`，不再各写一份 heredoc/函数。
// 两份实现各写各的正是 2026-10-21 修掉的 marker 解析那类漂移的来源。
//
// 用 %~dp0 取自身目录（绝对路径），避免依赖当前工作目录 / PATH。
const WindowsShim = "@echo off\r\n" +
	"\"%~dp0cc-launcher.exe\" %*\r\n"

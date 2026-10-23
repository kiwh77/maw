package codebuddy

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// entry 是一次入口解析的结果。
//
// 四个字段各自回答一个不同的问题，「入口是不是存在」不足以构成启动规格：
//   - Requested：配置里写的那个路径（保留原样，报错时有话可说）；
//   - Resolved：符号链接全部展开后的真实文件；
//   - Runtime：真实运行时（解释器）的绝对路径，自包含二进制为空；
//   - ViaRuntime：启动时是否必须显式经过 Runtime。
type entry struct {
	Requested  string
	Resolved   string
	Runtime    string
	ViaRuntime bool
}

// resolveEntry 解析入口与运行时。
//
// 返回的 error 是「人话原因」，不带端口错误码：同一个失败在 Probe 里是 entry_missing、
// 在 Launch 里是 entry_unavailable，由调用方决定码，避免这里替上层做判定。
func resolveEntry(entryPath, runtimePath string) (entry, error) {
	requested := strings.TrimSpace(entryPath)
	if requested == "" {
		return entry{}, fmt.Errorf("入口为空")
	}
	if !filepath.IsAbs(requested) {
		return entry{}, fmt.Errorf("入口 %q 不是绝对路径", requested)
	}
	real, err := resolveExecutable(requested)
	if err != nil {
		return entry{}, err
	}
	ent := entry{Requested: requested, Resolved: real}

	if rt := strings.TrimSpace(runtimePath); rt != "" {
		if !filepath.IsAbs(rt) {
			return entry{}, fmt.Errorf("运行时 %q 不是绝对路径", rt)
		}
		resolvedRuntime, err := resolveExecutable(rt)
		if err != nil {
			return entry{}, fmt.Errorf("运行时不可用：%w", err)
		}
		ent.Runtime = resolvedRuntime
		ent.ViaRuntime = true
		return ent, nil
	}

	interp, ok, err := shebangInterpreter(real)
	if err != nil {
		return entry{}, err
	}
	if !ok {
		// 没 shebang：要么是自包含二进制，要么是别的东西。是否可执行已在上一步验过。
		return ent, nil
	}
	resolvedInterp, err := resolveInterpreter(interp)
	if err != nil {
		return entry{}, err
	}
	ent.Runtime = resolvedInterp
	ent.ViaRuntime = true
	return ent, nil
}

// resolveExecutable 把路径展开成真实文件，并确认它确实可执行。
func resolveExecutable(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("无法解析 %q：%w", path, err)
	}
	if !filepath.IsAbs(real) {
		// EvalSymlinks 对绝对输入返回绝对路径；这里只是不让相对路径漏进来。
		return "", fmt.Errorf("%q 解析后不是绝对路径", path)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("无法读取 %q：%w", real, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%q 是目录，不是可执行文件", real)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%q 没有可执行权限", real)
	}
	return real, nil
}

// shebangInterpreter 读出脚本第一行里的解释器。
//
// 只读前 512 字节，不把整个 CLI 文件读进内存（它是几 MB 的打包产物）。
func shebangInterpreter(path string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, fmt.Errorf("无法读取入口 %q：%w", path, err)
	}
	defer f.Close()

	head := make([]byte, 512)
	n, err := f.Read(head)
	if err != nil && err != io.EOF {
		return "", false, fmt.Errorf("无法读取入口 %q：%w", path, err)
	}
	head = head[:n]
	if len(head) < 2 || head[0] != '#' || head[1] != '!' {
		return "", false, nil
	}
	line := string(head[2:])
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", false, fmt.Errorf("%q 的 shebang 为空", path)
	}

	prog := fields[0]
	if filepath.Base(prog) == "env" {
		// `#!/usr/bin/env node` 与 `#!/usr/bin/env -S node --flag` 都要跳过 env 自己的选项。
		rest := fields[1:]
		for len(rest) > 0 && (strings.HasPrefix(rest[0], "-") || strings.Contains(rest[0], "=")) {
			rest = rest[1:]
		}
		if len(rest) == 0 {
			return "", false, fmt.Errorf("%q 的 shebang 用 env 但没有给出解释器", path)
		}
		prog = rest[0]
	}
	return prog, true, nil
}

// resolveInterpreter 把 shebang 里的解释器名解析成真实绝对路径。
func resolveInterpreter(prog string) (string, error) {
	if filepath.IsAbs(prog) {
		real, err := resolveExecutable(prog)
		if err != nil {
			return "", fmt.Errorf("运行时不可用：%w", err)
		}
		return real, nil
	}
	found, err := exec.LookPath(prog)
	if err != nil {
		return "", fmt.Errorf("入口声明的运行时 %q 不在 PATH 里：%w", prog, err)
	}
	real, err := resolveExecutable(found)
	if err != nil {
		return "", fmt.Errorf("运行时不可用：%w", err)
	}
	return real, nil
}

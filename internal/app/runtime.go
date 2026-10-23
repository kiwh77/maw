package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kiwh77/maw/internal/engine/dispatch"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/runner/process"
)

const (
	runnerTokenEnv  = "MAW_RUNNER_TOKEN"
	runnerTokenFile = "bootstrap.token"
	runnerLockFile  = "runner.lock"
)

// Runtime 是受管执行的应用侧装配：中枢协调与独立 Runner 进程分离。
//
// 中枢（本进程）只推进 Operation／意图；真正监督工具进程的是 `maw __runner`，
// 窗口或 HTTP 连接退出不会带走 Runner（独立进程组）。
type Runtime struct {
	pc       *projCtx
	stateDir string
	selfBin  string
}

// NewRuntime 从工程上下文装配运行时。selfBin 为空时用当前可执行文件。
func NewRuntime(pc *projCtx, selfBin string) (*Runtime, error) {
	if pc == nil {
		return nil, errx.New("runtime: missing project", "maw init")
	}
	if selfBin == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		selfBin = exe
	}
	global, err := home.GlobalDir()
	if err != nil {
		return nil, err
	}
	state := filepath.Join(global, "runtime", pc.ID())
	if err := os.MkdirAll(state, 0o755); err != nil {
		return nil, err
	}
	return &Runtime{pc: pc, stateDir: state, selfBin: selfBin}, nil
}

// StateDir 返回本工程受管运行状态根目录。
func (rt *Runtime) StateDir() string {
	if rt == nil {
		return ""
	}
	return rt.stateDir
}

// OpDir 返回单个 Operation 的本机状态目录。
func (rt *Runtime) OpDir(operationID string) string {
	return filepath.Join(rt.stateDir, "ops", operationID)
}

// ManagedView 返回配置中的受管执行视图（供命令与看板）。
func (rt *Runtime) ManagedView() map[string]any {
	enabled, limit := false, 1
	ver := 1
	if rt != nil && rt.pc != nil && rt.pc.Config != nil {
		enabled, limit = rt.pc.Config.AutoExecution()
		ver = rt.pc.Config.Managed.ConfigVersion
		if ver == 0 {
			ver = 1
		}
	}
	return map[string]any{
		"config_version":          ver,
		"auto_enabled":            enabled,
		"managed_execution_limit": limit,
		"platform":                runtime.GOOS,
		"platform_supported":      runtime.GOOS == "darwin",
	}
}

// AssertPlatform 在非验证平台上拒绝受管启动。
func (rt *Runtime) AssertPlatform() error {
	if runtime.GOOS != "darwin" {
		return i18n.E("err.managed_platform", "next.manual_or_observe", runtime.GOOS)
	}
	return nil
}

// runnerLauncher 通过同一二进制的内部入口拉起独立 Runner。
type runnerLauncher struct {
	rt *Runtime
}

func (l runnerLauncher) Launch(ctx context.Context, req dispatch.LaunchRequest) (dispatch.ProcessIdentity, error) {
	if err := ctx.Err(); err != nil {
		return dispatch.ProcessIdentity{}, err
	}
	if l.rt == nil {
		return dispatch.ProcessIdentity{}, errx.New("runtime missing", "")
	}
	if err := l.rt.AssertPlatform(); err != nil {
		return dispatch.ProcessIdentity{}, err
	}
	opID := string(req.Operation.OperationID)
	opDir := l.rt.OpDir(opID)
	if err := os.MkdirAll(opDir, 0o755); err != nil {
		return dispatch.ProcessIdentity{}, err
	}
	token, err := process.NewNonce()
	if err != nil {
		return dispatch.ProcessIdentity{}, err
	}
	tokenPath := filepath.Join(opDir, runnerTokenFile)
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		return dispatch.ProcessIdentity{}, err
	}
	repo := l.rt.pc.Repo
	if !filepath.IsAbs(repo) {
		abs, err := filepath.Abs(repo)
		if err != nil {
			return dispatch.ProcessIdentity{}, err
		}
		repo = abs
	}
	// process.Start 会 Setpgid：中枢退出不会带走 Runner。
	p, err := process.Start(process.StartSpec{
		RunID: opID,
		Argv: []string{
			l.rt.selfBin,
			"__runner",
			"--operation", opID,
			"--nonce", req.Nonce,
			"--state-dir", l.rt.stateDir,
			"--token", token,
		},
		Dir:   repo,
		Nonce: req.Nonce,
		Env:   []string{runnerTokenEnv + "=" + token},
	})
	if err != nil {
		return dispatch.ProcessIdentity{}, err
	}
	started := p.Identity.Start
	if started.IsZero() {
		started = time.Now().UTC()
	}
	return dispatch.ProcessIdentity{
		PID:       p.Identity.PID,
		PGID:      p.Identity.PGID,
		Nonce:     req.Nonce,
		StartedAt: started,
	}, nil
}

// BuildCoordinator 装配一个使用独立 Runner 启动端口的协调者。
func (rt *Runtime) BuildCoordinator(queue dispatch.Queue) (*dispatch.Coordinator, error) {
	if queue == nil {
		return nil, errx.New("runtime: missing queue", "")
	}
	return dispatch.New(dispatch.Config{
		StateDir:    rt.stateDir,
		Coordinator: fmt.Sprintf("maw-hub-%d", os.Getpid()),
		Queue:       queue,
		Launcher:    runnerLauncher{rt: rt},
	})
}

// EnsureManagedAllowed 检查配置与平台。
//
// explicitTask=true 表示调用方指定了任务／节点：即使 auto_enabled=false 也允许
// （显式启动 ≠ 自动选题）。未指定任务时要求 auto_enabled。
func (rt *Runtime) EnsureManagedAllowed(explicitTask bool) error {
	if rt == nil || rt.pc == nil || rt.pc.Config == nil {
		return errx.New("runtime: no config", "maw init")
	}
	view := rt.ManagedView()
	if !explicitTask {
		if enabled, _ := view["auto_enabled"].(bool); !enabled {
			return i18n.E("err.managed_auto_off", "next.managed_explicit")
		}
	}
	limit, _ := view["managed_execution_limit"].(int)
	if limit < 1 {
		return i18n.E("err.managed_limit_required", "next.fix_toml")
	}
	return rt.AssertPlatform()
}

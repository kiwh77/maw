package platform

import (
	"os"
	"strings"
	"testing"
)

// 本文件把「CI 有没有真的覆盖这些面」从口头约定变成机检。
//
// 为什么用文本匹配而不是 YAML 解析：仓库不引第三方依赖，标准库里没有 YAML。
// 这里要固化的都是「某条命令必须在／不许在」这种字面事实，文本匹配足够，
// 而且改坏了 ci.yml 的缩进也能被抓到（job id 的匹配带缩进前缀）。

const (
	ciWorkflowPath      = "../../.github/workflows/ci.yml"
	releaseWorkflowPath = "../../.github/workflows/release.yml"
)

// requiredCIJobs 是 CI 必须具备的任务集合。
//
// 每个 job 对应一种「不能被别的 job 掩盖」的失败面：
// 主干测试、可执行构建、静态导出、前端单测、真实浏览器、平台矩阵、桌面前置。
var requiredCIJobs = []string{
	"go-core",
	"go-build",
	"static-export",
	"frontend-unit",
	"frontend-browser",
	"platform-matrix",
	"desktop-prereq",
}

// forbiddenCIPatterns 是「把失败藏进绿灯」的写法，出现即视为回归。
var forbiddenCIPatterns = []string{
	"--grep-invert", // 跳过已知红用例
	"continue-on-error: true", // 让 job 红了也当绿
	"|| true",                 // 吞掉命令退出码
}

func readWorkflow(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到工作流 %s: %v", path, err)
	}
	return string(raw)
}

// readWorkflowCode 返回去掉注释后的工作流正文。
//
// 为什么要剥注释：本仓库的 ci.yml 在文件头用注释解释「不许用 --grep-invert」，
// 直接对原文做禁用词匹配会把解释本身判成违规。检查的应该是真正会执行的命令行。
func readWorkflowCode(t *testing.T, path string) string {
	t.Helper()
	var b strings.Builder
	for _, line := range strings.Split(readWorkflow(t, path), "\n") {
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// TestCIJobsPresent 验证 ci.yml 定义了全部必需 job。
//
// 匹配带两空格缩进，所以把 job 误缩进到别的层级会失败——那不是格式问题，
// 那会让 job 变成某个 mapping 的键，CI 根本不跑它。
func TestCIJobsPresent(t *testing.T) {
	body := readWorkflow(t, ciWorkflowPath)
	for _, job := range requiredCIJobs {
		if !strings.Contains(body, "\n  "+job+":\n") {
			t.Fatalf("ci.yml 缺少 job %q（期望行首两空格 + 冒号）", job)
		}
	}
}

// TestCICoverageFloorNotLowered 验证覆盖率下限没有被下调。
//
// 下限只许上调：下调一次，就等于把已有的未覆盖代码合法化，之后没人会再看它。
func TestCICoverageFloorNotLowered(t *testing.T) {
	body := readWorkflow(t, ciWorkflowPath)
	if !strings.Contains(body, "t+0 >= 35") {
		t.Fatal("ci.yml 的覆盖率下限不是 35；下调下限会掩盖已有失败")
	}
	if !strings.Contains(body, "-coverprofile=coverage.out") {
		t.Fatal("ci.yml 不再生成覆盖率文件，下限检查会失去数据源")
	}
}

// TestCINoFailureMasking 验证 CI 没有使用任何「跳过／吞掉失败」的写法。
//
// 已知红用例（vn-981 键盘导航）要暴露，不许用 --grep-invert 摘出去。
func TestCINoFailureMasking(t *testing.T) {
	body := readWorkflowCode(t, ciWorkflowPath)
	for _, pattern := range forbiddenCIPatterns {
		if strings.Contains(body, pattern) {
			t.Fatalf("ci.yml 出现掩盖失败的写法 %q；红就是红，不许绕过", pattern)
		}
	}
}

// TestCIRunsFullGoSuiteAndBrowserSuite 验证两个「全量」入口没有被收窄。
//
// go test 必须跑 ./...（不带 -run），浏览器必须跑整个 playwright 套件（不带 --grep）。
func TestCIRunsFullGoSuiteAndBrowserSuite(t *testing.T) {
	body := readWorkflow(t, ciWorkflowPath)
	if !strings.Contains(body, "go test ./...") {
		t.Fatal("ci.yml 不再跑 go test ./...，跨包集成会失去覆盖")
	}
	if !strings.Contains(body, "go test ./... -count=1 -coverprofile=coverage.out") {
		t.Fatal("go-core 的 go test ./... 缺少 -count=1 -coverprofile（前者防空缓存假绿，后者是下限的数据源）")
	}
	if !strings.Contains(body, "npm --prefix tests/browser test") {
		t.Fatal("ci.yml 不再跑 npm --prefix tests/browser test")
	}
	if strings.Contains(body, "playwright test --grep") {
		t.Fatal("浏览器套件被 --grep 收窄，等于跳过其余用例")
	}
}

// TestCIDocumentsEveryJob 验证「job 清单」在代码与文档两侧一致。
//
// 只往 ci.yml 加 job 而不同步平台矩阵文档，读者会以为 CI 没覆盖那块；
// 只在文档里写 job 而 ci.yml 没有，读者会以为有覆盖其实没有。两边都得对上。
func TestCIDocumentsEveryJob(t *testing.T) {
	ci := readWorkflow(t, ciWorkflowPath)
	docRaw, err := os.ReadFile(releaseMatrixPath)
	if err != nil {
		t.Fatalf("读不到平台矩阵文档: %v", err)
	}
	doc := string(docRaw)

	declared := ciJobIDs(t, ci)
	documented := documentedCIJobs(doc)

	for job := range declared {
		if !documented[job] {
			t.Fatalf("ci.yml 有 job %q，但平台矩阵文档 §4 没有登记它", job)
		}
	}
	for job := range documented {
		if !declared[job] {
			t.Fatalf("平台矩阵文档登记了 job %q，但 ci.yml 里不存在", job)
		}
	}
}

// ciJobIDs 抓取 jobs: 段下所有两空格缩进的键。
func ciJobIDs(t *testing.T, body string) map[string]bool {
	t.Helper()
	_, jobsSection, found := strings.Cut(body, "\njobs:\n")
	if !found {
		t.Fatal("ci.yml 没有 jobs: 段")
	}
	ids := map[string]bool{}
	for _, line := range strings.Split(jobsSection, "\n") {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "   ") {
			continue
		}
		name, isKey := strings.CutSuffix(strings.TrimPrefix(line, "  "), ":")
		if !isKey || name == "" {
			continue
		}
		ids[name] = true
	}
	if len(ids) == 0 {
		t.Fatal("没从 ci.yml 解析出任何 job id，解析规则需要跟着文件结构一起改")
	}
	return ids
}

// documentedCIJobs 抓取文档 §4 里形如 `| \`go-core\` | …` 的任务行。
//
// 只看 §4「CI 覆盖」这一节：§3 的边界表也用反引号写命令，混进来会把
// 命令名当成 job id 去比对，报出「文档有 ci.yml 没有的 job」这种假失败。
func documentedCIJobs(doc string) map[string]bool {
	ids := map[string]bool{}
	for _, line := range strings.Split(ciSection(doc), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		rest := strings.TrimPrefix(line, "| `")
		name, _, ok := strings.Cut(rest, "`")
		if !ok || name == "" {
			continue
		}
		ids[name] = true
	}
	return ids
}

// ciSection 截出文档里标题以「## 4.」开头的那一节。
func ciSection(doc string) string {
	_, rest, found := strings.Cut(doc, "\n## 4.")
	if !found {
		return ""
	}
	if idx := strings.Index(rest, "\n## "); idx >= 0 {
		rest = rest[:idx]
	}
	return rest
}

// TestReleaseWorkflowCoversAllTargets 验证发布脚本编得出矩阵里的每个组合。
//
// 发布脚本与 tests/platform 的 releaseTargets 必须同源：文档说支持发布但脚本漏编，
// 用户下到的就是半个产品。
func TestReleaseWorkflowCoversAllTargets(t *testing.T) {
	body := readWorkflow(t, releaseWorkflowPath)
	for _, target := range releaseTargets {
		quoted := `"` + target.os + " " + target.arch + `"`
		if !strings.Contains(body, quoted) {
			t.Fatalf("release.yml 缺少发布目标 %s/%s（期望 %s）", target.os, target.arch, quoted)
		}
	}
}

// TestReleaseWorkflowGuardsMatrix 验证发布前会跑平台矩阵用例。
//
// 发布是「跑一次就不会回头」的动作，支持范围必须先钉死再打包。
func TestReleaseWorkflowGuardsMatrix(t *testing.T) {
	body := readWorkflow(t, releaseWorkflowPath)
	if !strings.Contains(body, "go test ./tests/platform") {
		t.Fatal("release.yml 没有在打包前跑 tests/platform，支持范围没被钉住")
	}
	if strings.Contains(body, "continue-on-error: true") {
		t.Fatal("release.yml 出现 continue-on-error: true，发布前检查会被吞掉")
	}
}

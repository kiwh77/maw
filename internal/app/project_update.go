package app

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/home"
)

// ProjectUpdateInput 是工程属性与策略的增量更新。
type ProjectUpdateInput struct {
	Ref          string
	Name         *string
	Path         *string
	MainBranch   *string
	MaxParallel  *int
	AutoEnabled  *bool
	Limit        *int
	AllowedNodes *[]string
	WorkerNode   *string
	LLMNode      *string
	DryRun       bool
}

// ProjectUpdate 更新中枢工程行，并可选写回工程目录的 config.toml。
func ProjectUpdate(cwd string, in ProjectUpdateInput) (Result, error) {
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(in.Ref)
	if ref == "" {
		return nil, errx.New("需要指定工程（id 或名称）", "maw project list")
	}
	if err := ensureWritableDB(gdb); err != nil {
		return nil, err
	}

	var project *db.Project
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		var e error
		project, e = resolveProjectRef(tx, ref)
		if e != nil {
			return e
		}
		if in.Name != nil {
			project.Name = strings.TrimSpace(*in.Name)
		}
		if in.Path != nil {
			p := strings.TrimSpace(*in.Path)
			if p != "" {
				if abs, err := filepath.Abs(p); err == nil {
					p = abs
				}
				project.Path = p
			}
		}
		if in.MainBranch != nil {
			project.MainBranch = strings.TrimSpace(*in.MainBranch)
		}
		if in.MaxParallel != nil {
			project.MaxParallel = *in.MaxParallel
		}
		if in.DryRun {
			return nil
		}
		return db.UpsertProject(tx, project)
	})
	if err != nil {
		return nil, err
	}

	cfgPath := filepath.Join(project.Path, ".multiagent", "config.toml")
	wroteConfig := false
	if !in.DryRun && projectConfigNeedsWrite(in) {
		if err := patchProjectConfig(cfgPath, in); err != nil {
			return nil, err
		}
		wroteConfig = true
	}

	return Result{
		"ok": true, "dry_run": in.DryRun,
		"project_id": project.ID, "project_name": project.Name,
		"project_path": project.Path, "main_branch": project.MainBranch,
		"max_parallel": project.MaxParallel, "config_written": wroteConfig,
		"config_path": cfgPath,
	}, nil
}

func projectConfigNeedsWrite(in ProjectUpdateInput) bool {
	return in.AutoEnabled != nil || in.Limit != nil || in.AllowedNodes != nil ||
		in.WorkerNode != nil || in.LLMNode != nil || in.MainBranch != nil || in.MaxParallel != nil
}

func patchProjectConfig(path string, in ProjectUpdateInput) error {
	if _, err := os.Stat(path); err != nil {
		return errx.New("找不到工程配置: "+path, "确认工程 path 正确且已 maw init")
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if in.MainBranch != nil && strings.TrimSpace(*in.MainBranch) != "" {
		cfg.Project.MainBranch = strings.TrimSpace(*in.MainBranch)
	}
	if in.MaxParallel != nil {
		cfg.Project.MaxParallel = *in.MaxParallel
	}
	if in.AutoEnabled != nil {
		cfg.Managed.AutoEnabled = *in.AutoEnabled
	}
	if in.Limit != nil {
		cfg.Managed.Limit = *in.Limit
	}
	if in.AllowedNodes != nil {
		cfg.Managed.AllowedNodes = append([]string{}, (*in.AllowedNodes)...)
	}
	if in.WorkerNode != nil {
		cfg.SystemAgent.WorkerNode = strings.TrimSpace(*in.WorkerNode)
	}
	if in.LLMNode != nil {
		cfg.SystemAgent.LLMNode = strings.TrimSpace(*in.LLMNode)
	}
	return writeProjectConfig(path, cfg)
}

// writeProjectConfig 用行级替换写回常用字段，避免全量重排整个 toml。
func writeProjectConfig(path string, cfg *config.Config) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(raw)
	text = setTOMLString(text, "project", "main_branch", cfg.Project.MainBranch)
	text = setTOMLInt(text, "project", "max_parallel", cfg.Project.MaxParallel)
	text = setTOMLBool(text, "managed", "auto_enabled", cfg.Managed.AutoEnabled)
	text = setTOMLInt(text, "managed", "limit", cfg.Managed.Limit)
	text = setTOMLStringArray(text, "managed", "allowed_nodes", cfg.Managed.AllowedNodes)
	text = setTOMLString(text, "system_agent", "worker_node", cfg.SystemAgent.WorkerNode)
	text = setTOMLString(text, "system_agent", "llm_node", cfg.SystemAgent.LLMNode)
	return os.WriteFile(path, []byte(text), 0o644)
}

func setTOMLString(text, section, key, value string) string {
	return replaceTOMLValue(text, section, key, strconv.Quote(value))
}

func setTOMLInt(text, section, key string, value int) string {
	return replaceTOMLValue(text, section, key, strconv.Itoa(value))
}

func setTOMLBool(text, section, key string, value bool) string {
	return replaceTOMLValue(text, section, key, strconv.FormatBool(value))
}

func setTOMLStringArray(text, section, key string, values []string) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, strconv.Quote(v))
	}
	return replaceTOMLValue(text, section, key, "["+strings.Join(parts, ", ")+"]")
}

func replaceTOMLValue(text, section, key, rendered string) string {
	secRe := regexp.MustCompile(`(?m)^\s*\[` + regexp.QuoteMeta(section) + `\]\s*$`)
	loc := secRe.FindStringIndex(text)
	if loc == nil {
		return strings.TrimRight(text, "\n") + fmt.Sprintf("\n[%s]\n%s = %s\n", section, key, rendered)
	}
	rest := text[loc[1]:]
	next := regexp.MustCompile(`(?m)^\s*\[[^\]]+\]\s*$`).FindStringIndex(rest)
	end := len(text)
	if next != nil {
		end = loc[1] + next[0]
	}
	block := text[loc[0]:end]
	keyRe := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*.*$`)
	if keyRe.MatchString(block) {
		block = keyRe.ReplaceAllString(block, key+" = "+rendered)
	} else {
		block = strings.TrimRight(block, "\n") + "\n" + key + " = " + rendered + "\n"
	}
	return text[:loc[0]] + block + text[end:]
}

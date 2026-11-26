package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/scope"
)

// Candidate 是一次运行报上来的候选结果。
//
// 它是「输入」，不是结论：产物要逐条核实，提交链要逐跳验证，
// 完成判定由本包按契约推导。verifications／review／merge 是已经发生的事实，
// 本包只按契约检查它们能不能支撑 done（不执行命令、不做评审、不合入）。
type Candidate struct {
	// 三项绑定（E310–E312）：缺任何一项都不构成可判定交付。
	TaskInstance model.InstanceID `json:"task_instance"`
	TaskVersion  model.VersionNo  `json:"task_version"`
	RunID        model.RunID      `json:"run_id"`

	Mode           model.DeliveryMode `json:"mode"`
	BaselineCommit model.Commit       `json:"baseline_commit"`
	// CandidateCommit 是代码任务的候选提交；非代码任务为空。
	CandidateCommit *model.Commit `json:"candidate_commit,omitempty"`
	// Worktree 是收取时的目录。它只是「此刻还能读到文件」的线索，
	// 不是产物引用——目录随后会被复用。
	Worktree string `json:"worktree,omitempty"`

	// RunOutcome 是 VN-304 的判定。非 completed 的运行不得产出交付通过。
	RunOutcome model.Outcome `json:"run_outcome,omitempty"`

	// Artifacts 是 CLI 声明的候选产物；declared_by 必须是 cli。
	Artifacts []model.ArtifactCandidate `json:"artifacts,omitempty"`
	// Verifications／Review／Merge 是已发生的事实，原样交给契约校验。
	Verifications []model.Verification `json:"verification,omitempty"`
	Review        model.Review         `json:"review"`
	Merge         model.Merge          `json:"merge"`
	// Supersedes 指向被本次交付取代的旧交付。
	Supersedes *model.DeliveryID `json:"supersedes,omitempty"`
}

// validate 检查候选本身的绑定与形态；不碰仓库。
func (c Candidate) validate() Violations {
	var out Violations
	if !c.TaskInstance.Valid() {
		out = append(out, model.Violation(model.CodeDeliveryTaskMissing, "candidate.task_instance", "必须绑定任务实例"))
	}
	if !c.TaskVersion.Valid() {
		out = append(out, model.Violation(model.CodeDeliveryVersionMissing, "candidate.task_version", "必须绑定任务定义版本"))
	}
	if !c.RunID.Valid() {
		out = append(out, model.Violation(model.CodeDeliveryRunMissing, "candidate.run_id", "必须绑定产生它的运行"))
	}
	if !c.Mode.Valid() {
		out = append(out, model.Violationf(model.CodeDeliveryFieldInvalid, "candidate.mode", "只能是 code 或 non_code，收到 %q", c.Mode))
	}
	if !c.BaselineCommit.Valid() {
		out = append(out, model.Violation(model.CodeCommitRefInvalid, "candidate.baseline_commit", "必须记录基线提交"))
	}
	if c.Mode == model.DeliveryCode {
		if c.CandidateCommit == nil || !c.CandidateCommit.Valid() {
			out = append(out, model.Violation(model.CodeCommitRefInvalid, "candidate.candidate_commit", "代码交付必须记录候选提交"))
		}
	}
	for i, a := range c.Artifacts {
		if a.DeclaredBy != model.DeclaredByCLI {
			out = append(out, model.Violationf(model.CodeDeliveryFieldInvalid,
				fmt.Sprintf("candidate.artifacts[%d].declared_by", i),
				"候选产物只能由 cli 声明，收到 %q：人工确认是评审阶段的事", a.DeclaredBy))
		}
	}
	if c.Supersedes != nil && *c.Supersedes == "" {
		out = append(out, model.Violation(model.CodeSupersedeInvalid, "candidate.supersedes", "取代目标不得为空"))
	}
	return out
}

// Collector 收取并冻结交付。
type Collector struct {
	projectID model.ProjectID
	repo      string
	scope     []string
	g         git
	store     Store
	clock     func() time.Time
}

// Option 是构造选项。
type Option func(*Collector)

// WithClock 替换时间源（测试固定 delivered_at）。
func WithClock(now func() time.Time) Option {
	return func(c *Collector) {
		if now != nil {
			c.clock = now
		}
	}
}

// WithGit 替换 git 查询实现（测试注入假实现时用；正常路径走真实仓库）。
func WithGit(g git) Option {
	return func(c *Collector) {
		if g != nil {
			c.g = g
		}
	}
}

// New 创建收取器。
//
// writeScope 为空直接拒绝：没有写盘范围就无法判越界，而越界判断是这张卡的核心。
func New(projectID model.ProjectID, repo string, writeScope []string, store Store, opts ...Option) (*Collector, error) {
	if !projectID.Valid() {
		return nil, model.Violation(model.CodeDeliveryFieldInvalid, "project_id", "必须是 16 位小写十六进制")
	}
	if strings.TrimSpace(repo) == "" {
		return nil, model.Violation(model.CodeDeliveryFieldInvalid, "repo", "必须给出仓库路径：提交对象只能从仓库里核实")
	}
	if len(writeScope) == 0 {
		return nil, model.Violation(model.CodeDeliveryFieldInvalid, "write_scope", "必须给出写盘范围，否则无法判产物越界")
	}
	if store == nil {
		store = NewMemoryStore()
	}
	c := &Collector{
		projectID: projectID,
		repo:      repo,
		scope:     append([]string(nil), writeScope...),
		g:         gitCmd{repo: repo},
		store:     store,
		clock:     time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Collect 收取一份候选并冻结成交付（不写入 store）。
//
// 返回的交付始终可用：即使有硬问题，它也是「本次冻结的全部事实 + pending 判定」，
// 错误里列出具体违反，调用方可以据它决定返工还是等人接手。
func (c *Collector) Collect(cand Candidate) (model.Delivery, error) {
	if vs := cand.validate(); len(vs) > 0 {
		return model.Delivery{}, vs
	}

	d := model.Delivery{
		DeliveryID:      c.deliveryID(cand),
		TaskInstance:    cand.TaskInstance,
		TaskVersion:     cand.TaskVersion,
		RunID:           cand.RunID,
		Mode:            cand.Mode,
		BaselineCommit:  cand.BaselineCommit,
		CandidateCommit: cand.CandidateCommit,
		DeliveredAt:     c.clock().UTC(),
		Verifications:   append([]model.Verification(nil), cand.Verifications...),
		Review:          cand.Review,
		Merge:           cand.Merge,
		Supersedes:      cand.Supersedes,
	}

	var problems Violations
	problems = append(problems, c.verifyCommitChain(cand)...)

	artifacts, artProblems := c.verifyArtifacts(cand)
	d.Artifacts = artifacts
	problems = append(problems, artProblems...)

	d.Completion = c.decide(d, cand)
	if vs := c.validate(d); len(vs) > 0 {
		problems = append(problems, vs...)
	}
	if len(problems) == 0 {
		return d, nil
	}
	return d, problems
}

// Freeze 收取并持久化；重复收取同一份候选返回既有快照（created=false）。
func (c *Collector) Freeze(cand Candidate) (model.Delivery, bool, error) {
	id := c.deliveryID(cand)
	if existing, ok, err := c.store.Get(id); err != nil {
		return model.Delivery{}, false, err
	} else if ok {
		// 重复收取：返回已经冻结的那一份，交付时间不刷新——冻结意味着不再变。
		return existing, false, nil
	}

	d, err := c.Collect(cand)
	if d.DeliveryID == "" {
		return model.Delivery{}, false, err
	}

	// 取代链：旧交付不得保留 done（E370），否则同一任务会有两个完成结论。
	if d.Supersedes != nil {
		if prev, ok, gerr := c.store.Get(*d.Supersedes); gerr != nil {
			return model.Delivery{}, false, gerr
		} else if !ok {
			return d, false, Violations{model.Violationf(model.CodeSupersedeInvalid,
				"delivery.supersedes", "指向不存在的交付 %q", *d.Supersedes)}
		} else {
			if prev.Completion.Decision == model.DecisionDone {
				prev.Completion = model.Completion{
					Decision:  model.DecisionPending,
					DecidedAt: d.DeliveredAt,
					Reason:    "已被交付 " + string(d.DeliveryID) + " 取代",
				}
				prev.SupersededBy = &d.DeliveryID
				if serr := c.store.Save(prev); serr != nil {
					return model.Delivery{}, false, serr
				}
			}
		}
	}

	if err := c.store.Save(d); err != nil {
		return model.Delivery{}, false, err
	}
	return d, true, err
}

// verifyCommitChain 核实基线／候选提交的真实性与祖先关系，并检查改动是否越界。
//
// 只用 git 对象判断，不看目录：目录会被复用，对象不会。
func (c *Collector) verifyCommitChain(cand Candidate) Violations {
	var out Violations
	if !c.g.hasCommit(string(cand.BaselineCommit)) {
		out = append(out, model.Violationf(model.CodeCommitRefInvalid, "delivery.baseline_commit",
			"基线提交 %q 在仓库里不存在：没有基线就没有可判定的改动范围", cand.BaselineCommit))
		return out
	}
	if cand.Mode != model.DeliveryCode || cand.CandidateCommit == nil {
		return out
	}
	candidate := string(*cand.CandidateCommit)
	if !c.g.hasCommit(candidate) {
		out = append(out, model.Violationf(model.CodeCommitRefInvalid, "delivery.candidate_commit",
			"候选提交 %q 在仓库里不存在：不能凭 CLI 声明冻结交付", candidate))
		return out
	}
	if !c.g.isAncestor(string(cand.BaselineCommit), candidate) {
		out = append(out, model.Violationf(model.CodeCommitRefInvalid, "delivery.baseline_commit",
			"基线 %s 不是候选 %s 的祖先：改动集无法界定（可能被 rebase 或换了分支）",
			cand.BaselineCommit, candidate))
		return out
	}
	files, err := c.g.changedFiles(string(cand.BaselineCommit), candidate)
	if err != nil {
		out = append(out, model.Violation(model.CodeCommitRefInvalid, "delivery.candidate_commit",
			"无法列出改动文件："+err.Error()))
		return out
	}
	if bad := scope.OutOfScopeFiles(files, c.scope); len(bad) > 0 {
		out = append(out, model.Violationf(model.CodeArtifactOutOfScope, "delivery.candidate_commit",
			"候选提交改动了 %d 个越界文件：%s", len(bad), strings.Join(clipList(bad, 5), ", ")))
	}
	return out
}

// verifyArtifacts 逐条核实 CLI 声明的产物。
//
// 核实出的事实写进 model.Artifact 的三个字段（exists／in_write_scope／sha256），
// CLI 自己报的值一个都不采信。
func (c *Collector) verifyArtifacts(cand Candidate) ([]model.Artifact, Violations) {
	var out Violations
	artifacts := make([]model.Artifact, 0, len(cand.Artifacts))
	for i, declared := range cand.Artifacts {
		p := fmt.Sprintf("delivery.artifacts[%d]", i)
		a := model.Artifact{
			ArtifactID: artifactID(declared.Kind, declared.Ref),
			Kind:       declared.Kind,
			Ref:        strings.TrimSpace(declared.Ref),
			DeclaredBy: model.DeclaredByCLI,
		}
		switch declared.Kind {
		case model.ArtifactCommit:
			a.Exists = c.g.hasCommit(a.Ref)
			a.InWriteScope = a.Exists && c.commitInScope(cand, a.Ref)
		case model.ArtifactFile:
			content, from := c.fileContent(cand, a.Ref)
			a.Exists = content != nil
			a.InWriteScope = scope.PathInScope(a.Ref, c.scope)
			if a.Exists {
				sum := sha256.Sum256(content)
				h := model.SHA256(hex.EncodeToString(sum[:]))
				a.SHA256 = &h
			}
			if a.Exists && !from {
				// 只在工作目录里存在：目录一复用就没了，不能当作可长期引用的产物。
				out = append(out, model.Violationf(model.CodeArtifactMissing, p+".ref",
					"文件 %q 不在候选提交里，只在工作目录里：目录复用后无法恢复，不能支撑交付通过", a.Ref))
			}
		case model.ArtifactContentHash:
			// 内容哈希没有可核实的实体锚点：无法独立证明它对应哪个文件。
			a.Exists = false
			a.InWriteScope = false
			out = append(out, model.Violationf(model.CodeArtifactMissing, p,
				"内容哈希产物 %q 无法独立核实：必须绑定到 file 或 commit 产物", clip(a.Ref)))
		default:
			a.Exists = false
			a.InWriteScope = false
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, out
}

// commitInScope 判定某个提交能不能算「本卡范围内的产物」。
//
// 只有候选提交本身、以及它的祖先（改动是本候选改动的子集）可以；
// 无关提交不在候选链上，无法用本卡范围界定，判为越界由调用方处理。
func (c *Collector) commitInScope(cand Candidate, ref string) bool {
	if cand.CandidateCommit != nil && ref == string(*cand.CandidateCommit) {
		return true
	}
	if cand.CandidateCommit == nil {
		return false
	}
	return c.g.isAncestor(ref, string(*cand.CandidateCommit))
}

// fileContent 取文件内容：优先候选提交（跨目录稳定），其次工作目录。
func (c *Collector) fileContent(cand Candidate, rel string) ([]byte, bool) {
	if strings.TrimSpace(rel) == "" {
		return nil, false
	}
	if cand.CandidateCommit != nil && c.g.hasCommit(string(*cand.CandidateCommit)) {
		if raw, err := c.g.fileAt(string(*cand.CandidateCommit), rel); err == nil {
			return raw, true
		}
	}
	if raw, ok := readWorktreeFile(cand.Worktree, rel); ok {
		// 读到了，但不是从提交里读到的：目录复用后就没了。
		return raw, false
	}
	return nil, false
}

// decide 推导完成判定。
//
// 推导方式是「先按 done 组装，再让契约校验器说话」：
// 校验器（model.Manifest.Validate）是完成条件的唯一权威，本包不另抄一套规则。
// 只要 done 有一处不成立，就降级为 pending，并把阻碍项写进 reason——
// 于是「缺失结果」永远不可能被标成通过。
func (c *Collector) decide(d model.Delivery, cand Candidate) model.Completion {
	now := d.DeliveredAt

	// 运行没完成（或结果未知）时，交付不可能是通过：先挡住这一层。
	if cand.RunOutcome != "" && cand.RunOutcome != model.OutcomeCompleted {
		return model.Completion{
			Decision:  model.DecisionPending,
			DecidedAt: now,
			Reason:    "运行结果不是 completed（" + string(cand.RunOutcome) + "）：没有可判定的交付结果",
		}
	}

	attempt := d
	attempt.Completion = model.Completion{Decision: model.DecisionDone, DecidedAt: now}
	if vs := c.validate(attempt); len(vs) == 0 {
		return attempt.Completion
	} else {
		return model.Completion{
			Decision:  model.DecisionPending,
			DecidedAt: now,
			Reason:    "交付条件不足：" + summarize(vs),
		}
	}
}

// validate 用契约校验器检查单条交付（它只在 Manifest 上暴露）。
func (c *Collector) validate(d model.Delivery) Violations {
	m := model.Manifest{
		Contract:   model.DeliveryContract,
		ProjectID:  c.projectID,
		Deliveries: []model.Delivery{d},
	}
	vs := Violations(m.Validate())
	if d.Supersedes == nil || *d.Supersedes == d.DeliveryID {
		return vs
	}
	// E371 在这里会是误报：取代目标很可能存在于 store 而不在这份单条快照里。
	// 只在该目标确实存在时压掉这条，自指与真缺失仍然报。
	if _, ok, err := c.store.Get(*d.Supersedes); err == nil && ok {
		return dropCode(vs, model.CodeSupersedeInvalid)
	}
	return vs
}

// dropCode 去掉指定码的违反，保留其余。
func dropCode(vs Violations, code model.Code) Violations {
	out := make(Violations, 0, len(vs))
	for _, v := range vs {
		if v.Code == code {
			continue
		}
		out = append(out, v)
	}
	return out
}

// deliveryID 由候选内容确定性地推导交付身份。
//
// 只取候选里「事实性」的字段，不含时间与核实结论：同一份候选重复收取必须同 ID，
// 这样重复收取才是幂等而不是新增一次交付。
func (c *Collector) deliveryID(cand Candidate) model.DeliveryID {
	parts := []string{
		string(cand.TaskInstance),
		fmt.Sprintf("%d", cand.TaskVersion),
		string(cand.RunID),
		string(cand.Mode),
		string(cand.BaselineCommit),
	}
	if cand.CandidateCommit != nil {
		parts = append(parts, string(*cand.CandidateCommit))
	}
	refs := make([]string, 0, len(cand.Artifacts))
	for _, a := range cand.Artifacts {
		refs = append(refs, string(a.Kind)+":"+strings.TrimSpace(a.Ref))
	}
	sort.Strings(refs)
	parts = append(parts, refs...)
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return model.DeliveryID("dlv_" + hex.EncodeToString(sum[:])[:16])
}

// artifactID 由类型与引用确定性地推导产物身份。
func artifactID(kind model.ArtifactKind, ref string) model.ArtifactID {
	sum := sha256.Sum256([]byte(string(kind) + "|" + strings.TrimSpace(ref)))
	return model.ArtifactID("art_" + hex.EncodeToString(sum[:])[:16])
}

// summarize 把违反压成一句可读原因（reason 是给人看的返工依据）。
func summarize(vs []*model.Error) string {
	parts := make([]string, 0, len(vs))
	for i, v := range vs {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("等 %d 项", len(vs)))
			break
		}
		parts = append(parts, v.Error())
	}
	return strings.Join(parts, "；")
}

func clipList(items []string, max int) []string {
	if len(items) <= max {
		return items
	}
	return append(items[:max], "…")
}

func clip(s string) string {
	const max = 60
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

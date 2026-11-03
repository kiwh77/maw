package systemagent

import (
	_ "embed"
	"strings"
	"text/template"
)

//go:embed prompts/diagnose.txt
var diagnosePromptTpl string

var diagnoseT = template.Must(template.New("diagnose").Parse(diagnosePromptTpl))

// BuildDiagnosePrompt 渲染诊断提示词。
func BuildDiagnosePrompt(req DiagnoseRequest) string {
	var b strings.Builder
	_ = diagnoseT.Execute(&b, req)
	return b.String()
}

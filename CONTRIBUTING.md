# Contributing

感谢贡献 maw。

## 开发

```bash
git clone https://github.com/kiwh77/maw.git
cd maw
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build -o maw ./cmd/maw
```

- 需要 Go 版本与根目录 `go.mod` 一致
- 只改与问题相关的代码；大范围重构请先开 Issue 讨论
- 公开文档放在 `docs/`；不要提交本机诊断笔记、`.workbuddy/`、`output/`

## Pull Request

1. Fork 并基于 `main` 开分支
2. 保证 `go test ./...` 通过
3. PR 说明动机与验证方式（可用仓库 PR 模板）

## 行为准则

参与本项目即表示同意遵守 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。

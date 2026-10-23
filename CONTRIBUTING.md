# Contributing

> Languages: **English** | [简体中文](CONTRIBUTING.zh-CN.md)

Thanks for contributing to maw.

## Development

```bash
git clone https://github.com/kiwh77/maw.git
cd maw
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build -o maw ./cmd/maw
```

- Use the Go version from root `go.mod`
- Keep changes scoped to the problem; open an Issue before large refactors
- Public docs live under `docs/`; do not commit local diagnosis notes, `.workbuddy/`, or `output/`

## Pull requests

1. Fork and branch from `main`
2. Ensure `go test ./...` passes
3. Describe motivation and how you verified (PR template welcome)

## Code of conduct

By participating you agree to follow [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

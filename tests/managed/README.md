# tests/managed — first CodeBuddy managed loop (VN-982)

## Default CI

```sh
CGO_ENABLED=0 go test ./tests/managed -count=1
```

Uses:

- Temporary `MAW_GLOBAL` + isolated git repo
- Controlled CLI: [`testdata/fake-codebuddy.sh`](testdata/fake-codebuddy.sh) (**synthetic fixture**, not a vendor field measurement)
- Real maw application APIs: Probe → explicit Claim → ManagedStart / Runner → Manifest negative gate → recovery

## Opt-in real CLI smoke (not default CI)

```sh
MAW_MANAGED_SMOKE=1 CGO_ENABLED=0 go test ./tests/managed -count=1 -run Smoke
```

Requires a real CodeBuddy CLI on `PATH` that the operator has already verified locally. Evidence must be filed under `docs/evidence/codebuddy/` with an explicit source label; do not treat the fake script as field proof.

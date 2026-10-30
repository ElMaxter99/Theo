# Theo web interface

Source of the interface served by `theo ui`. It builds into `internal/ui/dist/`, which is committed so that `go build` needs no Node.

```
npm ci && npm run build      # rebuild internal/ui/dist (commit it)
npm run check && npm test    # typecheck and unit tests
npm run mock                 # mock API server to develop without Docker
npm run test:e2e             # Playwright against the mock
npm run test:e2e:real        # Playwright against the real `theo serve` (needs Docker)
```

Structure, design decisions, tests and what is not verified: `docs/cli/ui-web.md`. Dependencies and licences: `DEPENDENCIES.md`.
The API it talks to: `docs/api.md`. The artwork in `public/brand/` is provisional and not MIT-licensed (`public/brand/NOTICE.md`).

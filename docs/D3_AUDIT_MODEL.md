# D3 Audit Model Extension

D3 extends the audit storage model with three new run-scoped tables:

- `steps`
- `decisions`
- `approvals`

Migration file:

- `toolhub/internal/db/migrations/003_steps_decisions_approvals.sql`

DB layer support added in `toolhub/internal/db/db.go` with typed structures and basic read/write methods for all three tables.

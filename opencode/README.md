# plansync opencode plugin

Exposes the plansync CLI as custom tools so agents can audit plans without
shell access to the binary path:

- `plansync_audit` — report plan claims that have gone stale
- `plansync_brief` — session-start digest (findings + remaining work + open
  checkboxes), capped at N lines
- `plansync_sync` — refresh tool-owned plan regions
- `plansync_log` — append a dated doc-log entry from session facts + git state
- `plansync_init` — scaffold a continuation plan with live git state

The binary is located via `PLANSYNC_BIN`, then the repo's `bin/plansync`,
and is built from source if missing.

## Register

```json
{ "plugin": ["file:///home/burningserenity/Documents/ByteWheel/plansync/opencode/plansync.ts"] }
```

## Type-check

```sh
cd opencode
npm install
npx tsc --noEmit plansync.ts
```

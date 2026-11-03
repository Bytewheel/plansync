// plansync plugin for opencode.
//
// Exposes the plansync CLI (plan documentation audit/sync against repository
// state) as custom tools:
//
//   - plansync_audit — report plan claims that have gone stale
//   - plansync_brief — session-start digest: findings + remaining work +
//                      open checkboxes, capped at N lines
//   - plansync_sync  — refresh tool-owned plan regions (preview by default)
//   - plansync_log   — append a dated doc-log entry built from session facts
//   - plansync_init  — scaffold a continuation plan with live git state
//
// The plugin locates the plansync binary via PLANSYNC_BIN, then the repo's
// bin/plansync, and finally builds it from source if missing.
//
// Register this plugin in opencode config:
//
//	{ "plugin": ["file:///path/to/this/repo/opencode/plansync.ts"] }
//
// Type-check with:  cd opencode && npx tsc --noEmit plansync.ts
// (requires:  npm install)
import { type Plugin, tool } from "@opencode-ai/plugin"
import fs from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

const PLUGIN_DIR = path.dirname(fileURLToPath(import.meta.url))
const REPO_DIR = path.resolve(PLUGIN_DIR, "..")
const BIN = path.join(REPO_DIR, "bin", "plansync")

// Build commands tried in order when bin/plansync is missing. The `go` on
// PATH can inherit a broken GOROOT on this machine, so the absolute
// toolchain is tried first, then PATH go with GOROOT unset.
const BUILD_COMMANDS = [
	"GOROOT=/programs/dev-lang/go/current/lib/go /programs/dev-lang/go/current/lib/go/bin/go build -o bin/plansync ./cmd/plansync",
	"env -u GOROOT go build -o bin/plansync ./cmd/plansync",
	"env -u GOROOT /usr/lib/go/bin/go build -o bin/plansync ./cmd/plansync",
]

// ensureBinary returns a working path to the plansync binary, building it
// from source in the repo if it does not exist yet.
async function ensureBinary($: any): Promise<string> {
	const envBin = process.env.PLANSYNC_BIN
	if (envBin && fs.existsSync(envBin)) return envBin
	if (fs.existsSync(BIN)) return BIN

	const shell = $.cwd(REPO_DIR).nothrow().quiet()
	for (const cmd of BUILD_COMMANDS) {
		const out = await shell`${{ raw: cmd }}`
		if (out.exitCode === 0 && fs.existsSync(BIN)) return BIN
	}
	throw new Error(
		`plansync binary missing at ${BIN} and auto-build failed. ` +
			`Build it manually (cd ${REPO_DIR} && env -u GOROOT go build -o bin/plansync ./cmd/plansync) ` +
			`or set PLANSYNC_BIN to an existing binary.`,
	)
}

interface RunResult {
	ok: boolean
	text: string
}

// runPlansync executes the CLI with the given arguments in the project
// directory (so relative plan paths resolve against the user's project) and
// captures its output.
async function runPlansync($: any, cwd: string, args: string[]): Promise<RunResult> {
	const bin = await ensureBinary($)
	const cmd = [bin, ...args].map((a) => $.escape(a)).join(" ")
	const out = await $.cwd(cwd).nothrow().quiet()`${{ raw: cmd }}`
	const stdout = (out.stdout?.toString() ?? "").trim()
	const stderr = (out.stderr?.toString() ?? "").trim()
	if (out.exitCode !== 0) {
		const parts = [`plansync exited with code ${out.exitCode}`]
		if (stdout) parts.push(stdout)
		if (stderr) parts.push(stderr)
		return { ok: false, text: parts.join("\n") }
	}
	return { ok: true, text: stdout || stderr }
}

const planArg = tool.schema
	.string()
	.describe(
		"Plan file or directory. Directories are walked for markdown files whose names look like plans (plan, roadmap, todo, continuation, next_agent).",
	)

const reposArg = tool.schema
	.array(tool.schema.string())
	.optional()
	.describe("Candidate repository paths (repeatable). Discovered automatically from the plan's **Repo:** headers when omitted; pass explicitly for plans that live outside their repos (e.g. omnical plans need ~/router-dav/rustical and ~/router-dav).")

function repoFlags(repos: string[] | undefined): string[] {
	const a: string[] = []
	for (const r of repos ?? []) a.push("--repo", r)
	return a
}

export const PlansyncPlugin: Plugin = async ({ $, directory }) => {
	const cwd = directory ?? process.cwd()
	return {
		tool: {
			plansync_audit: tool({
				description:
					"Audit plan documentation against the actual state of the git repositories it describes: stale git-tree blocks (with the commit that landed each file), moved file:line anchors (with corrected lines), missing files, moved heads/branches, open checkboxes. Read the report instead of the whole plan; stale/moved/missing lines are established fact. Use at session start before reading any PLAN*.md / CONTINUATION_PLAN.md.",
				args: {
					plan: planArg,
					repos: reposArg,
					verbose: tool.schema
						.boolean()
						.optional()
						.default(false)
						.describe("Also print ok claims individually"),
					fail: tool.schema
						.boolean()
						.optional()
						.default(false)
						.describe("Exit non-zero on stale/moved/missing findings (CI-able)"),
					gates: tool.schema
						.boolean()
						.optional()
						.default(false)
						.describe("Run .plansync.toml gate commands and verify the plan's numeric claims against their output"),
					gateConfig: tool.schema
						.string()
						.optional()
						.describe("Explicit gate config path (default: .plansync.toml from a candidate repo or the plan dir)"),
					json: tool.schema.boolean().optional().default(false).describe("Machine-readable output"),
				},
				async execute(args) {
					const a = ["audit", args.plan, ...repoFlags(args.repos)]
					if (args.verbose) a.push("--verbose")
					if (args.fail) a.push("--fail")
					if (args.gates) a.push("--gates")
					if (args.gateConfig) a.push("--gate-config", args.gateConfig)
					if (args.json) a.push("--json")
					const res = await runPlansync($, cwd, a)
					return res.ok ? res.text : `plansync: ${res.text}`
				},
			}),

			plansync_brief: tool({
				description:
					"Session-start digest of a plan, capped at N lines: audit findings (established facts) first, then the plan's remaining-work sections excerpted verbatim, then open checkboxes outside those sections. The token-saving replacement for reading the whole plan. Use at session start; combine with plansync_audit when the full claim-by-claim report is needed.",
				args: {
					plan: planArg,
					repos: reposArg,
					maxLines: tool.schema
						.number()
						.int()
						.positive()
						.optional()
						.default(40)
						.describe("Cap the digest at n lines (total across plans)"),
					json: tool.schema.boolean().optional().default(false).describe("Machine-readable output"),
				},
				async execute(args) {
					const a = ["brief", args.plan, ...repoFlags(args.repos), "--max-lines", String(args.maxLines ?? 40)]
					if (args.json) a.push("--json")
					const res = await runPlansync($, cwd, a)
					return res.ok ? res.text : `plansync: ${res.text}`
				},
			}),

			plansync_sync: tool({
				description:
					"Refresh tool-owned plan regions back to current repository state: plansync marker regions (git-status blocks, heads tables) and symbol-verified re-anchoring of moved file:line refs. Preview diff by default; apply writes with a .bak backup and a post-apply re-audit. Use at session end before handing off.",
				args: {
					plan: planArg,
					repos: reposArg,
					policy: tool.schema
						.enum(["markers", "heuristic", "report"])
						.optional()
						.default("markers")
						.describe("markers: marker regions + re-anchoring only; heuristic: also refresh the most recent unmarked git-status block per repo and stale head-table commits; report: compute but never write"),
					apply: tool.schema
						.boolean()
						.optional()
						.default(false)
						.describe("Write changes (default: preview diff only)"),
					noBackup: tool.schema
						.boolean()
						.optional()
						.default(false)
						.describe("Skip the .bak backup when applying"),
					json: tool.schema.boolean().optional().default(false).describe("Machine-readable output"),
				},
				async execute(args) {
					const a = ["sync", args.plan, ...repoFlags(args.repos), "--policy", args.policy ?? "markers"]
					if (args.apply) a.push("--apply")
					if (args.noBackup) a.push("--no-backup")
					if (args.json) a.push("--json")
					const res = await runPlansync($, cwd, a)
					return res.ok ? res.text : `plansync: ${res.text}`
				},
			}),

			plansync_log: tool({
				description:
					"Append a house-style dated doc-log entry to a plan, assembled from session facts (diagnosis, fixes, test counts, deploy transcript) and live git state of every candidate repo. The entry is written in audit grammar so future audits re-verify it. Preview by default; apply writes with a .bak backup. Use at session end to record what happened.",
				args: {
					plan: tool.schema.string().describe("Plan file to append to (exactly one)"),
					repos: reposArg,
					subject: tool.schema.string().optional().describe("Entry subject for the heading"),
					diagnosis: tool.schema.string().optional().describe("Diagnosis text"),
					fixes: tool.schema.string().optional().describe("Fixes text"),
					tests: tool.schema.string().optional().describe("Test-count text"),
					deploy: tool.schema.string().optional().describe("Deploy transcript text"),
					date: tool.schema.string().optional().describe("Entry date, YYYY-MM-DD (default today)"),
					template: tool.schema.string().optional().describe("Template file path (default: .plansync/log-template.md from a candidate repo or the plan dir)"),
					apply: tool.schema
						.boolean()
						.optional()
						.default(false)
						.describe("Write the entry (default: preview only)"),
					noBackup: tool.schema.boolean().optional().default(false).describe("Skip the .bak backup when applying"),
					json: tool.schema.boolean().optional().default(false).describe("Machine-readable output"),
				},
				async execute(args) {
					const a = ["log", args.plan, ...repoFlags(args.repos)]
					if (args.subject) a.push("--subject", args.subject)
					if (args.diagnosis) a.push("--diagnosis", args.diagnosis)
					if (args.fixes) a.push("--fixes", args.fixes)
					if (args.tests) a.push("--tests", args.tests)
					if (args.deploy) a.push("--deploy", args.deploy)
					if (args.date) a.push("--date", args.date)
					if (args.template) a.push("--template", args.template)
					if (args.apply) a.push("--apply")
					if (args.noBackup) a.push("--no-backup")
					if (args.json) a.push("--json")
					const res = await runPlansync($, cwd, a)
					return res.ok ? res.text : `plansync: ${res.text}`
				},
			}),

			plansync_init: tool({
				description:
					"Scaffold a new continuation plan pre-filled with live git state: **Repo:** headers, git-status + heads marker regions (tool-owned, kept current by plansync_sync), a remaining-steps section, and a gotchas section. A fresh scaffold audits clean and is a sync no-op. Preview by default; apply writes (parent dirs created; existing files never overwritten).",
				args: {
					plan: tool.schema.string().describe("Plan file to create (must not already exist)"),
					repos: reposArg,
					title: tool.schema.string().optional().describe("Plan title (default: derived from the file name)"),
					date: tool.schema.string().optional().describe("Plan date, YYYY-MM-DD (default today)"),
					apply: tool.schema
						.boolean()
						.optional()
						.default(false)
						.describe("Create the file (default: preview only)"),
					json: tool.schema.boolean().optional().default(false).describe("Machine-readable output"),
				},
				async execute(args) {
					const a = ["init", args.plan, ...repoFlags(args.repos)]
					if (args.title) a.push("--title", args.title)
					if (args.date) a.push("--date", args.date)
					if (args.apply) a.push("--apply")
					if (args.json) a.push("--json")
					const res = await runPlansync($, cwd, a)
					return res.ok ? res.text : `plansync: ${res.text}`
				},
			}),
		},
	}
}

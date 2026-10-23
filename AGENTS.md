# Notes for coding agents

Mica is built and maintained by agent sessions. This file is the entry point; the documents it links are binding.

## Before changing code

- Read [docs/LESSONS.md](docs/LESSONS.md): what recent work got wrong and the rule that would have caught it.
- Read [docs/design.md](docs/design.md) for the current UI, data flow, caching, and verification conventions. Update it in the same PR when a contract changes.
- For MyGo work, read [.agents/skills/mygo-maintenance/SKILL.md](.agents/skills/mygo-maintenance/SKILL.md) and verify APIs against the pinned module version, not current upstream documentation.

## Tests and review

- A regression fix comes with a test that fails without it. Break the behavior by hand, watch the target assertion fail, then restore and watch it pass. A compile error or skipped test does not count.
- Run the full suite before merging: `CGO_ENABLED=0 go test ./...`, `go test -race ./...`, `go vet ./...`, and `go tool mygo vet .`.
- Keep generated files and catalogs current with `go tool mygo generate .` and `go tool mygo i18n check .`; finish with `git diff --check`.
- Tests use fake services or the demo mailbox, never destructive operations against a real Gmail account.
- UI changes use `ui.NewTester` and rendered output where possible. Check animation start/middle/end frames and real content pixels when layout bounds are insufficient.

## Commits and merges

- Commit messages and PR text are English and conventional (`feat:`, `fix:`, `chore:`, `docs:`, `ci:`). One PR has one goal; unrelated refactors go separately.
- PR descriptions record the goal, changes, and tests actually run. Squash merges use a hand-written subject and body, never GitHub's default message.
- Large or dependent changes land in order, one PR at a time. Review the complete diff for functionality that belongs to a later PR.
- Right before merging, re-read PR comments and new commits. Merge only the reviewed head with CI green, using `--match-head-commit <reviewed sha>` so a later push stops the merge.
- The agent's standing authorization: routine work verified against these rules may be merged with CI green on the reviewed head without another confirmation. Deliberate product decisions and destructive or outward-facing operations still require explicit approval.

## Releases

- The version lives in `mygo.json`. Features call for a minor bump, fixes a patch bump, and breaking changes a major bump. Keep the release PR limited to the version and `CHANGELOG.md` notes.
- `mygo build` requires a `## <version>` section in `CHANGELOG.md`; prepare it before merging the version change.
- CI builds, signs, and publishes release artifacts. Do not build DMGs locally unless explicitly requested, and never create a tag or GitHub release by hand.
- Use `workflow_dispatch` to release or retry from the latest main. `gh run rerun --failed` uses the original failing commit and can miss fixes merged afterward.
- A release counts only when the workflow is green and `gh release view` shows the expected artifacts. A failed or cancelled run is not a release.

## Public writing

- Docs, lessons, and issue/PR text record mechanisms and verification evidence, not local machine details or personal attribution. Do not include hostnames, absolute paths, internal addresses, or private conversation content.

## Session ritual

- Record a lesson in [docs/LESSONS.md](docs/LESSONS.md) as soon as work teaches one, with the rule and evidence in its own commit. Promote recurring rules into this file.

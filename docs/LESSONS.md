# Development Lessons

## Do not copy external service concepts directly

- An internal virtual label is not necessarily a real label on the server.
- All Mail in Gmail should use a global query without a `labelId`, not the unsupported `ALL` value.
- When changing query mappings, verify request parameters, cache keys, and list filtering together instead of fixing only the visible UI.

## Design optimistic updates together with request ordering

- Applying changes locally first does not allow requests to run concurrently and out of order; mail mutations must synchronize in FIFO order.
- Consecutive actions, automatic read marking, and Undo belong to the same queue so later results cannot overwrite earlier actions.
- Undo should capture the post-action snapshot and then apply the inverse mutation, keeping local state consistent with the server's final state.
- Failure reconciliation must avoid a refresh-mark-read-failure-refresh loop.
- After an account switch, requests and Undo entries from the previous account must not write into the new state.

## Filtering, pagination, and selection are one problem

- Keep the raw list and the filtered visible list separate. Mouse selection, keyboard navigation, deletion, and counts must all use the same mapping.
- When the current conversation is filtered out, the list selection may be cleared without interrupting the open reader.
- Avoid fetching a large first page of complete conversations; use a small first page and load more with `nextPageToken`.
- Deduplicate appended pages and preserve selection. Do not mix the base query snapshot with pagination request parameters.
- When a load-at-end request fails, view rebuilds must not retry it automatically and repeatedly.
- Opening a thread by ID must also synchronize the selected row before detail loading. A regression reproduced `selectedRow == -1` before loading; do not synchronize only in `showThread`.
- Run automatic startup selection once for each account's first non-empty list. The previous behavior left no selection after offline restoration and account switches. New mail inserted above may move only the selected row by ID; it must not reselect or switch the detail.
- When single selection is bound only by row index, archiving the first conversation lets the next one occupy the same row and makes the list keep tracking the old key. Highlight the list with a Selection keyed by thread ID, and use first/last archive comparisons to verify that detail and selection stay aligned.

## Label assignment and label lifecycle are different operations

- Adding or removing a label on a conversation is not the same as creating or deleting the label itself.
- Keep label creation and deletion in the sidebar's management area; the reader menu is only for assigning labels to the current conversation. Creation must remain available when no custom labels exist. Right-clicking a label should expose color editing and deletion, and color updates must use Gmail-supported background/text pairs rather than arbitrary picker values.
- Creating a label must wait for the server's real ID; do not invent temporary IDs.
- Deleting a label removes it from every conversation. Require explicit confirmation and clean up the sidebar, current conversation, caches, and stale Undo entries.
- When the active label is deleted, return to a mailbox view that still exists.

## Respect content type in body layout

- HTML does not always provide its own inset. The default reading inset must be overridable by root email CSS or top-level inline padding; do not add a fixed card or container padding.
- Plain text and HTML fallbacks need the same reading inset. Verify the cascade with computed styles: in this case, bare HTML measured 24px, the wrapper with inline padding measured 0, and root email CSS measured 40px.
- Do not use one container to handle both HTML and plain-text spacing requirements.

## Verify visuals through real rendering

- Do not validate animation only by its parameters. Inspect start, middle, and end frames for jumps, ghosting, and blank screens.
- A layout object's `Bounds` may include padding; X coordinates or outer widths alone cannot establish the content position.
- For visual differences such as padding, compare text origins in rendered images when necessary.
- A localized Select must not display a persisted code directly as its option label. Map state to display text explicitly and inspect the collapsed control's actual rendering for empty, `en`, and `zh-Hans` values.
- A toggle needs a clearly different active color, not only a changed tooltip or filtered result. Verify color transitions at start, middle, and end, and move the pointer away before checking the inactive style because hover can mask the default background.
- Glass materials can be nearly invisible on a light reader while looking strong in dark mode. Check both appearances with real content behind the material, including while content scrolls under it, and tune the tint and shadow so the backdrop cannot reduce control contrast.
- UI capture helpers must not call production credential-loading paths. Build modal state directly, or a screenshot run can block on the real macOS Keychain instead of rendering.
- Call `Tester.Frame()` after changing app state directly; `Advance` only runs frames already scheduled. A Settings capture that omitted the rebuild rendered the previous mailbox instead.
- `Tester.Find` returns logical coordinates, while `Tester.Image` contains physically scaled pixels. Scale crop bounds accordingly and inspect the result; an unscaled 2x crop showed an unrelated conversation row and pane.
- Drive capture timers with a controllable callback and verify the final content. `Tester.Advance` does not run `time.AfterFunc`, so a toast capture still showed the notice in its end frame until the test invoked expiration explicitly.
- Give an unmeasured embedded document the available viewport height, then honor its measured or cached height. A fixed one-line placeholder made HTML mail open collapsed; the rendering regression test checks both the placeholder and the measured state.
- Start embedded-document measurement at DOM readiness rather than full page load when slow subresources would otherwise delay the initial layout.
- A gradient fill does not interpolate like a solid color transition. Keep animated toggles on a solid accent and verify start, middle, and end pixels before replacing them with a gradient.
- Small account photos use a stable first frame rather than animated frame swaps. A multi-frame GIF must keep the same rendered pixels after its frame delays and schedule no animation frames.
- Rounded row selection needs vertical as well as horizontal insets. Measure the first row's rendered distance from the divider; horizontal padding alone lets the selection touch it.

## Match live theme changes in native and web content

- A WebView background that follows the app appearance exposes stale document colors if the loaded page keeps its initial theme. Let email CSS respond to `prefers-color-scheme` instead of binding colors to the page's creation time.
- Verify an already-open HTML message through light-to-dark and dark-to-light transitions. A real-window check opened in Light, switched to Dark, and exposed dark text on the dark background; after the CSS change, the same path kept the text readable.

## Regression tests must prove the guard works

- Confirm that the test fails on the old behavior before implementing the fix; avoid tests that always pass.
- Assertions must target the behavior itself. A build failure does not count as a reproduction.
- When restoring behavior by hand for verification, break and restore one thing at a time so uncommitted fixes are not overwritten.
- Use controllable fake services for queue and timing tests; never run destructive tests against a real Gmail account.
- A toast timer may expire only the toast with the ID it was created for; stale timers must not expire the current toast.

## Check feature boundaries when splitting large changes

- Make a safety commit before splitting so the complete change can be recovered at any stage.
- Zero-context diffs can insert new functions or cases in the wrong place. Use contextual hunks or function-level edits for complex splits.
- Review the complete diff for each group to ensure that no functionality from a later group has leaked in, then run the tests.
- Changes touching many shared files should land in dependency order, one PR at a time, instead of parallel branches that conflict with each other.

## Validate release metadata early

- `mygo build` requires a `## <version>` section for the current version in `CHANGELOG.md`; prepare the release notes before the version PR.
- A successful build and notarization do not prove that the release succeeded. Also inspect the final packaging, artifact upload, and Release creation logs.
- `gh run rerun --failed` uses the original failing commit and misses later fixes. Retry from the latest main with `workflow_dispatch`.
- CI generates all release artifacts. Do not build DMGs locally unless explicitly requested, and never create tags or GitHub releases manually.

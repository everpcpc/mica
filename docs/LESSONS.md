# Development Lessons

## Do not copy external service concepts directly

- An internal virtual label is not necessarily a real label on the server.
- All Mail in Gmail should use a global query without a `labelId`, not the unsupported `ALL` value.
- When changing query mappings, verify request parameters, cache keys, and list filtering together instead of fixing only the visible UI.
- An OAuth exchange does not establish account ownership. Verify the Gmail identity, commit an account token and an active-account marker, and rebuild the client from the formal store so later refreshes cannot write the pending or legacy store. Keep the marker independent of SQLite and token form, retire legacy tokens after a formal restore, and make logout a credential-layer closed state so late refresh and reauthorization writes fail instead of resurrecting the session. Cancel in-flight authentication on logout and service replacement, and bind reopening to the current credential generation so a cancelled operation cannot reopen credentials. Authentication callbacks must clear cancel state only for their own operation ID. Hold the credential lifecycle lock across the entire formal commit, not only the reopen check, and include manual Connect in the same generation as restore and reauthorization. Share the token source between the HTTP client and Pub/Sub so deactivation covers both. Deactivate old sources on replacement and recheck deactivation immediately before saving a refresh, so an old service cannot overwrite newer credentials.
- Check credential commits against startup recovery. Save the target active marker before changing `LastAccount`; remove old-account artifacts before deleting the marker for Demo. A shared legacy token also has an owner: verify its identity during migration and removal rather than deleting it on every login. Token refresh and account verification can take the lifecycle read lock, so keep them outside the commit write lock.

## Enforce privacy at the resource boundary

- A privacy toggle must be enforced by the protocol handler, not only by a visible button. Default-block remote HTTP before any request and prove a zero-request path, while keeping inline data, Content-ID attachments, and account avatars on their separate loaders.
- Per-message permission needs an owner/thread/message key and must not become a global allow. Persist only the explicit global auto-load preference, and keep message permission session-only.
- Protocol-relative URLs are remote content too: normalize them to HTTPS, skip hidden subtrees when offering permission, and wait for preference writes before closing the cache. Otherwise single-message enablement misses valid images, hidden content gets a useless button, and quitting can restore an older preference.
- Tracking-pixel filtering applies to actual `img` elements, not arbitrary containers with small dimensions; otherwise a valid CSS background loses its permission action.

## Treat system handlers and deep links as background workflows

- Do not call MyGo onMain-backed system handlers from a view. A direct default-handler query deadlocked Settings; query and register in the background and render cached state.
- Preserve literal `+` in mailto values with path unescaping, and open links only after account and draft-snapshot restoration so they cannot replace an existing draft. Decode `URL.Path` only once because Go has already decoded it.
- Every authentication or account-transition exit must release pending deep links. A failed restore must clear `authCancel`, and service replacement must clear the old draft-restore pending state; otherwise a link can remain blocked forever. Retry the draft snapshot before opening a pending link so a skipped callback cannot make the link overwrite the older draft.

## Design optimistic updates together with request ordering

- Applying changes locally first does not allow requests to run concurrently and out of order; mail mutations must synchronize in FIFO order.
- Consecutive actions, automatic read marking, and Undo belong to the same queue so later results cannot overwrite earlier actions.
- Undo should capture the post-action snapshot and then apply the inverse mutation, keeping local state consistent with the server's final state.
- Undo entries must preserve the operation kind, not only label deltas. Gmail Trash and Untrash are dedicated endpoints, so a label-only inverse cannot faithfully reverse them.
- Failure reconciliation must avoid a refresh-mark-read-failure-refresh loop.
- After an account switch, requests and Undo entries from the previous account must not write into the new state.
- Account activation needs a handoff, not a state reset. Restore the target first, then wait for the mutation FIFO, cache writes, and notification worker before installing it; a blocked-mutation regression proves that activation cannot overtake unfinished work.
- Draining a queue without closing new work is incomplete. Close composer and mutation entry during account transitions, put label creation in the same FIFO, and keep delayed account or draft callbacks from reopening workers or composers.
- Active and auxiliary accounts need one monotonic generation sequence, with tombstones on removal, rather than independent counters or only the active `serviceVersion`. Capture the owner and generation in every async operation, and reject only that owner's stale results when it is replaced or removed. Empty owner values still mean the active account for legacy single-account snapshots, while selection keys remain raw in single-account views. Mutation jobs also need owner validation and ordered dequeue, not just a global version check.
- Background cache and HTTP validation must not read the account registry through an App method. Capture the atomic client generation or read the cache version under its lock; a concurrent replacement test produced `DATA RACE` before this boundary was fixed.
- Cancellation may discard delayed callbacks that have not started, but running callbacks must stay in the worker's wait set. Two pending callbacks originally kept an account transition alive after cancellation; the fix also covers the opposite case so cache writes cannot outlive account removal.
- Old notification workers must be identified before their errors can stop or reset current state. A late-error regression reproduced cancellation of the replacement worker before the identity check was restored.
- When an action toggles between forward and recovery behavior, update native application-menu labels as well as the toolbar. A shortcut dispatcher alone does not prevent the menu from promising the opposite action.
- Removing a recovered conversation unconditionally can drop it from a view it still matches through another label. Check post-action labels against the current query before changing the list or persisted cache.
- Keep recovery-specific visibility logic out of forward actions. Query-aware recovery must preserve Report spam's existing All Mail removal and selection behavior, with a regression test on that path.

## Filtering, pagination, and selection are one problem

- Keep the raw list and the filtered visible list separate. Mouse selection, keyboard navigation, deletion, and counts must all use the same mapping.
- When the current conversation is filtered out, the list selection may be cleared without interrupting the open reader.
- Avoid fetching a large first page of complete conversations; use a small first page and load more with `nextPageToken`.
- Deduplicate appended pages and preserve selection. Do not mix the base query snapshot with pagination request parameters.
- A k-way merge needs independent cursors and emitted sets, not a concatenated first page. Rebuild the current prefix on initial or history refresh, consume cached remainders before fetching, and never let a page inserted behind the cursor disappear or print twice.
- A merged view is a projection, not a complete account snapshot. Applying a star to two 30-thread accounts truncated their caches to the visible 25; local mutations must update the complete per-owner snapshots, removals need tombstones through later page responses, and persistence must never write the visible prefix back as the full cache.
- Network and cache writes must not reorder optimistic state. Preserve same-generation local modifications when applying pages and skip superseded per-account/query list writes and per-account/thread detail writes; deliberately reversed cache jobs resurrected an archived row, a slow first page erased a local star, and an old unread rollback overwrote a newer successful read before these guards were added. History saves and deletions must join the same detail generation and cache-write mutex rather than bypass them.
- Cache restoration and network completion must apply owner state on the UI thread. A background cache read originally mutated Unified streams directly, and a late page from a removed account restored its row; publish cached values and validate the owner generation before changing snapshots or caches.
- Unified Undo cannot borrow the history-refresh path. Reinsert only summaries that match the current query after the inverse labels, and enqueue inverse mutations in FIFO order instead of starting a network generation that can overtake the local restoration. Undo belongs to its source view: entering or leaving Unified must invalidate it rather than inserting another account's raw ID into an ordinary list.
- A visible cached summary is not permission to mutate it. An account whose client could not be restored originally panicked the mutation worker; reject unavailable owner services before optimistic changes and keep a nil-service guard at the queue boundary.
- A view-mode transition must invalidate the list request and hide the previous projection immediately. When a target refresh waited for the mutation FIFO, retained Unified rows acquired raw IDs and routed B actions to A; keep those rows unavailable until the target list is installed. Drop the previous detail cache too, and do not persist label changes with no local target; an automatic-read failure otherwise wrote an empty target-view snapshot. Restore UNREAD in the original owner/query list and detail caches as well, rather than leaving the failed account with a stale read state.
- When a load-at-end request fails, view rebuilds must not retry it automatically and repeatedly.
- Opening a thread by ID must also synchronize the selected row before detail loading. A regression reproduced `selectedRow == -1` before loading; do not synchronize only in `showThread`.
- Run automatic startup selection once for each account's first non-empty list. The previous behavior left no selection after offline restoration and account switches. New mail inserted above may move only the selected row by ID; it must not reselect or switch the detail.
- When single selection is bound only by row index, archiving the first conversation lets the next one occupy the same row and makes the list keep tracking the old key. Highlight the list with a Selection keyed by thread ID, and use first/last archive comparisons to verify that detail and selection stay aligned.
- A multi-selection is not the reader. Programmatic opens may collapse it, but list input must preserve it; ordinary filters and history should prune only invisible keys without clearing the remaining set. Unified history keeps off-screen composite keys until an explicit removal or account change.
- `Selection.All()` is unordered and retains disappeared keys. Resolve action targets in visible-list order, and represent a batch Undo as one action with ordered items rather than looping over a single-slot Undo.
- Batch Archive must preserve the single action's view membership: only Inbox and All Inboxes remove rows. For non-contiguous removal, choose the next survivor after the reader in the original list, then the previous survivor; reusing the old row index skips a conversation.
- A selectable account group needs distinct IDs for its merged view and each account's Inbox. Activate the owner before ordinary Inbox queries, preserve the child selection through the handoff, and return to the current account's Inbox if its account is removed or activation fails. An early return that only handled the merged view left a removed account's child selected; a failed activation also left the new child selected over the old merged view.
- Clear the previous projection before account activation and carry the requested child through both the transition and reauthentication. Changing only the label leaves merged cursors, Undo, and cache routing active; controlled intermediate-frame tests catch mismatches that a final label assertion misses.
- Superseding child reauthentication must explicitly clear or transfer its pending request. An authentication-ID mismatch can silently discard the login callback, so cleanup cannot live only in the failure handler; cover removal, Settings cancellation, and a replacement connection.
- Account deletion completion must invalidate clients and refresh application navigation regardless of the Settings modal's lifetime. Only Settings widget updates belong behind the modal identity check; otherwise closing the window after deletion leaves a removed account available.
- Mixed-state batches need one target state and must filter out items already in that state before recording Undo. Applying a uniform inverse to originally mixed items can remove a star or read state that the user already had.

## Label assignment and label lifecycle are different operations

- Adding or removing a label on a conversation is not the same as creating or deleting the label itself.
- Keep label creation and deletion in the sidebar's management area; the reader menu is only for assigning labels to the current conversation. Creation must remain available when no custom labels exist. Right-clicking a label should expose color editing and deletion, and color updates must use Gmail-supported background/text pairs rather than arbitrary picker values.
- Creating a label must wait for the server's real ID; do not invent temporary IDs.
- Deleting a label removes it from every conversation in that account. Require explicit confirmation and clean up the sidebar, current conversation, caches, and stale Undo entries. Unified cleanup must filter by owner because two accounts can use the same raw label ID.
- When the active label is deleted, return to a mailbox view that still exists.

## Respect content type in body layout

- HTML does not always provide its own inset. The default reading inset must be overridable by root email CSS or top-level inline padding; do not add a fixed card or container padding.
- Plain text and HTML fallbacks need the same reading inset. Verify the cascade with computed styles: in this case, bare HTML measured 24px, the wrapper with inline padding measured 0, and root email CSS measured 40px.
- Do not use one container to handle both HTML and plain-text spacing requirements.

## Preserve attachment data through asynchronous workflows

- Check attachment bytes at every boundary: file selection or drop, background reads, removal, send and retry, MIME output, and demo retrieval. A visible filename or metadata row does not prove that the content survived.
- Serialize attachment reads per composer and recompute the remaining allowance before each read instead of reserving it from a stale snapshot. If an in-flight read fails against a stale smaller allowance, retry after loaded items are removed; true oversize files still fail. Late results for removed items must not reappear or consume allowance, and read failures must block sending rather than silently dropping the attachment.
- Build message actions from the selected message, not the latest message in the thread. A three-message fixture should prove recipients, quotes, threading headers, and forwarded attachments all come from the first message.
- Inline MIME `body.data` needs no attachment fetcher, but it must be cloned for asynchronous use and excluded from persisted snapshots. Missing bytes must fail explicitly instead of disappearing from a forward.
- Do not fill missing reply headers from the thread's latest message when the caller selected a different message. An explicit resolved-threading marker distinguishes intentional empty headers from unresolved legacy calls.
- ID-less inline attachments need a stable per-message position key; message ID alone would make their loading and error states collide.

## Bind offline caches to their owner and lifetime

- An empty `storeAccount` is not a storage owner. Use the active account's normalized email as the Demo fallback instead of silently skipping cache reads or attachment writes.
- Downloads, forwards, and WebView inline images must share one attachment cache boundary. Carry owner, thread, message, and position keys through the real protocol loader, and bind writes to the captured owner generation and the cache drain. Testing only a standalone loader misses a production WebView binding that omits the store; a stale-owner regression and a blocked write must prove deletion fencing and waiting.
- Bound binary caches by both item size and per-account total size. Evict by the complete primary key, and verify binary bytes after reopening the database, quota eviction, and account cleanup.
- Perform fallback cache scans before publishing a failed network result. Searching every cached body on the UI callback can stall rendering; a deferred-publish test proves where the work runs.
- A message timestamp is not a cache revision. Record local write versions, choose one current summary before filtering, and never union labels from different list and detail snapshots. Read the candidates in one SQL statement so a concurrent write cannot split the read into different cache states.
- Eviction and insertion belong to one immediate SQLite transaction. A failing insert must roll back evicted bytes, while concurrent writers still respect the per-account quota. Accept both BLOB and TEXT JSON snapshots and match legacy account keys case-insensitively.

## Serialize draft saves and preserve identity

- Persist send intent before any network request, and link the Outbox record and draft snapshot in one transaction. A separate snapshot and Outbox entry can otherwise become two independent send paths after a crash.
- Complete an Outbox row and its linked snapshot atomically. A missing row can follow a successful send, a cleanup crash, or manual deletion, so a linked snapshot must never treat it as a first send.
- Scope Sent reconciliation to the exact message's Message-ID and labels, not the containing thread's aggregated labels. An empty result is not proof that a response was never received; require an additional explicit confirmation before another send.
- Editing after a failed attempt needs fresh local and remote identities while preserving the original entry. Send and discard install snapshot fences under the writer's cache mutex, and remote deletion failure must restore the local entry instead of leaving an unusable draft ID.
- Non-idempotent send endpoints must not use the generic transient retry loop. Keep a stable Message-ID and reconcile with Sent before an explicit retry; a timeout or recovered `sending` state is uncertain, not proof that resending is safe.
- Outbox updates and deletes need attempt fencing, not only owner checks. A stale callback can otherwise overwrite or remove a newer attempt after the current operation finishes.
- Sending an existing draft must wait for the current create or update to finish. A generation check can discard a stale UI callback, but it cannot cancel the older server write or prevent a deleted draft from reappearing.
- When a create succeeds with newer unsaved edits, persist the new draft ID immediately so crash recovery cannot create a duplicate.
- Empty-draft checks must include HTML and attachments, not only plain text.
- Disable composer inputs while send, close, or discard is pending. Otherwise users can create edits that the current revision and close path never save.
- Restore snapshots by cached account identity rather than network verification; an offline profile failure must not block local recovery. A mismatched account must never synchronize that snapshot.
- Cached identity permits local recovery and editing, not remote writes. Require a verified matching account for draft saves, deletion, and sends; never fall back to `messages/send` for an existing or recovered draft when the draft manager is unavailable.
- Account verification is not a draft capability check. If verification blocks a close-time save, keep the local snapshot and composer open instead of treating the composer as one without draft support.
- Guard file picking and drops while close or discard is pending. Disabling the visible button alone does not reject dropped files or a file-picker result that arrives after the action starts.

## Verify visuals through real rendering

- Do not validate animation only by its parameters. Inspect start, middle, and end frames for jumps, ghosting, and blank screens.
- A layout object's `Bounds` may include padding; X coordinates or outer widths alone cannot establish the content position.
- For visual differences such as padding, compare text origins in rendered images when necessary.
- A localized Select must not display a persisted code directly as its option label. Map state to display text explicitly and inspect the collapsed control's actual rendering for empty, `en`, and `zh-Hans` values.
- A toggle needs a clearly different active color, not only a changed tooltip or filtered result. Verify color transitions at start, middle, and end, and move the pointer away before checking the inactive style because hover can mask the default background.
- Glass materials can be nearly invisible on a light reader while looking strong in dark mode. Check both appearances with real content behind the material, including while content scrolls under it, and tune the tint and shadow so the backdrop cannot reduce control contrast.
- A transparent WebView backdrop is sampled as black by Liquid Glass and can turn a lightly tinted toolbar gray. Keep the tint strong enough to preserve contrast and compare the same pixel before and after the WebView scrolls underneath.
- UI capture helpers must not call production credential-loading paths. Build modal state directly, or a screenshot run can block on the real macOS Keychain instead of rendering.
- Call `Tester.Frame()` after changing app state directly; `Advance` only runs frames already scheduled. A Settings capture that omitted the rebuild rendered the previous mailbox instead.
- `Tester.Find` returns logical coordinates, while `Tester.Image` contains physically scaled pixels. Scale crop bounds accordingly and inspect the result; an unscaled 2x crop showed an unrelated conversation row and pane.
- Drive capture timers with a controllable callback and verify the final content. `Tester.Advance` does not run `time.AfterFunc`, so a toast capture still showed the notice in its end frame until the test invoked expiration explicitly.
- Give an unmeasured embedded document the available viewport height, then honor its measured or cached height. A fixed one-line placeholder made HTML mail open collapsed; the rendering regression test checks both the placeholder and the measured state.
- Start embedded-document measurement at DOM readiness rather than full page load when slow subresources would otherwise delay the initial layout.
- A gradient fill does not interpolate like a solid color transition. Keep animated toggles on a solid accent and verify start, middle, and end pixels before replacing them with a gradient.
- Small account photos use a stable first frame rather than animated frame swaps. A multi-frame GIF must keep the same rendered pixels after its frame delays and schedule no animation frames.
- Rounded row selection needs vertical as well as horizontal insets. Measure the first row's rendered distance from the divider; horizontal padding alone lets the selection touch it.
- Icon-only toolbar actions need full-radius hover, not the default rectangular button shape. Verify that corner pixels stay transparent while the edge center takes the hover color.
- Shifted punctuation can bypass the native UI shortcut mapper on macOS. Native menu equivalents must be disabled while text input is active so commands do not consume ordinary `?`, `#`, or `!` input.

## Match live theme changes in native and web content

- A WebView background that follows the app appearance exposes stale document colors if the loaded page keeps its initial theme. Let email CSS respond to `prefers-color-scheme` instead of binding colors to the page's creation time.
- Verify an already-open HTML message through light-to-dark and dark-to-light transitions. A real-window check opened in Light, switched to Dark, and exposed dark text on the dark background; after the CSS change, the same path kept the text readable.

## Regression tests must prove the guard works

- Use controllable timers for debounce in UI tests. A real `time.AfterFunc` can run concurrently with direct test publishes and create data races even though production publishes through `Window.Update`.
- Confirm that the test fails on the old behavior before implementing the fix; avoid tests that always pass.
- Assertions must target the behavior itself. A build failure does not count as a reproduction.
- When restoring behavior by hand for verification, break and restore one thing at a time so uncommitted fixes are not overwritten.
- Use controllable fake services for queue and timing tests; never run destructive tests against a real Gmail account.
- Synchronous account-switch tests must hide push-only methods behind a `mail.Service` wrapper. A fake that satisfied `pushService` ran the notification worker inline and blocked in a real Keychain read.
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

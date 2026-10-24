# Mica design notes

## Purpose

Mica is a macOS Gmail client built with MyGo native UI.
It follows Mimestream's efficient three-pane workflow without copying its branding or assets.
The primary tasks are reading conversations, processing the inbox, and replying quickly.

## Direction

The current message is the focus. The sidebar and conversation list stay quiet and compact.
The sidebar is about 200pt, the list about 340pt, and the reader content is centered and capped at 960pt for comfortable line lengths.
The interface uses the system font, subtle dividers, unread weight and dots, muted avatars, and one blue accent.
The reader uses one continuous theme background across native UI and email WebViews.
Email documents follow live appearance changes through `prefers-color-scheme`, keeping an open message readable without reloading it.
HTML and plain-text bodies have a 24px horizontal reading inset. Root email styles and top-level inline padding take precedence over the HTML default.
Before the first HTML measurement, the body fills the available reader viewport instead of a one-line placeholder. Measurement starts at DOM readiness, and measured or cached heights replace the placeholder.
Subjects, sender metadata, and recipients use a compact Gmail-style hierarchy. Expanded messages offer compact Reply, Reply all, and Forward actions; collapsed messages keep the same actions in one menu. The actions after the conversation still target its latest message.
The reader toolbar floats above the conversation with Liquid Glass tinted toward the theme background so it stays readable over native and WebView content. Primary actions share a blue-indigo gradient and restrained shadow.
Grouped dialog content uses 12pt-radius bordered panels with consistent padding. Conversation rows use rounded selection with a small inset and vertical separation from the list divider.
Reader reply actions use interactive Liquid Glass, and other secondary actions use light borders and shadows. Icon-only toolbar actions use fully circular hover feedback without changing their layout size.
The app icon is a white rounded square with a four-color envelope inspired by Gmail, while the interface keeps its own identity.
English is the source and fallback language, with Simplified Chinese available through MyGo's catalogs.
Gmail sign-in uses the embedded OAuth client by default; a custom client is available in a collapsed section. OAuth tokens are scoped by normalized Gmail email. New logins verify the Gmail identity in memory before committing a token and an active-account marker, and reauthorization requires the same email. The marker lives in Keychain independently of SQLite and identifies the latest verified session. Restoration reads the marker first and falls back to `LastAccount` only for legacy installations. Login and migration rebuild the client from the formal store and retire the legacy token after a successful formal restore, so refreshes stay account-scoped and store availability cannot select an older account. Logout requires a confirmed matching identity, cancels in-flight authentication, closes both token forms and the active marker, and rejects late refresh or reauthorization writes while preserving shared OAuth client configuration. Explicit login or recovery may reopen the account only with the current credential generation; service replacement also cancels prior authentication and advances that generation. Authentication callbacks clear their cancel function only when both the service version and their own operation ID still match. Formal credential commits hold the lifecycle lock across reopening, token and marker writes, formal restore, and legacy cleanup; manual Connect, restore, and reauthorization all enter the same generation and operation lifecycle. The HTTP client and Pub/Sub share one token source, and service deactivation covers both paths. Refresh holds the lifecycle read lock and rechecks deactivation before saving so an old service cannot overwrite a newer authorization.
Appearance follows the system unless the user selects Light or Dark. Motion respects Reduce Motion.
The language selector stores locale codes but presents readable names in both its collapsed value and its options.

## Key states

- Without a Gmail connection, a clearly labeled demo mailbox supports the full workflow.
- Label switching, global Gmail search, conversation loading, empty results, request failures, and offline states.
- The conversation list can instantly filter to unread threads without changing the Gmail query; opening a thread may remove it from the filtered list while keeping it open in the reader.
- Gmail mailboxes, categories, label visibility, and user labels share one navigation model; internal system labels stay out of the Labels section.
- Label details are fetched individually because the label list endpoint omits message counts.
- Remote, data, and Content-ID inline images load through the `mica-img` protocol with per-message tokens. Remote requests never carry Gmail credentials and only connect to public IP addresses; closing a message revokes its tokens.
- HTML email is sanitized with a provider-independent mail sanitizer and rendered in per-message sandboxed WebViews. CSP disables scripts and unproxied subresources, while tables, inline styles, and scoped stylesheets use the browser's real CSS layout.
- Hidden images and tracking images with both dimensions at most 1px are omitted, and narrow readers scale images down proportionally. Hidden inline metadata is removed before document generation and text previews.
- Older read messages in a thread collapse to an accessible sender, preview, and date row; unread and latest messages stay expanded, and manual expansion state follows message IDs.
- The account name and photo come from Google UserInfo. Existing Gmail-only sessions can request the added OpenID scopes once instead of repeatedly prompting.
- Account photos are static; animated images use their first frame without scheduling playback.
- Gmail-style filled importance markers appear in both the conversation list and reader.
- Account metadata, labels, exact query snapshots, and loaded conversations are stored in `~/Library/Application Support/Mica/mail.db` through MyGo's SQLite plugin, with account-scoped records and transactional writes.
- Cached content appears before network revalidation, while fresh in-memory conversations still avoid duplicate requests during the same session. Network failures leave cached content visible, and background body updates preserve the reading position. Attachment binaries and remote images remain on demand except for inline MIME payloads already present in a thread response; only attachment metadata is persisted.
- Conversation lists load 25 threads initially and fetch the next 25 when scrolled to the end; loaded pages are merged into the same query snapshot without losing selection.
- Opening a conversation synchronizes the reader row before cached or network detail loading. Programmatic opens and `j` / `k` collapse to one selection, while direct list input preserves Cmd/Ctrl, Shift, and select-all multi-selection.
- Selection highlighting is keyed by thread ID and the reader remains a separate identity. Filtering, pagination, and history prune only selection keys that are no longer visible; choosing an adjacent conversation after an archive cannot confuse row indices with thread identity.
- Startup and account switches select the newest conversation from the first non-empty list and restore cached detail when available. Later refreshes can insert newer conversations above it without changing the selected thread or switching the reader.
- Closing the main window hides it and keeps Mica in the Dock. Dock activation restores the same window, while Quit still exits.
- Settings opens from the application menu with Cmd+, and the main window is restored when necessary.
- Sidebar and conversation-list widths persist in local preferences and restore before the first frame; other reading state stays session-only.
- Gmail watch and Pub/Sub Streaming Pull cover account changes, while notifications remain limited to unread INBOX activity. The watch renews daily, and an incremental history reconciliation runs hourly. A local history checkpoint catches up after restarts without treating existing unread mail as new. History additions, deletions, and label changes refresh complete conversations in SQLite before the checkpoint advances. The visible thread list updates without invalidating opened conversations, and selection follows the thread ID when rows move. Draft view and search results always refresh from their own query instead of merging ordinary thread IDs.
- Compose and reply preserve unsent input and attachments after a failure. Multi-file selection and drops read files in the background; each item shows its name, size, reading or error state, and a remove action. Results from removed items or a replaced composer are discarded, and sending is blocked until every attachment has loaded without errors. File selection and drops during send, close, or discard are rejected with an explicit wait message.
- Outgoing attachments are local name, MIME type, and byte data, separate from received attachment metadata. The total limit is 25 MiB (26,214,400 bytes) of unencoded attachment data per message. Each composer reads one attachment at a time and recomputes the remaining allowance before every read, with another total check as results arrive. A read that fails against a stale smaller allowance is retried after loaded items are removed. MIME detection uses the file extension, then content sniffing, with `application/octet-stream` as the fallback.
- The Drafts view uses paginated `drafts.list`, hydrates every item with full `drafts.get`, and forwards the search query. Draft IDs are selection keys while full drafts retain their message and thread IDs. Create and update accept incomplete MIME, including empty recipients and bodies. Sending an existing draft updates its content and then calls `drafts/send`; new messages continue to use `messages/send`, and strict validation happens only at send time.
- Actual composer edits increment the content revision and trigger debounced saves. A separate in-flight flag, independent of the visible save state, allows at most one create or update and rejects duplicate debounces before completion. The returned draft ID is recorded and newer revisions use updates. Empty new composers are not created, while cleared existing drafts are saved as empty; HTML-only content is not treated as empty. Saving, saved, and save-failed states are visible.
- Cancel waits for the latest save and keeps the composer open on failure. If account verification blocks that save, Cancel preserves both the local snapshot and the open composer. Closing an existing draft invalidates its current detail so reopening performs a fresh full get; ordinary readers are unaffected. Composer inputs are disabled while send, close, or discard is pending. Discard waits for any current create or update before deleting; delete failures preserve the draft without leaving saving or actions blocked. Sending also waits for the current create or update before using the latest content, with body, attachments, and draft ID preserved on failure. Reopened drafts restore Bcc, threading, and attachments without adding another reply quote.
- Account-scoped SQLite draft snapshots contain the complete compose snapshot and attachment bytes. Actual edits write the latest snapshot, synchronized saves clear it, and sending or explicit discard also clears it. A successful create immediately records the new draft ID when newer edits remain unsaved. Snapshots restore when the cached account identity matches, including after an offline profile failure, and remain editable offline. Remote draft saves, deletion, and sends wait for a verified account matching both store and recovery identities; an existing or recovered draft never falls back to `messages/send` when this check fails. A recovery account that differs from the current account cannot read, write, delete, or synchronize snapshots. Attachments still being read are restored as explicit errors instead of being omitted.
- Reply and Reply all take recipients, subject, quote, and threading headers from the selected message. Replies mark their threading as resolved so Gmail and demo services cannot borrow headers from the latest message; legacy unresolved calls retain the fallback. Forward takes its subject, quoted content, and attachments only from that message and starts without a thread ID or reply headers. Conversation-level actions remain wrappers around the latest message.
- Forward attachment sources distinguish local paths from Gmail downloads. Selected attachments load through the same serial composer pipeline as local files; inline `body.data` payloads are copied directly, while other attachments use `AttachmentFetcher`. Inline bytes remain in memory only and are not persisted in the mail cache. Download state for attachments without an ID is keyed by their position in the message.
- Messages with attachments use a `multipart/mixed` outer part. Text and HTML bodies remain nested in `multipart/alternative`; messages without attachments retain their single-body or alternative form. Attachment filenames use RFC encoding, and binary data uses Base64 with lines capped at 76 columns.
- The sidebar Labels section always provides label creation, including when no custom labels exist yet. Right-clicking a custom label opens Gmail-supported color choices and confirmed deletion; color changes use the serial mutation queue and persist with label metadata.
- The reader's native label menu only assigns existing custom labels, shows their current state, and keeps label edits, list membership, and undo consistent.
- Star, archive, trash, spam, and read/unread changes apply locally first and synchronize through a serial background queue. Failures are reconciled with Gmail, and undo restores the local snapshot before queuing the inverse request. Removing the current conversation opens the next one at the same position, or the previous one at the end; the reader clears only when no conversations remain.
- Archive, Trash/Untrash, Spam/Not spam, Star, and Read/Unread resolve action targets in visible-list order; Archive shows the selection count. Mixed Star, Read/Unread, Trash, and Spam selections choose one target state from the first item and skip items already in that state, so Undo never changes items that did not need the action. Single-target actions keep their existing local and remote paths.
- Batch mutations apply local updates once and store ordered undo items in one action. Removal follows the action: never for Star/Read, Inbox-only for Archive, all targets for Trash/Spam, and post-action query membership for Untrash/Not spam. Reader replacement follows the original list's next surviving conversation, or the previous survivor when none follows; a reader outside the batch stays open. Undo restores all summaries in one list update and enqueues per-thread inverse requests through the same FIFO. Custom label assignment, replies, and forwards remain scoped to the reader.
- Trash and spam conversations offer Untrash and Not spam based on their actual label IDs, including in search results; the toolbar, application menu, and `#` / `!` shortcuts share this forward/recovery dispatch and menu labels update with the selection. Untrash uses Gmail's dedicated thread endpoint and removes only `TRASH`, while Not spam applies `+INBOX / -SPAM`. Undo records the operation kind so Trash and Untrash reverse through their dedicated endpoints rather than label modification alone. Recovery removes a conversation from a non-search view only when its post-action labels no longer match; search retains the existing remove-and-refresh reconciliation.

## Motion

- Buttons and selection states use MyGo's small, built-in feedback. The unread filter uses a 140ms color transition to a clear Accent active state with a high-contrast icon.
- Loaded conversation content fades in with a 10pt upward movement over 240ms rather than flashing a blank pane.
- The first reader entrance and later conversation transitions use the same short motion. Toasts fade in and out; plain notices remain for 3 seconds, and action notices remain for 5 seconds.
- Message expansion and collapse use an interruptible spring transition. Reduce Motion completes the change immediately.
- MyGo removes or shortens nonessential motion when Reduce Motion is enabled.

## Build configuration

Builds embed `internal/gmail/oauth-client.json` and fail when it is missing or invalid.
CI writes it from `MICA_OAUTH_CLIENT_JSON` before testing and packaging.
OAuth requests Gmail, OpenID, and Pub/Sub scopes. The embedded configuration supplies the Cloud project ID, and custom OAuth settings accept one too.
The OAuth file and the update signing private key are ignored by Git.
Release signing imports `MACOS_CERTIFICATE` into a temporary keychain; notarization uses `APPLE_API_KEY`, `APPLE_API_KEY_ID`, and `APPLE_API_ISSUER_ID` through `notarytool store-credentials`.

## Verification

The real macOS window is checked for pane proportions, long sender/subject truncation, readable body text, keyboard operation, light and dark appearances, and Retina crops.
The final build exercises search, labels, categories, conversations, inline images, cached reopening, reply sending, appearance switching, language switching, and Dock lifecycle.
Real Gmail acceptance requires a Google test account.

## Current verification record

- `CGO_ENABLED=0 go test ./...`, `go test -race ./...`, `go vet ./...`, `go tool mygo vet .`, and `go tool mygo build -platform darwin/universal` pass. Both GitHub Actions workflows pass actionlint.
- Regression tests failed on the previous behavior for duplicate thread requests, missing categories, leaked `YELLOW_STAR`, missing image sources, incorrect mailbox queries, and ignored inline image dimensions before the fixes.
- The packaged build was exercised with the restored Gmail account: Purchases results, email-sized images, hidden-image suppression, A-B-A cached reopening, and user-only labels all worked.
- The packaged app kept running after the window closed, restored the same reader state from the Dock, and exited with Cmd+Q. The new icon was checked at 128px and in the Dock.
- Removing `internal/gmail/oauth-client.json` produces the expected `go:embed` build error.
- An independent visual review scored 8.5/10 with no blocking issues. Dark secondary text and selected-row snippets were strengthened. The reader is capped at 960pt.
- The final build was rechecked in English and Simplified Chinese, including the complete demo workflow.
- The packaged settings UI shows the built-in Gmail connection first and keeps custom OAuth fields collapsed until requested.
- Shifted Gmail punctuation shortcuts use native menu equivalents and are disabled while search or modal text input is active.
- Marketing copy and nonfunctional placeholder buttons were removed. Only actions with real behavior remain.
- Final screenshots are in `docs/screenshots/`: `reading-light.jpg`, `reading-dark.jpg`, `mailbox.jpg`, `settings.jpg`, `list-retina.png`, and `message-retina.png`.
- English motion evidence is in `reader-*.png`, `compose-*.png`, `toast-*.png`, and `button-pressed.png`, generated by `MICA_CAPTURE=1 go test ./internal/app -run TestCaptureMotion -count=1`. `motion-sheet.png` composites the reader, composer, and toast sequences.
- The native updater window was exercised. Its feed returns 404 until a release is published; a real update installation has not been tested.
- Google OAuth incremental authorization and real Keychain writes succeeded. A later launch restored the account name and photo without another authorization prompt.
- The packaged build displayed Important markers and the real Inoreader newsletter with its centered layout, text styles, inline icons, and 40/60 article columns. Toast notifications expired in the real window. The application menu opened Settings with Cmd+, including while the main window was hidden; no sidebar Settings entry remained. The sidebar title bar contains only Compose, and Gmail-style filled importance markers were checked in normal, selected, and reader states.
- Notification tests cover the initial baseline, new threads and replies, duplicate delivery, manual read/unread changes, restart catch-up, history reset, badges, and opening conversations. A real Gmail watch and a Pub/Sub test message completed the stream/history/ACK path, including numeric history IDs. macOS displayed a native Dock badge and accepted a test notification; the notification click path was not exercised through the system UI.
- Notary API access and release workflow lint passed. A matching Developer ID Application certificate and private key were verified, and all required signing and notarization secrets are configured. A signed and notarized release has not been run.

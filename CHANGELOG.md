# Changelog

## 0.13.0 - 2026-10-24

- Added Unified Inbox across saved Gmail accounts, with deterministic merging, independent per-account pagination, and complete offline snapshots.
- Added account ownership in unified rows and owner-aware reading, replies, drafts, attachments, mutations, and label cleanup.
- Preserved optimistic changes and query-aware Undo across refreshes, cache writes, account removal, and view transitions.
- Added safeguards for unavailable account services, stale responses, cross-view cache ordering, and same-ID labels across accounts.
- Added English Unified Inbox documentation and screenshot coverage.

## 0.12.0 - 2026-10-24

- Added Gmail multi-account management in Settings, including adding, switching, reauthorizing, and removing accounts with English and Simplified Chinese interfaces.
- Isolated OAuth credentials, local caches, drafts, mutations, and notifications by account, with ordered activation, safe worker shutdown, and account-aware notification clicks.
- Protected local unsent drafts and legacy-token ownership during account transitions and removal, including Demo fallback and cleanup-failure handling.
- Refined icon-only toolbar buttons with circular hover feedback.

## 0.11.0 - 2026-10-24

- Added Move out of Trash and Not spam with label-aware toolbar and native-menu actions, dedicated Gmail endpoints, and operation-aware Undo.
- Added Cmd/Ctrl, Shift, and select-all multi-selection with batch Archive, Trash, Spam, Star, and Read/Unread actions and coherent batch Undo.
- Preserved mixed read, star, Trash, and Spam states, combined-label visibility, and reader adjacency across batch actions.

## 0.10.0 - 2026-10-24

- Added multi-file attachments to composing, replying, and forwarding, with background reads, a 25 MB total limit, and byte-preserving retries.
- Added Reply, Reply all, and Forward actions for individual messages, using the selected message's recipients, quote, threading headers, and attachments.
- Added Gmail draft autosave, offline recovery, editing, discard, and draft-specific sending, with account-scoped SQLite snapshots and attachment preservation.

## 0.9.2 - 2026-10-24

- Kept account photos static by showing the first frame of animated images without scheduling playback.
- Added vertical separation between the selected conversation row and the list divider.
- Stabilized the reader toolbar's Liquid Glass when HTML content scrolls underneath it.
- Restored Gmail shifted-punctuation shortcuts with native menu commands that yield to search and dialog text input.

## 0.9.1 - 2026-10-24

- Prevented HTML messages from opening as a one-line strip while waiting for their body height, using the reader viewport until measurement completes at DOM readiness.

## 0.9.0 - 2026-10-24

- Unified application styling with shared gradient primary actions, bordered dialog panels, rounded conversation selection, and shortcut keycaps.
- Added a Liquid Glass reader toolbar and interactive reply actions, backdrop-aware surfaces, and interruptible spring motion for message expansion.
- Added animated GIF account photos with frame delays, finite loop handling, and first-frame rendering for static images and Reduce Motion.

## 0.8.0 - 2026-10-24

- Refreshed the conversation reader with a continuous theme background, a lighter subject hierarchy, compact sender details, and Reply, Reply all, and Forward actions.
- Matched email WebView backgrounds to the native reader, increased HTML body line height, and made mail follow live appearance changes through `prefers-color-scheme`.

## 0.7.0 - 2026-10-23

- Added sidebar label creation, right-click management, Gmail-supported color editing, and confirmed deletion; kept the reader menu scoped to label assignment.
- Made the unread filter's active state clear with an Accent background, high-contrast icon, and a 140ms color transition.
- Kept the list highlight and reader on the same conversation after archiving, including when the next conversation occupies the same row.
- Replaced raw locale codes with readable language names and added an overridable default reading inset for HTML mail with matching text fallbacks.

## 0.6.0 - 2026-10-23

- Added automatic selection of the latest conversation on startup and account switches, with cached detail restoration and stable selection as newer mail arrives.
- Synchronized list selection before cached or network detail loading.
- Shortened toast notifications to 3 seconds and actionable Undo notifications to 5 seconds.

## 0.5.0 - 2026-10-23

- Added unread-only filtering, smoother reader transitions, and content-aware spacing for HTML and plain-text mail.
- Made mail mutations optimistic with serialized background synchronization and immediate undo.
- Added custom-label assignment, creation, and deletion with confirmation for destructive changes.
- Reduced initial sync work with 25-thread pages and automatic loading when scrolling to the end.
- Fixed All Mail by using a global query instead of the unsupported ALL label.

## 0.4.0 - 2026-10-23

- Fixed first email rendering so WebViews appear before the initial height measurement.
- Reduced Gmail request overhead with single-request labels, changed-thread synchronization, and coalesced push delivery.
- Added bounded Retry-After backoff with jitter and concurrent request deduplication.

## 0.3.0 - 2026-10-23

- Added browser-native HTML email rendering with smooth height transitions and keyboard shortcuts in focused messages.
- Added account-scoped SQLite caching with offline reading and background Gmail History synchronization.
- Kept archive selection and shortcuts advancing through conversations.
- Added attachment opening and saving with safe filenames and download feedback.
- Added collapsible reply quotes and Reply-To recipient handling.
- Added persistent sidebar and conversation-list widths.

## 0.2.0 - 2026-10-22

- Added remote, data URI, and Content-ID images with email dimensions, inline pixel sizes, and hidden-image handling.
- Added in-memory conversation caching and Dock hide-and-restore behavior while keeping Cmd+Q as a full quit.
- Added Gmail mailbox and category navigation with label visibility and internal system-label filtering.
- Replaced the app icon with a Gmail-inspired four-color envelope.

## 0.1.0 - 2026-10-22

- Added a native three-pane macOS interface for Gmail labels, search, and thread reading.
- Added Google OAuth sign-in and Gmail actions for composing, replying, archiving, starring, read state, and trash.
- Added an interactive demo mailbox for use without connecting a Google account.
- Added appearance settings, keyboard shortcuts, and English and Simplified Chinese localization.
- Added signed updates through GitHub Releases.

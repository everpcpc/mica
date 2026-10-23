# Changelog

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

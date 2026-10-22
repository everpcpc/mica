# Mica design notes

## Purpose

Mica is a macOS Gmail client built with MyGo native UI.
It follows Mimestream's efficient three-pane workflow without copying its branding or assets.
The primary tasks are reading conversations, processing the inbox, and replying quickly.

## Direction

The current message is the focus. The sidebar and conversation list stay quiet and compact.
The sidebar is about 200pt, the list about 340pt, and the reader content is centered and capped at 960pt for comfortable line lengths.
The interface uses the system font, subtle dividers, unread weight and dots, muted avatars, and one blue accent.
A gray reading surface and a white message body separate content from the surrounding chrome without marketing gradients or decorative illustrations.
The app icon is a white rounded square with a four-color envelope inspired by Gmail, while the interface keeps its own identity.
English is the source and fallback language, with Simplified Chinese available through MyGo's catalogs.
Gmail sign-in uses the embedded OAuth client by default; a custom client is available in a collapsed section.
Appearance follows the system unless the user selects Light or Dark. Motion respects Reduce Motion.

## Key states

- Without a Gmail connection, a clearly labeled demo mailbox supports the full workflow.
- Label switching, global Gmail search, conversation loading, empty results, request failures, and offline states.
- Gmail mailboxes, categories, label visibility, and user labels share one navigation model; internal system labels stay out of the Labels section.
- Label details are fetched individually because the label list endpoint omits message counts.
- Remote, data, and Content-ID inline images load through the `mica-img` protocol with per-message tokens. Remote requests never carry Gmail credentials and only connect to public IP addresses; closing a message revokes its tokens.
- HTML email is sanitized with a provider-independent mail sanitizer and rendered in per-message sandboxed WebViews. CSP disables scripts and unproxied subresources, while tables, inline styles, and scoped stylesheets use the browser's real CSS layout.
- Hidden images and tracking images with both dimensions at most 1px are omitted, and narrow readers scale images down proportionally. Hidden inline metadata is removed before document generation and text previews.
- Older read messages in a thread collapse to an accessible sender, preview, and date row; unread and latest messages stay expanded, and manual expansion state follows message IDs.
- The account name and photo come from Google UserInfo. Existing Gmail-only sessions can request the added OpenID scopes once instead of repeatedly prompting.
- Gmail-style filled importance markers appear in both the conversation list and reader.
- Account metadata, labels, exact query snapshots, and loaded conversations are stored in `~/Library/Application Support/Mica/mail.db` through MyGo's SQLite plugin, with account-scoped records and transactional writes.
- Cached content appears before network revalidation, while fresh in-memory conversations still avoid duplicate requests during the same session. Network failures leave cached content visible, and background body updates preserve the reading position. Attachment binaries and remote images remain on demand; only attachment metadata is persisted.
- Closing the main window hides it and keeps Mica in the Dock. Dock activation restores the same window, while Quit still exits.
- Settings opens from the application menu with Cmd+, and the main window is restored when necessary.
- Sidebar and conversation-list widths persist in local preferences and restore before the first frame; other reading state stays session-only.
- Gmail watch and Pub/Sub Streaming Pull cover account changes, while notifications remain limited to unread INBOX activity. The watch renews daily, and an incremental history reconciliation runs hourly. A local history checkpoint catches up after restarts without treating existing unread mail as new. History additions, deletions, and label changes refresh complete conversations in SQLite before the checkpoint advances. The visible thread list updates without invalidating opened conversations, and selection follows the thread ID when rows move.
- Compose and reply preserve unsent input after a failure.
- Star, archive, trash, spam, and read/unread changes provide immediate feedback and undo where supported. Removing the current conversation opens the next one at the same position, or the previous one at the end; the reader clears only when no conversations remain.

## Motion

- Buttons and selection states use MyGo's small, built-in feedback.
- Loaded conversation content fades in rather than flashing a blank pane.
- The first reader entrance and later conversation transitions use the same short motion. Toasts fade in and out.
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
- MyGo's current macOS key mapping does not recognize the shifted punctuation used by `?`, `#`, and `!` as their Gmail key equivalents. No native-menu workaround is included.
- Marketing copy and nonfunctional placeholder buttons were removed. Only actions with real behavior remain.
- Final screenshots are in `docs/screenshots/`: `reading-light.jpg`, `reading-dark.jpg`, `settings.jpg`, `list-retina.png`, and `message-retina.png`.
- Motion evidence is in `reader-*.png`, `compose-*.png`, `toast-*.png`, and `motion-sheet.png`, generated by `MICA_CAPTURE=1 go test ./internal/app -run TestCaptureMotion -count=1`.
- The native updater window was exercised. Its feed returns 404 until a release is published; a real update installation has not been tested.
- Google OAuth incremental authorization and real Keychain writes succeeded. A later launch restored the account name and photo without another authorization prompt.
- The packaged build displayed Important markers and the real Inoreader newsletter with its centered layout, text styles, inline icons, and 40/60 article columns. Toast notifications expired in the real window. The application menu opened Settings with Cmd+, including while the main window was hidden; no sidebar Settings entry remained. The sidebar title bar contains only Compose, and Gmail-style filled importance markers were checked in normal, selected, and reader states.
- Notification tests cover the initial baseline, new threads and replies, duplicate delivery, manual read/unread changes, restart catch-up, history reset, badges, and opening conversations. A real Gmail watch and a Pub/Sub test message completed the stream/history/ACK path, including numeric history IDs. macOS displayed a native Dock badge and accepted a test notification; the notification click path was not exercised through the system UI.
- Notary API access and release workflow lint passed. A matching Developer ID Application certificate and private key were verified, and all required signing and notarization secrets are configured. A signed and notarized release has not been run.

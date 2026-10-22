# Mica design notes

## Purpose

Mica is a macOS Gmail client built with MyGo native UI.
It follows Mimestream's efficient three-pane workflow without copying its branding or assets.
The primary tasks are reading conversations, processing the inbox, and replying quickly.

## Direction

The current message is the focus. The sidebar and conversation list stay quiet and compact.
The sidebar is about 200pt, the list about 340pt, and the reader takes the remaining width, capped at 780pt for comfortable line lengths.
The interface uses the system font, subtle dividers, unread weight and dots, muted avatars, and one blue accent.
It avoids nested cards, marketing gradients, and decorative illustrations.
The app icon is a white rounded square with a four-color envelope inspired by Gmail, while the interface keeps its own identity.
English is the source and fallback language, with Simplified Chinese available through MyGo's catalogs.
Gmail sign-in uses the embedded OAuth client by default; a custom client is available in a collapsed section.
Appearance follows the system unless the user selects Light or Dark. Motion respects Reduce Motion.

## Key states

- Without a Gmail connection, a clearly labeled demo mailbox supports the full workflow.
- Label switching, global Gmail search, conversation loading, empty results, request failures, and offline states.
- Gmail mailboxes, categories, label visibility, and user labels share one navigation model; internal system labels stay out of the Labels section.
- Remote, data, and Content-ID inline images load asynchronously. Remote requests never carry Gmail credentials and only connect to public IP addresses.
- HTML image dimensions and inline pixel sizes are preserved, hidden images are omitted, and narrow readers scale images down proportionally.
- Loaded conversations stay in an in-memory cache until an explicit refresh. Label changes update cached state, and account switches clear it.
- Closing the main window hides it and keeps Mica in the Dock. Dock activation restores the same window, while Quit still exits.
- Compose and reply preserve unsent input after a failure.
- Star, archive, trash, spam, and read/unread changes provide immediate feedback and undo where supported.

## Motion

- Buttons and selection states use MyGo's small, built-in feedback.
- Loaded conversation content fades in rather than flashing a blank pane.
- The first reader entrance and later conversation transitions use the same short motion. Toasts fade in and out.
- MyGo removes or shortens nonessential motion when Reduce Motion is enabled.

## Build configuration

Builds embed `internal/gmail/oauth-client.json` and fail when it is missing or invalid.
CI writes it from `MICA_OAUTH_CLIENT_JSON` before testing and packaging.
The OAuth file and the update signing private key are ignored by Git.

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
- An independent visual review scored 8.5/10 with no blocking issues. Dark secondary text and selected-row snippets were strengthened, and the reader was capped at 780pt.
- The final build was rechecked in English and Simplified Chinese, including the complete demo workflow.
- The packaged settings UI shows the built-in Gmail connection first and keeps custom OAuth fields collapsed until requested.
- MyGo's current macOS key mapping does not recognize the shifted punctuation used by `?`, `#`, and `!` as their Gmail key equivalents. No native-menu workaround is included.
- Marketing copy and nonfunctional placeholder buttons were removed. Only actions with real behavior remain.
- Final screenshots are in `docs/screenshots/`: `reading-light.jpg`, `reading-dark.jpg`, `settings.jpg`, `list-retina.png`, and `message-retina.png`.
- Motion evidence is in `reader-*.png`, `compose-*.png`, `toast-*.png`, and `motion-sheet.png`, generated by `MICA_CAPTURE=1 go test ./internal/app -run TestCaptureMotion -count=1`.
- The native updater window was exercised. Its feed returns 404 until a release is published; a real update installation has not been tested.
- Google OAuth login and real token writes to Keychain have not been retested. The existing Keychain session restored successfully, and real Gmail reads were exercised.

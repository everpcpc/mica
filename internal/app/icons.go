package app

import "github.com/egoist/mygo/ui"

func icon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.45" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

var (
	iconInbox        = icon(`<path d="M3 3.5h14v13H3z"/><path d="M3 11h4l1.5 2h3L13 11h4"/>`)
	iconStar         = icon(`<path d="m10 2.8 2.1 4.3 4.7.7-3.4 3.3.8 4.7-4.2-2.2-4.2 2.2.8-4.7-3.4-3.3 4.7-.7z"/>`)
	iconSnoozed      = icon(`<circle cx="10" cy="10" r="7"/><path d="M10 6v4l2.8 1.8"/>`)
	iconSent         = icon(`<path d="m2.8 3.2 14.4 6.8-14.4 6.8 2-6.8z"/><path d="M4.8 10h12.4"/>`)
	iconImportant    = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><path d="M3.5 18.99l11 .01c.67 0 1.27-.33 1.63-.84L20.5 12l-4.37-6.16c-.36-.51-.96-.84-1.63-.84l-11 .01L8.34 12 3.5 18.99z"/></svg>`))
	iconAll          = icon(`<path d="m3 7 7-4 7 4-7 4zM3 10l7 4 7-4M3 13l7 4 7-4"/>`)
	iconSpam         = icon(`<path d="M7 3h6l4 4v6l-4 4H7l-4-4V7z"/><path d="M10 6v5M10 14v.2"/>`)
	iconTrash        = icon(`<path d="M3.5 5.5h13M8 2.8h4l1 2.7H7zM5.5 5.5l.8 11h7.4l.8-11M8.2 8.5v5M11.8 8.5v5"/>`)
	iconArchive      = icon(`<path d="M3 4h14v4H3zM4.5 8v8h11V8M8 11h4"/>`)
	iconCompose      = icon(`<path d="m12.8 3.2 4 4L8 16H4v-4zM11.5 4.5l4 4M4 20h12"/>`)
	iconReply        = icon(`<path d="m8 5-5 5 5 5M3 10h8c4 0 6 2 6 6"/>`)
	iconReplyAll     = icon(`<path d="m7 16-5-5 5-5M12 16l-5-5 5-5M7 11h5c4 0 6 2 6 6"/>`)
	iconForward      = icon(`<path d="m12 5 5 5-5 5M17 10H9c-4 0-6 2-6 6"/>`)
	iconRefresh      = icon(`<path d="M16 7a6.5 6.5 0 1 0 .5 6M16 3v4h-4"/>`)
	iconFilter       = icon(`<path d="M3 4h14l-5.5 6.5v5L8.5 17v-6.5z"/>`)
	iconMail         = icon(`<rect x="2.5" y="4" width="15" height="12" rx="2"/><path d="m3.5 5 6.5 5 6.5-5"/>`)
	iconLabel        = icon(`<path d="M3 3h6l8 8-6 6-8-8z"/><circle cx="6.5" cy="6.5" r="1"/>`)
	iconChevronRight = icon(`<path d="m7 3 7 7-7 7"/>`)
	iconChevronDown  = icon(`<path d="m3 7 7 7 7-7"/>`)
)

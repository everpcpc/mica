package app

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"testing"
	"time"

	"mica/internal/mail"

	"github.com/egoist/mygo/ui"
)

func TestHiddenSystemLabels(t *testing.T) {
	for _, id := range []string{"DRAFT", "CHAT", "IMPORTANT", "SPAM", "YELLOW_STAR", "CATEGORY_SOCIAL", "CATEGORY_PROMOTIONS"} {
		if !hiddenSystemLabel(id) {
			t.Errorf("%s should stay out of custom labels", id)
		}
	}
	for _, id := range []string{"Product", "Design", "Receipts"} {
		if hiddenSystemLabel(id) {
			t.Errorf("%s should be a custom label", id)
		}
	}
}

func TestMailboxLabelNames(t *testing.T) {
	a := New(newFakeService())
	for id, want := range map[string]string{
		"SNOOZED": "Snoozed", "DRAFT": "Drafts", "IMPORTANT": "Important", "ALL": "All Mail",
		"CATEGORY_SOCIAL": "Social", "CATEGORY_PROMOTIONS": "Promotions", "CATEGORY_UPDATES": "Updates",
		"CATEGORY_FORUMS": "Forums", "CATEGORY_PURCHASES": "Purchases", "CATEGORY_RESERVATIONS": "Reservations",
	} {
		if got := a.labelName(id); got != want {
			t.Errorf("labelName(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestSidebarShowsCategoriesAndOnlyCustomLabels(t *testing.T) {
	service := newFakeService()
	service.labels = []mail.Label{
		{ID: "YELLOW_STAR", Name: "YELLOW_STAR"},
		{ID: "CATEGORY_PURCHASES", Name: "Purchases"},
		{ID: "Label_9", Name: "Receipts"},
	}
	a := newTestApp(service)
	tt := ui.NewTester(a.View, 1280, 800)
	for _, want := range []string{"Snoozed", "Drafts", "Important", "All Mail", "Social", "Promotions", "Updates", "Forums", "Purchases", "Reservations", "Receipts"} {
		if !tt.HasText(want) {
			t.Errorf("sidebar is missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("YELLOW_STAR") {
		t.Errorf("internal star label appears as a custom label: %q", tt.Texts())
	}
}

func TestCustomSidebarLabelsUseMetadataAndVisibility(t *testing.T) {
	labels := customSidebarLabels([]mail.Label{
		{ID: "ORANGE_STAR", Name: "ORANGE_STAR", System: true},
		{ID: "Label_1", Name: "Visible"},
		{ID: "Label_2", Name: "Hidden", Hide: true},
		{ID: "Label_3", Name: "No unread", ShowIfUnread: true},
		{ID: "Label_4", Name: "Unread", ShowIfUnread: true, Unread: 2},
	})
	if len(labels) != 2 || labels[0].ID != "Label_1" || labels[1].ID != "Label_4" {
		t.Fatalf("custom labels = %+v", labels)
	}
}

func TestLabelMenuAddsRemovesAndUndoesCustomLabel(t *testing.T) {
	service := newFakeService()
	service.labels = append(service.labels, mail.Label{ID: "Label_9", Name: "Receipts"})
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(a.View, 1280, 800)
	openMenu := func() { openReaderActions(t, tt) }

	openMenu()
	if err := tt.ChooseMenuItem("Edit labels", "Receipts"); err != nil {
		t.Fatal(err)
	}
	if !containsLabel(a.thread.LabelIDs, "Label_9") || len(service.add) != 1 || service.add[0] != "Label_9" {
		t.Fatalf("label was not added locally and queued: thread=%v add=%v", a.thread.LabelIDs, service.add)
	}
	a.selectedLabel = "Label_9"
	a.clearThread()
	a.selectThread("thread-1")
	tt.Frame()

	openMenu()
	if err := tt.ChooseMenuItem("Edit labels", "Receipts"); err != nil {
		t.Fatal(err)
	}
	if len(a.threads) != 0 || len(service.remove) != 1 || service.remove[0] != "Label_9" {
		t.Fatalf("removing the current label did not remove the conversation: threads=%v remove=%v", a.threads, service.remove)
	}
	a.undoLast()
	if len(a.threads) != 1 || !containsLabel(a.threads[0].LabelIDs, "Label_9") {
		t.Fatalf("undo did not restore the labeled conversation: %+v", a.threads)
	}
}

func TestLabelMenuOnlyAssignsCustomLabels(t *testing.T) {
	service := newFakeService()
	service.labels = append(service.labels, mail.Label{ID: "Label_9", Name: "Receipts"})
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Button(c, "Labels").Menu(a.labelMenu)
	}, 400, 300)
	if err := tt.Click("Labels"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	for _, unwanted := range []string{"New label…", "Delete label"} {
		for _, item := range menu {
			if item == unwanted {
				t.Fatalf("reader label menu contains management action %q: %q", unwanted, menu)
			}
		}
	}
}

func TestSidebarCreatesCustomLabel(t *testing.T) {
	service := newFakeService()
	a := newTestApp(service)
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.Click("New label…"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Label name"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Receipts")
	if err := tt.Click("Create label"); err != nil {
		t.Fatal(err)
	}
	if len(service.createdLabels) != 1 || service.createdLabels[0] != "Receipts" {
		t.Fatalf("created labels = %v, want Receipts", service.createdLabels)
	}
	for _, label := range a.labels {
		if label.ID == "Label_new" && label.Name == "Receipts" {
			return
		}
	}
	t.Fatalf("created label missing from app state: %+v", a.labels)
}

func TestSidebarEditsLabelColor(t *testing.T) {
	service := newFakeService()
	service.labels = append(service.labels, mail.Label{ID: "Label_9", Name: "Receipts"})
	a := newTestApp(service)
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.RightClick("Receipts"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Label color", "🔵 Blue"); err != nil {
		t.Fatal(err)
	}
	want := mail.LabelColor{Background: "#4a86e8", Text: "#ffffff"}
	if got := service.labelColors["Label_9"]; got != want {
		t.Fatalf("label color = %+v, want %+v", got, want)
	}
	for _, label := range a.labels {
		if label.ID == "Label_9" && label.Color == want {
			return
		}
	}
	t.Fatalf("updated color is missing from app state: %+v", a.labels)
}

func TestSidebarLabelColorUsesLeadingCircle(t *testing.T) {
	service := newFakeService()
	service.labels = append(service.labels, mail.Label{ID: "Label_9", Name: "Receipts", Color: mail.LabelColor{Background: "#4a86e8", Text: "#ffffff"}})
	a := newTestApp(service)
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetScale(2)
	tt.Frame()
	r, ok := tt.Find("Receipts")
	if !ok {
		t.Fatalf("colored sidebar label not found: %q", tt.Texts())
	}
	scale := float32(tt.Image().Bounds().Dx()) / 1280
	pixel := func(x, y float32) color.RGBA {
		return tt.Image().RGBAAt(int(x*scale), int(y*scale))
	}
	centerX, centerY := r.X-16, r.Y+r.H/2
	background := pixel(centerX-10, centerY)
	if edge := pixel(centerX-4, centerY-5.5); edge != background {
		t.Fatalf("label icon is not a circle: edge=%v background=%v", edge, background)
	}
	want := color.RGBA{R: 0x4a, G: 0x86, B: 0xe8, A: 255}
	if center := pixel(centerX, centerY); center != want {
		t.Fatalf("label circle center = %v, want %v", center, want)
	}
}

func TestSidebarDeletesCustomLabelAfterConfirmation(t *testing.T) {
	service := newFakeService()
	service.labels = append(service.labels, mail.Label{ID: "Label_9", Name: "Receipts"})
	service.threads[0].LabelIDs = append(service.threads[0].LabelIDs, "Label_9")
	service.thread.LabelIDs = append(service.thread.LabelIDs, "Label_9")
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.RightClick("Receipts"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Delete label"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Delete label?") {
		t.Fatalf("delete confirmation missing: %q", tt.Texts())
	}
	if len(service.deletedLabels) != 0 {
		t.Fatal("label was deleted before confirmation")
	}
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeyEnter)
	if len(service.deletedLabels) != 1 || service.deletedLabels[0] != "Label_9" {
		t.Fatalf("deleted labels = %v, want Label_9", service.deletedLabels)
	}
	for _, label := range a.labels {
		if label.ID == "Label_9" {
			t.Fatal("deleted label remains in app state")
		}
	}
	if containsLabel(a.thread.LabelIDs, "Label_9") {
		t.Fatalf("deleted label remains on the conversation: %v", a.thread.LabelIDs)
	}
}

func newColoredThreadLabelApp() *App {
	service := newFakeService()
	service.labels = append(service.labels, mail.Label{
		ID: "Label_9", Name: "Receipts",
		Color: mail.LabelColor{Background: "#4a86e8", Text: "#ffffff"},
	})
	service.threads[0].LabelIDs = append(service.threads[0].LabelIDs, mail.Sent, "Label_9")
	service.thread.LabelIDs = append(service.thread.LabelIDs, mail.Sent, "Label_9")
	a := newTestApp(service)
	a.selectThread("thread-1")
	return a
}

func TestThreadCustomLabelAppearsInListAndReader(t *testing.T) {
	a := newColoredThreadLabelApp()
	tt := ui.NewTester(a.View, 1280, 800)
	tt.SetLocale("en")
	count := 0
	for _, text := range tt.Texts() {
		if text == "Receipts" {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("Receipts appears %d times, want sidebar, list, and reader: %q", count, tt.Texts())
	}
}

func TestThreadLabelChipColorAndPadding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		width    int
		selected bool
		view     func(*App) func(*ui.Context)
	}{
		{name: "selected list row", width: 380, selected: true, view: func(a *App) func(*ui.Context) { return a.threadList }},
		{name: "reader", width: 900, view: func(a *App) func(*ui.Context) { return a.threadContent }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newColoredThreadLabelApp()
			var text, muted, selectedSurface ui.Color
			view := tc.view(a)
			tt := ui.NewTester(func(c *ui.Context) {
				text, muted, selectedSurface = c.Theme().Text, c.Theme().TextMuted, c.Theme().SurfaceHover
				view(c)
			}, tc.width, 800)
			tt.SetLocale("en")
			tt.SetScale(2)
			tt.Advance(time.Second)
			r, ok := tt.Find("Receipts")
			if !ok || r.W == 0 || r.H == 0 {
				t.Fatalf("label text has no rendered bounds: %+v, %v; %q", r, ok, tt.Texts())
			}
			img := tt.Image()
			scale := float32(img.Bounds().Dx()) / float32(tc.width)
			pixel := func(x, y float32) color.RGBA {
				return img.RGBAAt(int(x*scale), int(y*scale))
			}
			want := color.RGBA{R: 0x4a, G: 0x86, B: 0xe8, A: 255}
			for _, point := range []struct {
				name string
				x    float32
				y    float32
			}{
				{name: "left padding", x: r.X - 3, y: r.Y + r.H/2},
				{name: "right padding", x: r.X + r.W + 3, y: r.Y + r.H/2},
				{name: "top padding", x: r.X + r.W/2, y: r.Y - 1},
			} {
				if got := pixel(point.x, point.y); got != want {
					t.Errorf("%s = %v, want Gmail background %v at text bounds %+v", point.name, got, want, r)
				}
			}
			whiteText := false
			for y := int(r.Y * scale); y < int((r.Y+r.H)*scale); y++ {
				for x := int(r.X * scale); x < int((r.X+r.W)*scale); x++ {
					p := img.RGBAAt(x, y)
					if p.R > 245 && p.G > 245 && p.B > 245 {
						whiteText = true
						break
					}
				}
			}
			if !whiteText {
				t.Error("label has no white text pixels inside its text bounds")
			}
			if tc.selected {
				sent, found := tt.Find("Sent")
				if !found || sent.W == 0 {
					t.Fatalf("neutral selected label is missing: %q", tt.Texts())
				}
				wantBackground := text.Alpha(0.08).Over(selectedSurface)
				got := pixel(sent.X-3, sent.Y+sent.H/2)
				if !sameVisualColor(ui.Color{R: got.R, G: got.G, B: got.B, A: got.A}, wantBackground) {
					t.Errorf("neutral selected background = %v, want %v", got, wantBackground)
				}
				mutedInk := false
				for y := int(sent.Y * scale); y < int((sent.Y+sent.H)*scale); y++ {
					for x := int(sent.X * scale); x < int((sent.X+sent.W)*scale); x++ {
						p := img.RGBAAt(x, y)
						if p.R == muted.R && p.G == muted.G && p.B == muted.B && p.A == muted.A {
							mutedInk = true
							break
						}
					}
				}
				if !mutedInk {
					t.Errorf("neutral selected label has no muted text pixels, want %v", muted)
				}
			}
		})
	}
}

func TestThreadLabelTextIsOpticallyBalanced(t *testing.T) {
	for _, name := range []string{"github", "Receipts", "Product", "Launch"} {
		t.Run(name, func(t *testing.T) {
			label := mail.Label{
				Name:  name,
				Color: mail.LabelColor{Background: "#16a766", Text: "#ffffff"},
			}
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Padding(20).Children(func() {
					threadLabel(c, label)
				})
			}, 200, 100)
			tt.SetScale(2)
			r, ok := tt.Find(name)
			if !ok || r.W == 0 || r.H == 0 {
				t.Fatalf("label text has no rendered bounds: %q", tt.Texts())
			}
			img := tt.Image()
			scale := float32(img.Bounds().Dx()) / 200
			background := color.RGBA{R: 0x16, G: 0xa7, B: 0x66, A: 255}
			chipTop, chipBottom := img.Bounds().Dy(), -1
			for y := range img.Bounds().Dy() {
				for x := range img.Bounds().Dx() {
					if img.RGBAAt(x, y) != background {
						continue
					}
					chipTop = min(chipTop, y)
					chipBottom = max(chipBottom, y)
				}
			}
			textTop, textBottom := -1, -1
			for y := int(r.Y * scale); y < int((r.Y+r.H)*scale); y++ {
				for x := int(r.X * scale); x < int((r.X+r.W)*scale); x++ {
					p := img.RGBAAt(x, y)
					if p.R < 245 || p.G < 245 || p.B < 245 {
						continue
					}
					if textTop == -1 {
						textTop = y
					}
					textBottom = y
				}
			}
			if chipTop > chipBottom || textTop == -1 {
				t.Fatalf("missing chip or text pixels: chip=%d..%d text=%d..%d", chipTop, chipBottom, textTop, textBottom)
			}
			top, bottom := textTop-chipTop, chipBottom-textBottom
			difference := top - bottom
			if difference < 0 {
				difference = -difference
			}
			if difference > 3 {
				t.Fatalf("label text is off-center: top=%d bottom=%d chip=%d..%d text=%d..%d", top, bottom, chipTop, chipBottom, textTop, textBottom)
			}
		})
	}
}

func newManyThreadLabelsApp() *App {
	service := newFakeService()
	for i := range 12 {
		id := fmt.Sprintf("Label_%02d", i)
		name := fmt.Sprintf("Long label %02d", i)
		service.labels = append(service.labels, mail.Label{ID: id, Name: name})
		service.threads[0].LabelIDs = append(service.threads[0].LabelIDs, id)
		service.thread.LabelIDs = append(service.thread.LabelIDs, id)
	}
	a := newTestApp(service)
	a.selectThread("thread-1")
	return a
}

func TestReaderLabelsWrap(t *testing.T) {
	a := newManyThreadLabelsApp()
	tt := ui.NewTester(a.threadContent, 600, 700)
	tt.SetLocale("en")
	tt.Advance(time.Second)
	first, firstOK := tt.Find("Long label 00")
	last, lastOK := tt.Find("Long label 11")
	if !firstOK || !lastOK || first.W == 0 || last.W == 0 {
		t.Fatalf("reader labels are missing or clipped: first=%+v last=%+v", first, last)
	}
	if last.Y <= first.Y {
		t.Fatalf("reader labels did not wrap: first=%+v last=%+v", first, last)
	}
}

func TestListLabelsStayCompactAndOmitCurrentLabel(t *testing.T) {
	a := newManyThreadLabelsApp()
	a.selectedLabel = "Label_00"
	tt := ui.NewTester(a.threadList, 380, 700)
	tt.SetLocale("en")
	currentOccurrences := 0
	for _, text := range tt.Texts() {
		if text == "Long label 00" {
			currentOccurrences++
		}
	}
	if currentOccurrences != 1 {
		t.Fatalf("current label appears %d times, want only the list title: %q", currentOccurrences, tt.Texts())
	}
	first, firstOK := tt.Find("Long label 01")
	last, lastOK := tt.Find("Long label 11")
	if !firstOK || !lastOK {
		t.Fatalf("list labels are missing: %q", tt.Texts())
	}
	if first.Y != last.Y {
		t.Fatalf("list labels escaped their single-line strip: first=%+v last=%+v", first, last)
	}
	if first.W == 0 || last.W != 0 || last.H != 0 {
		t.Fatalf("overflow label was not clipped: first=%+v last=%+v", first, last)
	}
}

func TestAccountInboxOmitsRepeatedInboxLabel(t *testing.T) {
	a := newColoredThreadLabelApp()
	a.selectedLabel = accountInboxPrefix + "b@example.com"
	tt := ui.NewTester(a.threadList, 380, 700)
	tt.SetLocale("en")
	inboxOccurrences := 0
	for _, text := range tt.Texts() {
		if text == "Inbox" {
			inboxOccurrences++
		}
	}
	if inboxOccurrences != 1 {
		t.Fatalf("Inbox appears %d times, want only the account list title: %q", inboxOccurrences, tt.Texts())
	}
}

func TestGlobalSearchKeepsLabels(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mailbox string
		label   string
	}{
		{name: "inbox", mailbox: mail.Inbox, label: "Inbox"},
		{name: "custom label", mailbox: "Label_9", label: "Receipts"},
		{name: "account inbox", mailbox: accountInboxPrefix + "b@example.com", label: "Inbox"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newColoredThreadLabelApp()
			a.selectedLabel = tc.mailbox
			a.query = "launch"
			if got := a.currentQuery().LabelID; got != "" {
				t.Fatalf("search query is scoped to label %q, want global", got)
			}
			tt := ui.NewTester(a.threadList, 380, 700)
			tt.SetLocale("en")
			if !tt.HasText("Search results") {
				t.Fatalf("search title is missing: %q", tt.Texts())
			}
			occurrences := 0
			for _, text := range tt.Texts() {
				if text == tc.label {
					occurrences++
				}
			}
			if occurrences != 1 {
				t.Fatalf("%s appears %d times in global results, want one row capsule: %q", tc.label, occurrences, tt.Texts())
			}
		})
	}
}

func TestUnifiedListKeepsInboxLabel(t *testing.T) {
	a := newColoredThreadLabelApp()
	a.selectedLabel = mail.Unified
	a.compositeThreads = true
	tt := ui.NewTester(a.threadList, 380, 700)
	tt.SetLocale("en")
	r, ok := tt.Find("Inbox")
	if !ok || r.W == 0 || r.H == 0 {
		t.Fatalf("All Inboxes hides Inbox membership: %q", tt.Texts())
	}
}

func TestListLabelsUseThreadOwner(t *testing.T) {
	a := New(newFakeService())
	a.labels = []mail.Label{{
		ID: "shared", Name: "Active label", Color: mail.LabelColor{Background: "#4a86e8", Text: "#ffffff"},
	}}
	a.registerAccountClient(newFakeService(), mail.Account{Email: "b@example.com"}, []mail.Label{
		{ID: "shared", Name: "B shared", Color: mail.LabelColor{Background: "#16a766", Text: "#ffffff"}},
	}, true)
	a.selectedLabel = mail.Unified
	a.compositeThreads = true
	a.threads = []mail.ThreadSummary{{
		ID: "thread", AccountEmail: "b@example.com", Subject: "Owner labels", LabelIDs: []string{"shared"},
	}}
	tt := ui.NewTester(a.threadList, 380, 700)
	tt.SetLocale("en")
	if !tt.HasText("B shared") || tt.HasText("Active label") {
		t.Fatalf("list used the wrong owner's labels: %q", tt.Texts())
	}
	r, ok := tt.Find("B shared")
	if !ok || r.W == 0 {
		t.Fatalf("owner label has no rendered bounds: %q", tt.Texts())
	}
	want := color.RGBA{R: 0x16, G: 0xa7, B: 0x66, A: 255}
	if got := tt.Image().RGBAAt(int(r.X-3), int(r.Y+r.H/2)); got != want {
		t.Fatalf("owner label background = %v, want B's color %v", got, want)
	}
}

func TestCaptureThreadLabelColors(t *testing.T) {
	if os.Getenv("MICA_CAPTURE") == "" {
		t.Skip("capture is opt-in")
	}
	colors := map[string]mail.LabelColor{
		"Label_1": {Background: "#4a86e8", Text: "#ffffff"},
		"Label_2": {Background: "#a479e2", Text: "#ffffff"},
		"Label_3": {Background: "#16a766", Text: "#ffffff"},
		"Label_4": {Background: "#ffad47", Text: "#000000"},
	}
	for _, tc := range []struct {
		name string
		dark bool
	}{
		{name: "readme-light"},
		{name: "readme-dark", dark: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := mail.NewDemo()
			for id, labelColor := range colors {
				if err := service.UpdateLabelColor(context.Background(), id, labelColor); err != nil {
					t.Fatal(err)
				}
			}
			a := newTestApp(service)
			a.selectThread("t-sync")
			tt := ui.NewTester(a.View, 1080, 675)
			tt.SetLocale("en")
			tt.SetDark(tc.dark)
			tt.SetScale(2)
			tt.Advance(time.Second)
			captureFrame(t, tt, tc.name)
		})
	}
}

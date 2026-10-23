package app

import (
	"testing"

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
	openMenu := func() {
		r, ok := tt.Find("Edit labels")
		if !ok {
			t.Fatal("label menu button not found")
		}
		x, y := r.X+r.W/2, r.Y+r.H/2
		tt.Press(x, y)
		if menu := tt.Menu(); len(menu) == 0 || menu[0] != "Receipts" {
			t.Fatalf("label menu = %q, want Receipts first", menu)
		}
		tt.Release(x, y)
	}

	openMenu()
	if err := tt.ChooseMenuItem("Receipts"); err != nil {
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
	if err := tt.ChooseMenuItem("Receipts"); err != nil {
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

func openLabelMenu(t *testing.T, tt *ui.Tester) {
	t.Helper()
	r, ok := tt.Find("Edit labels")
	if !ok {
		t.Fatal("label menu button not found")
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.Press(x, y)
	tt.Release(x, y)
}

func TestLabelMenuOnlyAssignsCustomLabels(t *testing.T) {
	service := newFakeService()
	service.labels = append(service.labels, mail.Label{ID: "Label_9", Name: "Receipts"})
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(a.View, 1280, 800)
	openLabelMenu(t, tt)
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

func TestSidebarDeletesCustomLabelAfterConfirmation(t *testing.T) {
	service := newFakeService()
	service.labels = append(service.labels, mail.Label{ID: "Label_9", Name: "Receipts"})
	service.threads[0].LabelIDs = append(service.threads[0].LabelIDs, "Label_9")
	service.thread.LabelIDs = append(service.thread.LabelIDs, "Label_9")
	a := newTestApp(service)
	a.selectThread("thread-1")
	tt := ui.NewTester(a.View, 1280, 800)
	if err := tt.Click("Delete label"); err != nil {
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

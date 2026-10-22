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

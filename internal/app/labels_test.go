package app

import "testing"

func TestHiddenSystemLabels(t *testing.T) {
	for _, id := range []string{"DRAFT", "CHAT", "IMPORTANT", "SPAM", "CATEGORY_SOCIAL", "CATEGORY_PROMOTIONS"} {
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

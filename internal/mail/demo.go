package mail

import (
	"context"
	"errors"
	"fmt"
	stdmail "net/mail"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const demoThreadLimit = 50

var demoLabelNames = map[string]string{
	Inbox:     "Inbox",
	Starred:   "Starred",
	Sent:      "Sent",
	Trash:     "Trash",
	Unread:    "Unread",
	"Label_1": "Product",
	"Label_2": "Design",
	"Label_3": "Engineering",
	"Label_4": "Launch",
}

type Demo struct {
	mu         sync.RWMutex
	account    Account
	threads    map[string]*Thread
	labelNames map[string]string
	labelOrder []string
	now        func() time.Time
	nextID     int
}

func NewDemo() *Demo {
	now := time.Now()
	d := &Demo{
		account:    Account{Name: "Alex Morgan", Email: "alex@mica.example", Demo: true},
		threads:    make(map[string]*Thread),
		labelNames: make(map[string]string, len(demoLabelNames)),
		labelOrder: []string{Inbox, Starred, Sent, Trash, Unread, "Label_1", "Label_2", "Label_3", "Label_4"},
		now:        time.Now,
	}
	for id, name := range demoLabelNames {
		d.labelNames[id] = name
	}
	message := func(id, from, to string, age time.Duration, text, html string, labels ...string) Message {
		return Message{
			ID:       id,
			From:     from,
			To:       to,
			Date:     now.Add(-age),
			Text:     text,
			HTML:     html,
			LabelIDs: labels,
		}
	}

	d.seed(Thread{
		ID:      "t-launch",
		Subject: "Mica 1.0 launch checklist",
		Messages: []Message{
			message("m-launch-1", "Maya Chen <maya@mica.example>", "Mica Team <team@mica.example>", 74*time.Hour,
				"Team,\n\nHere is the checklist for Thursday's Mica 1.0 launch. Please own your section and reply with any blockers by Tuesday noon.\n\n- Maya: launch notes and customer outreach\n- Leo: notarization and update feed\n- Priya: product page assets\n- Sam: release-candidate regression pass\n\nThanks,\nMaya",
				"<p>Team,</p><p>Here is the checklist for Thursday's <strong>Mica 1.0</strong> launch. Please own your section and reply with any blockers by Tuesday noon.</p><ul><li>Maya: launch notes</li><li>Leo: notarization and update feed</li><li>Priya: product page assets</li><li>Sam: regression pass</li></ul>",
				Inbox, "Label_1", "Label_4"),
			message("m-launch-2", "Leo Park <leo@mica.example>", "Maya Chen <maya@mica.example>", 50*time.Hour,
				"The signed build is ready. I attached the release checklist with the notarization ticket and update-feed checksum. The only remaining item is a clean install on macOS 15.\n\nLeo",
				"<p>The signed build is ready. I attached the release checklist with the notarization ticket and update-feed checksum.</p><p>The only remaining item is a clean install on macOS 15.</p>",
				Inbox, "Label_3", "Label_4"),
			message("m-launch-3", "Sam Rivera <sam@mica.example>", "Leo Park <leo@mica.example>", 7*time.Hour,
				"Clean install passed, including keychain access and the auto-update retry path. I also reran the Gmail fixture suite against the RC. No release blockers from QA.",
				"<p>Clean install passed, including keychain access and the auto-update retry path.</p><p><strong>No release blockers from QA.</strong></p>",
				Inbox, Unread, "Label_3", "Label_4"),
			message("m-launch-4", "Maya Chen <maya@mica.example>", "Mica Team <team@mica.example>", 95*time.Minute,
				"Excellent. I moved the launch notes to the final folder and tagged the RC as our release candidate. Priya will publish the artwork at 09:00 Thursday; Leo owns the update feed.\n\nLet's use this thread for any last-minute changes.",
				"<p>Excellent. I moved the launch notes to the final folder and tagged the RC as our release candidate.</p><p>Priya will publish the artwork at <strong>09:00 Thursday</strong>; Leo owns the update feed.</p>",
				Inbox, Unread, Starred, "Label_1", "Label_4"),
		},
	})
	d.threads["t-launch"].Messages[1].Attachments = []Attachment{
		{ID: "a-release-checklist", Name: "mica-1.0-release-checklist.pdf", MIMEType: "application/pdf", Size: 184320},
		{ID: "a-update-checksum", Name: "SHA256SUMS.txt", MIMEType: "text/plain", Size: 128},
	}

	d.seed(Thread{
		ID:      "t-sync",
		Subject: "Offline sync: conflict resolution",
		Messages: []Message{
			message("m-sync-1", "Leo Park <leo@mica.example>", "Alex Morgan <alex@mica.example>", 31*time.Hour,
				"I found a race when two queued label changes resolve after reconnect. The short version: we should order operations by the message history ID, not arrival time. I wrote a failing fixture for the star-then-trash case.",
				"<p>I found a race when two queued label changes resolve after reconnect.</p><p>We should order operations by the <code>historyId</code>, not arrival time. I wrote a failing fixture for the star-then-trash case.</p>",
				Inbox, Starred, "Label_3"),
			message("m-sync-2", "Alex Morgan <alex@mica.example>", "Leo Park <leo@mica.example>", 23*time.Hour,
				"Agreed on history ID ordering. Can we keep the queue retry bounded and surface a conflict banner instead of silently dropping the second operation?",
				"", Sent, "Label_3"),
			message("m-sync-3", "Leo Park <leo@mica.example>", "Alex Morgan <alex@mica.example>", 3*time.Hour,
				"Yes. The queue now stops after three attempts and preserves the local operation for manual retry. The fixture passes for star, trash, and mark-read. I want one more pair-review before merging.",
				"<p>Yes. The queue now stops after three attempts and preserves the local operation for manual retry.</p><p>The fixture passes for star, trash, and mark-read. I want one more pair-review before merging.</p>",
				Inbox, Unread, "Label_3"),
		},
	})

	d.seed(Thread{
		ID:      "t-onboarding",
		Subject: "New onboarding screens",
		Messages: []Message{
			message("m-onboarding-1", "Priya Shah <priya@mica.example>", "Mica Team <team@mica.example>", 54*time.Hour,
				"The onboarding v3 screens are ready for review. The flow now explains loopback OAuth before opening the browser, and the final screen has a single 'Open Mica' action. Empty, loading, and offline states are included.",
				"<p>The onboarding v3 screens are ready for review.</p><ul><li>Loopback OAuth explanation</li><li>Single final call to action</li><li>Empty, loading, and offline states</li></ul>",
				Inbox, Starred, "Label_2"),
			message("m-onboarding-2", "Maya Chen <maya@mica.example>", "Priya Shah <priya@mica.example>", 18*time.Hour,
				"The hierarchy looks good. Please keep 'Continue in browser' as the primary button and make the privacy copy plain: tokens stay in the macOS Keychain and Mica never sees the Google password.",
				"<p>The hierarchy looks good.</p><p>Please keep <strong>Continue in browser</strong> as the primary button and make the privacy copy plain: tokens stay in the macOS Keychain and Mica never sees the Google password.</p>",
				Inbox, "Label_1", "Label_2"),
		},
	})

	d.seed(Thread{
		ID:      "t-weekly",
		Subject: "Weekly product notes — October 21",
		Messages: []Message{
			message("m-weekly-1", "Maya Chen <maya@mica.example>", "Mica Team <team@mica.example>", 28*time.Hour,
				"This week's numbers: activation is 68%, search is the most-used action after archive, and seven testers asked for keyboard-only compose. Next week we will focus on launch stability rather than new surface area.",
				"<p>This week's numbers:</p><ul><li>Activation: <strong>68%</strong></li><li>Search is the most-used action after archive</li><li>Seven testers asked for keyboard-only compose</li></ul><p>Next week: launch stability.</p>",
				Inbox, "Label_1"),
			message("m-weekly-2", "Noor Khan <noor@mica.example>", "Maya Chen <maya@mica.example>", 20*time.Hour,
				"I added the support themes. Most setup questions are about choosing Desktop app credentials in Google Cloud; almost nobody asks about IMAP once the OAuth flow is complete.",
				"", Inbox, "Label_1"),
		},
	})

	d.seed(Thread{
		ID:      "t-shortcuts",
		Subject: "TestFlight feedback: keyboard shortcuts",
		Messages: []Message{
			message("m-shortcuts-1", "Sam Rivera <sam@mica.example>", "Mica Team <team@mica.example>", 12*time.Hour,
				"Three beta reports mention that Cmd+Enter is expected to send and Cmd+Shift+U to toggle read. The current shortcut sheet omits both. I can add these to the release-candidate checklist if we agree on the bindings.",
				"<p>Three beta reports mention <kbd>Cmd+Enter</kbd> to send and <kbd>Cmd+Shift+U</kbd> to toggle read.</p><p>The current shortcut sheet omits both.</p>",
				Inbox, Unread, "Label_1"),
			message("m-shortcuts-2", "Priya Shah <priya@mica.example>", "Sam Rivera <sam@mica.example>", 8*time.Hour,
				"Those bindings match the rest of the app. Please also verify that shortcuts do not fire while the search field or a dialog has focus.",
				"", Inbox, Unread, "Label_2"),
		},
	})

	d.seed(Thread{
		ID:      "t-roadmap",
		Subject: "Q4 roadmap review",
		Messages: []Message{
			message("m-roadmap-1", "Maya Chen <maya@mica.example>", "Mica Team <team@mica.example>", 9*24*time.Hour,
				"For Q4, the proposed sequence is reliable sync, faster local search, then rules. Multiple-account polish stays in scope, but a third-party plugin API is explicitly deferred.",
				"<p>For Q4: <strong>reliable sync → faster local search → rules</strong>.</p><p>Multiple-account polish stays in scope; a third-party plugin API is deferred.</p>",
				Starred, "Label_1"),
			message("m-roadmap-2", "Leo Park <leo@mica.example>", "Maya Chen <maya@mica.example>", 8*24*time.Hour,
				"The sequence works for engineering. Search indexing can reuse the local MIME cache, so we should avoid promising a separate backend until we measure the first version.",
				"", Starred, "Label_3"),
			message("m-roadmap-3", "Noor Khan <noor@mica.example>", "Maya Chen <maya@mica.example>", 7*24*time.Hour,
				"Support agrees. Rules and search are the two requests we can explain without changing our security story.",
				"", "Label_1"),
		},
	})

	d.seed(Thread{
		ID:      "t-newsletter",
		Subject: "Mica community newsletter — draft",
		Messages: []Message{
			message("m-newsletter-1", "Alex Morgan <alex@mica.example>", "Noor Khan <noor@mica.example>", 6*time.Hour,
				"Noor,\n\nHere is the launch-week newsletter draft. The lead story is offline triage, followed by a short section on keyboard workflows. Can you check the customer quote and add the registration link?\n\nAlex",
				"<p>Noor,</p><p>Here is the launch-week newsletter draft. The lead story is <strong>offline triage</strong>, followed by keyboard workflows.</p><p>Can you check the customer quote and add the registration link?</p><p>Alex</p>",
				Sent, "Label_1"),
		},
	})

	d.seed(Thread{
		ID:      "t-signing",
		Subject: "Old build signing notes",
		Messages: []Message{
			message("m-signing-1", "Leo Park <leo@mica.example>", "Mica Team <team@mica.example>", 30*24*time.Hour,
				"Superseded by the release runbook. Keeping this thread in trash for reference until the old certificate expires.",
				"", Trash, "Label_3"),
		},
	})

	return d
}

func (d *Demo) seed(thread Thread) {
	for i := range thread.Messages {
		m := &thread.Messages[i]
		m.ThreadID = thread.ID
		if m.Subject == "" {
			m.Subject = thread.Subject
		}
		if m.MessageID == "" {
			m.MessageID = "<" + m.ID + "@mica.example>"
		}
		if i > 0 {
			previous := thread.Messages[i-1]
			m.References = appendReference(previous.References, previous.MessageID)
		}
	}
	copy := cloneThread(thread)
	d.threads[thread.ID] = &copy
}

func (d *Demo) Account(context.Context) (Account, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.account, nil
}

func (d *Demo) Labels(context.Context) ([]Label, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	labels := make([]Label, 0, len(d.labelOrder))
	for _, id := range d.labelOrder {
		label := Label{ID: id, Name: d.labelNames[id]}
		for _, thread := range d.threads {
			for _, message := range thread.Messages {
				if !hasString(message.LabelIDs, id) {
					continue
				}
				label.Messages++
				if hasString(message.LabelIDs, Unread) {
					label.Unread++
				}
			}
		}
		labels = append(labels, label)
	}
	return labels, nil
}

func (d *Demo) CreateLabel(_ context.Context, name string) (Label, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return Label{}, errors.New("mail: empty label name")
	}
	for _, existing := range d.labelNames {
		if strings.EqualFold(existing, name) {
			return Label{}, errors.New("mail: label already exists")
		}
	}
	d.nextID++
	id := fmt.Sprintf("demo-label-%d", d.nextID)
	d.labelNames[id] = name
	d.labelOrder = append(d.labelOrder, id)
	return Label{ID: id, Name: name}, nil
}

func (d *Demo) DeleteLabel(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.labelNames[id]; !ok {
		return errors.New("mail: label not found")
	}
	switch id {
	case Inbox, Starred, Sent, Trash, Unread:
		return errors.New("mail: cannot delete system label")
	}
	delete(d.labelNames, id)
	for i, candidate := range d.labelOrder {
		if candidate == id {
			d.labelOrder = append(d.labelOrder[:i], d.labelOrder[i+1:]...)
			break
		}
	}
	for _, thread := range d.threads {
		for i := range thread.Messages {
			thread.Messages[i].LabelIDs = removeString(thread.Messages[i].LabelIDs, id)
		}
	}
	return nil
}

func (d *Demo) ListThreads(_ context.Context, query Query) ([]ThreadSummary, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	limit := query.Limit
	if limit <= 0 {
		limit = demoThreadLimit
	}
	includeTrash := query.LabelID == Trash || hasSearchOption(query.Search, "in:trash") || hasSearchOption(query.Search, "in:anywhere")
	summaries := make([]ThreadSummary, 0)
	for _, thread := range d.threads {
		labels := combinedLabels(thread.Messages)
		if query.LabelID != "" && !hasString(labels, query.LabelID) {
			continue
		}
		if query.LabelID == "" && !includeTrash && hasString(labels, Trash) {
			continue
		}
		if !matchesDemoSearch(thread, query.Search, d.labelNames) {
			continue
		}
		summaries = append(summaries, demoSummary(thread))
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Updated.Equal(summaries[j].Updated) {
			return summaries[i].ID < summaries[j].ID
		}
		return summaries[i].Updated.After(summaries[j].Updated)
	})
	if len(summaries) > limit {
		summaries = summaries[:limit]
	}
	return summaries, nil
}

func (d *Demo) Thread(_ context.Context, id string) (Thread, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	thread, ok := d.threads[id]
	if !ok {
		return Thread{}, errors.New("mail: thread not found")
	}
	return cloneThread(*thread), nil
}

func (d *Demo) ModifyThread(_ context.Context, id string, add, remove []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	thread, ok := d.threads[id]
	if !ok {
		return errors.New("mail: thread not found")
	}
	for i := range thread.Messages {
		labels := &thread.Messages[i].LabelIDs
		for _, label := range add {
			if label != "" && !hasString(*labels, label) {
				*labels = append(*labels, label)
			}
		}
		for _, label := range remove {
			*labels = removeString(*labels, label)
		}
	}
	return nil
}

func (d *Demo) TrashThread(ctx context.Context, id string) error {
	return d.ModifyThread(ctx, id, []string{Trash}, []string{Inbox})
}

func (d *Demo) Send(_ context.Context, compose Compose) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if strings.TrimSpace(compose.To) == "" && strings.TrimSpace(compose.Cc) == "" && strings.TrimSpace(compose.Bcc) == "" {
		return errors.New("mail: at least one recipient is required")
	}
	d.nextID++
	id := fmt.Sprintf("demo-message-%d", d.nextID)
	message := Message{
		ID:         id,
		ThreadID:   compose.ThreadID,
		From:       (&stdmail.Address{Name: d.account.Name, Address: d.account.Email}).String(),
		To:         strings.TrimSpace(compose.To),
		Cc:         strings.TrimSpace(compose.Cc),
		Subject:    strings.TrimSpace(compose.Subject),
		MessageID:  fmt.Sprintf("<%s@mica.example>", id),
		References: strings.TrimSpace(compose.References),
		Date:       d.now(),
		Text:       compose.Text,
		HTML:       compose.HTML,
		LabelIDs:   []string{Sent},
	}

	thread, ok := d.threads[compose.ThreadID]
	if compose.ThreadID == "" {
		d.nextID++
		threadID := fmt.Sprintf("demo-thread-%d", d.nextID)
		message.ThreadID = threadID
		if message.Subject == "" {
			message.Subject = "(no subject)"
		}
		thread = &Thread{ID: threadID, Subject: message.Subject}
		d.threads[threadID] = thread
	} else if !ok {
		return errors.New("mail: thread not found")
	} else {
		if message.Subject == "" {
			message.Subject = thread.Subject
		}
		if len(thread.Messages) > 0 && message.References == "" {
			last := thread.Messages[len(thread.Messages)-1]
			message.References = appendReference(last.References, last.MessageID)
		}
	}
	if compose.InReplyTo != "" && message.References == "" {
		message.References = compose.InReplyTo
	}
	thread.Messages = append(thread.Messages, message)
	if thread.Subject == "" {
		thread.Subject = message.Subject
	}
	return nil
}

func demoSummary(thread *Thread) ThreadSummary {
	labels := combinedLabels(thread.Messages)
	summary := ThreadSummary{
		ID:           thread.ID,
		Subject:      thread.Subject,
		MessageCount: len(thread.Messages),
		LabelIDs:     labels,
		Unread:       hasString(labels, Unread),
		Starred:      hasString(labels, Starred),
	}
	senders := make([]string, 0, len(thread.Messages))
	for _, message := range thread.Messages {
		if message.Date.After(summary.Updated) {
			summary.Updated = message.Date
			summary.Snippet = snippet(message.Text)
		}
		name := displayAddress(message.From)
		if !hasString(senders, name) {
			senders = append(senders, name)
		}
	}
	summary.Senders = strings.Join(senders, ", ")
	return summary
}

func cloneThread(thread Thread) Thread {
	clone := thread
	clone.LabelIDs = append([]string(nil), thread.LabelIDs...)
	clone.Messages = make([]Message, len(thread.Messages))
	for i, message := range thread.Messages {
		clone.Messages[i] = message
		clone.Messages[i].LabelIDs = append([]string(nil), message.LabelIDs...)
		clone.Messages[i].Attachments = append([]Attachment(nil), message.Attachments...)
	}
	clone.LabelIDs = combinedLabels(clone.Messages)
	return clone
}

func combinedLabels(messages []Message) []string {
	var labels []string
	for _, message := range messages {
		for _, label := range message.LabelIDs {
			if !hasString(labels, label) {
				labels = append(labels, label)
			}
		}
	}
	sort.Strings(labels)
	return labels
}

func matchesDemoSearch(thread *Thread, search string, names ...map[string]string) bool {
	labelNames := demoLabelNames
	if len(names) > 0 {
		labelNames = names[0]
	}
	search = strings.TrimSpace(search)
	if search == "" {
		return true
	}
	var text, from, recipients strings.Builder
	text.WriteString(thread.Subject)
	text.WriteByte('\n')
	hasAttachments := false
	for _, message := range thread.Messages {
		for _, value := range []string{message.Subject, message.Text, message.HTML} {
			text.WriteString(value)
			text.WriteByte('\n')
		}
		from.WriteString(message.From)
		from.WriteByte('\n')
		recipients.WriteString(message.To)
		recipients.WriteByte(' ')
		recipients.WriteString(message.Cc)
		recipients.WriteByte('\n')
		hasAttachments = hasAttachments || len(message.Attachments) > 0
	}
	for _, label := range combinedLabels(thread.Messages) {
		text.WriteString(label)
		text.WriteByte(' ')
		text.WriteString(labelNames[label])
		text.WriteByte('\n')
	}
	body := strings.ToLower(text.String())
	fromValue := strings.ToLower(from.String())
	toValue := strings.ToLower(recipients.String())
	subject := strings.ToLower(thread.Subject)
	labels := combinedLabels(thread.Messages)

	for _, term := range splitSearch(search) {
		lower := strings.ToLower(term)
		key, value, qualified := strings.Cut(lower, ":")
		if qualified {
			switch key {
			case "is":
				switch value {
				case "unread":
					if !hasString(labels, Unread) {
						return false
					}
					continue
				case "read":
					if hasString(labels, Unread) {
						return false
					}
					continue
				case "starred":
					if !hasString(labels, Starred) {
						return false
					}
					continue
				}
			case "in":
				label := map[string]string{"inbox": Inbox, "sent": Sent, "trash": Trash, "starred": Starred, "snoozed": Snoozed}[value]
				if label != "" {
					if !hasString(labels, label) {
						return false
					}
					continue
				}
				if value == "anywhere" {
					continue
				}
			case "category":
				label := map[string]string{
					"primary": "CATEGORY_PERSONAL", "social": CategorySocial, "promotions": CategoryPromotions,
					"updates": CategoryUpdates, "forums": CategoryForums, "purchases": CategoryPurchases, "reservations": CategoryReservations,
				}[value]
				if label != "" {
					if !hasString(labels, label) {
						return false
					}
					continue
				}
			case "label":
				matched := false
				for _, label := range labels {
					if strings.EqualFold(label, value) || strings.Contains(strings.ToLower(labelNames[label]), value) {
						matched = true
						break
					}
				}
				if !matched {
					return false
				}
				continue
			case "from":
				if !strings.Contains(fromValue, value) {
					return false
				}
				continue
			case "to":
				if !strings.Contains(toValue, value) {
					return false
				}
				continue
			case "subject":
				if !strings.Contains(subject, value) {
					return false
				}
				continue
			case "has":
				if value == "attachment" {
					if !hasAttachments {
						return false
					}
					continue
				}
			}
		}
		if !strings.Contains(body, lower) {
			return false
		}
	}
	return true
}

func splitSearch(search string) []string {
	var terms []string
	var term strings.Builder
	quoted := false
	flush := func() {
		if term.Len() > 0 {
			terms = append(terms, term.String())
			term.Reset()
		}
	}
	for _, r := range search {
		switch {
		case r == '"':
			quoted = !quoted
		case unicode.IsSpace(r) && !quoted:
			flush()
		default:
			term.WriteRune(r)
		}
	}
	flush()
	return terms
}

func hasSearchOption(search, option string) bool {
	for _, term := range splitSearch(search) {
		if strings.EqualFold(term, option) {
			return true
		}
	}
	return false
}

func snippet(text string) string {
	fields := strings.Fields(text)
	text = strings.Join(fields, " ")
	const max = 160
	runes := []rune(text)
	if len(runes) > max {
		return string(runes[:max-1]) + "…"
	}
	return text
}

func displayAddress(value string) string {
	address, err := stdmail.ParseAddress(value)
	if err != nil {
		return value
	}
	if address.Name != "" {
		return address.Name
	}
	return address.Address
}

func appendReference(references, messageID string) string {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return strings.TrimSpace(references)
	}
	if strings.Contains(references, messageID) {
		return strings.TrimSpace(references)
	}
	return strings.TrimSpace(references + " " + messageID)
}

func hasString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func removeString(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

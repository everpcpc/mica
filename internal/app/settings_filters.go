package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"mica/internal/gmail"
	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
)

var (
	errFilterManagerUnsupported = errors.New("mail: filter management is unsupported")
	errCreatedFilterID          = errors.New("mail: created filter has no ID")
)

type filterDialogState struct {
	open          bool
	owner         string
	filters       []mail.Filter
	loaded        bool
	loading       bool
	request       uint64
	busy          string
	err           string
	reauthorize   bool
	creating      bool
	deleteID      string
	from          string
	to            string
	subject       string
	query         string
	hasAttachment bool
	labelID       string
	optionsScroll ui.ScrollState
	starred       bool
	skipInbox     bool
	markRead      bool
}

type filterLabelOption struct {
	id   string
	name string
}

func (o filterLabelOption) String() string { return o.name }

func (a *App) openFilters() {
	if a.account.Demo || a.accountTransition != nil {
		return
	}
	owner := a.filterOwner()
	if owner == "" {
		return
	}
	s := &filterDialogState{open: true, owner: owner}
	a.filterDialog = s
	a.loadFilters(s)
}

func (a *App) filterOwner() string {
	if a.account.Demo {
		return ""
	}
	owner := normalizedEmail(a.storeAccount)
	if owner == "" {
		owner = normalizedEmail(a.account.Email)
	}
	return owner
}

func (a *App) filterManager() (mail.FilterManager, error) {
	manager, ok := a.service.(mail.FilterManager)
	if !ok {
		return nil, errFilterManagerUnsupported
	}
	return manager, nil
}

func (a *App) filterDialogCurrent(s *filterDialogState, owner string, version int) bool {
	return a.filterDialog == s && s.open && !a.account.Demo && a.accountTransition == nil && a.serviceVersion == version && a.filterOwner() == owner
}

func (a *App) filterControlsDisabled(s *filterDialogState) bool {
	return a.account.Demo || s.loading || s.busy != "" || a.accountTransition != nil || a.settings != nil && a.settings.busy
}

func (a *App) loadFilters(s *filterDialogState) {
	if a.filterDialog != s || !s.open || a.filterControlsDisabled(s) {
		return
	}
	s.loading = true
	s.err = ""
	s.reauthorize = false
	manager, err := a.filterManager()
	if err != nil {
		s.loading = false
		s.err = locales.FilterListFailed(err.Error())
		return
	}
	owner, version := s.owner, a.serviceVersion
	s.request++
	request := s.request
	a.schedule(func() {
		filters, err := manager.Filters(context.Background())
		a.publish(func() {
			if !a.filterDialogCurrent(s, owner, version) || s.request != request {
				return
			}
			s.loading = false
			s.loaded = true
			if err != nil {
				a.setFilterError(s, "list", err)
				return
			}
			s.filters = filters
		})
	})
}

func (a *App) createFilter() {
	s := a.filterDialog
	if s == nil || !s.creating || a.filterControlsDisabled(s) {
		return
	}
	criteria := mail.FilterCriteria{
		From: strings.TrimSpace(s.from), To: strings.TrimSpace(s.to), Subject: strings.TrimSpace(s.subject),
		Query: s.query, HasAttachment: s.hasAttachment,
	}
	if criteria.From == "" && criteria.To == "" && criteria.Subject == "" && strings.TrimSpace(criteria.Query) == "" && !criteria.HasAttachment {
		s.err = locales.FilterCriterionRequired()
		s.reauthorize = false
		return
	}
	action := mail.FilterAction{}
	if s.labelID != "" {
		found := false
		for _, label := range assignableLabels(a.labels) {
			if label.ID == s.labelID {
				found = true
				break
			}
		}
		if !found {
			s.err = locales.FilterLabelUnavailable()
			s.reauthorize = false
			return
		}
		action.AddLabelIDs = append(action.AddLabelIDs, s.labelID)
	}
	if s.starred {
		action.AddLabelIDs = append(action.AddLabelIDs, mail.Starred)
	}
	if s.skipInbox {
		action.RemoveLabelIDs = append(action.RemoveLabelIDs, mail.Inbox)
	}
	if s.markRead {
		action.RemoveLabelIDs = append(action.RemoveLabelIDs, mail.Unread)
	}
	if len(action.AddLabelIDs) == 0 && len(action.RemoveLabelIDs) == 0 {
		s.err = locales.FilterActionRequired()
		s.reauthorize = false
		return
	}
	manager, err := a.filterManager()
	if err != nil {
		s.err = locales.FilterCreateFailed(err.Error())
		return
	}
	owner, version := s.owner, a.serviceVersion
	s.busy = "create"
	s.err = ""
	s.reauthorize = false
	var created mail.Filter
	a.enqueueMutation(mutationJob{
		service: a.service,
		owner:   owner,
		runFunc: func() error {
			var err error
			created, err = manager.CreateFilter(context.Background(), mail.Filter{Criteria: criteria, Action: action})
			if err == nil && created.ID == "" {
				err = errCreatedFilterID
			}
			return err
		},
		done: func(err error) {
			if !a.filterDialogCurrent(s, owner, version) || s.busy != "create" {
				return
			}
			s.busy = ""
			if err != nil {
				a.setFilterError(s, "create", err)
				return
			}
			s.filters = append(s.filters, created)
			s.loaded = true
			s.creating = false
			s.from, s.to, s.subject, s.query = "", "", "", ""
			s.hasAttachment = false
			s.labelID = ""
			s.starred, s.skipInbox, s.markRead = false, false, false
		},
	})
}

func (a *App) requestFilterDelete(id string) {
	s := a.filterDialog
	if s == nil || id == "" || a.filterControlsDisabled(s) {
		return
	}
	s.deleteID = id
	s.err = ""
	s.reauthorize = false
}

func (a *App) deleteFilter(id string) {
	s := a.filterDialog
	if s == nil || id == "" || s.deleteID != id || a.filterControlsDisabled(s) {
		return
	}
	manager, err := a.filterManager()
	if err != nil {
		s.err = locales.FilterDeleteFailed(err.Error())
		return
	}
	owner, version := s.owner, a.serviceVersion
	busy := "delete:" + id
	s.busy = busy
	s.err = ""
	s.reauthorize = false
	a.enqueueMutation(mutationJob{
		service: a.service,
		owner:   owner,
		runFunc: func() error {
			return manager.DeleteFilter(context.Background(), id)
		},
		done: func(err error) {
			if !a.filterDialogCurrent(s, owner, version) || s.busy != busy {
				return
			}
			s.busy = ""
			if err != nil {
				a.setFilterError(s, "delete", err)
				return
			}
			filters := make([]mail.Filter, 0, len(s.filters))
			for _, filter := range s.filters {
				if filter.ID != id {
					filters = append(filters, filter)
				}
			}
			s.filters = filters
			s.deleteID = ""
		},
	})
}

func (a *App) setFilterError(s *filterDialogState, operation string, err error) {
	switch operation {
	case "create":
		s.err = locales.FilterCreateFailed(err.Error())
	case "delete":
		s.err = locales.FilterDeleteFailed(err.Error())
	default:
		s.err = locales.FilterListFailed(err.Error())
	}
	var apiErr *gmail.APIError
	s.reauthorize = errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden)
}

func (a *App) closeFilterDialog(s *filterDialogState) {
	if a.filterDialog != s {
		return
	}
	s.open = false
	s.request++
	a.filterDialog = nil
}

func (a *App) filterDialogView(c *ui.Context) {
	s := a.filterDialog
	t := c.Theme()
	dismissed := ui.Modal(c, &s.open, func() {
		// The scroll region must leave room for the footer at the minimum window height.
		ui.Column(c).Width(620).Gap(12).MaxHeight(520).Children(func() {
			ui.Text(c, locales.FilterDialogTitle()).FontSize(19).Bold()
			ui.Text(c, locales.FilterDialogHint()).FontSize(12).LineHeight(1.4).TextColor(t.TextMuted)
			disabled := a.filterControlsDisabled(s)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if !s.creating {
					primaryAction(c, locales.NewFilter()).Disabled(disabled).OnClick(func() {
						s.creating = true
						s.err = ""
						s.reauthorize = false
					})
				}
				ui.Button(c, locales.FilterRefresh()).Disabled(disabled).OnClick(func() { a.loadFilters(s) })
				if s.loading || s.busy != "" {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, locales.Processing()).FontSize(12).TextColor(t.TextMuted)
				}
			})
			ui.Scroll(c.Key("filter-content")).Height(390).Shrink(1).Children(func() {
				ui.Column(c).Gap(10).Padding(2).Children(func() {
					if s.loading && !s.loaded {
						ui.Text(c, locales.LoadingFilters()).TextColor(t.TextMuted)
					}
					if s.creating {
						a.filterCreateForm(c, s, disabled)
					}
					if !s.creating && s.loaded && len(s.filters) == 0 && s.err == "" {
						ui.Column(c).Center().Padding(24).Gap(6).Children(func() {
							ui.Text(c, locales.EmptyFilters()).Bold()
							ui.Text(c, locales.EmptyFiltersHint()).FontSize(12).TextColor(t.TextMuted).TextAlign(ui.Center)
						})
					}
					for _, filter := range s.filters {
						a.filterRow(c, s, filter, disabled)
					}
				})
			})
			if s.err != "" {
				ui.Text(c, s.err).FontSize(12).LineHeight(1.4).TextColor(t.Danger)
			}
			if s.reauthorize {
				ui.Text(c, locales.FilterReauthorizeHint()).FontSize(12).LineHeight(1.4).TextColor(t.TextMuted)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if s.creating {
					ui.Button(c, locales.Cancel()).Disabled(disabled).OnClick(func() { s.creating = false })
					primaryAction(c, locales.CreateFilter()).Disabled(disabled).OnClick(a.createFilter)
				}
				if s.reauthorize {
					ui.Button(c, locales.ReauthorizeAccount()).Disabled(disabled).OnClick(a.reauthorizeGmail)
				}
				ui.Button(c, locales.Close()).Disabled(s.busy != "").OnClick(func() { a.closeFilterDialog(s) })
			})
		})
	}).Dismissed()
	if dismissed && s.busy != "" {
		s.open = true
	}
	if !s.open && s.busy == "" && a.filterDialog == s {
		a.closeFilterDialog(s)
	}
}

func (a *App) filterRow(c *ui.Context, s *filterDialogState, filter mail.Filter, disabled bool) {
	t := c.Theme()
	sectionPanel(c.Key("filter-" + filter.ID)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.FilterCriteria()).Bold().Grow(1)
			ui.Button(c, locales.DeleteFilter()).Disabled(disabled).OnClick(func() { a.requestFilterDelete(filter.ID) })
		})
		for _, line := range a.filterCriteriaLines(filter.Criteria) {
			ui.Text(c, line).FontSize(12).LineHeight(1.35)
		}
		ui.Text(c, locales.FilterActions()).Bold().Padding(6, 0, 0, 0)
		for _, line := range a.filterActionLines(filter.Action) {
			ui.Text(c, line).FontSize(12).LineHeight(1.35)
		}
		if s.deleteID == filter.ID {
			ui.Text(c, locales.DeleteFilterConfirmation()).FontSize(12).LineHeight(1.4).TextColor(t.Danger)
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				ui.Button(c, locales.Cancel()).Disabled(disabled).OnClick(func() { s.deleteID = "" })
				ui.Button(c, locales.DeleteFilterConfirm()).TextColor(t.Danger).Disabled(disabled).OnClick(func() { a.deleteFilter(filter.ID) })
			})
		}
	})
}

func (a *App) filterCreateForm(c *ui.Context, s *filterDialogState, disabled bool) {
	t := c.Theme()
	sectionPanel(c.Key("filter-create")).Children(func() {
		ui.Text(c, locales.NewFilter()).FontSize(16).Bold()
		ui.Text(c, locales.FilterCriteria()).FontSize(12).Bold().TextColor(t.TextMuted)
		filterField(c, "from", locales.FilterFrom(), &s.from, locales.FilterFromPlaceholder(), disabled)
		filterField(c, "to", locales.To(), &s.to, locales.FilterToPlaceholder(), disabled)
		filterField(c, "subject", locales.Subject(), &s.subject, locales.SubjectPlaceholder(), disabled)
		filterField(c, "query", locales.FilterQuery(), &s.query, locales.FilterQueryPlaceholder(), disabled)
		ui.Checkbox(c.Key("filter-has-attachment"), &s.hasAttachment, locales.FilterHasAttachment()).Disabled(disabled)
		ui.Text(c, locales.FilterActions()).FontSize(12).Bold().TextColor(t.TextMuted).Padding(6, 0, 0, 0)
		labels := assignableLabels(a.labels)
		selection := filterLabelOption{name: locales.FilterNoLabel()}
		for _, label := range labels {
			if label.ID == s.labelID {
				selection = filterLabelOption{id: label.ID, name: label.Name}
				break
			}
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.FilterAddLabel()).Width(100).Shrink(0)
			selector := ui.SelectBase(c.Key("filter-label"), &selection)
			trigger := selector.Trigger
			face := t.Surface
			if !disabled {
				switch {
				case trigger.Pressed():
					face = t.SurfacePressed
				case trigger.Hovered():
					face = t.SurfaceHover
				}
			}
			trigger.Label(locales.FilterAddLabel()+": "+selection.name).Padding(t.Space(1.5), t.Space(3.5)).Gap(t.Space(1.5)).Radius(t.Radius).Background(face).TextColor(t.Text).MinWidth(t.Space(35)).Grow(1).Disabled(disabled).Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, selection.name).SingleLine()
				ui.Icon(c, iconChevronDown).Size(10, 10).TextColor(t.TextMuted)
			})
			if t.Border.A > 0 {
				trigger.Border(1, t.Border)
			}
			selector.Popup(func(panel ui.Element) {
				panel.Margin(t.Space(1), 0, 0, 0).Padding(4).Radius(t.Radius+2).Background(t.Background).MaxHeight(280).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18))
				if t.Border.A > 0 {
					panel.Border(1, t.Border)
				}
				ui.Scroll(c.Key("filter-label-options")).MaxHeight(280).TrackScroll(&s.optionsScroll).Shrink(1).Children(func() {
					option := func(value filterLabelOption) {
						item := selector.Item(value).Padding(t.Space(1.5), t.Space(2.5)).Radius(t.Radius)
						if item.Highlighted() {
							item.Background(t.Accent).TextColor(t.AccentText)
						}
						item.Children(func() { ui.Text(c, value.name).SingleLine() })
					}
					option(filterLabelOption{name: locales.FilterNoLabel()})
					for _, label := range labels {
						option(filterLabelOption{id: label.ID, name: label.Name})
					}
				})
			})
			if selector.Trigger.Changed() {
				s.labelID = selection.id
			}
		})
		ui.Row(c).Gap(14).Children(func() {
			ui.Checkbox(c.Key("filter-starred"), &s.starred, locales.FilterStarMessage()).Disabled(disabled)
			ui.Checkbox(c.Key("filter-skip-inbox"), &s.skipInbox, locales.FilterSkipInbox()).Disabled(disabled)
			ui.Checkbox(c.Key("filter-mark-read"), &s.markRead, locales.FilterMarkRead()).Disabled(disabled)
		})
	})
}

func filterField(c *ui.Context, key, label string, value *string, placeholder string, disabled bool) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Width(100).Shrink(0)
		ui.TextInput(c.Key("filter-"+key), value).Label(placeholder).Placeholder(placeholder).Grow(1).Disabled(disabled)
	})
}

func (a *App) filterCriteriaLines(criteria mail.FilterCriteria) []string {
	var lines []string
	if criteria.From != "" {
		lines = append(lines, locales.FilterFromValue(criteria.From))
	}
	if criteria.To != "" {
		lines = append(lines, locales.FilterToValue(criteria.To))
	}
	if criteria.Subject != "" {
		lines = append(lines, locales.FilterSubjectValue(criteria.Subject))
	}
	if criteria.Query != "" {
		lines = append(lines, locales.FilterQueryValue(criteria.Query))
	}
	if criteria.NegatedQuery != "" {
		lines = append(lines, locales.FilterNegatedQueryValue(criteria.NegatedQuery))
	}
	if criteria.HasAttachment {
		lines = append(lines, locales.FilterHasAttachment())
	}
	if criteria.ExcludeChats {
		lines = append(lines, locales.FilterExcludeChats())
	}
	if criteria.Size != 0 || criteria.SizeComparison != "" {
		size := strconv.FormatInt(criteria.Size, 10) + " B"
		switch criteria.SizeComparison {
		case "larger":
			lines = append(lines, locales.FilterSizeLarger(size))
		case "smaller":
			lines = append(lines, locales.FilterSizeSmaller(size))
		default:
			lines = append(lines, locales.FilterSize(criteria.SizeComparison, size))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, locales.FilterMatchAll())
	}
	return lines
}

func (a *App) filterActionLines(action mail.FilterAction) []string {
	var lines []string
	for _, id := range action.AddLabelIDs {
		lines = append(lines, locales.FilterAddLabelValue(a.filterLabelName(id)))
	}
	for _, id := range action.RemoveLabelIDs {
		lines = append(lines, locales.FilterRemoveLabelValue(a.filterLabelName(id)))
	}
	if action.Forward != "" {
		lines = append(lines, locales.FilterForwardValue(action.Forward))
	}
	if len(lines) == 0 {
		lines = append(lines, locales.FilterNoActions())
	}
	return lines
}

func (a *App) filterLabelName(id string) string {
	if id == mail.Unread {
		return locales.FilterUnreadName()
	}
	return a.labelName(id)
}

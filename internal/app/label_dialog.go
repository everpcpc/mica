package app

import (
	"context"
	"errors"
	"strings"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo/ui"
)

var errLabelManagerUnsupported = errors.New("mail: label management is unsupported")

type labelDialogState struct {
	open     bool
	isDelete bool
	label    mail.Label
	name     string
	busy     bool
	err      string
	input    ui.Handle
	focused  bool
}

func (a *App) openLabelCreate() {
	a.labelDialog = &labelDialogState{open: true}
}

func (a *App) openLabelDelete(label mail.Label) {
	a.labelDialog = &labelDialogState{open: true, isDelete: true, label: label}
}

func (a *App) labelDialogView(c *ui.Context) {
	s := a.labelDialog
	t := c.Theme()
	dismissed := ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(420).Gap(12).Children(func() {
			title := locales.NewLabel()
			if s.isDelete {
				title = locales.DeleteLabelQuestion()
			}
			ui.Text(c, title).FontSize(18).Bold()
			if s.isDelete {
				ui.Text(c, locales.DeleteLabelConfirmation(s.label.Name)).FontSize(13).LineHeight(1.45).TextColor(t.TextMuted)
			} else {
				ui.Text(c, locales.LabelName()).FontSize(12).TextColor(t.TextMuted)
				input := ui.TextInput(c.Key("label-name"), &s.name).Bind(&s.input).Label(locales.LabelName()).Placeholder(locales.LabelName()).Grow(1).Disabled(s.busy)
				if !s.focused {
					s.input.Focus(c)
					s.focused = true
				}
				if input.Submitted() && !s.busy {
					a.createLabel()
				}
			}
			if s.err != "" {
				ui.Text(c, s.err).FontSize(12).LineHeight(1.4).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if s.busy {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, locales.Processing()).FontSize(12).TextColor(t.TextMuted).Grow(1)
				} else {
					ui.Box(c).Grow(1)
				}
				ui.Button(c, locales.Cancel()).Disabled(s.busy).OnClick(func() { a.closeLabelDialog(s) })
				if s.isDelete {
					ui.Button(c, locales.DeleteLabel()).TextColor(t.Danger).Disabled(s.busy).OnClick(a.deleteLabel)
				} else {
					primaryAction(c, locales.CreateLabel()).Disabled(s.busy).OnClick(a.createLabel)
				}
			})
		})
	}).Dismissed()
	if dismissed && s.busy {
		s.open = true
	}
	if !s.open && !s.busy && a.labelDialog == s {
		a.labelDialog = nil
	}
}

func (a *App) labelManager() (mail.LabelManager, error) {
	manager, ok := a.service.(mail.LabelManager)
	if !ok {
		return nil, errLabelManagerUnsupported
	}
	return manager, nil
}

func (a *App) createLabel() {
	s := a.labelDialog
	if s == nil || s.isDelete || s.busy || a.accountTransition != nil {
		return
	}
	name := strings.TrimSpace(s.name)
	if name == "" {
		s.err = locales.LabelNameRequired()
		return
	}
	for _, label := range a.labels {
		if strings.EqualFold(label.Name, name) {
			s.err = locales.LabelExists(name)
			return
		}
	}
	manager, err := a.labelManager()
	if err != nil {
		s.err = locales.LabelActionFailed(err.Error())
		return
	}
	s.name = name
	s.busy = true
	s.err = ""
	var label mail.Label
	a.enqueueMutation(mutationJob{
		service: a.service,
		runFunc: func() error {
			var err error
			label, err = manager.CreateLabel(context.Background(), name)
			return err
		},
		done: func(err error) {
			if err != nil {
				if a.labelDialog == s {
					s.busy = false
					s.err = locales.LabelActionFailed(err.Error())
				}
				return
			}
			a.setLabel(label)
			a.notice = locales.LabelCreated(label.Name)
			a.closeLabelDialog(s)
		},
	})
}

func (a *App) deleteLabel() {
	s := a.labelDialog
	if s == nil || !s.isDelete || s.busy || a.accountTransition != nil {
		return
	}
	if _, err := a.labelManager(); err != nil {
		s.err = locales.LabelActionFailed(err.Error())
		return
	}
	s.busy = true
	s.err = ""
	label := s.label
	a.enqueueMutation(mutationJob{
		service:     a.service,
		id:          label.ID,
		deleteLabel: true,
		failure:     locales.ActionFailed,
		done: func(err error) {
			if err != nil {
				if a.labelDialog == s {
					s.busy = false
					s.err = locales.LabelActionFailed(err.Error())
				}
				return
			}
			a.removeLabelDefinition(label.ID)
			a.notice = locales.LabelDeleted(label.Name)
			a.closeLabelDialog(s)
		},
	})
}

func (a *App) updateLabelColor(label mail.Label, color mail.LabelColor) {
	if label.Color == color || a.accountTransition != nil {
		return
	}
	if _, err := a.labelManager(); err != nil {
		a.notice = locales.LabelActionFailed(err.Error())
		return
	}
	a.enqueueMutation(mutationJob{
		service:    a.service,
		id:         label.ID,
		labelColor: &color,
		failure:    locales.ActionFailed,
		done: func(err error) {
			if err != nil {
				return
			}
			label.Color = color
			a.setLabel(label)
			a.notice = locales.LabelColorUpdated(label.Name)
		},
	})
}

func (a *App) removeLabelDefinition(id string) {
	a.undo = nil
	labels := make([]mail.Label, 0, len(a.labels))
	for _, label := range a.labels {
		if label.ID != id {
			labels = append(labels, label)
		}
	}
	a.setLabels(labels)
	if a.accountVerified {
		a.persistLabels(labels)
	}
	for _, thread := range a.threads {
		if containsLabel(thread.LabelIDs, id) {
			a.updateThreadLabels(thread.ID, nil, []string{id})
		}
	}
	if a.selectedLabel == id {
		a.selectedLabel = mail.Inbox
		a.clearThread()
		a.refresh()
	}
}

func (a *App) closeLabelDialog(s *labelDialogState) {
	if a.labelDialog == s {
		s.open = false
		a.labelDialog = nil
	}
}

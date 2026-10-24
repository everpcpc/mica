package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	mailmodel "mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var errComposeAttachmentTooLarge = errors.New("attachment exceeds 25 MiB")

type composerAttachment struct {
	id         uint64
	source     composeAttachmentSource
	attachment mailmodel.OutgoingAttachment
	loading    bool
	err        string
}

func (a *App) chooseComposeAttachments() {
	m := a.compose
	if m == nil || m.sending {
		return
	}
	openDialog := a.openDialog
	options := mygo.OpenDialogOptions{Title: locales.AddAttachments(), Multiple: true}
	a.schedule(func() {
		paths, err := openDialog(options)
		a.publish(func() {
			if a.compose != m {
				return
			}
			if err != nil {
				m.err = locales.AttachmentDialogFailed(err.Error())
				return
			}
			m.err = ""
			a.addComposeAttachments(paths)
		})
	})
}

func (a *App) addComposeAttachments(paths []string) {
	sources := make([]composeAttachmentSource, 0, len(paths))
	for _, path := range paths {
		if path != "" {
			sources = append(sources, localComposeAttachmentSource{path: path})
		}
	}
	a.addComposeAttachmentSources(sources)
}

func (a *App) addComposeAttachmentSources(sources []composeAttachmentSource) {
	m := a.compose
	if m == nil {
		return
	}
	if m.sending {
		m.err = locales.AttachmentsSending()
		return
	}
	for _, source := range sources {
		m.nextAttachmentID++
		m.attachments = append(m.attachments, composerAttachment{
			id:         m.nextAttachmentID,
			source:     source,
			attachment: mailmodel.OutgoingAttachment{Name: source.name()},
			loading:    true,
		})
	}
	if len(sources) == 0 {
		return
	}
	m.err = ""
	a.pumpComposeAttachments(m)
}

func (a *App) pumpComposeAttachments(m *composer) {
	if a.compose != m || m.sending || m.readingAttachment {
		return
	}
	for i := range m.attachments {
		item := &m.attachments[i]
		if !item.loading {
			continue
		}
		m.readingAttachment = true
		remaining := mailmodel.MaxComposeAttachmentBytes - m.composeAttachmentBytes()
		a.readComposeAttachment(m, item.id, item.source, remaining)
		return
	}
}

func (a *App) readComposeAttachment(m *composer, id uint64, source composeAttachmentSource, remaining int64) {
	a.schedule(func() {
		attachment, err := source.read(context.Background(), remaining)
		a.publish(func() {
			if errors.Is(err, errComposeAttachmentTooLarge) && a.compose == m {
				latest := mailmodel.MaxComposeAttachmentBytes - m.composeAttachmentBytes()
				if latest > remaining {
					a.readComposeAttachment(m, id, source, latest)
					return
				}
			}
			m.readingAttachment = false
			a.finishComposeAttachment(m, id, attachment, err)
			a.pumpComposeAttachments(m)
		})
	})
}

func (a *App) finishComposeAttachment(m *composer, id uint64, attachment mailmodel.OutgoingAttachment, err error) {
	if a.compose != m {
		return
	}
	for i := range m.attachments {
		item := &m.attachments[i]
		if item.id != id {
			continue
		}
		if err == nil && int64(len(attachment.Data)) > mailmodel.MaxComposeAttachmentBytes-m.composeAttachmentBytes() {
			err = errComposeAttachmentTooLarge
		}
		item.loading = false
		if err != nil {
			item.attachment.Data = nil
			item.attachment.MIMEType = ""
			if errors.Is(err, errComposeAttachmentTooLarge) {
				item.err = locales.AttachmentSizeLimit()
			} else {
				item.err = locales.AttachmentReadFailed(err.Error())
			}
			return
		}
		item.attachment = attachment
		item.err = ""
		return
	}
}

func (a *App) removeComposeAttachment(id uint64) {
	m := a.compose
	if m == nil || m.sending {
		return
	}
	for i := range m.attachments {
		if m.attachments[i].id != id {
			continue
		}
		copy(m.attachments[i:], m.attachments[i+1:])
		m.attachments[len(m.attachments)-1] = composerAttachment{}
		m.attachments = m.attachments[:len(m.attachments)-1]
		m.err = ""
		return
	}
}

func (m *composer) composeAttachmentBytes() int64 {
	var total int64
	for _, item := range m.attachments {
		total += int64(len(item.attachment.Data))
	}
	return total
}

func (m *composer) composeAttachmentsState() (loading, failed bool) {
	for _, item := range m.attachments {
		loading = loading || item.loading
		failed = failed || item.err != ""
	}
	return loading, failed
}

func (m *composer) outgoingAttachments() []mailmodel.OutgoingAttachment {
	attachments := make([]mailmodel.OutgoingAttachment, len(m.attachments))
	for i, item := range m.attachments {
		attachments[i] = item.attachment
	}
	return attachments
}

func readOutgoingAttachment(path string) (mailmodel.OutgoingAttachment, error) {
	return readOutgoingAttachmentLimit(path, mailmodel.MaxComposeAttachmentBytes)
}

func readOutgoingAttachmentLimit(path string, limit int64) (mailmodel.OutgoingAttachment, error) {
	attachment := mailmodel.OutgoingAttachment{Name: filepath.Base(path)}
	if err := validateOutgoingAttachmentName(attachment.Name); err != nil {
		return attachment, err
	}
	if limit < 0 {
		return attachment, errComposeAttachmentTooLarge
	}
	file, err := os.Open(path)
	if err != nil {
		return attachment, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return attachment, fmt.Errorf("stat attachment: %w", err)
	}
	if !info.Mode().IsRegular() {
		return attachment, errors.New("attachment is not a regular file")
	}
	if info.Size() > limit {
		return attachment, errComposeAttachmentTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return attachment, fmt.Errorf("read attachment: %w", err)
	}
	if int64(len(data)) > limit {
		return attachment, errComposeAttachmentTooLarge
	}
	attachment.Data = data
	attachment.MIMEType = outgoingMIMEType(path, data)
	return attachment, nil
}

func outgoingMIMEType(path string, data []byte) string {
	if mimeType := mime.TypeByExtension(filepath.Ext(path)); mimeType != "" {
		return mimeType
	}
	if mimeType := http.DetectContentType(data); mimeType != "" {
		return mimeType
	}
	return "application/octet-stream"
}

func (a *App) composeAttachmentsView(c *ui.Context, m *composer) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Button(c, locales.AddAttachments()).Disabled(m.sending).OnClick(a.chooseComposeAttachments)
			ui.Text(c, locales.AttachmentDropHint()).FontSize(11).TextColor(c.Theme().TextMuted)
		})
		for _, item := range m.attachments {
			id := item.id
			ui.Column(c.Key(fmt.Sprintf("compose-attachment-%d", id))).Gap(2).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, item.attachment.Name).FontSize(12).Grow(1).MinWidth(0).SingleLine()
					switch {
					case item.loading:
						ui.Spinner(c).Size(12, 12)
						ui.Text(c, locales.AttachmentReading()).FontSize(11).TextColor(c.Theme().TextMuted)
					case item.err == "":
						ui.Text(c, formatAttachmentSize(int64(len(item.attachment.Data)))).FontSize(11).TextColor(c.Theme().TextMuted)
					}
					ui.Button(c, locales.RemoveAttachment()).Label(locales.RemoveAttachmentNamed(item.attachment.Name)).Disabled(m.sending).OnClick(func() {
						a.removeComposeAttachment(id)
					})
				})
				if item.err != "" {
					ui.Text(c, item.err).FontSize(11).TextColor(c.Theme().Danger)
				}
			})
		}
	})
}

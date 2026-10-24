package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mica/internal/mail"
	"mica/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type attachmentDownload struct {
	loading bool
	err     string
}

func attachmentKey(messageID, attachmentID string) string {
	return messageID + "\x00" + attachmentID
}

func (a *App) attachmentView(c *ui.Context, message mail.Message, attachment mail.Attachment) {
	state := a.attachmentDownloads[attachmentKey(message.ID, attachment.ID)]
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconLabel).Size(14, 14).TextColor(c.Theme().TextMuted)
			ui.Text(c, attachment.Name).FontSize(12).Grow(1).MinWidth(0).SingleLine()
			ui.Text(c, formatAttachmentSize(attachment.Size)).FontSize(11).TextColor(c.Theme().TextMuted)
			if state.loading {
				ui.Spinner(c).Size(12, 12)
			} else {
				ui.Button(c, locales.OpenAttachment()).OnClick(func() {
					a.downloadAttachment(message, attachment, false)
				})
				ui.Button(c, locales.SaveAttachment()).OnClick(func() {
					a.downloadAttachment(message, attachment, true)
				})
			}
		})
		if state.err != "" {
			ui.Text(c, state.err).FontSize(11).TextColor(c.Theme().Danger)
		}
	})
}

func formatAttachmentSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1<<20 {
		return fmt.Sprintf("%d KB", (size+1023)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
}

func (a *App) downloadAttachment(message mail.Message, attachment mail.Attachment, save bool) {
	key := attachmentKey(message.ID, attachment.ID)
	state := a.attachmentDownloads[key]
	if state.loading {
		return
	}
	if a.attachmentDownloads == nil {
		a.attachmentDownloads = make(map[string]attachmentDownload)
	}
	fetcher, ok := a.service.(mail.AttachmentFetcher)
	if !ok {
		state.err = locales.AttachmentUnavailable()
		a.attachmentDownloads[key] = state
		return
	}
	state.loading = true
	state.err = ""
	a.attachmentDownloads[key] = state
	version := a.serviceVersion
	a.schedule(func() {
		data, err := fetcher.Attachment(context.Background(), message.ID, attachment.ID)
		if err == nil {
			if save {
				err = a.saveAttachment(attachment, data)
			} else {
				err = a.openAttachment(attachment, data)
			}
		}
		a.publish(func() {
			if version != a.serviceVersion {
				return
			}
			state := a.attachmentDownloads[key]
			state.loading = false
			if err != nil {
				state.err = locales.ActionFailed(err.Error())
			}
			a.attachmentDownloads[key] = state
		})
	})
}

func (a *App) saveAttachment(attachment mail.Attachment, data []byte) error {
	path, err := a.saveDialog(mygo.SaveDialogOptions{
		Title:       locales.SaveAttachmentDialog(),
		DefaultPath: safeAttachmentName(attachment.Name),
	})
	if err != nil || path == "" {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (a *App) openAttachment(attachment mail.Attachment, data []byte) error {
	path, err := writeAttachmentTemp(attachment, data)
	if err != nil {
		return err
	}
	if err := a.openPath(path); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func writeAttachmentTemp(attachment mail.Attachment, data []byte) (path string, err error) {
	file, err := os.CreateTemp("", "mica-attachment-*"+filepath.Ext(safeAttachmentName(attachment.Name)))
	if err != nil {
		return "", err
	}
	path = file.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func safeAttachmentName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "attachment"
	}
	runes := []rune(name)
	if len(runes) > 128 {
		name = string(runes[:128])
	}
	return name
}

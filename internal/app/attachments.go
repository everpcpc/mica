package app

import (
	"context"
	"errors"
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

func attachmentStorageID(attachment mail.Attachment, index int) string {
	if attachment.ID != "" {
		return attachment.ID
	}
	return fmt.Sprintf("inline-%d", index)
}

func attachmentKey(ref messageRef, attachmentID string, index int) string {
	if attachmentID == "" {
		return fmt.Sprintf("%s\x00inline-%d", ref.key(), index)
	}
	return ref.key() + "\x00" + attachmentID
}

func (a *App) attachmentRef(message mail.Message) messageRef {
	return messageRefForAccount(a.storedMessageOwner(message), message.ThreadID, message.ID)
}

func (a *App) attachmentView(c *ui.Context, message mail.Message, attachment mail.Attachment, index int) {
	state := a.attachmentDownloads[attachmentKey(a.attachmentRef(message), attachment.ID, index)]
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconLabel).Size(14, 14).TextColor(c.Theme().TextMuted)
			ui.Text(c, attachment.Name).FontSize(12).Grow(1).MinWidth(0).SingleLine()
			ui.Text(c, formatAttachmentSize(attachment.Size)).FontSize(11).TextColor(c.Theme().TextMuted)
			if state.loading {
				ui.Spinner(c).Size(12, 12)
			} else {
				ui.Button(c, locales.OpenAttachment()).OnClick(func() {
					a.downloadAttachment(message, attachment, false, index)
				})
				ui.Button(c, locales.SaveAttachment()).OnClick(func() {
					a.downloadAttachment(message, attachment, true, index)
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

func (a *App) downloadAttachment(message mail.Message, attachment mail.Attachment, save bool, index int) {
	owner := a.storedMessageOwner(message)
	ref := messageRefForAccount(owner, message.ThreadID, message.ID)
	key := attachmentKey(ref, attachment.ID, index)
	state := a.attachmentDownloads[key]
	if state.loading {
		return
	}
	if a.attachmentDownloads == nil {
		a.attachmentDownloads = make(map[string]attachmentDownload)
	}
	service, generation := a.ownerService(owner)
	fetcher, hasFetcher := service.(mail.AttachmentFetcher)
	store, _ := a.store.(attachmentStore)
	cacheAttachment := a.attachmentCacheWriter(owner, generation)
	state.loading = true
	state.err = ""
	a.attachmentDownloads[key] = state
	a.schedule(func() {
		data := attachment.Data
		fromCache := false
		if data == nil && store != nil {
			cached, found, err := store.Attachment(context.Background(), owner, ref.ThreadID, ref.MessageID, attachmentStorageID(attachment, index))
			if err != nil {
				logCacheError(err)
			} else if found {
				data, fromCache = cached, true
			}
		}
		var err error
		if data == nil && !fromCache {
			if !hasFetcher {
				err = errors.New(locales.AttachmentUnavailable())
			} else {
				data, err = fetcher.Attachment(context.Background(), ref.MessageID, attachment.ID)
			}
		}
		if err == nil && !fromCache && cacheAttachment != nil {
			cacheAttachment(ref.ThreadID, ref.MessageID, attachmentStorageID(attachment, index), data)
		}
		if err == nil {
			if save {
				err = a.saveAttachment(attachment, data)
			} else {
				err = a.openAttachment(attachment, data)
			}
		}
		a.publish(func() {
			if !a.ownerOperationCurrent(owner, generation) {
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

package gmail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"

	"mica/internal/mail"
)

func writeMixedMessage(buffer *bytes.Buffer, compose mail.Compose) error {
	headers := make([]textproto.MIMEHeader, len(compose.Attachments))
	var total int64
	for i, attachment := range compose.Attachments {
		size := int64(len(attachment.Data))
		if size > mail.MaxComposeAttachmentBytes-total {
			return errors.New("gmail: attachments exceed 25 MiB total")
		}
		total += size
		header, err := outgoingAttachmentHeader(attachment)
		if err != nil {
			return err
		}
		headers[i] = header
	}

	writer := multipart.NewWriter(buffer)
	writeHeader(buffer, "Content-Type", `multipart/mixed; boundary="`+writer.Boundary()+`"`)
	buffer.WriteString("\r\n")
	if err := writeMixedBody(writer, compose); err != nil {
		return err
	}
	for i, attachment := range compose.Attachments {
		part, err := writer.CreatePart(headers[i])
		if err != nil {
			return fmt.Errorf("gmail: create attachment part: %w", err)
		}
		if err := writeBase64Lines(part, attachment.Data); err != nil {
			return fmt.Errorf("gmail: encode attachment: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("gmail: finish MIME message: %w", err)
	}
	return nil
}

func outgoingAttachmentHeader(attachment mail.OutgoingAttachment) (textproto.MIMEHeader, error) {
	if strings.TrimSpace(attachment.Name) == "" || strings.IndexFunc(attachment.Name, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) >= 0 {
		return nil, errors.New("gmail: invalid attachment name")
	}
	mediaType, params, err := mime.ParseMediaType(attachment.MIMEType)
	if err != nil {
		return nil, fmt.Errorf("gmail: invalid attachment MIME type: %w", err)
	}
	params["name"] = attachment.Name
	contentType := mime.FormatMediaType(mediaType, params)
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": attachment.Name})
	if contentType == "" || disposition == "" {
		return nil, errors.New("gmail: invalid attachment name or MIME type")
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Type", contentType)
	header.Set("Content-Disposition", disposition)
	header.Set("Content-Transfer-Encoding", "base64")
	return header, nil
}

func writeMixedBody(writer *multipart.Writer, compose mail.Compose) error {
	if compose.Text != "" && compose.HTML != "" {
		var nested bytes.Buffer
		alternative := multipart.NewWriter(&nested)
		for _, part := range []struct {
			mimeType string
			body     string
		}{
			{mimeType: "text/plain", body: compose.Text},
			{mimeType: "text/html", body: compose.HTML},
		} {
			header := make(textproto.MIMEHeader)
			header.Set("Content-Type", part.mimeType+`; charset="UTF-8"`)
			header.Set("Content-Transfer-Encoding", "quoted-printable")
			partWriter, err := alternative.CreatePart(header)
			if err != nil {
				return fmt.Errorf("gmail: create MIME part: %w", err)
			}
			if err := writeQuotedPrintable(partWriter, part.body); err != nil {
				return err
			}
		}
		if err := alternative.Close(); err != nil {
			return fmt.Errorf("gmail: finish MIME body: %w", err)
		}
		header := make(textproto.MIMEHeader)
		header.Set("Content-Type", `multipart/alternative; boundary="`+alternative.Boundary()+`"`)
		part, err := writer.CreatePart(header)
		if err != nil {
			return fmt.Errorf("gmail: create MIME body: %w", err)
		}
		if err := writeMIMEBytes(part, nested.Bytes()); err != nil {
			return fmt.Errorf("gmail: write MIME body: %w", err)
		}
		return nil
	}

	mimeType, body := "text/plain", compose.Text
	if compose.Text == "" && compose.HTML != "" {
		mimeType, body = "text/html", compose.HTML
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Type", mimeType+`; charset="UTF-8"`)
	header.Set("Content-Transfer-Encoding", "quoted-printable")
	part, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("gmail: create MIME body: %w", err)
	}
	return writeQuotedPrintable(part, body)
}

func writeBase64Lines(writer io.Writer, data []byte) error {
	const inputPerLine = 76 / 4 * 3
	var line [76]byte
	for offset := 0; offset < len(data); offset += inputPerLine {
		if offset > 0 {
			if err := writeMIMEBytes(writer, []byte("\r\n")); err != nil {
				return err
			}
		}
		end := min(offset+inputPerLine, len(data))
		n := base64.StdEncoding.EncodedLen(end - offset)
		base64.StdEncoding.Encode(line[:n], data[offset:end])
		if err := writeMIMEBytes(writer, line[:n]); err != nil {
			return err
		}
	}
	return nil
}

func writeMIMEBytes(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

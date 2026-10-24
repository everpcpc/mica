package gmail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mica/internal/mail"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	stdmail "net/mail"
	"net/textproto"
	"strings"
	"unicode/utf8"
)

func makeMessage(threadID string, message apiMessage) (mail.Message, error) {
	if message.ThreadID != "" {
		threadID = message.ThreadID
	}
	result := mail.Message{
		ID:         message.ID,
		ThreadID:   threadID,
		From:       decodedHeader(message.Payload, "From"),
		ReplyTo:    decodedHeader(message.Payload, "Reply-To"),
		To:         decodedHeader(message.Payload, "To"),
		Cc:         decodedHeader(message.Payload, "Cc"),
		Subject:    decodedHeader(message.Payload, "Subject"),
		MessageID:  header(message.Payload, "Message-ID"),
		References: header(message.Payload, "References"),
		Date:       messageTime(message),
		LabelIDs:   append([]string(nil), message.LabelIDs...),
	}
	var text, html []string
	attachments, err := walkParts(message.Payload, &text, &html)
	if err != nil {
		return mail.Message{}, err
	}
	result.Text = strings.Join(text, "\n")
	result.HTML = strings.Join(html, "\n")
	result.Attachments = attachments
	return result, nil
}

func walkParts(part apiPart, text, html *[]string) ([]mail.Attachment, error) {
	kind := canonicalMIMEType(part.MIMEType)
	if part.Filename != "" || part.Body.AttachmentID != "" {
		return []mail.Attachment{{
			ID:        part.Body.AttachmentID,
			Name:      part.Filename,
			MIMEType:  kind,
			Size:      part.Body.Size,
			ContentID: strings.Trim(strings.TrimSpace(header(part, "Content-ID")), "<>"),
		}}, nil
	}
	var attachments []mail.Attachment
	if strings.HasPrefix(kind, "multipart/") || len(part.Parts) > 0 {
		for _, child := range part.Parts {
			found, err := walkParts(child, text, html)
			if err != nil {
				return nil, err
			}
			attachments = append(attachments, found...)
		}
		return attachments, nil
	}
	if part.Body.Data == "" {
		return nil, nil
	}
	decoded, err := decodeBase64URL(part.Body.Data)
	if err != nil {
		return nil, fmt.Errorf("gmail: decode MIME part: %w", err)
	}
	switch kind {
	case "text/plain":
		*text = append(*text, string(decoded))
	case "text/html":
		*html = append(*html, string(decoded))
	}
	return attachments, nil
}

func canonicalMIMEType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err == nil {
		return strings.ToLower(mediaType)
	}
	return strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
}

func decodeBase64URL(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func header(part apiPart, name string) string {
	for _, header := range part.Headers {
		if strings.EqualFold(header.Name, name) {
			return strings.TrimSpace(header.Value)
		}
	}
	return ""
}

func decodedHeader(part apiPart, name string) string {
	value := header(part, name)
	if value == "" {
		return ""
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

func displayName(value string) string {
	address, err := stdmail.ParseAddress(value)
	if err != nil {
		return value
	}
	if address.Name != "" {
		return address.Name
	}
	return address.Address
}

func buildRFC5322(compose mail.Compose) ([]byte, error) {
	if compose.To == "" && compose.Cc == "" && compose.Bcc == "" {
		return nil, errors.New("gmail: at least one recipient is required")
	}
	var buffer bytes.Buffer
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "To", value: compose.To},
		{name: "Cc", value: compose.Cc},
		{name: "Bcc", value: compose.Bcc},
	} {
		if field.value == "" {
			continue
		}
		addresses, err := formatAddressList(field.value)
		if err != nil {
			return nil, fmt.Errorf("gmail: invalid %s: %w", field.name, err)
		}
		writeHeader(&buffer, field.name, addresses)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "Subject", value: compose.Subject},
		{name: "In-Reply-To", value: compose.InReplyTo},
		{name: "References", value: compose.References},
	} {
		if field.value == "" {
			continue
		}
		if strings.ContainsAny(field.value, "\r\n") {
			return nil, fmt.Errorf("gmail: invalid %s header", field.name)
		}
		value := strings.TrimSpace(field.value)
		if field.name == "Subject" {
			value = encodeHeader(value)
		}
		writeHeader(&buffer, field.name, value)
	}
	writeHeader(&buffer, "MIME-Version", "1.0")

	if len(compose.Attachments) > 0 {
		if err := writeMixedMessage(&buffer, compose); err != nil {
			return nil, err
		}
		return buffer.Bytes(), nil
	}

	if compose.Text != "" && compose.HTML != "" {
		writer := multipart.NewWriter(&buffer)
		writeHeader(&buffer, "Content-Type", `multipart/alternative; boundary="`+writer.Boundary()+`"`)
		buffer.WriteString("\r\n")
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
			partWriter, err := writer.CreatePart(header)
			if err != nil {
				return nil, fmt.Errorf("gmail: create MIME part: %w", err)
			}
			if err := writeQuotedPrintable(partWriter, part.body); err != nil {
				return nil, err
			}
		}
		if err := writer.Close(); err != nil {
			return nil, fmt.Errorf("gmail: finish MIME message: %w", err)
		}
		return buffer.Bytes(), nil
	}

	mimeType, body := "text/plain", compose.Text
	if compose.Text == "" && compose.HTML != "" {
		mimeType, body = "text/html", compose.HTML
	}
	writeHeader(&buffer, "Content-Type", mimeType+`; charset="UTF-8"`)
	writeHeader(&buffer, "Content-Transfer-Encoding", "quoted-printable")
	buffer.WriteString("\r\n")
	if err := writeQuotedPrintable(&buffer, body); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func formatAddressList(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", errors.New("header contains a newline")
	}
	addresses, err := stdmail.ParseAddressList(value)
	if err != nil {
		return "", err
	}
	formatted := make([]string, len(addresses))
	for i, address := range addresses {
		formatted[i] = address.String()
	}
	return strings.Join(formatted, ", "), nil
}

func encodeHeader(value string) string {
	for _, r := range value {
		if r > 127 || !utf8.ValidRune(r) {
			return mime.QEncoding.Encode("UTF-8", value)
		}
	}
	return value
}

func writeHeader(buffer *bytes.Buffer, name, value string) {
	buffer.WriteString(name)
	buffer.WriteString(": ")
	buffer.WriteString(value)
	buffer.WriteString("\r\n")
}

func writeQuotedPrintable(writer io.Writer, body string) error {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")
	quoted := quotedprintable.NewWriter(writer)
	if _, err := io.WriteString(quoted, body); err != nil {
		return fmt.Errorf("gmail: encode message body: %w", err)
	}
	if err := quoted.Close(); err != nil {
		return fmt.Errorf("gmail: encode message body: %w", err)
	}
	return nil
}

func joinReferences(references, messageID string) string {
	references = strings.TrimSpace(references)
	messageID = strings.TrimSpace(messageID)
	if messageID == "" || strings.Contains(references, messageID) {
		return references
	}
	return strings.TrimSpace(references + " " + messageID)
}

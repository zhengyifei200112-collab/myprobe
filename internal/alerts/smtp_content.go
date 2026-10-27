package alerts

import (
	"bytes"
	"mime"
	"mime/quotedprintable"
	"strings"
)

func smtpMessage(config ChannelConfig, n Notification) ([]byte, error) {
	if strings.ContainsAny(n.Title, "\r\n") {
		return nil, &DeliveryError{Class: "smtp_invalid_title", Permanent: true}
	}
	subject := mime.QEncoding.Encode("UTF-8", n.Title)
	if subject == n.Title && len(subject) > 989 {
		return nil, &DeliveryError{Class: "smtp_title_too_large", Permanent: true}
	}
	subject = strings.ReplaceAll(subject, "?= =?", "?=\r\n =?")
	var message bytes.Buffer
	message.WriteString("To: " + config.SMTPTo + "\r\nFrom: " + config.SMTPFrom + "\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	writer := quotedprintable.NewWriter(&message)
	if _, err := writer.Write([]byte(n.Message + incidentContentSuffix(n.IncidentPath, ""))); err != nil {
		return nil, &DeliveryError{Class: "smtp_encoding", Permanent: true}
	}
	if err := writer.Close(); err != nil {
		return nil, &DeliveryError{Class: "smtp_encoding", Permanent: true}
	}
	return message.Bytes(), nil
}

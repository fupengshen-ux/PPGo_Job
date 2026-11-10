package notify

import (
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// SendMonitorMail reports SMTP acceptance, not queue admission or inbox delivery.
// It is separate from the legacy task notification queues.
func SendMonitorMail(to, subject, body string) error {
	if config == nil || config.Host == "" {
		return fmt.Errorf("SMTP is not configured")
	}
	address, err := mail.ParseAddress(to)
	if err != nil || strings.ContainsAny(to, "\r\n") {
		return fmt.Errorf("invalid recipient")
	}
	from := config.From
	if from == "" {
		from = config.User
	}
	sender, err := mail.ParseAddress(from)
	if err != nil {
		return fmt.Errorf("invalid sender")
	}
	if config.Port != "25" && config.Port != "465" {
		return fmt.Errorf("SMTP supports ports 25 and 465")
	}
	deadline := time.Now().Add(10 * time.Second)
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(config.Host, config.Port), time.Until(deadline))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	if config.Port == "465" {
		conn = tls.Client(conn, &tls.Config{ServerName: config.Host, MinVersion: tls.VersionTLS12})
	}
	c, err := smtp.NewClient(conn, config.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if config.Port == "25" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err = c.StartTLS(&tls.Config{ServerName: config.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		}
	}
	if config.User != "" {
		if err = c.Auth(smtp.PlainAuth("", config.User, config.Pwd, config.Host)); err != nil {
			return err
		}
	}
	return writeMonitorMessage(c, sender.Address, address.Address, subject, body)
}

func writeMonitorMessage(c *smtp.Client, from, to, subject, body string) error {
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	subject = strings.NewReplacer("\r", " ", "\n", " ").Replace(subject)
	message := "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + mime.QEncoding.Encode("UTF-8", subject) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body
	if _, err = w.Write([]byte(message)); err != nil {
		return err
	}
	// DATA's positive completion is the acceptance boundary. A failed QUIT must
	// not turn an accepted message into a retry.
	if err = w.Close(); err != nil {
		return err
	}
	_ = c.Quit()
	return nil
}

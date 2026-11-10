package notify

import (
	"bufio"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"
)

func TestMonitorSMTPAcceptance(t *testing.T) {
	for _, accept := range []bool{true, false} {
		client, server := net.Pipe()
		_ = client.SetDeadline(time.Now().Add(2 * time.Second))
		_ = server.SetDeadline(time.Now().Add(2 * time.Second))
		messages := make(chan string, 1)
		go func() {
			defer server.Close()
			r := bufio.NewReader(server)
			fmt.Fprint(server, "220 fixture ESMTP\r\n")
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				switch {
				case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"), strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
					fmt.Fprint(server, "250 OK\r\n")
				case strings.HasPrefix(line, "DATA"):
					fmt.Fprint(server, "354 continue\r\n")
					var b strings.Builder
					for {
						part, err := r.ReadString('\n')
						if err != nil {
							return
						}
						if part == ".\r\n" {
							break
						}
						b.WriteString(part)
					}
					messages <- b.String()
					if accept {
						fmt.Fprint(server, "250 accepted\r\n")
					} else {
						fmt.Fprint(server, "550 rejected\r\n")
					}
				case strings.HasPrefix(line, "QUIT"):
					// Closing without 221 after accepting DATA must not cause a retry.
					return
				}
			}
		}()
		c, err := smtp.NewClient(client, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		err = writeMonitorMessage(c, "sender@example.invalid", "ops@example.invalid", "资源告警\r\nBcc: injected", "CPU 95%")
		c.Close()
		client.Close()
		if (err == nil) != accept {
			t.Fatal("wrong acceptance result", accept, err)
		}
		message := <-messages
		if strings.Contains(message, "\r\nBcc:") || !strings.Contains(message, "Content-Type: text/plain; charset=UTF-8") || !strings.Contains(message, "CPU 95%") {
			t.Fatal(message)
		}
	}
}

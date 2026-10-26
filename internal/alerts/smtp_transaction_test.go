package alerts

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"
)

func TestSMTPAcceptanceAndConnectionLoss(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprint("accepted=", accepted), func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			defer clientConn.Close()
			_ = clientConn.SetDeadline(time.Now().Add(3 * time.Second))
			done := make(chan error, 1)
			go func() {
				defer serverConn.Close()
				reader := bufio.NewReader(serverConn)
				write := func(text string) error { _, err := fmt.Fprint(serverConn, text); return err }
				if err := write("220 fixture ESMTP\r\n"); err != nil {
					done <- err
					return
				}
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						done <- err
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						if err := write("250 OK\r\n"); err != nil {
							done <- err
							return
						}
					case strings.HasPrefix(line, "DATA"):
						if err := write("354 send message\r\n"); err != nil {
							done <- err
							return
						}
						for {
							line, err = reader.ReadString('\n')
							if err != nil {
								done <- err
								return
							}
							if line == ".\r\n" {
								break
							}
						}
						if !accepted {
							done <- nil
							return
						}
						if err := write("250 accepted\r\n"); err != nil {
							done <- err
							return
						}
					case strings.HasPrefix(line, "QUIT"):
						// Disconnect without a QUIT reply after confirming DATA.
						done <- nil
						return
					default:
						done <- fmt.Errorf("unexpected command %q", line)
						return
					}
				}
			}()
			client, err := smtp.NewClient(clientConn, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			err = sendSMTPMessage(client, "sender@example.com", "recipient@example.com", []byte("Subject: fixture\r\n\r\nbody\r\n"))
			if accepted && err != nil {
				t.Fatalf("accepted DATA retried after QUIT failure: %v", err)
			}
			if !accepted {
				var failure *DeliveryError
				if !errors.As(err, &failure) || !failure.Ambiguous {
					t.Fatalf("lost DATA confirmation: %v", err)
				}
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

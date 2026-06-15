package mailer

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBuildMessageFormat(t *testing.T) {
	out := string(buildMessage(Message{
		From: "no-reply@budgie.example", To: "alice@dest.test",
		Subject: "Verify your email", Body: "Hello\nClick the link.",
	}))
	for _, want := range []string{
		"From: no-reply@budgie.example\r\n",
		"To: alice@dest.test\r\n",
		"Subject: Verify your email\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"@budgie.example>\r\n", // Message-ID host derived from From
		"\r\nHello\r\nClick the link.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("message missing %q:\n%s", want, out)
		}
	}
}

func TestNewFactory(t *testing.T) {
	if m, err := New(Config{Mode: "off"}); err != nil || m != nil {
		t.Fatalf("off should be nil mailer: m=%v err=%v", m, err)
	}
	if _, err := New(Config{Mode: "direct"}); err == nil {
		t.Fatal("direct without From should error")
	}
	if _, err := New(Config{Mode: "relay", From: "x@y.z"}); err == nil {
		t.Fatal("relay without host should error")
	}
	if _, err := New(Config{Mode: "bogus", From: "x@y.z"}); err == nil {
		t.Fatal("unknown mode should error")
	}
	if m, err := New(Config{Mode: "direct", From: "x@y.z"}); err != nil || m == nil {
		t.Fatalf("direct: %v %v", m, err)
	}
	if m, err := New(Config{Mode: "relay", From: "x@y.z", Host: "smtp.test"}); err != nil || m == nil {
		t.Fatalf("relay: %v %v", m, err)
	}
}

func TestRelaySendDeliversMessage(t *testing.T) {
	addr, received := fakeSMTP(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	m, err := New(Config{Mode: "relay", From: "no-reply@budgie.example", Host: host, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Send(context.Background(), Message{
		To: "bob@dest.test", Subject: "Hi", Body: "verify me",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case data := <-received:
		for _, want := range []string{"Subject: Hi", "To: bob@dest.test", "verify me"} {
			if !strings.Contains(data, want) {
				t.Errorf("delivered DATA missing %q:\n%s", want, data)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not deliver within timeout")
	}
}

// fakeSMTP is a minimal one-shot SMTP server that captures the DATA payload.
func fakeSMTP(t *testing.T) (addr string, received chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	rec := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 fake ESMTP\r\n")
		var data strings.Builder
		inData := false
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					fmt.Fprint(conn, "250 OK\r\n")
					rec <- data.String()
					continue
				}
				data.WriteString(line)
				continue
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				fmt.Fprint(conn, "250-fake\r\n250 OK\r\n")
			case strings.HasPrefix(cmd, "DATA"):
				fmt.Fprint(conn, "354 end with .\r\n")
				inData = true
			case strings.HasPrefix(cmd, "QUIT"):
				fmt.Fprint(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprint(conn, "250 OK\r\n")
			}
		}
	}()
	return ln.Addr().String(), rec
}

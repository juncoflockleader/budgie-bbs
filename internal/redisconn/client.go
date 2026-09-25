package redisconn

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Commander interface {
	Do(ctx context.Context, args ...any) (any, error)
}

type Client struct {
	mu       sync.Mutex
	addr     string
	username string
	password string
	db       int
	wait     time.Duration
	conn     net.Conn
	reader   *bufio.Reader
}

func NewClient(rawURL string) (*Client, error) {
	addr, username, password, db, err := parseRedisURL(rawURL)
	if err != nil {
		return nil, err
	}
	return &Client{
		addr:     addr,
		username: username,
		password: password,
		db:       db,
		wait:     5 * time.Second,
	}, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeLocked()
}

func (c *Client) Do(ctx context.Context, args ...any) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("redis: nil client")
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("redis: command is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureConnLocked(ctx); err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
	} else if c.wait > 0 {
		_ = c.conn.SetDeadline(time.Now().Add(c.wait))
	}
	if err := writeRedisCommand(c.conn, args...); err != nil {
		_ = c.closeLocked()
		return nil, err
	}
	reply, err := readRedisReply(c.reader)
	if err != nil {
		_ = c.closeLocked()
		return nil, err
	}
	return reply, nil
}

func (c *Client) ensureConnLocked(ctx context.Context) error {
	if c.conn != nil {
		return nil
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return err
	}
	c.conn = conn
	c.reader = bufio.NewReader(conn)
	if c.wait > 0 {
		_ = c.conn.SetDeadline(time.Now().Add(c.wait))
	}
	if c.password != "" {
		authArgs := []any{"AUTH"}
		if c.username != "" {
			authArgs = append(authArgs, c.username)
		}
		authArgs = append(authArgs, c.password)
		if err := c.doHandshakeLocked(authArgs...); err != nil {
			_ = c.closeLocked()
			return err
		}
	}
	if c.db > 0 {
		if err := c.doHandshakeLocked("SELECT", c.db); err != nil {
			_ = c.closeLocked()
			return err
		}
	}
	return nil
}

func (c *Client) doHandshakeLocked(args ...any) error {
	if err := writeRedisCommand(c.conn, args...); err != nil {
		return err
	}
	_, err := readRedisReply(c.reader)
	return err
}

func (c *Client) closeLocked() error {
	conn := c.conn
	c.conn = nil
	c.reader = nil
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func parseRedisURL(raw string) (addr, username, password string, db int, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", 0, fmt.Errorf("redis: URL or address is required")
	}
	if !strings.Contains(raw, "://") {
		return raw, "", "", 0, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", "", 0, err
	}
	if u.Scheme != "redis" {
		return "", "", "", 0, fmt.Errorf("redis: unsupported URL scheme %q", u.Scheme)
	}
	addr = u.Host
	if addr == "" {
		return "", "", "", 0, fmt.Errorf("redis: address is required")
	}
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}
	path := strings.Trim(strings.TrimSpace(u.Path), "/")
	if path != "" {
		parsed, parseErr := strconv.Atoi(path)
		if parseErr != nil || parsed < 0 {
			return "", "", "", 0, fmt.Errorf("redis: invalid database %q", path)
		}
		db = parsed
	}
	return addr, username, password, db, nil
}

func writeRedisCommand(w io.Writer, args ...any) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "*%d\r\n", len(args))
	for _, arg := range args {
		data, err := redisArgBytes(arg)
		if err != nil {
			return err
		}
		fmt.Fprintf(&buf, "$%d\r\n", len(data))
		buf.Write(data)
		buf.WriteString("\r\n")
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func redisArgBytes(arg any) ([]byte, error) {
	switch v := arg.(type) {
	case string:
		return []byte(v), nil
	case []byte:
		return v, nil
	case int:
		return []byte(strconv.Itoa(v)), nil
	case int64:
		return []byte(strconv.FormatInt(v, 10)), nil
	case time.Duration:
		return []byte(strconv.FormatInt(int64(v.Seconds()), 10)), nil
	default:
		return nil, fmt.Errorf("redis: unsupported argument type %T", arg)
	}
}

func readRedisReply(r *bufio.Reader) (any, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	switch prefix {
	case '+':
		line, err := readRedisLine(r)
		return string(line), err
	case '-':
		line, err := readRedisLine(r)
		if err != nil {
			return nil, err
		}
		return nil, errors.New(string(line))
	case ':':
		line, err := readRedisLine(r)
		if err != nil {
			return nil, err
		}
		return strconv.ParseInt(string(line), 10, 64)
	case '$':
		line, err := readRedisLine(r)
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(string(line))
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		data := make([]byte, n+2)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, err
		}
		if data[n] != '\r' || data[n+1] != '\n' {
			return nil, fmt.Errorf("redis: malformed bulk string")
		}
		return data[:n], nil
	case '*':
		line, err := readRedisLine(r)
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(string(line))
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil
		}
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			item, err := readRedisReply(r)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("redis: unsupported response prefix %q", prefix)
	}
}

func readRedisLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, fmt.Errorf("redis: malformed line")
	}
	return line[:len(line)-2], nil
}

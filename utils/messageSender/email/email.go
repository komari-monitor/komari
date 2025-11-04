package email

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/komari-monitor/komari/utils/messageSender/factory"
)

type EmailSender struct {
	Addition
}

// loginAuth 是一个更宽松的 SMTP 认证实现,支持更多 SMTP 服务器
// 它不会严格验证服务器主机名,从而兼容微软邮箱、网易邮箱等服务
type loginAuth struct {
	username string
	password string
	host     string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte{}, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		switch string(fromServer) {
		case "Username:", "User Name":
			return []byte(a.username), nil
		case "Password:", "password:":
			return []byte(a.password), nil
		default:
			// 某些服务器可能发送 base64 编码的提示
			// 尝试返回用户名或密码
			prompt := strings.ToLower(strings.TrimSpace(string(fromServer)))
			if strings.Contains(prompt, "user") {
				return []byte(a.username), nil
			}
			return []byte(a.password), nil
		}
	}
	return nil, nil
}

// plainAuthWithoutCheck 是一个不检查主机名的 PlainAuth 实现
// 用于解决某些 SMTP 服务器主机名与配置不匹配的问题
type plainAuthWithoutCheck struct {
	identity string
	username string
	password string
	host     string
}

func (a *plainAuthWithoutCheck) Start(server *smtp.ServerInfo) (string, []byte, error) {
	resp := []byte(a.identity + "\x00" + a.username + "\x00" + a.password)
	return "PLAIN", resp, nil
}

func (a *plainAuthWithoutCheck) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		return nil, fmt.Errorf("unexpected server challenge")
	}
	return nil, nil
}

func (e *EmailSender) GetName() string {
	return "email"
}

func (e *EmailSender) GetConfiguration() factory.Configuration {
	return &e.Addition
}

func (e *EmailSender) Init() error {
	return nil
}

func (e *EmailSender) Destroy() error {
	return nil
}

func (e *EmailSender) SendTextMessage(message, title string) error {

	if e.Addition.Host == "" || e.Addition.Sender == "" || e.Addition.Username == "" || e.Addition.Password == "" || e.Addition.Receiver == "" {
		return fmt.Errorf("email sending is not fully configured")
	}

	// 使用更宽松的认证方式,优先尝试 PLAIN,如果失败则尝试 LOGIN
	// 这样可以兼容更多的 SMTP 服务器,包括微软邮箱、网易邮箱等
	var auth smtp.Auth
	if e.Addition.UseLoginAuth {
		// 使用 LOGIN 认证(适用于某些旧的或特殊的 SMTP 服务器)
		auth = &loginAuth{
			username: e.Addition.Username,
			password: e.Addition.Password,
			host:     e.Addition.Host,
		}
	} else {
		// 使用不检查主机名的 PLAIN 认证(适用于大多数现代 SMTP 服务器)
		auth = &plainAuthWithoutCheck{
			identity: "",
			username: e.Addition.Username,
			password: e.Addition.Password,
			host:     e.Addition.Host,
		}
	}

	// Parse sender address (for MAIL FROM and header)
	var senderAddr string
	var senderHeader string
	if addr, err := mail.ParseAddress(e.Addition.Sender); err == nil {
		senderAddr = addr.Address
		senderHeader = addr.String()
	} else {
		// Fallback: use raw string
		senderAddr = e.Addition.Sender
		senderHeader = e.Addition.Sender
	}

	// Parse recipients (support comma-separated list)
	var rcptList []string
	var rcptHeaderParts []string
	if addrs, err := mail.ParseAddressList(e.Addition.Receiver); err == nil {
		for _, a := range addrs {
			rcptList = append(rcptList, a.Address)
			rcptHeaderParts = append(rcptHeaderParts, a.String())
		}
	} else {
		// Fallback simple split
		parts := strings.Split(e.Addition.Receiver, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if a, err := mail.ParseAddress(p); err == nil {
				rcptList = append(rcptList, a.Address)
				rcptHeaderParts = append(rcptHeaderParts, a.String())
			} else {
				rcptList = append(rcptList, p)
				rcptHeaderParts = append(rcptHeaderParts, p)
			}
		}
	}
	if len(rcptList) == 0 {
		return fmt.Errorf("no valid recipient address parsed")
	}

	// RFC 2047 encode subject if non-ASCII
	encodedSubject := mime.QEncoding.Encode("UTF-8", title)

	// Encode body as quoted-printable to be safe with UTF-8 on servers lacking 8BITMIME
	var bodyBuf bytes.Buffer
	qp := quotedprintable.NewWriter(&bodyBuf)
	if _, err := qp.Write([]byte(message)); err != nil {
		return fmt.Errorf("failed to encode body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return fmt.Errorf("failed to finalize body encoding: %w", err)
	}

	// Compose headers
	headers := []string{
		"To: " + strings.Join(rcptHeaderParts, ", "),
		"From: " + senderHeader,
		"Subject: " + encodedSubject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: quoted-printable",
		"Date: " + time.Now().Format(time.RFC1123Z),
		fmt.Sprintf("Message-ID: <%d@%s>", time.Now().UnixNano(), e.Addition.Host),
	}

	fullMsg := []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + bodyBuf.String())

	addr := e.Addition.Host + ":" + strconv.Itoa(e.Addition.Port)

	if e.Addition.UseSSL {
		// Use TLS. If port is 465, prefer implicit TLS. Otherwise, use STARTTLS.
		if e.Addition.Port == 465 {
			// Implicit TLS (SMTPS)
			tlsCfg := &tls.Config{ServerName: e.Addition.Host}
			conn, err := tls.Dial("tcp", addr, tlsCfg)
			if err != nil {
				return fmt.Errorf("failed to establish implicit TLS connection: %w", err)
			}
			defer conn.Close()

			c, err := smtp.NewClient(conn, e.Addition.Host)
			if err != nil {
				return fmt.Errorf("failed to create SMTP client over TLS: %w", err)
			}
			defer c.Close()

			if err = c.Auth(auth); err != nil {
				return fmt.Errorf("failed to authenticate: %w", err)
			}

			if err = c.Mail(senderAddr); err != nil {
				return fmt.Errorf("failed to set sender: %w", err)
			}
			for _, rcpt := range rcptList {
				if err = c.Rcpt(rcpt); err != nil {
					return fmt.Errorf("failed to add recipient %s: %w", rcpt, err)
				}
			}

			w, err := c.Data()
			if err != nil {
				return fmt.Errorf("failed to get data writer: %w", err)
			}
			if _, err = w.Write(fullMsg); err != nil {
				return fmt.Errorf("failed to write message: %w", err)
			}
			if err = w.Close(); err != nil {
				return fmt.Errorf("failed to close data writer: %w", err)
			}
			return c.Quit()
		} else {
			// STARTTLS
			c, err := smtp.Dial(addr)
			if err != nil {
				return fmt.Errorf("failed to dial SMTP server: %w", err)
			}
			defer c.Close()

			if err = c.StartTLS(&tls.Config{ServerName: e.Addition.Host}); err != nil {
				return fmt.Errorf("failed to start TLS: %w", err)
			}

			if err = c.Auth(auth); err != nil {
				return fmt.Errorf("failed to authenticate: %w", err)
			}

			if err = c.Mail(senderAddr); err != nil {
				return fmt.Errorf("failed to set sender: %w", err)
			}
			for _, rcpt := range rcptList {
				if err = c.Rcpt(rcpt); err != nil {
					return fmt.Errorf("failed to add recipient %s: %w", rcpt, err)
				}
			}

			w, err := c.Data()
			if err != nil {
				return fmt.Errorf("failed to get data writer: %w", err)
			}
			if _, err = w.Write(fullMsg); err != nil {
				return fmt.Errorf("failed to write message: %w", err)
			}
			if err = w.Close(); err != nil {
				return fmt.Errorf("failed to close data writer: %w", err)
			}

			return c.Quit()
		}
	} else {
		// Send without SSL/TLS (less secure). We still reuse the composed message and parsed addresses.
		return smtp.SendMail(
			addr,
			auth,
			senderAddr,
			rcptList,
			fullMsg,
		)
	}
}

// 确保实现了 IMessageSender 接口
var _ factory.IMessageSender = (*EmailSender)(nil)

package auth

import (
	"crypto/tls"
	"encoding/base64"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"yingce/backend/internal/kernel"
)

// smtpSessionTimeout 限制一次投递的总时长；没有读写期限时，465 端口误配 STARTTLS 会一直等欢迎语，
// 期间持有 emailCodeMu，后续所有验证码发送都会被卡住。
const smtpSessionTimeout = 45 * time.Second

type smtpStageError struct {
	stage string
	err   error
}

func (e *smtpStageError) Error() string { return "smtp " + e.stage + ": " + e.err.Error() }
func (e *smtpStageError) Unwrap() error { return e.err }

func smtpEncryption(setting EmailSettingValue) string {
	// 465 是隐式 TLS 端口，按 STARTTLS 连接只会超时；这里按端口纠正，其余端口尊重配置。
	if setting.Port == 465 && setting.Encryption != "none" {
		return "tls"
	}
	return setting.Encryption
}

func sendSMTPMail(setting EmailSettingValue, recipient string, mailItem OutgoingMail) (err error) {
	stage := "connect"
	defer func() {
		if err != nil {
			err = &smtpStageError{stage: stage, err: err}
		}
	}()
	address := net.JoinHostPort(setting.Host, strconv.Itoa(setting.Port))
	tlsConfig := &tls.Config{ServerName: setting.Host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: 12 * time.Second}
	deadline := time.Now().Add(smtpSessionTimeout)
	encryption := smtpEncryption(setting)
	var connection net.Conn
	if encryption == "tls" {
		stage = "connect_tls"
		connection, err = tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
	} else {
		connection, err = dialer.Dial("tcp", address)
	}
	if err != nil {
		return err
	}
	if err := connection.SetDeadline(deadline); err != nil {
		_ = connection.Close()
		return err
	}
	stage = "greeting"
	client, err := smtp.NewClient(connection, setting.Host)
	if err != nil {
		_ = connection.Close()
		return err
	}
	defer client.Close()
	if encryption == "starttls" {
		stage = "starttls"
		if err := client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if setting.Username != "" {
		stage = "auth"
		if err := client.Auth(smtp.PlainAuth("", setting.Username, setting.Password, setting.Host)); err != nil {
			return err
		}
	}
	stage = "mail_from"
	if err := client.Mail(setting.FromEmail); err != nil {
		return err
	}
	stage = "rcpt_to"
	if err := client.Rcpt(recipient); err != nil {
		return err
	}
	stage = "data"
	wc, err := client.Data()
	if err != nil {
		return err
	}
	stage = "write_body"
	if _, err := wc.Write(buildMIMEMessage(setting, recipient, mailItem, time.Now())); err != nil {
		_ = wc.Close()
		return err
	}
	stage = "finish_data"
	if err := wc.Close(); err != nil {
		return err
	}
	stage = "quit"
	return client.Quit()
}

// buildMIMEMessage 生成 multipart/alternative 邮件。Date 与 Message-ID 是 RFC 5322 必填头，
// 缺失时 QQ 邮箱、Gmail 等会提高垃圾邮件评分。
func buildMIMEMessage(setting EmailSettingValue, recipient string, mailItem OutgoingMail, now time.Time) []byte {
	from := mail.Address{Name: setting.FromName, Address: setting.FromEmail}
	domain := setting.Host
	if _, after, ok := strings.Cut(setting.FromEmail, "@"); ok && after != "" {
		domain = after
	}
	var b strings.Builder
	header := func(key, value string) { b.WriteString(key + ": " + value + "\r\n") }
	header("From", from.String())
	header("To", recipient)
	header("Subject", mime.QEncoding.Encode("UTF-8", mailItem.Subject))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", "<"+kernel.NewID()+"@"+domain+">")
	header("MIME-Version", "1.0")
	header("Auto-Submitted", "auto-generated")
	header("X-Auto-Response-Suppress", "All")
	if strings.TrimSpace(mailItem.HTML) == "" {
		header("Content-Type", "text/plain; charset=UTF-8")
		header("Content-Transfer-Encoding", "base64")
		b.WriteString("\r\n")
		b.WriteString(wrapBase64(mailItem.Text))
		return []byte(b.String())
	}
	boundary := "=_yc_" + kernel.NewID()
	header("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	for _, part := range []struct{ kind, body string }{{"text/plain", mailItem.Text}, {"text/html", mailItem.HTML}} {
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + part.kind + "; charset=UTF-8\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		b.WriteString(wrapBase64(part.body))
	}
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

func wrapBase64(value string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(value))
	var b strings.Builder
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n")
	return b.String()
}

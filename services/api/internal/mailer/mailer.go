// Package mailer 實作 SMTP Email 寄送，供後台推播擴充的「email」頻道使用。
// 契約：Config 全來自環境變數 SMTP_HOST/SMTP_PORT/SMTP_USER/SMTP_PASS/SMTP_FROM。
// 未設 Host 或 From 時 enabled()=false，Send 直接回 (0,0) no-op，不報錯。
package mailer

import (
	"context"
	"time"

	gomail "github.com/wneessen/go-mail"
)

// Config SMTP 設定，全來自環境變數（由 cmd/api/main.go 傳入）。
type Config struct {
	Host string
	Port int // 預設 587
	User string
	Pass string
	From string
}

func (c Config) enabled() bool {
	return c.Host != "" && c.From != ""
}

// Mailer 寄送 Email 的 client 封裝。
type Mailer struct {
	cfg Config
}

// NewMailer 建立 Mailer；Port 未設時預設 587。
func NewMailer(cfg Config) *Mailer {
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	return &Mailer{cfg: cfg}
}

// Send 寄送 htmlBody 給 to 名單，每個收件者各一封。
// enabled()=false（未設 SMTP_HOST/SMTP_FROM）時直接回 (0,0)，不報錯、不連線。
// 回傳 (sent, failed) 統計。
func (m *Mailer) Send(ctx context.Context, to []string, subject, htmlBody string) (sent, failed int) {
	if !m.cfg.enabled() || len(to) == 0 {
		return 0, 0
	}

	opts := []gomail.Option{
		gomail.WithPort(m.cfg.Port),
		gomail.WithTimeout(15 * time.Second),
		gomail.WithTLSPolicy(gomail.TLSMandatory), // STARTTLS
	}
	if m.cfg.User != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
			gomail.WithUsername(m.cfg.User),
			gomail.WithPassword(m.cfg.Pass),
		)
	}

	client, err := gomail.NewClient(m.cfg.Host, opts...)
	if err != nil {
		return 0, len(to)
	}
	defer client.Close()

	for _, addr := range to {
		if addr == "" {
			failed++
			continue
		}

		msg := gomail.NewMsg()
		if err := msg.From(m.cfg.From); err != nil {
			failed++
			continue
		}
		if err := msg.To(addr); err != nil {
			failed++
			continue
		}
		msg.Subject(subject)
		msg.SetBodyString(gomail.TypeTextHTML, htmlBody)

		if err := client.DialAndSendWithContext(ctx, msg); err != nil {
			failed++
			continue
		}
		sent++
	}

	return sent, failed
}

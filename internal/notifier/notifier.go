package notifier

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	secretbox "w8nc/internal/crypto"
	"w8nc/internal/models"
	"w8nc/internal/socks5"
)

type Notifier struct {
	BinPath            string
	ProviderConfigPath string
	Timeout            time.Duration
	Secrets            *secretbox.SecretBox
}

type BinaryStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func New(binPath, providerConfigPath string, timeout time.Duration, secrets *secretbox.SecretBox) *Notifier {
	return &Notifier{BinPath: binPath, ProviderConfigPath: providerConfigPath, Timeout: timeout, Secrets: secrets}
}

func (n *Notifier) Check(ctx context.Context) BinaryStatus {
	if _, err := os.Stat(n.BinPath); err != nil {
		return BinaryStatus{Status: "missing", Message: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, n.BinPath, "-version")
	if output, err := cmd.CombinedOutput(); err != nil {
		return BinaryStatus{Status: "error", Message: strings.TrimSpace(string(output) + " " + err.Error())}
	}
	return BinaryStatus{Status: "ok"}
}

func (n *Notifier) Send(ctx context.Context, settings models.NotificationSettings, message string) error {
	if !settings.TelegramEnabled {
		return fmt.Errorf("telegram notifications are disabled")
	}
	if settings.TelegramAPIKeyEncrypted == nil || settings.TelegramChatID == nil || *settings.TelegramChatID == "" {
		return fmt.Errorf("telegram token and chat id must be configured")
	}
	if n.Secrets == nil {
		return fmt.Errorf("secret encryption is not configured")
	}
	token, err := n.telegramToken(settings)
	if err != nil {
		return err
	}
	if err := n.writeProviderConfig(token, *settings.TelegramChatID, settings.TelegramParseMode); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, n.Timeout)
	defer cancel()
	proxyURL, err := n.proxyURL(settings.Proxy)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, n.BinPath, CommandArgs(n.ProviderConfigPath, proxyURL)...)
	cmd.Stdin = strings.NewReader(message)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("notify failed: %s %s", strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (n *Notifier) SendScreenshot(ctx context.Context, settings models.NotificationSettings, urlValue string, imagePath string) error {
	if !settings.TelegramEnabled {
		return fmt.Errorf("telegram notifications are disabled")
	}
	if settings.TelegramChatID == nil || *settings.TelegramChatID == "" {
		return fmt.Errorf("telegram chat id must be configured")
	}
	token, err := n.telegramToken(settings)
	if err != nil {
		return err
	}
	image, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("screenshot image could not be opened")
	}
	defer image.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chat_id", *settings.TelegramChatID); err != nil {
		return err
	}
	if err := writer.WriteField("caption", telegramCaption("Screenshot of "+urlValue)); err != nil {
		return err
	}
	part, err := writer.CreateFormFile("photo", filepath.Base(imagePath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, image); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	client, err := n.telegramHTTPClient(settings)
	if err != nil {
		return err
	}
	requestURL := "https://api.telegram.org/bot" + token + "/sendPhoto"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, &body)
	if err != nil {
		return fmt.Errorf("telegram screenshot request could not be created")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram screenshot upload failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = resp.Status
		}
		return fmt.Errorf("telegram screenshot upload failed: %s", message)
	}
	return nil
}

func CommandArgs(providerConfigPath string, proxyURL string) []string {
	args := []string{"-provider", "telegram", "-provider-config", providerConfigPath, "-bulk", "-silent"}
	if proxyURL != "" {
		args = append(args, "-proxy", proxyURL)
	}
	return args
}

func (n *Notifier) proxyURL(proxy models.ProxyConfig) (string, error) {
	if !proxy.Enabled {
		return "", nil
	}
	if proxy.Address == "" {
		return "", fmt.Errorf("SOCKS5 proxy address is not configured")
	}
	password, err := n.proxyPassword(proxy)
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "socks5", Host: proxy.Address}
	if proxy.Username != "" {
		u.User = url.UserPassword(proxy.Username, password)
	}
	return u.String(), nil
}

func (n *Notifier) telegramToken(settings models.NotificationSettings) (string, error) {
	if settings.TelegramAPIKeyEncrypted == nil || *settings.TelegramAPIKeyEncrypted == "" {
		return "", fmt.Errorf("telegram token and chat id must be configured")
	}
	if n.Secrets == nil {
		return "", fmt.Errorf("secret encryption is not configured")
	}
	token, err := n.Secrets.Decrypt(*settings.TelegramAPIKeyEncrypted)
	if err != nil {
		return "", fmt.Errorf("telegram token could not be decrypted")
	}
	return token, nil
}

func (n *Notifier) telegramHTTPClient(settings models.NotificationSettings) (*http.Client, error) {
	client := &http.Client{Timeout: n.Timeout}
	if !settings.Proxy.Enabled {
		return client, nil
	}
	if settings.Proxy.Address == "" {
		return nil, fmt.Errorf("SOCKS5 proxy address is not configured")
	}
	password, err := n.proxyPassword(settings.Proxy)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := socks5.Dialer{
		ProxyAddress: settings.Proxy.Address,
		Username:     settings.Proxy.Username,
		Password:     password,
		Timeout:      n.Timeout,
	}
	transport.DialContext = dialer.DialContext
	client.Transport = transport
	return client, nil
}

func (n *Notifier) proxyPassword(proxy models.ProxyConfig) (string, error) {
	if proxy.Username == "" {
		return "", nil
	}
	if proxy.Password != "" && proxy.Password != "********" {
		return proxy.Password, nil
	}
	if proxy.PasswordEncrypted == nil || *proxy.PasswordEncrypted == "" {
		return "", fmt.Errorf("SOCKS5 proxy password is not configured")
	}
	if n.Secrets == nil {
		return "", fmt.Errorf("SOCKS5 proxy password cannot be decrypted")
	}
	password, err := n.Secrets.Decrypt(*proxy.PasswordEncrypted)
	if err != nil {
		return "", fmt.Errorf("SOCKS5 proxy password cannot be decrypted")
	}
	return password, nil
}

func (n *Notifier) writeProviderConfig(token, chatID, parseMode string) error {
	if parseMode == "" || parseMode == "None" {
		parseMode = ""
	}
	if err := os.MkdirAll(filepath.Dir(n.ProviderConfigPath), 0o700); err != nil {
		return err
	}
	content := fmt.Sprintf("telegram:\n  - id: \"default\"\n    telegram_api_key: %q\n    telegram_chat_id: %q\n    telegram_format: \"{{data}}\"\n", token, chatID)
	if parseMode != "" {
		content += fmt.Sprintf("    telegram_parsemode: %q\n", parseMode)
	}
	return os.WriteFile(n.ProviderConfigPath, []byte(content), 0o600)
}

func telegramCaption(value string) string {
	const limit = 1024
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-3]) + "..."
}

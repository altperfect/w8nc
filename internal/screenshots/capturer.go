package screenshots

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	secretbox "w8nc/internal/crypto"
	"w8nc/internal/models"
	"w8nc/internal/validation"
)

var ErrUnsupported = errors.New("unsupported screenshot request")

type Result struct {
	Path        string
	ContentType string
	Size        int64
}

type Capturer interface {
	Capture(ctx context.Context, attemptID string, endpoint models.Endpoint) (Result, error)
}

type ChromiumCapturer struct {
	ChromePath          string
	StoragePath         string
	Timeout             time.Duration
	ViewportWidth       int
	ViewportHeight      int
	AllowPrivateTargets bool
	Secrets             *secretbox.SecretBox
}

func (c *ChromiumCapturer) Capture(ctx context.Context, attemptID string, endpoint models.Endpoint) (Result, error) {
	if endpoint.HTTPMethod != "GET" {
		return Result{}, fmt.Errorf("%w: screenshots are supported only for GET endpoints", ErrUnsupported)
	}
	parsed, err := url.Parse(endpoint.URL)
	if err != nil {
		return Result{}, err
	}
	if !c.AllowPrivateTargets && validation.IsPrivateHost(parsed.Hostname()) {
		return Result{}, fmt.Errorf("private target is blocked")
	}
	if endpoint.Proxy.Enabled && endpoint.Proxy.Username != "" {
		return Result{}, fmt.Errorf("%w: authenticated SOCKS5 proxy screenshots are not supported", ErrUnsupported)
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	width := c.ViewportWidth
	if width <= 0 {
		width = 1365
	}
	height := c.ViewportHeight
	if height <= 0 {
		height = 900
	}
	storagePath := c.StoragePath
	if storagePath == "" {
		storagePath = "/app/data/screenshots"
	}
	if err := os.MkdirAll(storagePath, 0o700); err != nil {
		return Result{}, err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.WindowSize(width, height),
	)
	if c.ChromePath != "" {
		opts = append(opts, chromedp.ExecPath(c.ChromePath))
	}
	if endpoint.Proxy.Enabled && endpoint.Proxy.Address != "" {
		opts = append(opts, chromedp.ProxyServer("socks5://"+endpoint.Proxy.Address))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()
	captureCtx, cancelCapture := context.WithTimeout(browserCtx, timeout)
	defer cancelCapture()

	headers, err := c.headers(endpoint)
	if err != nil {
		return Result{}, err
	}
	actions := []chromedp.Action{network.Enable()}
	if len(headers) > 0 {
		actions = append(actions, network.SetExtraHTTPHeaders(headers))
	}
	var image []byte
	actions = append(actions,
		chromedp.Navigate(endpoint.URL),
		chromedp.Sleep(750*time.Millisecond),
		chromedp.FullScreenshot(&image, 90),
	)
	if err := chromedp.Run(captureCtx, actions...); err != nil {
		return Result{}, err
	}
	imagePath := filepath.Join(storagePath, attemptID+".png")
	if err := os.WriteFile(imagePath, image, 0o600); err != nil {
		return Result{}, err
	}
	return Result{Path: imagePath, ContentType: "image/png", Size: int64(len(image))}, nil
}

func (c *ChromiumCapturer) headers(endpoint models.Endpoint) (network.Headers, error) {
	headers := network.Headers{}
	for _, header := range endpoint.Headers {
		value := ""
		if header.Sensitive {
			if header.ValueEncrypted == nil || c.Secrets == nil {
				return nil, fmt.Errorf("sensitive header %q cannot be decrypted", header.Name)
			}
			decrypted, err := c.Secrets.Decrypt(*header.ValueEncrypted)
			if err != nil {
				return nil, fmt.Errorf("sensitive header %q cannot be decrypted", header.Name)
			}
			value = decrypted
		} else if header.ValuePlain != nil {
			value = *header.ValuePlain
		}
		headers[header.Name] = value
	}
	return headers, nil
}

func IsUnsupported(err error) bool {
	return errors.Is(err, ErrUnsupported)
}

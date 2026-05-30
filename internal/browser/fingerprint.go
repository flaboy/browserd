package browser

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

var ErrFingerprintInitFailed = fmt.Errorf("fingerprint init failed")

type FingerprintConfig struct {
	Seed                string
	Locale              string
	AcceptLanguage      string
	Timezone            string
	Platform            string
	UserAgent           string
	ViewportWidth       int64
	ViewportHeight      int64
	ScreenWidth         int64
	ScreenHeight        int64
	DeviceScaleFactor   float64
	HardwareConcurrency int64
	DeviceMemory        int64
	WebGLVendor         string
	WebGLRenderer       string
}

func FingerprintFromSeed(seed string) FingerprintConfig {
	seed = strings.TrimSpace(seed)
	sum := sha256.Sum256([]byte(seed))
	pick := func(offset int, size int) int {
		return int(binary.BigEndian.Uint32(sum[offset:offset+4]) % uint32(size))
	}
	locales := []struct {
		locale         string
		acceptLanguage string
		timezone       string
	}{
		{"zh-CN", "zh-CN,zh;q=0.9,en;q=0.8", "Asia/Shanghai"},
		{"en-US", "en-US,en;q=0.9", "America/New_York"},
		{"ja-JP", "ja-JP,ja;q=0.9,en;q=0.8", "Asia/Tokyo"},
	}
	viewports := []struct{ width, height int64 }{{1366, 768}, {1440, 900}, {1536, 864}, {1920, 1080}}
	hardware := []int64{4, 6, 8, 12}
	memory := []int64{4, 8, 16}
	webgl := []struct {
		vendor   string
		renderer string
	}{
		{"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) UHD Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)"},
		{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce GTX 1660 Direct3D11 vs_5_0 ps_5_0, D3D11)"},
		{"Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)"},
	}
	locale := locales[pick(0, len(locales))]
	viewport := viewports[pick(4, len(viewports))]
	webglProfile := webgl[pick(8, len(webgl))]
	return FingerprintConfig{
		Seed:                seed,
		Locale:              locale.locale,
		AcceptLanguage:      locale.acceptLanguage,
		Timezone:            locale.timezone,
		Platform:            "Win32",
		UserAgent:           "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
		ViewportWidth:       viewport.width,
		ViewportHeight:      viewport.height,
		ScreenWidth:         viewport.width,
		ScreenHeight:        viewport.height,
		DeviceScaleFactor:   1,
		HardwareConcurrency: hardware[pick(12, len(hardware))],
		DeviceMemory:        memory[pick(16, len(memory))],
		WebGLVendor:         webglProfile.vendor,
		WebGLRenderer:       webglProfile.renderer,
	}
}

func applyRuntimeOptions(ctx context.Context, fp FingerprintConfig, proxy ProxyConfig) error {
	if fp.Seed == "" {
		return fmt.Errorf("%w: fingerprint seed is required", ErrFingerprintInitFailed)
	}
	if proxy.HasAuth() {
		chromedp.ListenTarget(ctx, func(ev any) {
			switch e := ev.(type) {
			case *fetch.EventAuthRequired:
				go func() {
					_ = chromedp.Run(ctx, fetch.ContinueWithAuth(e.RequestID, &fetch.AuthChallengeResponse{
						Response: fetch.AuthChallengeResponseResponseProvideCredentials,
						Username: proxy.Username,
						Password: proxy.Password,
					}))
				}()
			case *fetch.EventRequestPaused:
				go func() {
					_ = chromedp.Run(ctx, fetch.ContinueRequest(e.RequestID))
				}()
			}
		})
	}

	actions := []chromedp.Action{
		emulation.SetUserAgentOverride(fp.UserAgent).
			WithAcceptLanguage(fp.AcceptLanguage).
			WithPlatform(fp.Platform).
			WithUserAgentMetadata(&emulation.UserAgentMetadata{
				Brands: []*emulation.UserAgentBrandVersion{
					{Brand: "Chromium", Version: "125"},
					{Brand: "Google Chrome", Version: "125"},
					{Brand: "Not.A/Brand", Version: "24"},
				},
				FullVersionList: []*emulation.UserAgentBrandVersion{
					{Brand: "Chromium", Version: "125.0.0.0"},
					{Brand: "Google Chrome", Version: "125.0.0.0"},
					{Brand: "Not.A/Brand", Version: "24.0.0.0"},
				},
				Platform:     "Windows",
				Architecture: "x86",
				Bitness:      "64",
				Mobile:       false,
			}),
		emulation.SetLocaleOverride().WithLocale(strings.ReplaceAll(fp.Locale, "-", "_")),
		emulation.SetTimezoneOverride(fp.Timezone),
		emulation.SetDeviceMetricsOverride(fp.ViewportWidth, fp.ViewportHeight, fp.DeviceScaleFactor, false).
			WithScreenWidth(fp.ScreenWidth).
			WithScreenHeight(fp.ScreenHeight),
		emulation.SetHardwareConcurrencyOverride(fp.HardwareConcurrency),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(fingerprintInitScript(fp)).Do(ctx)
			return err
		}),
	}
	if proxy.HasAuth() {
		actions = append([]chromedp.Action{fetch.Enable().WithHandleAuthRequests(true)}, actions...)
	}
	if err := chromedp.Run(ctx, actions...); err != nil {
		return fmt.Errorf("%w: %v", ErrFingerprintInitFailed, err)
	}
	return nil
}

func fingerprintInitScript(fp FingerprintConfig) string {
	payload, _ := json.Marshal(map[string]any{
		"platform":            fp.Platform,
		"deviceMemory":        fp.DeviceMemory,
		"hardwareConcurrency": fp.HardwareConcurrency,
		"webglVendor":         fp.WebGLVendor,
		"webglRenderer":       fp.WebGLRenderer,
		"seed":                fp.Seed,
	})
	return fmt.Sprintf(`(() => {
  const fp = %s;
  const define = (obj, key, value) => {
    try { Object.defineProperty(obj, key, { get: () => value, configurable: true }); } catch {}
  };
  define(Navigator.prototype, "platform", fp.platform);
  define(Navigator.prototype, "deviceMemory", fp.deviceMemory);
  define(Navigator.prototype, "hardwareConcurrency", fp.hardwareConcurrency);
  define(Navigator.prototype, "webdriver", undefined);
  const originalGetParameter = WebGLRenderingContext.prototype.getParameter;
  WebGLRenderingContext.prototype.getParameter = function(parameter) {
    if (parameter === 37445) return fp.webglVendor;
    if (parameter === 37446) return fp.webglRenderer;
    return originalGetParameter.call(this, parameter);
  };
  if (window.WebGL2RenderingContext) {
    const originalGetParameter2 = WebGL2RenderingContext.prototype.getParameter;
    WebGL2RenderingContext.prototype.getParameter = function(parameter) {
      if (parameter === 37445) return fp.webglVendor;
      if (parameter === 37446) return fp.webglRenderer;
      return originalGetParameter2.call(this, parameter);
    };
  }
  const canvasMark = (value) => value + ":" + fp.seed.slice(0, 8);
  const originalToDataURL = HTMLCanvasElement.prototype.toDataURL;
  HTMLCanvasElement.prototype.toDataURL = function(...args) { return canvasMark(originalToDataURL.apply(this, args)); };
  const originalGetChannelData = AudioBuffer.prototype.getChannelData;
  AudioBuffer.prototype.getChannelData = function(...args) {
    const data = originalGetChannelData.apply(this, args);
    if (data && data.length > 0) data[0] = data[0] + 0.0000001;
    return data;
  };
  window.RTCPeerConnection = undefined;
  window.webkitRTCPeerConnection = undefined;
  window.AudioContext = window.AudioContext || window.webkitAudioContext;
})()`, string(payload))
}

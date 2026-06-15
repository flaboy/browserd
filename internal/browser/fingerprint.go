package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"browserd/internal/fingerprint"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

var ErrFingerprintInitFailed = fmt.Errorf("fingerprint init failed")

type FingerprintConfig = fingerprint.Config

func FingerprintFromSeed(seed string) FingerprintConfig {
	return fingerprint.FromSeed(seed)
}

func applyRuntimeOptions(ctx context.Context, fp FingerprintConfig, proxy ProxyConfig) error {
	fp = fp.Normalized()
	if err := fp.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrFingerprintInitFailed, err)
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
				Platform:     fp.OS,
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
		"language":            fp.Locale,
		"languages":           fp.Languages,
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
  define(Navigator.prototype, "language", fp.language);
  define(Navigator.prototype, "languages", fp.languages);
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

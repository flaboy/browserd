package fingerprint

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidConfig = errors.New("invalid fingerprint config")

type Config struct {
	Seed                string   `json:"seed"`
	Locale              string   `json:"locale"`
	Languages           []string `json:"languages"`
	AcceptLanguage      string   `json:"acceptLanguage"`
	Timezone            string   `json:"timezone"`
	Platform            string   `json:"platform"`
	OS                  string   `json:"os"`
	UserAgent           string   `json:"userAgent"`
	ViewportWidth       int64    `json:"viewportWidth"`
	ViewportHeight      int64    `json:"viewportHeight"`
	ScreenWidth         int64    `json:"screenWidth"`
	ScreenHeight        int64    `json:"screenHeight"`
	DeviceScaleFactor   float64  `json:"deviceScaleFactor"`
	HardwareConcurrency int64    `json:"hardwareConcurrency"`
	DeviceMemory        int64    `json:"deviceMemory"`
	WebGLVendor         string   `json:"webglVendor"`
	WebGLRenderer       string   `json:"webglRenderer"`
}

func FromSeed(seed string) Config {
	seed = strings.TrimSpace(seed)
	sum := sha256.Sum256([]byte(seed))
	pick := func(offset int, size int) int {
		return int(binary.BigEndian.Uint32(sum[offset:offset+4]) % uint32(size))
	}
	locales := []struct {
		locale         string
		languages      []string
		acceptLanguage string
		timezone       string
	}{
		{"zh-CN", []string{"zh-CN", "zh", "en"}, "zh-CN,zh;q=0.9,en;q=0.8", "Asia/Shanghai"},
		{"en-US", []string{"en-US", "en"}, "en-US,en;q=0.9", "America/New_York"},
		{"ja-JP", []string{"ja-JP", "ja", "en"}, "ja-JP,ja;q=0.9,en;q=0.8", "Asia/Tokyo"},
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
	return Config{
		Seed:                seed,
		Locale:              locale.locale,
		Languages:           append([]string(nil), locale.languages...),
		AcceptLanguage:      locale.acceptLanguage,
		Timezone:            locale.timezone,
		Platform:            "Win32",
		OS:                  "Windows",
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

func (c Config) Normalized() Config {
	c.Seed = strings.TrimSpace(c.Seed)
	c.Locale = strings.TrimSpace(c.Locale)
	c.Languages = normalizeStringList(c.Languages)
	c.AcceptLanguage = strings.TrimSpace(c.AcceptLanguage)
	c.Timezone = strings.TrimSpace(c.Timezone)
	c.Platform = strings.TrimSpace(c.Platform)
	c.OS = strings.TrimSpace(c.OS)
	c.UserAgent = strings.TrimSpace(c.UserAgent)
	c.WebGLVendor = strings.TrimSpace(c.WebGLVendor)
	c.WebGLRenderer = strings.TrimSpace(c.WebGLRenderer)
	return c
}

func (c Config) Validate() error {
	c = c.Normalized()
	switch {
	case c.Seed == "":
		return fmt.Errorf("%w: seed is required", ErrInvalidConfig)
	case c.Locale == "":
		return fmt.Errorf("%w: locale is required", ErrInvalidConfig)
	case len(c.Languages) == 0:
		return fmt.Errorf("%w: languages is required", ErrInvalidConfig)
	case c.AcceptLanguage == "":
		return fmt.Errorf("%w: acceptLanguage is required", ErrInvalidConfig)
	case c.Timezone == "":
		return fmt.Errorf("%w: timezone is required", ErrInvalidConfig)
	case c.Platform == "":
		return fmt.Errorf("%w: platform is required", ErrInvalidConfig)
	case c.OS == "":
		return fmt.Errorf("%w: os is required", ErrInvalidConfig)
	case c.UserAgent == "":
		return fmt.Errorf("%w: userAgent is required", ErrInvalidConfig)
	case c.ViewportWidth <= 0 || c.ViewportHeight <= 0:
		return fmt.Errorf("%w: viewport dimensions are required", ErrInvalidConfig)
	case c.ScreenWidth <= 0 || c.ScreenHeight <= 0:
		return fmt.Errorf("%w: screen dimensions are required", ErrInvalidConfig)
	case c.DeviceScaleFactor <= 0:
		return fmt.Errorf("%w: deviceScaleFactor is required", ErrInvalidConfig)
	case c.HardwareConcurrency <= 0:
		return fmt.Errorf("%w: hardwareConcurrency is required", ErrInvalidConfig)
	case c.DeviceMemory <= 0:
		return fmt.Errorf("%w: deviceMemory is required", ErrInvalidConfig)
	case c.WebGLVendor == "":
		return fmt.Errorf("%w: webglVendor is required", ErrInvalidConfig)
	case c.WebGLRenderer == "":
		return fmt.Errorf("%w: webglRenderer is required", ErrInvalidConfig)
	default:
		return nil
	}
}

func normalizeStringList(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			normalized = append(normalized, value)
		}
	}
	return normalized
}

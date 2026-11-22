package i18n

import (
	"embed"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
)

const (
	LangZhCN = "zh-CN"
	LangZhTW = "zh-TW"
	LangEn   = "en"
)

// DefaultLang is used only when the request has no language preference.
// Init validates DEFAULT_LANGUAGE before the server starts accepting requests.
var DefaultLang = LangEn

//go:embed locales/*.yaml
var localeFS embed.FS

var (
	bundle     *i18n.Bundle
	localizers = make(map[string]*i18n.Localizer)
	mu         sync.RWMutex
	initOnce   sync.Once
	initErr    error
)

// Init initializes the i18n bundle and loads all translation files
func Init() error {
	initOnce.Do(func() {
		if configured := strings.TrimSpace(os.Getenv("DEFAULT_LANGUAGE")); configured != "" {
			if !IsSupported(configured) {
				initErr = fmt.Errorf("invalid DEFAULT_LANGUAGE %q: supported languages are zh-CN, zh-TW, en", configured)
				return
			}
			DefaultLang = normalizeLang(configured)
		}
		bundle = i18n.NewBundle(language.English)
		bundle.RegisterUnmarshalFunc("yaml", yaml.Unmarshal)

		// Load embedded translation files
		files := []string{"locales/zh-CN.yaml", "locales/zh-TW.yaml", "locales/en.yaml"}
		for _, file := range files {
			_, err := bundle.LoadMessageFileFS(localeFS, file)
			if err != nil {
				initErr = err
				return
			}
		}

		// Pre-create localizers for supported languages
		localizers[LangZhCN] = i18n.NewLocalizer(bundle, LangZhCN)
		localizers[LangZhTW] = i18n.NewLocalizer(bundle, LangZhTW)
		localizers[LangEn] = i18n.NewLocalizer(bundle, LangEn)

		// Set the TranslateMessage function in common package
		common.TranslateMessage = T
	})
	return initErr
}

// GetLocalizer returns a localizer for the specified language
func GetLocalizer(lang string) *i18n.Localizer {
	lang = normalizeLang(lang)

	mu.RLock()
	loc, ok := localizers[lang]
	mu.RUnlock()

	if ok {
		return loc
	}

	// Create new localizer for unknown language (fallback to default)
	mu.Lock()
	defer mu.Unlock()

	// Double-check after acquiring write lock
	if loc, ok = localizers[lang]; ok {
		return loc
	}

	loc = i18n.NewLocalizer(bundle, lang, DefaultLang)
	localizers[lang] = loc
	return loc
}

// T translates a message key using the language from gin context
func T(c *gin.Context, key string, args ...map[string]any) string {
	lang := GetLangFromContext(c)
	return Translate(lang, key, args...)
}

// Translate translates a message key for the specified language
func Translate(lang, key string, args ...map[string]any) string {
	loc := GetLocalizer(lang)

	config := &i18n.LocalizeConfig{
		MessageID: key,
	}

	if len(args) > 0 && args[0] != nil {
		config.TemplateData = args[0]
	}

	msg, err := loc.Localize(config)
	if err != nil {
		// Return key as fallback if translation not found
		return key
	}
	return msg
}

// userLangLoaderFunc is a function that loads user language from database/cache
// It's set by the model package to avoid circular imports
var userLangLoaderFunc func(userId int) string

// SetUserLangLoader sets the function to load user language (called from model package)
func SetUserLangLoader(loader func(userId int) string) {
	userLangLoaderFunc = loader
}

// StatedLang preserves an explicit user/request preference. An unsupported
// explicit language uses English, independently of the deployment default.
func StatedLang(c *gin.Context) string {
	if c == nil {
		return ""
	}
	userSetting, loaded := common.GetContextKeyType[dto.UserSetting](c, constant.ContextKeyUserSetting)
	if loaded {
		if strings.TrimSpace(userSetting.Language) != "" {
			return normalizeLang(userSetting.Language)
		}
	} else if userLangLoaderFunc != nil {
		if uid, ok := c.Get("id"); ok {
			if id, ok := uid.(int); ok && id > 0 {
				if lang := strings.TrimSpace(userLangLoaderFunc(id)); lang != "" {
					return normalizeLang(lang)
				}
			}
		}
	}
	if lang := ParseAcceptLanguage(c.GetHeader("Accept-Language")); lang != "" {
		return lang
	}
	if lang := c.GetString(string(constant.ContextKeyLanguage)); lang != "" && lang != DefaultLang {
		return normalizeLang(lang)
	}
	return ""
}

// GetLangFromContext uses DEFAULT_LANGUAGE only in the absence of a preference.
func GetLangFromContext(c *gin.Context) string {
	if lang := StatedLang(c); lang != "" {
		return lang
	}
	return DefaultLang
}

// ParseAcceptLanguage parses the Accept-Language header and returns the preferred language
func ParseAcceptLanguage(header string) string {
	firstLang := strings.SplitN(strings.SplitN(header, ",", 2)[0], ";", 2)[0]
	firstLang = strings.TrimSpace(firstLang)
	if firstLang == "" || firstLang == "*" {
		return ""
	}
	return normalizeLang(firstLang)
}

// normalizeLang normalizes language code to supported format
func normalizeLang(lang string) string {
	if canonical := supportedLang(lang); canonical != "" {
		return canonical
	}
	return LangEn
}

func supportedLang(lang string) string {
	lang = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(lang), "_", "-"))
	if lang == "zhcn" {
		lang = "zh-cn"
	} else if lang == "zhtw" {
		lang = "zh-tw"
	}
	tag, err := language.Parse(lang)
	if err != nil || lang == "" {
		return ""
	}
	base, _ := tag.Base()
	switch base.String() {
	case "zh":
		script, _ := tag.Script()
		if script.String() == "Hant" {
			return LangZhTW
		}
		return LangZhCN
	case "en":
		return LangEn
	default:
		return ""
	}
}

// SupportedLanguages returns a list of supported language codes
func SupportedLanguages() []string {
	return []string{LangZhCN, LangZhTW, LangEn}
}

// IsSupported checks if a language code is supported
func IsSupported(lang string) bool {
	return supportedLang(lang) != ""
}

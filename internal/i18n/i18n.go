// Package i18n provides English/Chinese catalogs for CLI, protocol, and terminal board.
package i18n

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/kiwh77/maw/internal/errx"
)

// Lang is a supported UI language.
type Lang string

const (
	En Lang = "en"
	Zh Lang = "zh"
)

var (
	mu       sync.RWMutex
	current  = En
	resolved bool
)

// Resolve picks language from MAW_LANG, then LC_ALL / LC_MESSAGES / LANG, default en.
func Resolve() Lang {
	mu.Lock()
	defer mu.Unlock()
	if resolved {
		return current
	}
	current = detect()
	resolved = true
	return current
}

// Set forces a language (tests / early main). Marks resolved.
func Set(lang Lang) {
	mu.Lock()
	defer mu.Unlock()
	if lang != En && lang != Zh {
		lang = En
	}
	current = lang
	resolved = true
}

// ResetForTest clears cached Resolve (tests only).
func ResetForTest() {
	mu.Lock()
	defer mu.Unlock()
	resolved = false
	current = En
}

// Current returns the active language without re-detecting env.
func Current() Lang {
	mu.RLock()
	if resolved {
		c := current
		mu.RUnlock()
		return c
	}
	mu.RUnlock()
	return Resolve()
}

func detect() Lang {
	for _, key := range []string{"MAW_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" || v == "C" || v == "POSIX" {
			continue
		}
		return Parse(v)
	}
	return En
}

// Parse maps locale tags like zh_CN.UTF-8 / en-US to Lang.
func Parse(tag string) Lang {
	t := strings.ToLower(strings.ReplaceAll(tag, "-", "_"))
	if strings.HasPrefix(t, "zh") {
		return Zh
	}
	if strings.HasPrefix(t, "en") {
		return En
	}
	return En
}

// T looks up key in the active catalog and formats with args (fmt.Sprintf).
func T(key string, args ...any) string {
	if key == "" {
		return ""
	}
	lang := Current()
	msg, ok := lookup(lang, key)
	if !ok {
		msg, ok = lookup(En, key)
	}
	if !ok {
		msg = key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

func lookup(lang Lang, key string) (string, bool) {
	var cat map[string]string
	switch lang {
	case Zh:
		cat = catalogZh
	default:
		cat = catalogEn
	}
	v, ok := cat[key]
	return v, ok
}

// E builds an errx.Error from message/next catalog keys (msgArgs format the message only).
func E(msgKey, nextKey string, msgArgs ...any) *errx.Error {
	return errx.New(T(msgKey, msgArgs...), T(nextKey))
}

// EN builds an errx.Error with separate format args for message and next.
func EN(msgKey string, msgArgs []any, nextKey string, nextArgs ...any) *errx.Error {
	return errx.New(T(msgKey, msgArgs...), T(nextKey, nextArgs...))
}

// EW wraps an error with translated message/next keys (msgArgs format the message only).
func EW(msgKey, nextKey string, err error, msgArgs ...any) *errx.Error {
	return errx.Wrap(T(msgKey, msgArgs...), T(nextKey), err)
}

// EWN wraps with separate next format args.
func EWN(msgKey string, msgArgs []any, nextKey string, err error, nextArgs ...any) *errx.Error {
	return errx.Wrap(T(msgKey, msgArgs...), T(nextKey, nextArgs...), err)
}

// StatusLabel returns the localized label for a task status code.
func StatusLabel(code string) string {
	return T("status." + code)
}

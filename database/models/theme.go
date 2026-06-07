package models

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

const (
	ThemeConfigurationManaged  = "managed"
	ThemeConfigurationRaw      = "raw"
	ThemeConfigurationRedirect = "redirect"
)

// Theme represents a komari theme information
type Theme struct {
	Name          string        `json:"name"`          // 主题名称
	Short         string        `json:"short"`         // 短名称，用作文件夹名
	Description   string        `json:"description"`   // 主题描述
	Version       string        `json:"version"`       // 版本号
	Author        string        `json:"author"`        // 作者
	URL           string        `json:"url"`           // 主题URL
	Preview       string        `json:"preview"`       // 预览图片相对路径
	Configuration Configuration `json:"configuration"` // 声明配置项
}

type Configuration struct {
	Type string `json:"type"` // managed raw redirect
	Icon string `json:"icon"` // 图标
	Name any    `json:"name"`
	Data any    `json:"data"` // 配置数据
}

type ManagedThemeConfigurationItem struct {
	Key      string `json:"key"`
	Name     any    `json:"name"`
	Required bool   `json:"required"`
	Type     string `json:"type"` // string number select switch title
	Options  string `json:"options"`
	Default  any    `json:"default"`
	Help     any    `json:"help"`
}

type ThemeConfiguration struct {
	Short string `json:"short" gorm:"primaryKey;unique;not null"`
	Data  string `json:"data" gorm:"type:longtext" default:"{}"`
}

func (t Theme) ConfigurationType() string {
	typ := strings.ToLower(strings.TrimSpace(t.Configuration.Type))
	if typ == "" {
		return ThemeConfigurationManaged
	}
	return typ
}

func (t Theme) RawHTML() (string, bool) {
	if t.ConfigurationType() != ThemeConfigurationRaw {
		return "", false
	}
	return configurationDataString(t.Configuration.Data)
}

func (t Theme) RedirectTarget() (string, bool) {
	if t.ConfigurationType() != ThemeConfigurationRedirect {
		return "", false
	}
	return NormalizeThemeRedirectTarget(t.Configuration.Data)
}

func (t Theme) ValidateConfiguration() error {
	switch t.ConfigurationType() {
	case ThemeConfigurationManaged:
		return nil
	case ThemeConfigurationRaw:
		html, ok := t.RawHTML()
		if !ok || strings.TrimSpace(html) == "" {
			return fmt.Errorf("raw 类型主题需要在 configuration.data 中提供 HTML 字符串")
		}
		return nil
	case ThemeConfigurationRedirect:
		if _, ok := t.RedirectTarget(); !ok {
			return fmt.Errorf("redirect 类型主题需要在 configuration.data 中提供站内相对路径")
		}
		return nil
	default:
		return fmt.Errorf("不支持的主题类型: %s", t.Configuration.Type)
	}
}

func NormalizeThemeRedirectTarget(data any) (string, bool) {
	target, ok := configurationDataString(data)
	if !ok {
		return "", false
	}

	target = strings.TrimSpace(target)
	if target == "" || strings.Contains(target, "\\") || strings.HasPrefix(target, "//") {
		return "", false
	}

	parsed, err := url.Parse(target)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return "", false
	}

	cleanInputPath := parsed.Path
	if strings.HasPrefix(cleanInputPath, "/") {
		cleanInputPath = strings.TrimLeft(cleanInputPath, "/")
	} else {
		for strings.HasPrefix(cleanInputPath, "../") {
			cleanInputPath = strings.TrimPrefix(cleanInputPath, "../")
		}
	}

	for _, segment := range strings.Split(cleanInputPath, "/") {
		if segment == ".." {
			return "", false
		}
	}

	cleanPath := cleanInputPath
	if cleanPath == "" {
		cleanPath = "/"
	} else {
		cleanPath = path.Clean(cleanPath)
		if cleanPath == "." {
			cleanPath = "/"
		} else {
			cleanPath = "/" + strings.TrimPrefix(cleanPath, "/")
		}
	}

	normalized := url.URL{
		Path:     cleanPath,
		RawQuery: parsed.RawQuery,
		Fragment: parsed.Fragment,
	}
	return normalized.String(), true
}

func configurationDataString(data any) (string, bool) {
	value, ok := data.(string)
	return value, ok
}

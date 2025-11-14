package models

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
	Type string `json:"type"` // managed
	Icon string `json:"icon"` // 图标
	Name string `json:"name"`
	Data any    `json:"data"` // 配置数据
}

type ManagedThemeConfigurationItem struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Type     string `json:"type"` // string number select switch title
	Options  string `json:"options"`
	Default  any    `json:"default"`
	Help     string `json:"help"`
}

type ThemeConfiguration struct {
	Short string `json:"short" gorm:"primaryKey;unique;not null"`
	Data  string `json:"data" gorm:"type:longtext" default:"{}"`
}

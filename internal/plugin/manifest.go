package plugin

type Manifest struct {
	Name         any               `json:"name"` // string | map[string]string
	Id           string            `json:"id"`
	Version      string            `json:"version"`
	Description  any               `json:"description"` // string | map[string]string
	Author       string            `json:"author"`
	License      string            `json:"license"`
	Dependencies map[string]string `json:"dependencies"`
	Config       []Config          `json:"config"`
	Url          string            `json:"url"`
	//Permissions  []string          `json:"permissions"`
}

type Config struct {
	Name        any      `json:"name"` // string | map[string]string
	Type        string   `json:"type"`
	Default     any      `json:"default"`
	Options     []string `json:"options"`     // 仅在 type 为 select 时有效
	Description any      `json:"description"` // string | map[string]string
	Validation  string   `json:"validation"`  // 用于前端验证的正则表达式或js函数
}

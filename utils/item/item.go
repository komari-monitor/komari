package item

import "reflect"

type Item struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Type     string `json:"type"`
	Options  string `json:"options"`
	Default  string `json:"default"`
	Help     string `json:"help"`
}

func Parse(v any) []Item {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}

	var allowTypes = []string{"option", "richtext"}

	var items []Item
	for i := 0; i < val.NumField(); i++ {
		field := val.Type().Field(i)
		typ := field.Tag.Get("type")
		if !contains(allowTypes, typ) {
			typ = field.Type.Name()
		}
		item := Item{
			Name:     field.Tag.Get("json"),
			Required: field.Tag.Get("required") == "true",
			Type:     typ,
			Options:  field.Tag.Get("options"),
			Default:  field.Tag.Get("default"),
			Help:     field.Tag.Get("help"),
		}
		if item.Type == "" {
			item.Type = "string"
		}
		items = append(items, item)
	}
	return items
}

// contains reports whether s is present in slice.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

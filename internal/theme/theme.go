package theme

import "strings"

type Definition struct {
	Name   string
	Label  string
	Accent string
}

const DefaultName = "purple"

var definitions = []Definition{
	{Name: "purple", Label: "Purple", Accent: "213"},
	{Name: "blue", Label: "Blue", Accent: "#1971c2"},
	{Name: "green", Label: "Green", Accent: "#2f9e44"},
	{Name: "teal", Label: "Teal", Accent: "#0ca678"},
	{Name: "amber", Label: "Amber", Accent: "#f59f00"},
}

var definitionsByName = func() map[string]Definition {
	m := make(map[string]Definition, len(definitions))
	for _, def := range definitions {
		m[def.Name] = def
	}
	return m
}()

func Definitions() []Definition {
	out := make([]Definition, len(definitions))
	copy(out, definitions)
	return out
}

func Default() Definition {
	def, _ := Lookup(DefaultName)
	return def
}

func Lookup(name string) (Definition, bool) {
	def, ok := definitionsByName[Normalize(name)]
	return def, ok
}

func Label(name string) string {
	if def, ok := Lookup(name); ok {
		return def.Label
	}
	return Default().Label
}

func Normalize(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func Resolve(envTheme, savedTheme string) string {
	if envTheme = Normalize(envTheme); envTheme != "" {
		if _, ok := Lookup(envTheme); ok {
			return envTheme
		}
		return DefaultName
	}
	if savedTheme = Normalize(savedTheme); savedTheme != "" {
		if _, ok := Lookup(savedTheme); ok {
			return savedTheme
		}
	}
	return DefaultName
}

func Next(name string) string {
	normalized := Normalize(name)
	for i, def := range definitions {
		if def.Name == normalized {
			return definitions[(i+1)%len(definitions)].Name
		}
	}
	return DefaultName
}

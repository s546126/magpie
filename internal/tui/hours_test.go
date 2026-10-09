package tui

import (
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// A provider whose accounts follow DeepSeek's preset is marked off-peak
// on the Providers page. Any other schedule is marked hours.
func TestProvidersMarkHours(t *testing.T) {
	h, ok := provider.HoursPreset("deepseek-offpeak")
	if !ok {
		t.Fatal("no deepseek-offpeak preset")
	}
	key := "sk-deepseek-test"
	with := func(hours provider.Hours) string {
		p := provider.Provider{
			ID: "deepseek", Name: "DeepSeek", Key: key,
			AccountHours: map[string]provider.Hours{provider.KeyID(key): hours},
		}
		m := model{w: 160, h: 40, provs: []provider.Provider{p, {ID: "ollama", Name: "Ollama"}}}
		return m.viewProviders()
	}
	var deepseek, ollama string
	for _, l := range strings.Split(with(h), "\n") {
		switch {
		case strings.Contains(l, "DeepSeek"):
			deepseek = l
		case strings.Contains(l, "Ollama"):
			ollama = l
		}
	}
	if !strings.Contains(deepseek, "off-peak") {
		t.Fatalf("DeepSeek's row: %q", deepseek)
	}
	if strings.Contains(ollama, "off-peak") || strings.Contains(ollama, "hours") {
		t.Fatalf("Ollama's row: %q", ollama)
	}
	deepseek = ""
	custom := provider.Hours{Mode: provider.HoursActive, Windows: []string{"09:00-17:00"}}
	for _, l := range strings.Split(with(custom), "\n") {
		if strings.Contains(l, "DeepSeek") {
			deepseek = l
		}
	}
	if !strings.Contains(deepseek, "hours") || strings.Contains(deepseek, "off-peak") {
		t.Fatalf("a custom window: %q", deepseek)
	}
}

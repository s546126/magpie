package main

import (
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// magpie provider hours sets when a provider's keys take requests, and
// clear (or off alone) removes it. A window that is empty, and a preset
// that does not exist, change nothing.
func TestHoursCmd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	p := provider.Provider{
		ID: "deepseek", Name: "DeepSeek", Key: "sk-one", KeyName: "One",
		Chat: "https://api.deepseek.com/v1",
		Keys: []provider.KeyAccount{{Name: "Two", Key: "sk-two"}},
	}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := providerCmd([]string{"provider", "hours", "presets"}); err != nil {
		t.Fatal(err)
	}
	if err := providerCmd([]string{"provider", "hours", "deepseek", "all", "preset", "deepseek-offpeak"}); err != nil {
		t.Fatal(err)
	}
	got, err := provider.Find("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sk-one", "sk-two"} {
		h, ok := got.HoursOf(provider.KeyID(key))
		if !ok || h.Preset != "deepseek-offpeak" {
			t.Fatalf("%s: %+v %v", key, h, ok)
		}
	}
	if got.Key != "sk-one" || got.Off {
		t.Fatalf("the command changed the key: %+v", got)
	}
	if err := providerCmd([]string{"provider", "hours", "deepseek"}); err != nil {
		t.Fatal(err)
	}
	if err := providerCmd([]string{"provider", "hours", "deepseek", "Two", "active", "09:00-09:00"}); err == nil {
		t.Fatal("an empty window was taken")
	}
	if h, ok := mustHours(t, "sk-two"); !ok || h.Preset != "deepseek-offpeak" {
		t.Fatalf("a refused window replaced the preset: %+v %v", h, ok)
	}
	if err := providerCmd([]string{"provider", "hours", "deepseek", "all", "preset", "nope"}); err == nil {
		t.Fatal("an unknown preset was taken")
	}
	if err := providerCmd([]string{"provider", "hours", "deepseek", "One", "off", "22:00-06:00", "days=sat,sun"}); err != nil {
		t.Fatal(err)
	}
	h, ok := mustHours(t, "sk-one")
	if !ok || h.Mode != provider.HoursOff || h.Preset != "" || len(h.Days) != 2 {
		t.Fatalf("custom: %+v %v", h, ok)
	}
	if h2, ok := mustHours(t, "sk-two"); !ok || h2.Preset != "deepseek-offpeak" {
		t.Fatalf("the other key changed: %+v %v", h2, ok)
	}
	if err := providerCmd([]string{"provider", "hours", "deepseek", "One", "off"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := mustHours(t, "sk-one"); ok {
		t.Fatal("off alone left the hours")
	}
	if err := providerCmd([]string{"provider", "hours", "deepseek", "all", "clear"}); err != nil {
		t.Fatal(err)
	}
	got, _ = provider.Find("deepseek")
	if len(got.HoursSummary()) != 0 || got.Key != "sk-one" {
		t.Fatalf("clear: %+v", got)
	}
}

func mustHours(t *testing.T, key string) (provider.Hours, bool) {
	t.Helper()
	p, err := provider.Find("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	return p.HoursOf(provider.KeyID(key))
}

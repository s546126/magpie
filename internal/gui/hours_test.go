package gui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// The editor's save does not send the hours, which are set on their own.
// Saving the rest of the provider keeps them, and keeps the key.
func TestEditorSaveKeepsHours(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	p := provider.Provider{
		ID: "deepseek", Name: "DeepSeek", Chat: "https://api.deepseek.com/v1",
		Key: "sk-1", Keys: []provider.KeyAccount{{Name: "Spare", Key: "sk-2"}},
	}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	h, ok := provider.HoursPreset("deepseek-offpeak")
	if !ok {
		t.Fatal("no deepseek-offpeak preset")
	}
	if err := provider.SetAccountHours("deepseek", "all", h); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	w := httptest.NewRecorder()
	body := `{"id":"deepseek","name":"DeepSeek","chat":"https://api.deepseek.com/v1"}`
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/provider/save", strings.NewReader(body)))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	got, err := provider.Find("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "sk-1" || got.Off || len(got.Keys) != 1 || got.Keys[0].Key != "sk-2" {
		t.Fatalf("save changed the keys: %+v", got)
	}
	for _, key := range []string{"sk-1", "sk-2"} {
		hh, ok := got.HoursOf(provider.KeyID(key))
		if !ok || hh.Preset != "deepseek-offpeak" {
			t.Fatalf("%s lost its hours: %+v %v", key, hh, ok)
		}
	}
}

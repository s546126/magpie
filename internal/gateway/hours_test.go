package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// windowAround is an off window that contains now, so the account it is
// set on is closed at the moment the test runs, including when that
// window crosses midnight.
func windowAround(now time.Time) string {
	start, end := now.Add(-2*time.Hour), now.Add(2*time.Hour)
	return fmt.Sprintf("%02d:%02d-%02d:%02d", start.Hour(), start.Minute(), end.Hour(), end.Minute())
}

func savePlan(t *testing.T, up http.Handler, keys ...provider.KeyAccount) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	restingUntil.Lock()
	restingUntil.m = map[string]time.Time{}
	restingUntil.Unlock()
	srv := httptest.NewServer(up)
	t.Cleanup(srv.Close)
	p := provider.Provider{
		ID: "plan", Name: "Plan", Chat: srv.URL + "/v1", Models: []string{"m1"},
		Key: "k-personal", KeyName: "Personal", Keys: keys,
	}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
}

// The first key is inside an off window, so the request goes to the next
// key. The closed key is not called, not deleted and not switched off.
// With the hours removed it is used again. The routing trace says it was
// left out, and until when.
func TestHoursSkip(t *testing.T) {
	up := &byKey{}
	savePlan(t, up, provider.KeyAccount{Name: "Team", Key: "k-team"})
	personal := provider.KeyID("k-personal")
	h := provider.Hours{Mode: provider.HoursOff, Windows: []string{windowAround(time.Now())}}
	if err := provider.SetAccountHours("plan", personal, h); err != nil {
		t.Fatal(err)
	}
	saved, err := provider.Find("plan")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Key != "k-personal" || saved.Off || len(saved.Keys) != 1 || saved.Keys[0].Key != "k-team" || saved.Keys[0].Off {
		t.Fatalf("hours changed the keys: %+v", saved)
	}

	s := New()
	send := func() (int, string) {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatReq)))
		return rec.Code, rec.Body.String()
	}
	code, body := send()
	if code != 200 || !strings.Contains(body, "from-k-team") || strings.Join(up.seen, ",") != "k-team" {
		t.Fatalf("%d %s tried %v", code, body, up.seen)
	}
	var closed []string
	for _, r := range s.Trace(t.Context(), 0, 0).Routes {
		for _, w := range r.Left {
			if w.Closed {
				closed = append(closed, w.Who+" "+w.ClosedText)
				if w.ClosedUntil == nil {
					t.Fatal("a window that opens again had no time")
				}
			}
		}
	}
	if len(closed) != 1 || !strings.Contains(closed[0], "Personal") {
		t.Fatalf("traced as closed: %v", closed)
	}

	if err := provider.ClearAccountHours("plan", personal); err != nil {
		t.Fatal(err)
	}
	up.seen = nil
	code, body = send()
	if code != 200 || !strings.Contains(body, "from-k-personal") || strings.Join(up.seen, ",") != "k-personal" {
		t.Fatalf("hours cleared: %d %s tried %v", code, body, up.seen)
	}
	saved, err = provider.Find("plan")
	if err != nil || saved.Key != "k-personal" || saved.Off {
		t.Fatalf("after: %+v %v", saved, err)
	}
}

// Every key is outside its hours all day: the caller is told, nothing is
// sent, and the key is unchanged. Retry-After is set when the window
// opens again.
func TestHoursSkipEveryAccount(t *testing.T) {
	up := &byKey{}
	savePlan(t, up)
	if err := provider.SetAccountHours("plan", "all", provider.Hours{
		Mode: provider.HoursOff, Windows: []string{"00:00-12:00", "12:00-00:00"},
	}); err != nil {
		t.Fatal(err)
	}
	s := New()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatReq)))
	if rec.Code != 429 || up.seen != nil {
		t.Fatalf("%d tried %v body %s", rec.Code, up.seen, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "outside its hours") {
		t.Fatalf("body %s", rec.Body)
	}
	if calls := s.Recent(); len(calls) == 0 || calls[0].Status != 429 || calls[0].Error != "every account outside its hours" {
		t.Fatalf("request log %+v", calls)
	}
	saved, err := provider.Find("plan")
	if err != nil || saved.Key != "k-personal" || saved.Off {
		t.Fatalf("the refusal changed the key: %+v %v", saved, err)
	}

	// a window that opens again says when, and still sends nothing
	end := time.Now().Add(30 * time.Minute)
	win := fmt.Sprintf("%02d:%02d-%02d:%02d", time.Now().Add(-time.Hour).Hour(), time.Now().Add(-time.Hour).Minute(), end.Hour(), end.Minute())
	if err := provider.SetAccountHours("plan", "all", provider.Hours{Mode: provider.HoursOff, Windows: []string{win}}); err != nil {
		t.Fatal(err)
	}
	up.seen = nil
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatReq)))
	n, _ := strconv.Atoi(rec.Header().Get("Retry-After"))
	if rec.Code != 429 || up.seen != nil || n < 60 || n > 40*60 {
		t.Fatalf("%d retry-after %d tried %v", rec.Code, n, up.seen)
	}
	if !strings.Contains(rec.Body.String(), "until") {
		t.Fatalf("body %s", rec.Body)
	}
}

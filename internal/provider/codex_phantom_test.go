package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

// ChatGPT /wham/usage returns a primary five-hour window for Pro and a
// Business Premium seat (plan_type self_serve_business_prolite) that have
// no five-hour allowance. It is often already full. Counting it marks the
// account used up and takes it out of rotation while its week has room.
// Plus and Team keep the window they actually have. A Pro account whose
// primary window is the week (seven days, as a real /wham/usage reply is)
// keeps that week, and is used up when the week is.
func TestCodexWindowsDropPhantomFiveHour(t *testing.T) {
	five := map[string]any{"used_percent": 100, "limit_window_seconds": 5 * 60 * 60, "reset_after_seconds": 3600}
	week := map[string]any{"used_percent": 41, "limit_window_seconds": 7 * 24 * 60 * 60, "reset_after_seconds": 86400}
	fullWeek := map[string]any{"used_percent": 100, "limit_window_seconds": 7 * 24 * 60 * 60, "reset_after_seconds": 86400}
	for _, c := range []struct {
		name  string
		plan  string
		rate  map[string]any
		names []string
		spent bool
	}{
		{"Pro", "pro", map[string]any{"primary_window": five, "secondary_window": week}, []string{"7 days"}, false},
		{"Business Premium", "self_serve_business_prolite", map[string]any{"primary_window": five, "secondary_window": week}, []string{"7 days"}, false},
		{"Plus", "plus", map[string]any{"primary_window": five, "secondary_window": week}, []string{"5 hours", "7 days"}, true},
		{"Team", "team", map[string]any{"primary_window": five, "secondary_window": week}, []string{"5 hours", "7 days"}, true},
		{"Pro Lite", "prolite", map[string]any{"primary_window": five, "secondary_window": week}, []string{"5 hours", "7 days"}, true},
		{"Business", "business", map[string]any{"primary_window": five, "secondary_window": week}, []string{"5 hours", "7 days"}, true},
		{"Pro, week as primary", "pro", map[string]any{"primary_window": fullWeek}, []string{"7 days"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"plan_type": c.plan, "rate_limit": c.rate})
			}))
			defer fake.Close()
			old := CodexBase
			CodexBase = fake.URL + "/backend-api/codex"
			defer func() { CodexBase = old }()

			plan, windows, _, _, _, err := codexWindows(context.Background(), "tok", "acct-1")
			if err != nil {
				t.Fatal(err)
			}
			if plan != c.plan {
				t.Fatalf("plan %q", plan)
			}
			var names []string
			for _, w := range windows {
				names = append(names, w.Name)
			}
			if !slices.Equal(names, c.names) {
				t.Fatalf("windows %v, want %v", names, c.names)
			}
			if got := usedUp(SubscriptionQuota{Windows: windows}, time.Now()); got != c.spent {
				t.Fatalf("used up %v, want %v", got, c.spent)
			}
		})
	}
}

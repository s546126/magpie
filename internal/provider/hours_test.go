package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// DeepSeek's published peak, read from the pricing page on 2026-10-09:
// 01:00–04:00 and 06:00–10:00 UTC, Monday–Friday. The windows are
// half-open, weekends are off-peak all day, and a Chinese public holiday
// is not in the preset (2026-10-01 is a Thursday and still peak).
func TestDeepSeekOffPeak(t *testing.T) {
	h, ok := HoursPreset(deepSeekOffPeakID)
	if !ok || h.Preset != deepSeekOffPeakID || h.Zone != "UTC" || h.Mode != HoursOff {
		t.Fatalf("%+v %v", h, ok)
	}
	utc := time.UTC
	monday := func(hour, min int) time.Time {
		return time.Date(2026, 1, 5, hour, min, 0, 0, utc) // Monday
	}
	for _, tc := range []struct {
		name string
		at   time.Time
		open bool
	}{
		{"just before the first peak", monday(0, 59), true},
		{"first peak starts", monday(1, 0), false},
		{"inside the first peak", monday(3, 59), false},
		{"04:00 is off-peak", monday(4, 0), true},
		{"the gap between peaks", monday(5, 30), true},
		{"second peak starts", monday(6, 0), false},
		{"inside the second peak", monday(9, 59), false},
		{"10:00 is off-peak", monday(10, 0), true},
		{"Saturday is off-peak", time.Date(2026, 1, 10, 2, 0, 0, 0, utc), true},
		{"Sunday is off-peak", time.Date(2026, 1, 11, 8, 0, 0, 0, utc), true},
		{"a holiday that is a weekday is still peak", time.Date(2026, 10, 1, 2, 0, 0, 0, utc), false},
	} {
		if got := h.OpenAt(tc.at, utc); got != tc.open {
			t.Errorf("%s (%s): open %v, want %v", tc.name, tc.at.Format(time.RFC3339), got, tc.open)
		}
	}
	// the preset's zone is UTC, so a local zone of UTC+8 does not move it:
	// 10:00 there is 02:00 UTC, inside Monday's peak
	plus8 := time.FixedZone("UTC+8", 8*3600)
	if h.OpenAt(time.Date(2026, 1, 5, 10, 0, 0, 0, plus8), plus8) {
		t.Fatal("the preset followed the local zone instead of UTC")
	}
	back, ok := h.NextOpen(monday(2, 30), utc)
	if !ok || !back.Equal(monday(4, 0)) {
		t.Fatalf("next after 02:30: %v %v", back, ok)
	}
	back, ok = h.NextOpen(monday(7, 0), utc)
	if !ok || !back.Equal(monday(10, 0)) {
		t.Fatalf("next after 07:00: %v %v", back, ok)
	}
	if _, ok := h.NextOpen(monday(4, 0), utc); ok {
		t.Fatal("an open hour named a next opening")
	}
}

// A window whose end is not after its start crosses midnight. An empty
// zone is read in the location the caller passes; a named zone is not.
// A zone that does not load, a window that does not parse, and a preset
// this magpie does not know are closed.
func TestHoursCrossMidnightAndZones(t *testing.T) {
	utc := time.UTC
	cross := Hours{Mode: HoursActive, Windows: []string{"22:00-06:00"}}
	at := func(day, hour, min int) time.Time {
		return time.Date(2026, 1, day, hour, min, 0, 0, utc)
	}
	for _, tc := range []struct {
		at   time.Time
		open bool
	}{
		{at(5, 21, 59), false},
		{at(5, 22, 0), true},
		{at(6, 0, 30), true},
		{at(6, 5, 59), true},
		{at(6, 6, 0), false},
		{at(6, 12, 0), false},
	} {
		if got := cross.OpenAt(tc.at, utc); got != tc.open {
			t.Errorf("%s open %v, want %v", tc.at.Format("15:04"), got, tc.open)
		}
	}
	// off on weekdays only: a weekend inside the window stays on
	weekdays := Hours{Mode: HoursOff, Windows: []string{"09:00-17:00"}, Days: []string{"mon", "tue", "wed", "thu", "fri"}}
	if weekdays.OpenAt(at(5, 10, 0), utc) { // Monday
		t.Fatal("Monday inside an off window was on")
	}
	if !weekdays.OpenAt(at(5, 18, 0), utc) {
		t.Fatal("Monday outside the window was off")
	}
	if !weekdays.OpenAt(at(10, 10, 0), utc) { // Saturday
		t.Fatal("Saturday was off for a weekday window")
	}

	plus8 := time.FixedZone("UTC+8", 8*3600)
	local := Hours{Mode: HoursActive, Windows: []string{"09:00-17:00"}}
	instant := time.Date(2026, 1, 5, 10, 0, 0, 0, plus8) // 02:00 UTC
	if !local.OpenAt(instant, plus8) {
		t.Fatal("10:00 in the local zone was outside 09:00-17:00")
	}
	if local.OpenAt(instant, utc) {
		t.Fatal("the same instant read in UTC was inside 09:00-17:00")
	}
	named := Hours{Mode: HoursActive, Zone: "UTC", Windows: []string{"09:00-17:00"}}
	if named.OpenAt(instant, plus8) {
		t.Fatal("a UTC window followed the local zone")
	}

	if (Hours{}).OpenAt(instant, utc) == false {
		t.Fatal("no schedule was closed")
	}
	for _, h := range []Hours{
		{Mode: HoursOff, Zone: "Not/AZone", Windows: []string{"00:00-01:00"}},
		{Mode: HoursActive, Windows: []string{"25:99"}},
		{Preset: "no-such-preset"},
	} {
		if h.OpenAt(instant, utc) {
			t.Errorf("%+v was open", h)
		}
	}
	allDay := Hours{Mode: HoursOff, Windows: []string{"00:00-12:00", "12:00-00:00"}}
	if allDay.OpenAt(instant, utc) {
		t.Fatal("a window covering the day was open")
	}
	if _, ok := allDay.NextOpen(instant, utc); ok {
		t.Fatal("a window covering the day named a next opening")
	}
}

// Naming the preset, even with a stale copy of its windows, follows the
// table's current hours.
func TestHoursPresetFollowsTheTable(t *testing.T) {
	utc := time.UTC
	peak := time.Date(2026, 1, 5, 2, 0, 0, 0, utc)
	before := time.Date(2026, 1, 5, 0, 15, 0, 0, utc)
	for _, h := range []Hours{
		{Preset: deepSeekOffPeakID},
		{Preset: deepSeekOffPeakID, Mode: HoursOff, Zone: "UTC", Windows: []string{"00:00-00:30"}, Days: []string{"mon"}},
	} {
		if h.OpenAt(peak, utc) {
			t.Errorf("%+v was open at peak", h)
		}
		if !h.OpenAt(before, utc) {
			t.Errorf("%+v was closed before the peak", h)
		}
	}
	if got := (Hours{Preset: deepSeekOffPeakID}).Text(); !strings.Contains(got, "01:00-04:00") || !strings.Contains(got, deepSeekOffPeakID) {
		t.Fatalf("text %q", got)
	}
}

func TestParseHours(t *testing.T) {
	h, clear, err := ParseHours([]string{"preset", deepSeekOffPeakID})
	if err != nil || clear || h.Preset != deepSeekOffPeakID || h.Zone != "UTC" {
		t.Fatalf("%+v clear %v err %v", h, clear, err)
	}
	h, clear, err = ParseHours([]string{"off"})
	if err != nil || !clear {
		t.Fatalf("off alone: %+v %v %v", h, clear, err)
	}
	h, clear, err = ParseHours([]string{"active", "22:00–06:00", "zone=UTC", "days=mon-fri"})
	if err != nil || clear || h.Mode != HoursActive || h.Zone != "UTC" || h.Windows[0] != "22:00-06:00" {
		t.Fatalf("%+v %v %v", h, clear, err)
	}
	if len(h.Days) != 5 || h.Days[0] != "mon" || h.Days[4] != "fri" {
		t.Fatalf("days %v", h.Days)
	}
	if got := h.Text(); !strings.Contains(got, "Mon-Fri") {
		t.Fatalf("text %q", got)
	}
	h, _, err = ParseHours([]string{"off", "6:00-7:00"})
	if err != nil || len(h.Windows) != 1 || h.Windows[0] != "06:00-07:00" {
		t.Fatalf("%+v %v", h, err)
	}
	if _, err := (Hours{Mode: HoursActive, Windows: []string{"09:00-09:00"}}).normalize(); err == nil {
		t.Fatal("a window with the same start and end was kept")
	}
	if _, _, err := ParseHours([]string{"preset", "nope"}); err == nil {
		t.Fatal("an unknown preset was taken")
	}
}

// A providers.json from before the field loads with no hours, and saving
// it does not add the field. Setting the preset writes it; clearing
// removes it. A hand-edited zone that does not load is kept, so the
// account stays closed rather than being turned on.
func TestHoursRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	dir := filepath.Join(home, ".config", "magpie")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"providers":[{"id":"deepseek","name":"DeepSeek","key":"sk-old","chat":"https://api.deepseek.com/v1"}]}`)
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Find("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.HoursOf(KeyID("sk-old")); ok {
		t.Fatal("a file from before the field had hours")
	}
	got.Name = "DeepSeek API"
	if err := Save(*got); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "accountHours") {
		t.Fatalf("saving an old file added hours:\n%s", b)
	}
	if !strings.Contains(string(b), "sk-old") {
		t.Fatalf("the key was not kept:\n%s", b)
	}

	h, _ := HoursPreset(deepSeekOffPeakID)
	if err := SetAccountHours("deepseek", "all", h); err != nil {
		t.Fatal(err)
	}
	again, err := Find("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	gotH, ok := again.HoursOf(KeyID("sk-old"))
	if !ok || gotH.Preset != deepSeekOffPeakID {
		t.Fatalf("after set: %+v %v", gotH, ok)
	}
	if again.Key != "sk-old" || again.Off {
		t.Fatalf("setting hours changed the key or the switch: %+v", again)
	}
	if gotH.OpenAt(time.Date(2026, 1, 5, 2, 0, 0, 0, time.UTC), time.UTC) {
		t.Fatal("the saved preset was open at peak")
	}
	b, _ = os.ReadFile(Path())
	if !strings.Contains(string(b), `"preset": "deepseek-offpeak"`) && !strings.Contains(string(b), `"preset":"deepseek-offpeak"`) {
		t.Fatalf("file:\n%s", b)
	}

	if err := ClearAccountHours("deepseek", "all"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(Path())
	if strings.Contains(string(b), "accountHours") {
		t.Fatalf("clear left the field:\n%s", b)
	}
	if again, err = Find("deepseek"); err != nil || again.Key != "sk-old" || again.Off {
		t.Fatalf("clear changed the provider: %+v %v", again, err)
	}
}

// A known preset stored with yesterday's windows is refreshed from the
// table when the file is read. A zone that does not load is left in the
// file. An entry that is not a schedule is dropped, and the file still loads.
func TestHoursMigrate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	dir := filepath.Join(home, ".config", "magpie")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := KeyID("sk-old")
	raw := `{
	  "providers": [{
	    "id": "deepseek", "name": "DeepSeek", "key": "sk-old", "chat": "https://api.deepseek.com/v1",
	    "accountHours": {
	      "` + id + `": {"mode": "off", "zone": "UTC", "windows": ["00:00-00:30"], "days": ["mon"], "preset": "deepseek-offpeak"},
	      "gone": {"mode": "sometimes"},
	      "typo": {"mode": "off", "zone": "Not/AZone", "windows": ["01:00-02:00"]}
	    }
	  }]
	}`
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Find("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	h, ok := got.HoursOf(id)
	if !ok || h.Windows[0] != "01:00-04:00" || !strings.Contains(strings.Join(h.Windows, ","), "06:00-10:00") {
		t.Fatalf("preset not refreshed: %+v %v", h, ok)
	}
	if _, ok := got.AccountHours["gone"]; ok {
		t.Fatal("an entry that is not a schedule was kept")
	}
	if got.AccountHours["typo"].Zone != "Not/AZone" {
		t.Fatalf("a bad zone was dropped or rewritten: %+v", got.AccountHours["typo"])
	}
	if got.AccountHours["typo"].OpenAt(time.Now(), time.UTC) {
		t.Fatal("a bad zone was open")
	}
	if err := Save(*got); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path())
	if !strings.Contains(string(b), "01:00-04:00") || !strings.Contains(string(b), "Not/AZone") {
		t.Fatalf("saved:\n%s", b)
	}
	if strings.Contains(string(b), "sometimes") {
		t.Fatalf("the empty entry was written back:\n%s", b)
	}
}

// Saving a signed-in account keeps the hours. The picks-only record a
// subscription is stored as would drop them if they were left out of it.
func TestAccountSaveKeepsHours(t *testing.T) {
	signIn(t)
	h, _ := HoursPreset(deepSeekOffPeakID)
	if err := SetAccountHours("codex", "Me@Example.com", h); err != nil {
		t.Fatal(err)
	}
	p, err := Find("codex")
	if err != nil {
		t.Fatal(err)
	}
	p.Family = "work"
	if err := Save(*p); err != nil {
		t.Fatal(err)
	}
	p, err = Find("codex")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := p.HoursOf("me@example.com")
	if !ok || got.Preset != deepSeekOffPeakID || p.Family != "work" {
		t.Fatalf("hours %+v ok %v family %q", got, ok, p.Family)
	}
	if p.Account == nil || p.Account.User != "me@example.com" {
		t.Fatalf("sign-in changed: %+v", p.Account)
	}
	b, _ := os.ReadFile(Path())
	if strings.Contains(string(b), "refresh_token") || strings.Contains(string(b), "acct-1") {
		t.Fatalf("the sign-in was written into providers.json:\n%s", b)
	}
}

// The next key that is inside its hours is the one a single send uses.
// A provider with no key and no account stays as it is.
func TestFirstOpenSkipsAClosedKey(t *testing.T) {
	closed := Hours{Mode: HoursOff, Windows: []string{"00:00-12:00", "12:00-00:00"}}
	p := Provider{
		ID: "deepseek", Name: "DeepSeek", Key: "k1", KeyName: "One",
		Keys:         []KeyAccount{{Name: "Two", Key: "k2"}},
		AccountHours: map[string]Hours{KeyID("k1"): closed},
	}
	q, ok := p.FirstOpen(time.Now())
	if !ok || q.Key != "k2" {
		t.Fatalf("open %v key %q", ok, q.Key)
	}
	p.AccountHours[KeyID("k2")] = closed
	if _, ok := p.FirstOpen(time.Now()); ok {
		t.Fatal("both keys closed and one was returned")
	}
	p.AccountHours = nil
	q, ok = p.FirstOpen(time.Now())
	if !ok || q.Key != "k1" {
		t.Fatalf("no hours: %v %q", ok, q.Key)
	}
	local := Provider{ID: "ollama", Name: "Ollama"}
	q, ok = local.FirstOpen(time.Now())
	if !ok || q.ID != "ollama" {
		t.Fatalf("local: %v %+v", ok, q)
	}
}

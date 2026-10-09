package provider

// Hours are when one account or key takes requests, by the clock. An
// account saved before this, and one with no hours, is on whenever it is
// switched on. The hours never delete it, never change its key or its
// sign-in, and never switch it off: routing passes over it while it is
// closed and uses it again when the window opens.
//
// The shape follows the warm-up times (settings' CodexWarmAts and
// CodexWarmAtOf): a time of day, "HH:MM", in a zone, not a crontab. A
// window is "HH:MM-HH:MM", half-open, and one whose end is not after its
// start crosses midnight.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/settings"
)

const (
	// HoursActive: the account takes requests only inside its windows.
	HoursActive = "active"
	// HoursOff: the account takes requests except inside its windows.
	HoursOff = "off"
)

// Hours is one account's or key's schedule. The zero value is no
// schedule. Mode is HoursActive or HoursOff. Zone is an IANA time zone
// the times are read in; empty is the machine's local zone. Windows are
// "HH:MM-HH:MM". Days, when set, are the weekdays the windows apply on
// ("mon"…"sun"); empty is every day. On a day not listed, HoursOff leaves
// the account on all day and HoursActive leaves it off all day.
//
// Preset, when set, names a built-in schedule (HoursPresets). The windows
// evaluated are that preset's current ones, so a change to the preset
// reaches every account that still names it. The other fields are the
// preset's as last saved, for a reader of the file.
type Hours struct {
	Mode    string   `json:"mode,omitempty"`
	Zone    string   `json:"zone,omitempty"`
	Windows []string `json:"windows,omitempty"`
	Days    []string `json:"days,omitempty"`
	Preset  string   `json:"preset,omitempty"`
}

// hoursPreset is one built-in schedule. The hours are what is evaluated;
// the note is what `magpie provider hours presets` prints.
type hoursPreset struct {
	id    string
	note  string
	hours Hours
}

// hoursPresets is the built-in schedules. DeepSeek's off-peak discount is
// the published peak, taken from
// https://api-docs.deepseek.com/quick_start/pricing on 2026-10-09: peak
// is 01:00–04:00 and 06:00–10:00 UTC, Monday through Friday, and every
// other hour is off-peak, weekends included. Chinese public holidays are
// also off-peak on that page and are not in this table: they change each
// year. Weekdays are the zone's (UTC), which during these peak hours is
// the same calendar day as Beijing.
var hoursPresets = []hoursPreset{
	{
		id: deepSeekOffPeakID,
		note: "DeepSeek's off-peak discount: the account is off during peak, " +
			"01:00-04:00 and 06:00-10:00 UTC, Monday-Friday, and on otherwise, " +
			"weekends included. From https://api-docs.deepseek.com/quick_start/pricing " +
			"(2026-10-09). Chinese public holidays are also off-peak there and are not in this preset.",
		hours: Hours{
			Mode:    HoursOff,
			Zone:    "UTC",
			Windows: []string{"01:00-04:00", "06:00-10:00"},
			Days:    []string{"mon", "tue", "wed", "thu", "fri"},
		},
	},
}

// deepSeekOffPeakID is the preset a DeepSeek account uses to take
// requests only at DeepSeek's off-peak rates.
const deepSeekOffPeakID = "deepseek-offpeak"

// HoursPreset is a built-in schedule by its id, with Preset set so saving
// it keeps following the table.
func HoursPreset(id string) (Hours, bool) {
	p, ok := hoursPresetBy(id)
	if !ok {
		return Hours{}, false
	}
	h := p.hours
	h.Preset = p.id
	return h, true
}

// HoursPresetNote is the sentence `magpie provider hours presets` prints
// for id, and whether there is such a preset.
func HoursPresetNote(id string) (string, bool) {
	p, ok := hoursPresetBy(id)
	if !ok {
		return "", false
	}
	return p.note, true
}

// HoursPresetIDs are the built-in schedules, in the order they are offered.
func HoursPresetIDs() []string {
	out := make([]string, len(hoursPresets))
	for i, p := range hoursPresets {
		out[i] = p.id
	}
	return out
}

func hoursPresetBy(id string) (hoursPreset, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, p := range hoursPresets {
		if p.id == id {
			return p, true
		}
	}
	return hoursPreset{}, false
}

// effective is the schedule that is evaluated: a known preset's current
// hours, otherwise h itself.
func (h Hours) effective() Hours {
	if p, ok := hoursPresetBy(h.Preset); ok {
		out := p.hours
		out.Preset = p.id
		return out
	}
	return h
}

// OpenAt says whether h takes requests at now. local is the zone an empty
// Zone is read in; nil is time.Local. A schedule with nothing to enforce
// is open. A named zone that does not load is closed, so a typo does not
// spend the account in the machine's zone by mistake.
func (h Hours) OpenAt(now time.Time, local *time.Location) bool {
	h = h.effective()
	if h.Mode != HoursActive && h.Mode != HoursOff {
		// a preset name this magpie doesn't know, with no windows of its
		// own, cannot be judged: closed, rather than always on
		return strings.TrimSpace(h.Preset) == ""
	}
	spans, ok := windowsOf(h.Windows)
	if !ok {
		// a mode with no readable window is closed: a hand-edited
		// "25:00" must not be read as always on
		if h.Mode == HoursActive || h.Mode == HoursOff {
			return false
		}
		return strings.TrimSpace(h.Preset) == ""
	}
	loc, ok := h.location(local)
	if !ok {
		return false
	}
	t := now.In(loc)
	applies := len(h.Days) == 0 || dayListed(h.Days, t.Weekday())
	inside := applies && inSpans(spans, minutes(t))
	if h.Mode == HoursActive {
		return inside
	}
	return !inside
}

// NextOpen is the next time at or after the minute after now that OpenAt
// is true, within eight days. The second result is false when it is
// already open, or when nothing in that span opens it.
func (h Hours) NextOpen(now time.Time, local *time.Location) (time.Time, bool) {
	if h.OpenAt(now, local) {
		return time.Time{}, false
	}
	loc, ok := h.effective().location(local)
	if !ok {
		return time.Time{}, false
	}
	t := now.In(loc).Truncate(time.Minute).Add(time.Minute)
	end := now.Add(8 * 24 * time.Hour)
	for !t.After(end) {
		if h.OpenAt(t, local) {
			return t, true
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, false
}

// Text is h as a person reads it: "off 01:00-04:00, 06:00-10:00 UTC, Mon-Fri".
func (h Hours) Text() string {
	h = h.effective()
	if h.Mode != HoursActive && h.Mode != HoursOff {
		if h.Preset != "" {
			return "preset " + h.Preset + " (unknown)"
		}
		return "on all day"
	}
	var b strings.Builder
	b.WriteString(h.Mode)
	b.WriteString(" ")
	b.WriteString(strings.Join(h.Windows, ", "))
	if h.Zone != "" {
		b.WriteString(" ")
		b.WriteString(h.Zone)
	} else {
		b.WriteString(" local")
	}
	if s := daysText(h.Days); s != "" {
		b.WriteString(", ")
		b.WriteString(s)
	}
	if h.Preset != "" {
		b.WriteString(" (")
		b.WriteString(h.Preset)
		b.WriteString(")")
	}
	return b.String()
}

func (h Hours) location(local *time.Location) (*time.Location, bool) {
	if h.Zone == "" {
		if local == nil {
			return time.Local, true
		}
		return local, true
	}
	loc, err := time.LoadLocation(h.Zone)
	if err != nil {
		return nil, false
	}
	return loc, true
}

// HoursGate is why an account or key is outside its hours.
type HoursGate struct {
	Back time.Time // when it next takes requests; zero when that isn't known
	Text string    // the schedule, as an error says it
}

// HoursGate reports whether ref (an account's name, or a key's KeyID) of
// p is outside its hours at now. An account with no hours is not.
func (p Provider) HoursGate(ref string, now time.Time) (HoursGate, bool) {
	h, ok := p.hoursFor(ref)
	if !ok || !h.scheduled() {
		return HoursGate{}, false
	}
	if h.OpenAt(now, time.Local) {
		return HoursGate{}, false
	}
	g := HoursGate{Text: h.Text()}
	if back, ok := h.NextOpen(now, time.Local); ok {
		g.Back = back
	}
	return g, true
}

// scheduled says h names a schedule, including one whose preset this
// magpie doesn't know.
func (h Hours) scheduled() bool {
	if strings.TrimSpace(h.Preset) != "" {
		return true
	}
	return h.Mode == HoursActive || h.Mode == HoursOff
}

// HoursOf is ref's schedule, and whether one is set. ref is an account's
// name or a key's KeyID.
func (p Provider) HoursOf(ref string) (Hours, bool) {
	h, ok := p.hoursFor(ref)
	if !ok || !h.scheduled() {
		return Hours{}, false
	}
	return h.effective(), true
}

func (p Provider) hoursFor(ref string) (Hours, bool) {
	m := p.AccountHours
	if m == nil {
		if s, ok := storedPicks(p.ID); ok {
			m = s.AccountHours
		}
	}
	if m == nil {
		return Hours{}, false
	}
	h, ok := m[accountKey(ref)]
	return h, ok
}

// FirstOpen is p when the account or key it would send on is inside its
// hours, or the next of its accounts or keys that is. The bool is false
// when every one is outside. A provider with no key and no account (a
// local server) is open. A key that is already open is returned as p, not
// rebuilt, so a caller that had no hours sees the same provider as before.
func (p Provider) FirstOpen(now time.Time) (Provider, bool) {
	if p.Account != nil {
		if _, shut := p.HoursGate(p.Account.User, now); !shut {
			return p, true
		}
		for _, q := range p.AlsoOn() {
			if _, shut := q.HoursGate(q.Account.User, now); !shut {
				return q, true
			}
		}
		return Provider{}, false
	}
	if p.Key == "" {
		return p, true
	}
	if _, shut := p.HoursGate(KeyID(p.Key), now); !shut {
		return p, true
	}
	for _, k := range p.KeysOn() {
		if KeyID(k.Key) == KeyID(p.Key) {
			continue
		}
		if _, shut := p.HoursGate(KeyID(k.Key), now); shut {
			continue
		}
		return p.WithKey(k), true
	}
	return Provider{}, false
}

// HoursSummary is the schedules p has, one line each, for the provider
// screen and `magpie provider show`. Empty when none are set.
func (p Provider) HoursSummary() []string {
	var out []string
	label := map[string]string{}
	for _, k := range p.KeyList() {
		if k.Name != "" {
			label[k.ID] = k.Name
		} else {
			label[k.ID] = k.Masked
		}
	}
	for _, r := range p.AccountRefs() {
		h, ok := p.hoursFor(r)
		if !ok || !h.scheduled() {
			continue
		}
		name := r
		if l, ok := label[r]; ok {
			name = l
		}
		out = append(out, name+" · "+h.Text())
	}
	return out
}

// HoursNote is a short mark for a provider list: "off-peak" when every
// schedule is DeepSeek's preset, "hours" when any other is set.
func (p Provider) HoursNote() string {
	var n int
	offpeak := true
	for _, r := range p.AccountRefs() {
		h, ok := p.hoursFor(r)
		if !ok || !h.scheduled() {
			continue
		}
		n++
		if h.effective().Preset != deepSeekOffPeakID {
			offpeak = false
		}
	}
	if n == 0 {
		return ""
	}
	if offpeak {
		return "off-peak"
	}
	return "hours"
}

// SetAccountHours sets the hours of ref, an account or key of id, or of
// every one when ref is "all". The hours are checked first; a bad window
// or zone changes nothing.
func SetAccountHours(id, ref string, h Hours) error {
	h, err := h.normalize()
	if err != nil {
		return err
	}
	return writeAccountHours(id, ref, h, false)
}

// ClearAccountHours removes the hours of ref, or of every account and key
// when ref is "all". The account stays switched on.
func ClearAccountHours(id, ref string) error {
	return writeAccountHours(id, ref, Hours{}, true)
}

func writeAccountHours(id, ref string, h Hours, clear bool) error {
	p, err := Find(id)
	if err != nil {
		return err
	}
	refs, err := hourRefs(*p, ref)
	if err != nil {
		return err
	}
	m := map[string]Hours{}
	for k, v := range p.AccountHours {
		m[k] = v
	}
	for _, r := range refs {
		key := accountKey(r)
		if clear {
			delete(m, key)
			continue
		}
		m[key] = h
	}
	p.AccountHours = m
	return Save(*p)
}

func hourRefs(p Provider, ref string) ([]string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("name the account or key, or all")
	}
	if strings.EqualFold(ref, "all") {
		refs := p.AccountRefs()
		if len(refs) == 0 {
			return nil, fmt.Errorf("%s has no account or key", p.Name)
		}
		return refs, nil
	}
	r, ok := p.accountRef(ref)
	if !ok {
		if p.Account == nil {
			return nil, fmt.Errorf("%s has no key %q", p.Name, ref)
		}
		return nil, fmt.Errorf("%s has no account %q", p.Name, ref)
	}
	return []string{r}, nil
}

// ParseHours reads a schedule as the CLI and the TUI take it.
//
//	clear | none | - | off
//	preset <id>
//	active <HH:MM-HH:MM>… [zone=<iana>] [days=<mon,tue|mon-fri>]
//	off <HH:MM-HH:MM>… [zone=<iana>] [days=<mon-fri>]
//
// "off" alone clears the schedule, as a cap's off does. "off" with a
// window is HoursOff. clear is true when the schedule is removed.
func ParseHours(args []string) (Hours, bool, error) {
	if len(args) == 0 {
		return Hours{}, false, fmt.Errorf("say clear, preset %s, active 09:00-18:00, or off 01:00-04:00", deepSeekOffPeakID)
	}
	switch strings.ToLower(args[0]) {
	case "clear", "none", "default", "-":
		if len(args) != 1 {
			return Hours{}, false, fmt.Errorf("clear takes nothing after it")
		}
		return Hours{}, true, nil
	case "off":
		if len(args) == 1 {
			return Hours{}, true, nil
		}
	case "active", "on":
	case "preset":
		if len(args) != 2 {
			return Hours{}, false, fmt.Errorf("preset takes its name: %s", strings.Join(HoursPresetIDs(), ", "))
		}
		h, ok := HoursPreset(args[1])
		if !ok {
			return Hours{}, false, fmt.Errorf("no preset %q — %s", args[1], strings.Join(HoursPresetIDs(), ", "))
		}
		return h, false, nil
	default:
		return Hours{}, false, fmt.Errorf("say clear, preset %s, active 09:00-18:00, or off 01:00-04:00", deepSeekOffPeakID)
	}
	h := Hours{Mode: HoursOff}
	if strings.EqualFold(args[0], "active") || strings.EqualFold(args[0], "on") {
		h.Mode = HoursActive
	}
	for _, a := range args[1:] {
		k, v, cut := strings.Cut(a, "=")
		if cut {
			switch strings.ToLower(k) {
			case "zone", "tz":
				h.Zone = strings.TrimSpace(v)
			case "days", "day":
				days, err := parseDays(v)
				if err != nil {
					return Hours{}, false, err
				}
				h.Days = days
			default:
				return Hours{}, false, fmt.Errorf("unknown %q — a window is HH:MM-HH:MM, or zone= or days=", a)
			}
			continue
		}
		h.Windows = append(h.Windows, a)
	}
	out, err := h.normalize()
	if err != nil {
		return Hours{}, false, err
	}
	return out, false, nil
}

// normalize is h as it is kept. A known preset is replaced by the table's
// current hours. An error is a window, zone or day the user typed that
// cannot be a schedule; the caller changes nothing.
func (h Hours) normalize() (Hours, error) {
	if p, ok := hoursPresetBy(h.Preset); ok {
		out := p.hours
		out.Preset = p.id
		h = out
	}
	h.Mode = strings.ToLower(strings.TrimSpace(h.Mode))
	if h.Mode != HoursActive && h.Mode != HoursOff {
		return Hours{}, fmt.Errorf("hours are active or off, not %q", h.Mode)
	}
	if len(h.Windows) == 0 {
		return Hours{}, fmt.Errorf("name a window, such as 09:00-18:00")
	}
	var wins []string
	seen := map[string]bool{}
	for _, w := range h.Windows {
		span, err := parseWindow(w)
		if err != nil {
			return Hours{}, err
		}
		s := span.text()
		if seen[s] {
			continue
		}
		seen[s] = true
		wins = append(wins, s)
	}
	slices.Sort(wins)
	h.Windows = wins
	if h.Zone != "" {
		loc, err := time.LoadLocation(h.Zone)
		if err != nil {
			return Hours{}, fmt.Errorf("zone %q is not a known time zone", h.Zone)
		}
		h.Zone = loc.String()
	}
	if len(h.Days) > 0 {
		days, err := parseDays(strings.Join(h.Days, ","))
		if err != nil {
			return Hours{}, err
		}
		h.Days = days
	}
	return h, nil
}

// normalAccountHours is m as it is kept. A known preset is refreshed from
// the table. An entry that is not a schedule is left out, so a hand-edited
// file that names nothing still loads. A named zone that does not load is
// kept: dropping it would turn the account on.
func normalAccountHours(m map[string]Hours) map[string]Hours {
	var out map[string]Hours
	for u, h := range m {
		u = accountKey(u)
		if u == "" || !h.scheduled() {
			continue
		}
		if p, ok := hoursPresetBy(h.Preset); ok {
			next := p.hours
			next.Preset = p.id
			h = next
		} else if cleaned, err := cleanHours(h); err == nil {
			h = cleaned
		}
		if out == nil {
			out = map[string]Hours{}
		}
		out[u] = h
	}
	return out
}

// cleanHours canonicalizes a custom schedule. It returns an error when the
// windows or the zone cannot be read; the caller then keeps h as it was.
func cleanHours(h Hours) (Hours, error) {
	h.Preset = strings.TrimSpace(h.Preset)
	h.Mode = strings.ToLower(strings.TrimSpace(h.Mode))
	if h.Mode != HoursActive && h.Mode != HoursOff {
		return h, fmt.Errorf("mode")
	}
	var wins []string
	seen := map[string]bool{}
	for _, w := range h.Windows {
		span, err := parseWindow(w)
		if err != nil {
			continue
		}
		s := span.text()
		if seen[s] {
			continue
		}
		seen[s] = true
		wins = append(wins, s)
	}
	if len(wins) == 0 {
		return h, fmt.Errorf("windows")
	}
	slices.Sort(wins)
	h.Windows = wins
	if h.Zone != "" {
		loc, err := time.LoadLocation(strings.TrimSpace(h.Zone))
		if err != nil {
			return h, err
		}
		h.Zone = loc.String()
	}
	if len(h.Days) > 0 {
		days, err := parseDays(strings.Join(h.Days, ","))
		if err != nil || len(days) == 0 {
			return h, fmt.Errorf("days")
		}
		h.Days = days
	}
	return h, nil
}

func cloneHours(m map[string]Hours) map[string]Hours {
	if m == nil {
		return nil
	}
	out := make(map[string]Hours, len(m))
	for k, h := range m {
		h.Windows = slices.Clone(h.Windows)
		h.Days = slices.Clone(h.Days)
		out[k] = h
	}
	return out
}

// span is minutes from midnight, half-open. end < start crosses midnight.
type span struct{ start, end int }

func (s span) text() string {
	return fmt.Sprintf("%02d:%02d-%02d:%02d", s.start/60, s.start%60, s.end/60, s.end%60)
}

func parseWindow(w string) (span, error) {
	a, b, ok := splitWindow(w)
	if !ok {
		return span{}, fmt.Errorf("a window looks like 09:00-18:00, not %q", w)
	}
	sh, sm, sok := parseClock(a)
	eh, em, eok := parseClock(b)
	if !sok || !eok {
		return span{}, fmt.Errorf("a window looks like 09:00-18:00, not %q", w)
	}
	start, end := sh*60+sm, eh*60+em
	if start == end {
		return span{}, fmt.Errorf("a window %s-%s is empty; name the hours it covers", a, b)
	}
	return span{start, end}, nil
}

func windowsOf(ws []string) ([]span, bool) {
	var out []span
	for _, w := range ws {
		s, err := parseWindow(w)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	return out, len(out) > 0
}

func splitWindow(w string) (string, string, bool) {
	w = strings.TrimSpace(w)
	for _, sep := range []string{"—", "–", "-"} {
		i := strings.Index(w, sep)
		if i <= 0 || i >= len(w)-len(sep) {
			continue
		}
		return strings.TrimSpace(w[:i]), strings.TrimSpace(w[i+len(sep):]), true
	}
	return "", "", false
}

func parseClock(s string) (hour, min int, ok bool) {
	s = strings.TrimSpace(s)
	if h, m, ok := settings.Clock(s); ok {
		return h, m, true
	}
	// "6:00", which Clock's layout doesn't read
	if s != "" && s[0] >= '0' && s[0] <= '9' && (len(s) == 1 || s[1] == ':') {
		if h, m, ok := settings.Clock("0" + s); ok {
			return h, m, true
		}
	}
	return 0, 0, false
}

func minutes(t time.Time) int { return t.Hour()*60 + t.Minute() }

func inSpans(ss []span, m int) bool {
	for _, s := range ss {
		if s.start < s.end {
			if m >= s.start && m < s.end {
				return true
			}
			continue
		}
		if m >= s.start || m < s.end {
			return true
		}
	}
	return false
}

var dayNames = []struct {
	name string
	day  time.Weekday
}{
	{"sun", time.Sunday}, {"sunday", time.Sunday},
	{"mon", time.Monday}, {"monday", time.Monday},
	{"tue", time.Tuesday}, {"tues", time.Tuesday}, {"tuesday", time.Tuesday},
	{"wed", time.Wednesday}, {"wednesday", time.Wednesday},
	{"thu", time.Thursday}, {"thur", time.Thursday}, {"thurs", time.Thursday}, {"thursday", time.Thursday},
	{"fri", time.Friday}, {"friday", time.Friday},
	{"sat", time.Saturday}, {"saturday", time.Saturday},
}

func lookupDay(s string) (time.Weekday, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, d := range dayNames {
		if d.name == s {
			return d.day, true
		}
	}
	return 0, false
}

func parseDays(s string) ([]string, error) {
	var got []time.Weekday
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if a, b, ok := strings.Cut(part, "-"); ok {
			from, fok := lookupDay(a)
			to, tok := lookupDay(b)
			if !fok || !tok {
				return nil, fmt.Errorf("a day is mon, tue, wed, thu, fri, sat, sun, or mon-fri, not %q", part)
			}
			for i := 0; i < 7; i++ {
				d := time.Weekday((int(from) + i) % 7)
				got = append(got, d)
				if d == to {
					break
				}
			}
			continue
		}
		d, ok := lookupDay(part)
		if !ok {
			return nil, fmt.Errorf("a day is mon, tue, wed, thu, fri, sat, sun, or mon-fri, not %q", part)
		}
		got = append(got, d)
	}
	if len(got) == 0 {
		return nil, fmt.Errorf("name the days, such as mon-fri")
	}
	return canonicalDays(got), nil
}

func canonicalDays(ds []time.Weekday) []string {
	var on [7]bool
	for _, d := range ds {
		on[d] = true
	}
	short := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	var out []string
	// Monday first, the order a person lists a week
	for _, d := range []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday} {
		if on[d] {
			out = append(out, short[d])
		}
	}
	return out
}

func dayListed(days []string, d time.Weekday) bool {
	for _, name := range days {
		if got, ok := lookupDay(name); ok && got == d {
			return true
		}
	}
	return false
}

func daysText(days []string) string {
	if len(days) == 0 || len(days) == 7 {
		return ""
	}
	label := map[string]string{"sun": "Sun", "mon": "Mon", "tue": "Tue", "wed": "Wed", "thu": "Thu", "fri": "Fri", "sat": "Sat"}
	order := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	var on []string
	for _, d := range order {
		if slices.Contains(days, d) {
			on = append(on, d)
		}
	}
	if len(on) == 5 && on[0] == "mon" && on[4] == "fri" {
		return "Mon-Fri"
	}
	var b []string
	for _, d := range on {
		b = append(b, label[d])
	}
	return strings.Join(b, ", ")
}

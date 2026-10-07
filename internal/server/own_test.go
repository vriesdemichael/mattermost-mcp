package server

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Reminder times, where no request is involved.

func TestAReminderTimeIsReadWithItsOffsetOrInThePersonsZone(t *testing.T) {
	t.Parallel()
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for at, want := range map[string]string{
		"2026-10-12T09:00":          "2026-10-12T09:00:00+09:00",
		"2026-10-12T09:00:00":       "2026-10-12T09:00:00+09:00",
		"2026-10-12T09:00+02:00":    "2026-10-12T16:00:00+09:00",
		"2026-10-12T09:00:00Z":      "2026-10-12T18:00:00+09:00",
		"2026-10-12T09:00:30+02:00": "2026-10-12T16:00:30+09:00",
	} {
		got, err := reminderTime(at, tokyo, now)
		if err != nil || got.In(tokyo).Format(time.RFC3339) != want {
			t.Errorf("%s: got %v, %v; want %s", at, got.In(tokyo), err, want)
		}
	}
	for _, at := range []string{"2026-10-01T09:00", "tomorrow", ""} {
		if _, err := reminderTime(at, tokyo, now); err == nil {
			t.Errorf("%q was taken", at)
		}
	}
	for at, want := range map[string]bool{"2026-10-12T09:00+02:00": true, "2026-10-12T09:00Z": true, "2026-10-12T09:00": false, "2026-10-12": false} {
		if got := hasOffset(at); got != want {
			t.Errorf("hasOffset(%q) = %v", at, got)
		}
	}
}

func TestAZoneThatDoesNotLoadIsRefusedRatherThanReadAsUTC(t *testing.T) {
	t.Parallel()
	unknown := &model.User{Timezone: model.StringMap{"useAutomaticTimezone": "false", "manualTimezone": "Mars/Olympus_Mons"}}
	if _, err := userZone(unknown); err == nil {
		t.Error("an unknown zone was taken")
	}
	// A time with its own offset needs no zone of the person's.
	if _, _, err := reminderAt("2099-01-01T09:00:00+01:00", unknown); err != nil {
		t.Errorf("a time with an offset: %v", err)
	}
	if _, _, err := reminderAt("2099-01-01T09:00", unknown); err == nil {
		t.Error("a time without an offset was read in a zone that does not load")
	}
	none := &model.User{}
	if zone, err := userZone(none); err != nil || zone != time.UTC {
		t.Errorf("no zone set: %v, %v", zone, err)
	}
}

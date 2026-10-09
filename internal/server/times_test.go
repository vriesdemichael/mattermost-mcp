package server

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// The times an answer gives, where no request is involved: in the person's
// own zone, at the instant Mattermost recorded.

func tokyo(t *testing.T) *time.Location {
	t.Helper()
	zone, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	return zone
}

func TestAnAnswerGivesTimesInThePersonsZoneOrElseUTC(t *testing.T) {
	t.Parallel()
	inTokyo := &model.User{Timezone: model.StringMap{"useAutomaticTimezone": "false", "manualTimezone": "Asia/Tokyo"}}
	if zone := zoneOf(inTokyo); zone.String() != "Asia/Tokyo" {
		t.Errorf("a person in Tokyo: %v", zone)
	}
	// Unlike a reminder, which is refused rather than set at the wrong hour, an
	// answer still gives its times, in UTC, which their offset says.
	unknown := &model.User{Timezone: model.StringMap{"useAutomaticTimezone": "false", "manualTimezone": "Mars/Olympus_Mons"}}
	if zone := zoneOf(unknown); zone != time.UTC {
		t.Errorf("a zone that does not load: %v", zone)
	}
	if zone := zoneOf(&model.User{}); zone != time.UTC {
		t.Errorf("no zone set: %v", zone)
	}

	at := time.Date(2026, 10, 9, 1, 30, 0, 0, time.UTC)
	if got := timestamp(at.UnixMilli(), tokyo(t)); got != "2026-10-09T10:30:00+09:00" {
		t.Errorf("got %q", got)
	}
	if got := timestamp(0, tokyo(t)); got != "" {
		t.Errorf("never is %q", got)
	}
}

func TestAStatusGivesItsTimesInThePersonsZone(t *testing.T) {
	t.Parallel()
	zone := tokyo(t)
	ends := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	active := time.Date(2026, 10, 8, 23, 15, 0, 0, time.UTC)
	user := &model.User{Username: "sam"}
	if err := user.SetCustomStatus(&model.CustomStatus{Emoji: "palm_tree", Text: "on leave", ExpiresAt: time.Now().Add(48 * time.Hour).Truncate(time.Second)}); err != nil {
		t.Fatal(err)
	}
	// Mattermost keeps a do-not-disturb end in seconds, unlike its other times.
	got := toStatus(user, &model.Status{Status: model.StatusDnd, DNDEndTime: ends.Unix(), LastActivityAt: active.UnixMilli()}, zone)
	if got.DNDUntil != "2026-10-09T10:00:00+09:00" || got.LastActivityAt != "2026-10-09T08:15:00+09:00" {
		t.Errorf("do not disturb until %q, last active %q", got.DNDUntil, got.LastActivityAt)
	}
	if until, err := time.Parse(time.RFC3339, got.CustomUntil); err != nil || got.CustomUntil[len(got.CustomUntil)-6:] != "+09:00" || !until.Equal(user.GetCustomStatus().ExpiresAt) {
		t.Errorf("the status message lasts until %q, want %v in Tokyo", got.CustomUntil, user.GetCustomStatus().ExpiresAt)
	}

	// A status message that has expired is not said at all.
	if err := user.SetCustomStatus(&model.CustomStatus{Text: "at lunch", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got := toStatus(user, nil, zone); got.CustomText != "" || got.CustomUntil != "" || got.Status != model.StatusOffline {
		t.Errorf("an expired status message: %+v", got)
	}
}

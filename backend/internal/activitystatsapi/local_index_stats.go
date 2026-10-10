package activitystatsapi

import (
	"errors"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

// IndexProcessedStats converts an actual SQL projection through the existing
// canonical derivation. It owns no config, transcript or cache reader.
func IndexProcessedStats(input protocol.JSONObject, now time.Time, location *time.Location, shots bool) (Stats, error) {
	value, e := decode(indexRaw(input))
	if e != nil {
		return Stats{}, e
	}
	data := emptyProcessed()
	arrays := []struct {
		name   string
		target *[]any
	}{{"dailyActivity", &data.daily}, {"dailyModelTokens", &data.tokens}, {"sessionStats", &data.sessions}}
	for _, field := range arrays {
		array, ok := get(value, field.name).([]any)
		if !ok {
			return Stats{}, errors.New("Index processed " + field.name + " is not iterable")
		}
		*field.target = array
	}
	objects := []struct {
		name   string
		target *object
	}{{"modelUsage", &data.models}, {"toolUsage", &data.tools}, {"skillUsage", &data.skills}, {"hourCounts", &data.hours}}
	for _, field := range objects {
		mapped, e := fields(get(value, field.name))
		if e != nil {
			return Stats{}, e
		}
		*field.target = mapped
	}
	data.totalMessages = get(value, "totalMessages")
	data.speculation = get(value, "totalSpeculationTimeSavedMs")
	if raw := input.Get("shotDistribution"); len(raw) > 0 {
		mapped, e := fields(get(value, "shotDistribution"))
		if e != nil {
			return Stats{}, e
		}
		data.shots = mapped
	}
	if location == nil {
		location = time.Local
	}
	s := &Service{options: Options{Location: location, ShotStatsEnabled: shots}, dateVM: goja.New()}
	return s.processedStats(data, now)
}
func IndexCalendarDays(first, last any, location *time.Location) (float64, error) {
	if location == nil {
		location = time.Local
	}
	s := &Service{options: Options{Location: location}, dateVM: goja.New()}
	return s.totalDays(first, last)
}

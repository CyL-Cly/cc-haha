package activitystatsapi

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

// IndexDateParser owns a native ECMAScript Date primitive, with the same local
// timezone interpretation as the existing canonical activity consumer.
type IndexDateParser struct {
	mu sync.Mutex
	s  *Service
}

func indexRaw(v any) json.RawMessage { data, _ := json.Marshal(v); return data }

func NewIndexDateParser(location *time.Location) *IndexDateParser {
	if location == nil {
		location = time.Local
	}
	return &IndexDateParser{s: &Service{options: Options{Location: location}, dateVM: goja.New()}}
}
func (p *IndexDateParser) Parse(value string) (float64, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	date, valid, err := p.s.date(value)
	return float64(date.UnixMilli()), valid, err
}
func IndexBillableUsage(entry protocol.JSONObject) bool {
	value, e := decode(indexRaw(entry))
	if e != nil {
		return false
	}
	return billable(value, get(value, "message"))
}
func IndexUsageKey(entry protocol.JSONObject) (string, bool) {
	value, e := decode(indexRaw(entry))
	if e != nil {
		return "", false
	}
	return usageKey(value, get(value, "message"))
}
func IndexShotCount(content json.RawMessage) (int, bool) {
	value, e := decode(content)
	if e != nil {
		return 0, false
	}
	return shotCount([]any{object{"type": "assistant", "message": object{"content": value}}})
}
func IndexEstimateCost(model string, tokens protocol.JSONObject, speed string) (float64, bool, error) {
	value, e := decode(indexRaw(tokens))
	if e != nil {
		return 0, false, e
	}
	fields, e := fields(value)
	if e != nil {
		return 0, false, e
	}
	cost, priced := estimateCost(model, fields, speed)
	return cost, priced, nil
}
func IndexAdd(a, b json.RawMessage) json.RawMessage {
	var first, second any = undefined, undefined
	if len(a) > 0 {
		first, _ = decode(a)
	}
	if len(b) > 0 {
		second, _ = decode(b)
	}
	return indexRaw(clean(add(first, second)))
}
func IndexDateKey(milliseconds float64) string { return dateKey(time.UnixMilli(int64(milliseconds))) }

package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

const DingTalkAICardTemplateID = "02fcf2f4-5e02-4a85-b672-46d1f715543e.schema"

func dingTalkRandom() float64 { return rand.Float64() }
func dingTalkRandomSuffix(value float64, length int) string {
	// Only the generic ECMAScript Number radix primitive is delegated to Goja.
	// It has no host functions, Source business, OS, environment or network.
	vm := goja.New()
	n := vm.ToValue(value)
	fn, _ := goja.AssertFunction(n.ToObject(vm).Get("toString"))
	result, e := fn(n, vm.ToValue(36))
	if e != nil {
		return ""
	}
	units := NewText(result.String())
	return units.Slice(2, min(len(units), 2+length)).String()
}

type DingTalkCardLimiter struct {
	mu                          sync.Mutex
	now                         func() time.Time
	tokens, lastRefill, backoff float64
	tail                        chan struct{}
}

func NewDingTalkCardLimiter(now func() time.Time) *DingTalkCardLimiter {
	if now == nil {
		now = time.Now
	}
	return &DingTalkCardLimiter{now: now, tokens: 20, lastRefill: float64(now().UnixMilli())}
}
func (l *DingTalkCardLimiter) refillLocked() {
	now := float64(l.now().UnixMilli())
	seconds := (now - l.lastRefill) / 1000
	if seconds <= 0 {
		return
	}
	l.tokens = math.Min(20, l.tokens+seconds*20)
	l.lastRefill = now
}
func dingTalkWait(ctx context.Context, millis float64) error {
	timer := time.NewTimer(time.Duration(millis * float64(time.Millisecond)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (l *DingTalkCardLimiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	previous := l.tail
	done := make(chan struct{})
	l.tail = done
	l.mu.Unlock()
	defer close(done)
	if previous != nil {
		select {
		case <-previous:
		case <-ctx.Done():
			// Cancellation cannot let a later queue entry bypass its predecessor.
			// The API's Close cancels each admitted wait and joins this chain.
			<-previous
			return ctx.Err()
		}
	}
	l.mu.Lock()
	delay := l.backoff - float64(l.now().UnixMilli())
	l.mu.Unlock()
	if delay > 0 {
		if e := dingTalkWait(ctx, delay); e != nil {
			return e
		}
	}
	l.mu.Lock()
	l.refillLocked()
	delay = math.Ceil((1 - l.tokens) / 20 * 1000)
	l.mu.Unlock()
	if delay > 0 {
		if e := dingTalkWait(ctx, delay); e != nil {
			return e
		}
	}
	l.mu.Lock()
	l.refillLocked()
	l.tokens--
	l.mu.Unlock()
	return nil
}
func (l *DingTalkCardLimiter) Backoff() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.backoff = float64(l.now().UnixMilli()) + 2000
	l.tokens = 0
	l.lastRefill = l.backoff
}
func (a *DingTalkAPI) cardLimited(ctx context.Context, method, path string, token json.RawMessage, body protocol.JSONObject) error {
	wait := func() error {
		scope, done, e := a.begin(ctx)
		if e != nil {
			return e
		}
		defer done()
		return a.cards.Wait(scope)
	}
	if e := wait(); e != nil {
		return e
	}
	e := a.cardRequest(ctx, method, path, token, body)
	var api *DingTalkHTTPError
	if !errors.As(e, &api) || api.Status != 403 || !strings.Contains(api.Body.String(), "QpsLimit") {
		return e
	}
	a.cards.Backoff()
	if e := wait(); e != nil {
		return e
	}
	return a.cardRequest(ctx, method, path, token, body)
}

type DingTalkCard struct {
	mu              sync.Mutex
	InstanceID      Text
	InstanceIDJSON  json.RawMessage
	AccessToken     json.RawMessage
	TokenExpireTime float64
	InputingStarted bool
}

func (c *DingTalkCard) Snapshot() protocol.JSONObject {
	c.mu.Lock()
	defer c.mu.Unlock()
	return protocol.NewJSONObject(protocol.JSONField{Name: "cardInstanceId", Value: c.idJSON()}, protocol.JSONField{Name: "accessToken", Value: c.AccessToken}, dingTalkField("tokenExpireTime", c.TokenExpireTime), dingTalkField("inputingStarted", c.InputingStarted))
}
func (c *DingTalkCard) idJSON() json.RawMessage {
	if c.InstanceIDJSON != nil {
		return append(json.RawMessage{}, c.InstanceIDJSON...)
	}
	return raw(c.InstanceID)
}
func BuildDingTalkDeliverBody(instanceID Text, target protocol.JSONObject, robotCode json.RawMessage) protocol.JSONObject {
	result := protocol.NewJSONObject(dingTalkField("outTrackId", instanceID), dingTalkField("userIdType", 1))
	if target.String("type") == "group" {
		result.Set("openSpaceId", raw(NewText("dtv1.card//IM_GROUP.").Join(dingTalkCoerce(target.Get("openConversationId")))))
		result.Set("imGroupOpenDeliverModel", raw(protocol.NewJSONObject(protocol.JSONField{Name: "robotCode", Value: robotCode})))
	} else {
		result.Set("openSpaceId", raw(NewText("dtv1.card//IM_ROBOT.").Join(dingTalkCoerce(target.Get("userId")))))
		result.Set("imRobotOpenDeliverModel", raw(protocol.NewJSONObject(dingTalkField("spaceType", "IM_ROBOT"), protocol.JSONField{Name: "robotCode", Value: robotCode}, dingTalkField("extension", protocol.NewJSONObject(dingTalkField("dynamicSummary", "true"))))))
	}
	return result
}
func dingTalkSpread(value json.RawMessage) protocol.JSONObject {
	value = bytes.TrimSpace(value)
	out := protocol.NewJSONObject()
	if len(value) == 0 {
		return out
	}
	if value[0] == '{' {
		return object(value)
	}
	if value[0] == '"' {
		for i, v := range TextJSON(value) {
			out.Set(strconv.Itoa(i), raw(Text{v}))
		}
	}
	if value[0] == '[' {
		var values []json.RawMessage
		_ = json.Unmarshal(value, &values)
		for i, v := range values {
			out.Set(strconv.Itoa(i), v)
		}
	}
	return out
}
func (a *DingTalkAPI) CreateCard(ctx context.Context, target, options protocol.JSONObject) (*DingTalkCard, error) {
	ctx, done, e := a.track(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	token, e := a.AccessToken(ctx)
	if e != nil {
		a.o.Log("[DingTalk][AICard] create failed: " + e.Error())
		return nil, nil
	}
	id := options.Get("outTrackId")
	if len(id) == 0 || string(id) == "null" {
		id = raw(fmt.Sprintf("card_%d_%s", a.o.Now().UnixMilli(), dingTalkRandomSuffix(a.o.Random(), 8)))
	}
	template := options.Get("cardTemplateId")
	if !credentialTruthy(template) {
		template = raw(DingTalkAICardTemplateID)
	}
	params := protocol.NewJSONObject(dingTalkField("config", `{"autoLayout":true}`))
	for _, field := range dingTalkSpread(options.Get("cardParamMap")).OwnProperties() {
		_ = params.SetPropertyJSON(field.NameJSON, field.Value)
	}
	body := protocol.NewJSONObject(protocol.JSONField{Name: "cardTemplateId", Value: template}, protocol.JSONField{Name: "outTrackId", Value: id}, dingTalkField("cardData", protocol.NewJSONObject(dingTalkField("cardParamMap", params))), dingTalkField("callbackType", "STREAM"), dingTalkField("imGroupOpenSpaceModel", protocol.NewJSONObject(dingTalkField("supportForward", true))), dingTalkField("imRobotOpenSpaceModel", protocol.NewJSONObject(dingTalkField("supportForward", true))))
	if route := options.Get("callbackRouteKey"); credentialTruthy(route) {
		body.Set("callbackRouteKey", route)
	}
	if e = a.cardRequest(ctx, "POST", "/v1.0/card/instances", token, body); e == nil {
		deliver := BuildDingTalkDeliverBody(dingTalkCoerce(id), target, a.o.Credentials.Get("clientId"))
		deliver.Set("outTrackId", id)
		e = a.cardRequest(ctx, "POST", "/v1.0/card/instances/deliver", token, deliver)
	}
	if e != nil {
		a.o.Log("[DingTalk][AICard] create failed: " + e.Error())
		return nil, nil
	}
	return &DingTalkCard{InstanceID: dingTalkCoerce(id), InstanceIDJSON: append(json.RawMessage{}, id...), AccessToken: token, TokenExpireTime: float64(a.o.Now().UnixMilli()) + 7200000}, nil
}
func (a *DingTalkAPI) validCardToken(ctx context.Context, card *DingTalkCard) error {
	card.mu.Lock()
	expired := float64(a.o.Now().UnixMilli()) > card.TokenExpireTime-300000
	card.mu.Unlock()
	if !expired {
		return nil
	}
	token, e := a.AccessToken(ctx)
	if e != nil {
		return e
	}
	card.mu.Lock()
	card.AccessToken = token
	card.TokenExpireTime = float64(a.o.Now().UnixMilli()) + 7200000
	card.mu.Unlock()
	return nil
}
func (a *DingTalkAPI) cardStatus(ctx context.Context, card *DingTalkCard, status string, content Text) error {
	card.mu.Lock()
	id, token := card.idJSON(), append(json.RawMessage(nil), card.AccessToken...)
	card.mu.Unlock()
	params := protocol.NewJSONObject(dingTalkField("flowStatus", status), dingTalkField("msgContent", DingTalkTableBlankLines(content)), dingTalkField("staticMsgContent", ""), dingTalkField("sys_full_json_obj", `{"order":["msgContent"]}`), dingTalkField("config", `{"autoLayout":true}`))
	body := protocol.NewJSONObject(protocol.JSONField{Name: "outTrackId", Value: id}, dingTalkField("cardData", protocol.NewJSONObject(dingTalkField("cardParamMap", params))))
	if status == "3" {
		body.Set("cardUpdateOptions", raw(protocol.NewJSONObject(dingTalkField("updateCardDataByKey", true))))
	}
	return a.cardLimited(ctx, "PUT", "/v1.0/card/instances", token, body)
}
func (a *DingTalkAPI) StreamCard(ctx context.Context, card *DingTalkCard, content Text, finished bool) error {
	ctx, done, e := a.track(ctx)
	if e != nil {
		return e
	}
	defer done()
	if e = a.validCardToken(ctx, card); e != nil {
		return e
	}
	card.mu.Lock()
	started := card.InputingStarted
	card.mu.Unlock()
	if !started {
		if e = a.cardStatus(ctx, card, "2", content); e != nil {
			return e
		}
		card.mu.Lock()
		card.InputingStarted = true
		card.mu.Unlock()
	}
	card.mu.Lock()
	id, token := card.idJSON(), append(json.RawMessage(nil), card.AccessToken...)
	card.mu.Unlock()
	body := protocol.NewJSONObject(dingTalkField("outTrackId", id), dingTalkField("guid", fmt.Sprintf("%d_%s", a.o.Now().UnixMilli(), dingTalkRandomSuffix(a.o.Random(), 6))), dingTalkField("key", "msgContent"), dingTalkField("content", DingTalkTableBlankLines(content)), dingTalkField("isFull", true), dingTalkField("isFinalize", finished), dingTalkField("isError", false))
	return a.cardLimited(ctx, "PUT", "/v1.0/card/streaming", token, body)
}
func (a *DingTalkAPI) FinishCard(ctx context.Context, card *DingTalkCard, content Text) error {
	ctx, complete, e := a.track(ctx)
	if e != nil {
		return e
	}
	defer complete()
	if e := a.StreamCard(ctx, card, content, true); e != nil {
		return e
	}
	if e := a.cardStatus(ctx, card, "3", DingTalkTableBlankLines(content)); e != nil {
		a.o.Log("[DingTalk][AICard] finish status failed: " + e.Error())
	}
	return nil
}

const dingTalkWhitespace = `[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

var dingTalkTableDivider = regexp.MustCompile(`^` + dingTalkWhitespace + `*\|?` + dingTalkWhitespace + `*:?-+:?` + dingTalkWhitespace + `*(\|?` + dingTalkWhitespace + `*:?-+:?` + dingTalkWhitespace + `*)+\|?` + dingTalkWhitespace + `*$`)
var dingTalkTableRow = regexp.MustCompile(`^` + dingTalkWhitespace + `*\|?[^\r\n\x{2028}\x{2029}]*\|[^\r\n\x{2028}\x{2029}]*\|?` + dingTalkWhitespace + `*$`)

func DingTalkTableBlankLines(text Text) Text {
	lines := text.Split('\n')
	result := []Text{}
	row := func(t Text) bool { return dingTalkTableRow.MatchString(t.String()) }
	for i, current := range lines {
		next := Text{}
		if i+1 < len(lines) {
			next = lines[i+1]
		}
		if row(current) && row(next) && dingTalkTableDivider.MatchString(next.String()) && i > 0 && len(lines[i-1].Trim()) > 0 && !row(lines[i-1]) {
			result = append(result, Text{})
		}
		result = append(result, current)
	}
	return JoinText(result, NewText("\n"))
}

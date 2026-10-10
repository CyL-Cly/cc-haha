// Original controls drive the real public Go components on one existing Owner.
// This fixture does not implement CLI startup or inject observed Source values.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type object map[string]json.RawMessage
type profile struct {
	ID         string            `json:"id"`
	Program    string            `json:"program"`
	SessionIDs map[string]string `json:"sessionIds"`
	Listeners  map[string]struct {
		First []object `json:"first"`
	} `json:"listeners"`
	Operations []object `json:"operations"`
}
type pair struct {
	key   string
	value any
}
type originalError struct {
	Message string
	Stack   string
}

func (e *originalError) Error() string { return e.Message }

type program struct {
	home           string
	input          profile
	state          *bootstrapstate.State
	signal         *bootstrapstate.Signal[[]string]
	config         *bootstrapstate.ConfigResolver
	cache          *bootstrapstate.PlanSlugs
	initialID      string
	env            map[string]string
	trace          []any
	snapshots      []any
	controls       []object
	refs           []pair
	counts         []pair
	tokens         map[string]bootstrapstate.Unsubscribe
	callbacks      map[string]*bootstrapstate.Listener[[]string]
	stateCallbacks map[string]*bootstrapstate.Listener[string]
	thrown         map[string]*originalError
	emitDepth      int
	uuidCalls      int
}

func scalar(op object, name string) string {
	var value string
	_ = json.Unmarshal(op[name], &value)
	return value
}
func present(op object, name string) bool { _, ok := op[name]; return ok }
func isNull(raw json.RawMessage) bool     { return string(raw) == "null" }
func decodeObject(raw json.RawMessage) (object, error) {
	var value object
	if err := json.Unmarshal(raw, &value); err != nil || value == nil || len(raw) == 0 || raw[0] != '{' {
		return nil, errors.New("original object required")
	}
	return value, nil
}
func keysAllowed(op object, fields ...string) bool {
	for key := range op {
		found := false
		for _, field := range fields {
			if key == field {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var listenerPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Public Go strings cannot represent lone UTF-16 units. Reject that domain
// before encoding/json can replace them; the original 30 inputs use valid UTF-8.
func stringDomain(raw []byte) error {
	if !utf8.Valid(raw) {
		return bootstrapstate.ErrStringDomain
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return errors.New("invalid original JSON")
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return errors.New("invalid original Unicode escape")
		}
		unit, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return err
		}
		i += 4
		if unit >= 0xdc00 && unit <= 0xdfff {
			return bootstrapstate.ErrStringDomain
		}
		if unit < 0xd800 || unit > 0xdbff {
			continue
		}
		if i+6 >= len(raw) || string(raw[i+1:i+3]) != `\u` {
			return bootstrapstate.ErrStringDomain
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return bootstrapstate.ErrStringDomain
		}
		i += 6
	}
	return nil
}
func privateValue(raw json.RawMessage, home string) (string, error) {
	value, err := decodeObject(raw)
	if err != nil || len(value) != 1 {
		return "", errors.New("original private value required")
	}
	if literal, ok := value["literal"]; ok {
		var s string
		if json.Unmarshal(literal, &s) == nil && s == "" {
			return "", nil
		}
		return "", errors.New("only original empty literal admitted")
	}
	name := scalar(value, "private")
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return "", errors.New("original private single leaf required")
	}
	return filepath.Join(home, name), nil
}
func validate(raw []byte, home string) (profile, error) {
	var input profile
	if !json.Valid(raw) {
		return input, errors.New("finite valid original JSON required")
	}
	if err := stringDomain(raw); err != nil {
		return input, err
	}
	top, err := decodeObject(raw)
	if err != nil || !keysAllowed(top, "id", "program", "sessionIds", "listeners", "operations") || len(top) != 5 {
		return input, errors.New("finite original bootstrap program required")
	}
	if err = json.Unmarshal(raw, &input); err != nil || input.ID == "" || input.Listeners == nil || input.Operations == nil || len(input.Operations) > 64 {
		return input, errors.New("finite original bootstrap shape required")
	}
	ids, err := decodeObject(top["sessionIds"])
	if err != nil || len(ids) != 3 || !keysAllowed(ids, "A", "B", "C") {
		return input, errors.New("original SID definitions required")
	}
	for _, name := range []string{"A", "B", "C"} {
		if !uuidPattern.MatchString(input.SessionIDs[name]) {
			return input, errors.New("original UUID4 literal required")
		}
	}
	allowed := map[string][]string{
		"signal":  {"subscribe", "unsubscribe", "emit", "clear", "throw", "snapshot"},
		"session": {"subscribe", "unsubscribe", "throw", "snapshot", "switch", "regenerate", "slugSet", "slugDelete", "slugClear", "setOriginalCwd", "setProjectRoot", "setCwdState"},
		"config":  {"snapshot", "configCall", "setConfig", "setHome", "configClear", "configPrime", "configDelete"},
	}
	if _, ok := allowed[input.Program]; !ok {
		return input, errors.New("unsupported original program")
	}
	fields := map[string][]string{"subscribe": {"listener", "token"}, "unsubscribe": {"token"}, "emit": {"label", "args"}, "clear": {}, "throw": {"error"}, "snapshot": {"label"}, "switch": {"sid", "projectDir"}, "regenerate": {"remember", "setCurrentAsParent"}, "slugSet": {"sid", "slug"}, "slugDelete": {"sid"}, "slugClear": {}, "setOriginalCwd": {"value"}, "setProjectRoot": {"value"}, "setCwdState": {"value"}, "configCall": {"label"}, "setConfig": {"value"}, "setHome": {"value"}, "configClear": {}, "configPrime": {"value"}, "configDelete": {}}
	validateOp := func(op object) error {
		name := scalar(op, "op")
		permitted := false
		for _, a := range allowed[input.Program] {
			if a == name {
				permitted = true
			}
		}
		if !permitted {
			return errors.New("unsupported original operation")
		}
		if !keysAllowed(op, append([]string{"op"}, fields[name]...)...) {
			return errors.New("unexpected processed bootstrap field")
		}
		for _, field := range fields[name] {
			if (name == "switch" && field == "projectDir") || (name == "regenerate" && field == "setCurrentAsParent") {
				continue
			}
			if !present(op, field) {
				return errors.New("missing original operation field")
			}
		}
		for _, field := range []string{"label", "token", "slug", "error", "remember"} {
			if present(op, field) {
				var s string
				if json.Unmarshal(op[field], &s) != nil || isNull(op[field]) {
					return errors.New("original string scalar required")
				}
			}
		}
		if name == "subscribe" {
			if _, ok := input.Listeners[scalar(op, "listener")]; !ok {
				return errors.New("original listener identity required")
			}
		}
		if present(op, "sid") {
			switch scalar(op, "sid") {
			case "A", "B", "C", "initial", "current", "generated-one", "generated-two":
			default:
				return errors.New("original SID reference required")
			}
		}
		if present(op, "projectDir") && !isNull(op["projectDir"]) {
			if _, e := privateValue(op["projectDir"], home); e != nil {
				return e
			}
		}
		if present(op, "value") {
			if isNull(op["value"]) {
				if name != "setConfig" {
					return errors.New("required original private value")
				}
			} else if _, e := privateValue(op["value"], home); e != nil {
				return e
			}
		}
		if name == "setHome" {
			v, _ := decodeObject(op["value"])
			if scalar(v, "private") == "" {
				return errors.New("HOME must remain private and defined")
			}
		}
		if present(op, "setCurrentAsParent") {
			var b bool
			if json.Unmarshal(op["setCurrentAsParent"], &b) != nil || isNull(op["setCurrentAsParent"]) {
				return errors.New("original regenerate boolean required")
			}
		}
		if present(op, "args") {
			var a []string
			if json.Unmarshal(op["args"], &a) != nil || a == nil {
				return errors.New("original signal string args required")
			}
		}
		return nil
	}
	for _, op := range input.Operations {
		if err = validateOp(op); err != nil {
			return input, err
		}
	}
	defs, err := decodeObject(top["listeners"])
	if err != nil {
		return input, err
	}
	for id, def := range input.Listeners {
		rawDef, e := decodeObject(defs[id])
		if e != nil || !listenerPattern.MatchString(id) || !keysAllowed(rawDef, "first") || len(def.First) > 8 {
			return input, errors.New("finite original listener definition required")
		}
		for _, op := range def.First {
			if err = validateOp(op); err != nil {
				return input, err
			}
		}
	}
	return input, nil
}
func privateHome() (string, error) {
	home := os.Getenv("HOME")
	temp := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if home == "" || temp == "" || !filepath.IsAbs(home) || !filepath.IsAbs(temp) {
		return "", errors.New("explicit disposable HOME/root required")
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", err
	}
	realTemp, err := filepath.EvalSymlinks(temp)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realTemp, realHome)
	if err != nil || rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") || !strings.HasPrefix(filepath.Base(realHome), "cc-haha-bootstrap-go-") {
		return "", errors.New("disposable bootstrap HOME leaf required")
	}
	cwd, err := os.Getwd()
	if err != nil || cwd != realHome || os.Getenv("USERPROFILE") != realHome || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(realHome, ".claude") {
		return "", errors.New("original private cwd/config environment required")
	}
	for _, name := range []string{"input.json", "production-imported.json", "result.json"} {
		stat, e := os.Lstat(filepath.Join(realHome, name))
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return "", e
		}
		if e == nil && !stat.Mode().IsRegular() {
			return "", errors.New("private input/observer regular file required")
		}
	}
	return realHome, nil
}
func randomUUID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = bytes[6]&15 | 64
	bytes[8] = bytes[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:]), nil
}
func (p *program) event(name string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["event"] = name
	p.trace = append(p.trace, data)
}
func pairs(items []pair) []any {
	result := make([]any, 0, len(items))
	for _, item := range items {
		result = append(result, []any{item.key, item.value})
	}
	return result
}
func setPair(items *[]pair, key string, value any) {
	for i := range *items {
		if (*items)[i].key == key {
			(*items)[i].value = value
			return
		}
	}
	*items = append(*items, pair{key, value})
}
func (p *program) getSID(ref string) (string, error) {
	if ref == "current" {
		return p.state.SessionID(), nil
	}
	for _, v := range p.refs {
		if v.key == ref {
			return v.value.(string), nil
		}
	}
	return "", errors.New("original SID reference not yet generated")
}
func (p *program) view(scope *cooperative.Scope) (map[string]any, error) {
	if p.state == nil {
		return nil, nil
	}
	s := p.state.Snapshot()
	cache, err := p.state.PlanSlugCache(scope)
	if err != nil {
		return nil, err
	}
	entries, err := cache.Entries(scope)
	if err != nil {
		return nil, err
	}
	plan := make([]any, 0, len(entries))
	for _, e := range entries {
		plan = append(plan, []string{e.SessionID, e.Slug})
	}
	view := map[string]any{"sessionId": s.SessionID, "parentDefined": s.ParentSessionID != nil, "sessionProjectDir": s.SessionProjectDir, "originalCwd": s.OriginalCWD, "projectRoot": s.ProjectRoot, "cwdState": s.CWD, "samePlanMap": cache == p.cache, "planEntries": plan}
	if s.ParentSessionID != nil {
		view["parentSessionId"] = *s.ParentSessionID
	}
	return view, nil
}
func (p *program) snapshot(scope *cooperative.Scope, label string) error {
	view, err := p.view(scope)
	if err != nil {
		return err
	}
	row := map[string]any{"label": label, "listenerCallCounts": pairs(p.counts)}
	if view != nil {
		row["state"] = view
	}
	p.snapshots = append(p.snapshots, row)
	event := map[string]any{}
	for k, v := range row {
		event[k] = v
	}
	p.event("snapshot", event)
	return nil
}
func (p *program) listener(scope *cooperative.Scope, id string, args []string) error {
	count := 0
	for _, v := range p.counts {
		if v.key == id {
			count = v.value.(int)
		}
	}
	count++
	setPair(&p.counts, id, count)
	view, err := p.view(scope)
	if err != nil {
		return err
	}
	data := map[string]any{"listener": id, "call": count, "args": args, "depth": p.emitDepth}
	if view != nil {
		data["state"] = view
	}
	p.event("listener:call", data)
	if count == 1 {
		for _, op := range p.input.Listeners[id].First {
			if err = p.execute(scope, op); err != nil {
				return err
			}
		}
	}
	p.event("listener:return", map[string]any{"listener": id, "call": count})
	return nil
}
func (p *program) execute(scope *cooperative.Scope, op object) error {
	p.controls = append(p.controls, op)
	name := scalar(op, "op")
	switch name {
	case "subscribe":
		id := scalar(op, "listener")
		var fn bootstrapstate.Unsubscribe
		var err error
		if p.signal != nil {
			fn, err = p.signal.Subscribe(scope, p.callbacks[id])
		} else {
			fn, err = p.state.OnSessionSwitch(scope, p.stateCallbacks[id])
		}
		if err != nil {
			return err
		}
		token := scalar(op, "token")
		p.tokens[token] = fn
		p.event("subscribe:return", map[string]any{"listener": id, "token": token, "returnedFunction": fn != nil})
	case "unsubscribe":
		token := scalar(op, "token")
		fn := p.tokens[token]
		if fn == nil {
			return errors.New("original subscription token not yet created")
		}
		if err := fn(scope); err != nil {
			return err
		}
		p.event("unsubscribe:return", map[string]any{"token": token, "returnedUndefined": true})
	case "clear":
		if err := p.signal.Clear(scope); err != nil {
			return err
		}
		p.event("clear:return", nil)
	case "emit":
		var args []string
		_ = json.Unmarshal(op["args"], &args)
		label := scalar(op, "label")
		p.event("emit:call", map[string]any{"label": label, "args": args, "depth": p.emitDepth})
		p.emitDepth++
		err := func() error { defer func() { p.emitDepth-- }(); return p.signal.Emit(scope, args) }()
		if err != nil {
			return err
		}
		p.event("emit:return", map[string]any{"label": label, "returnedUndefined": true})
	case "throw":
		label := scalar(op, "error")
		e := p.thrown[label]
		if e == nil {
			e = &originalError{Message: label, Stack: string(debug.Stack())}
			p.thrown[label] = e
		}
		p.event("listener:throw", map[string]any{"error": label})
		return e
	case "snapshot":
		return p.snapshot(scope, scalar(op, "label"))
	case "switch":
		id, err := p.getSID(scalar(op, "sid"))
		if err != nil {
			return err
		}
		var dir *string
		data := map[string]any{"sid": id, "projectDirOwn": present(op, "projectDir")}
		if present(op, "projectDir") {
			if !isNull(op["projectDir"]) {
				s, e := privateValue(op["projectDir"], p.home)
				if e != nil {
					return e
				}
				dir = &s
			}
			data["projectDir"] = dir
		}
		p.event("switch:call", data)
		if err = p.state.SwitchSession(scope, id, dir); err != nil {
			return err
		}
		p.event("switch:return", map[string]any{"returnedUndefined": true})
		return p.snapshot(scope, "after-switch")
	case "regenerate":
		data := map[string]any{"before": p.state.SessionID(), "optionOwn": present(op, "setCurrentAsParent")}
		var parent bool
		if present(op, "setCurrentAsParent") {
			_ = json.Unmarshal(op["setCurrentAsParent"], &parent)
			data["setCurrentAsParent"] = parent
		}
		p.event("regenerate:call", data)
		id, err := p.state.RegenerateSessionID(scope, parent)
		if err != nil {
			return err
		}
		setPair(&p.refs, scalar(op, "remember"), id)
		p.event("regenerate:return", map[string]any{"generated": id, "sameGetter": p.state.SessionID() == id})
		return p.snapshot(scope, "after-regenerate")
	case "slugSet":
		id, err := p.getSID(scalar(op, "sid"))
		if err != nil {
			return err
		}
		if err = p.cache.Set(scope, id, scalar(op, "slug")); err != nil {
			return err
		}
		cache, err := p.state.PlanSlugCache(scope)
		if err != nil {
			return err
		}
		p.event("slugSet:return", map[string]any{"sameMap": cache == p.cache})
	case "slugDelete":
		id, err := p.getSID(scalar(op, "sid"))
		if err != nil {
			return err
		}
		deleted, err := p.cache.Delete(scope, id)
		if err != nil {
			return err
		}
		p.event("slugDelete:return", map[string]any{"deleted": deleted})
	case "slugClear":
		if err := p.cache.Clear(scope); err != nil {
			return err
		}
		p.event("slugClear:return", nil)
	case "setOriginalCwd", "setProjectRoot", "setCwdState":
		value, err := privateValue(op["value"], p.home)
		if err != nil {
			return err
		}
		switch name {
		case "setOriginalCwd":
			err = p.state.SetOriginalCWD(scope, value)
		case "setProjectRoot":
			err = p.state.SetProjectRoot(scope, value)
		case "setCwdState":
			err = p.state.SetCWD(scope, value)
		}
		if err != nil {
			return err
		}
		p.event(name+":return", nil)
	case "setConfig":
		if isNull(op["value"]) {
			delete(p.env, "CLAUDE_CONFIG_DIR")
		} else {
			v, err := privateValue(op["value"], p.home)
			if err != nil {
				return err
			}
			p.env["CLAUDE_CONFIG_DIR"] = v
		}
		v, ok := p.env["CLAUDE_CONFIG_DIR"]
		data := map[string]any{"defined": ok}
		if ok {
			data["value"] = v
		}
		p.event("setConfig:return", data)
	case "setHome":
		v, err := privateValue(op["value"], p.home)
		if err != nil {
			return err
		}
		p.env["HOME"] = v
		p.event("setHome:return", map[string]any{"HOME": v})
	case "configClear":
		if err := p.config.ClearCache(scope); err != nil {
			return err
		}
		size, err := p.config.CacheSize(scope)
		if err != nil {
			return err
		}
		p.event("configClear:return", map[string]any{"size": size})
	case "configPrime":
		v, err := privateValue(op["value"], p.home)
		if err != nil {
			return err
		}
		if err = p.config.PrimeCache(scope, v); err != nil {
			return err
		}
		key, ok := p.env["CLAUDE_CONFIG_DIR"]
		data := map[string]any{"keyDefined": ok, "value": v, "sameCache": true}
		if ok {
			data["key"] = key
		}
		p.event("configPrime:return", data)
	case "configDelete":
		deleted, err := p.config.DeleteCache(scope)
		if err != nil {
			return err
		}
		key, ok := p.env["CLAUDE_CONFIG_DIR"]
		data := map[string]any{"keyDefined": ok, "deleted": deleted}
		if ok {
			data["key"] = key
		}
		p.event("configDelete:return", data)
	case "configCall":
		key, ok := p.env["CLAUDE_CONFIG_DIR"]
		_, hit, err := p.config.CacheLookup(scope)
		if err != nil {
			return err
		}
		v, err := p.config.ConfigHome(scope)
		if err != nil {
			return err
		}
		cached, _, err := p.config.CacheLookup(scope)
		if err != nil {
			return err
		}
		cc, err := p.config.CcHahaDir(scope)
		if err != nil {
			return err
		}
		teams, err := p.config.TeamsDir(scope)
		if err != nil {
			return err
		}
		size, err := p.config.CacheSize(scope)
		if err != nil {
			return err
		}
		data := map[string]any{"label": scalar(op, "label"), "keyDefined": ok, "nativeHome": p.home, "beforeHas": hit, "value": v, "cacheValue": cached, "ccHaha": cc, "teams": teams, "size": size}
		if ok {
			data["key"] = key
		}
		p.event("configCall:return", data)
	default:
		return errors.New("unsupported original operation")
	}
	return nil
}
func run() error {
	home, err := privateHome()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(home, "input.json"))
	if err != nil {
		return err
	}
	input, err := validate(raw, home)
	if err != nil {
		return err
	}
	owner := cooperative.New()
	defer owner.Close()
	p := &program{home: home, input: input, env: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude")}, trace: []any{}, snapshots: []any{}, controls: []object{}, refs: []pair{}, counts: []pair{}, tokens: map[string]bootstrapstate.Unsubscribe{}, callbacks: map[string]*bootstrapstate.Listener[[]string]{}, stateCallbacks: map[string]*bootstrapstate.Listener[string]{}, thrown: map[string]*originalError{}}
	uuid := func() (string, error) { p.uuidCalls++; return randomUUID() }
	if input.Program == "session" {
		p.initialID, err = uuid()
		if err != nil {
			return err
		}
		p.state, err = bootstrapstate.New(owner, p.initialID, home, uuid)
		if err != nil {
			return err
		}
		setPair(&p.refs, "initial", p.initialID)
	}
	for _, key := range []string{"A", "B", "C"} {
		setPair(&p.refs, key, input.SessionIDs[key])
	}
	if input.Program == "signal" {
		p.signal, err = bootstrapstate.NewSignal[[]string](owner)
		if err != nil {
			return err
		}
	}
	if input.Program == "config" {
		p.config, err = bootstrapstate.NewConfigResolver(owner, bootstrapstate.ConfigHost{Environment: func(key string) (string, bool) { value, ok := p.env[key]; return value, ok }, HomeDir: func() (string, error) { return home, nil }})
		if err != nil {
			return err
		}
	}
	for id := range input.Listeners {
		id := id
		p.callbacks[id] = bootstrapstate.NewListener(func(scope *cooperative.Scope, args []string) error { return p.listener(scope, id, args) })
		p.stateCallbacks[id] = bootstrapstate.NewListener(func(scope *cooperative.Scope, sid string) error { return p.listener(scope, id, []string{sid}) })
	}
	marker, _ := json.Marshal(map[string]any{"imported": true, "program": input.Program})
	if err = os.WriteFile(filepath.Join(home, "production-imported.json"), marker, 0600); err != nil {
		return err
	}
	var final map[string]any
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if p.state != nil {
			var e error
			p.cache, e = p.state.PlanSlugCache(scope)
			if e != nil {
				return e
			}
		}
		if e := p.snapshot(scope, "initial"); e != nil {
			return e
		}
		for _, op := range input.Operations {
			if e := p.execute(scope, op); e != nil {
				identity := false
				var original *originalError
				for _, t := range p.thrown {
					if e == t {
						identity = true
						original = t
						break
					}
				}
				data := map[string]any{"operation": op, "originalErrorIdentity": identity, "name": "Error", "message": e.Error(), "actualGoErrorType": fmt.Sprintf("%T", e), "diagnosticMetadataPending": true}
				if original != nil {
					data["stack"] = original.Stack
					data["ownKeys"] = []string{"Message", "Stack"}
				} else {
					data["stack"] = string(debug.Stack())
					data["ownKeys"] = []string{}
				}
				p.event("control:caught", data)
				if e = p.snapshot(scope, "after-control-throw"); e != nil {
					return e
				}
			}
		}
		if e := p.snapshot(scope, "final"); e != nil {
			return e
		}
		var e error
		final, e = p.view(scope)
		return e
	})
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	result := map[string]any{"input": json.RawMessage(raw), "actualPid": os.Getpid(), "home": home, "physicalCwd": cwd, "references": pairs(p.refs), "trace": p.trace, "snapshots": p.snapshots, "controls": p.controls, "capabilities": map[string]any{"blocked": []any{}, "secureReads": []any{}, "absentReads": []any{}}, "scope": "Original controls drive public Go components on one canonical Owner; explicit pinned native startup homedir; no CLI/resume lifecycle proof."}
	if p.state != nil {
		result["initialSID"] = p.initialID
		result["finalState"] = final
	}
	bytes, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(home, "result.json"), bytes, 0600); err != nil {
		return err
	}
	admin, _ := json.Marshal(map[string]any{"sameOwner": true, "UUIDPrimitiveCalls": p.uuidCalls, "nativeHomeProfile": "Bun1.3.14 startup private HOME snapshot; HOME env mutations remain raw original controls", "errorDiagnostics": "Go raw stack/type retained; JavaScript stack/own-key literal parity unimplemented", "programResultCapturedBeforeOwnerClose": true})
	return os.WriteFile(filepath.Join(home, "go-administrative-observation.json"), admin, 0600)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
